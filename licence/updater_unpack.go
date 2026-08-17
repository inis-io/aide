package licence

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// deleteListName - 增量包根级删除清单文件名（每行一个相对路径，`#` 开头为注释）
const deleteListName = "delete.list"

// unpackArtifact - 解包发布物到 staging 目录，返回（staging 路径, 删除清单, error）。
//
// 按扩展名分发（全部标准库，零新依赖）：
//   - `.zip` → archive/zip
//   - `.tar.gz` / `.tgz` → archive/tar + compress/gzip
//   - 其它 / 无扩展名 → 裸二进制单文件（SelfBinary 自更新场景）
//
// 增量包（artifactType=incremental）内含变更文件 + 根级 delete.list：
// 解出的变更文件树合并进目标目录，delete.list 逐行给出应删除的相对路径。
// 路径安全：条目统一 filepath.Clean，拒绝绝对路径与 `..` 逃逸（设计 §7.3）。
func (this *Updater) unpackArtifact(ctx context.Context, artifact ManifestArtifact, targetVersion string, src string) (string, []string, error) {

	staging := filepath.Join(this.updateDir(), "work", targetVersion)
	_ = os.RemoveAll(staging)
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return "", nil, err
	}

	var deleteList []string
	var err error
	switch {
	case strings.HasSuffix(strings.ToLower(artifact.FileName), ".zip"):
		deleteList, err = unpackZip(src, staging)
	case strings.HasSuffix(strings.ToLower(artifact.FileName), ".tar.gz") ||
		strings.HasSuffix(strings.ToLower(artifact.FileName), ".tgz"):
		deleteList, err = unpackTarGz(src, staging)
	default:
		// 裸二进制：单文件直接落位 staging（文件名取发布物文件名 basename）
		dest := filepath.Join(staging, filepath.Base(artifact.FileName))
		if err := copyFile(src, dest); err != nil {
			return "", nil, err
		}
	}
	if err != nil {
		return "", nil, err
	}
	return staging, deleteList, nil
}

// unpackZip - 解压 zip 到目标目录（返回根级 delete.list 内容）
func unpackZip(src string, destDir string) ([]string, error) {

	reader, err := zip.OpenReader(src)
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()

	var deleteList []string
	for _, entry := range reader.File {
		// 目录条目跳过
		if entry.FileInfo().IsDir() {
			continue
		}
		name, err := safeArchivePath(entry.Name)
		if err != nil {
			return nil, err
		}
		// 根级 delete.list 读取内容后不再落盘
		if name == deleteListName && !strings.Contains(name, string(os.PathSeparator)) {
			raw, readErr := readZipEntry(entry)
			if readErr != nil {
				return nil, readErr
			}
			deleteList = parseDeleteList(raw)
			continue
		}
		if err = extractZipEntry(entry, destDir, name); err != nil {
			return nil, err
		}
	}
	return deleteList, nil
}

// unpackTarGz - 解压 tar.gz 到目标目录（返回根级 delete.list 内容）
func unpackTarGz(src string, destDir string) ([]string, error) {

	file, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gzipReader.Close() }()

	tarReader := tar.NewReader(gzipReader)
	var deleteList []string
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		name, err := safeArchivePath(header.Name)
		if err != nil {
			return nil, err
		}
		if name == deleteListName && !strings.Contains(name, string(os.PathSeparator)) {
			raw, readErr := io.ReadAll(tarReader)
			if readErr != nil {
				return nil, readErr
			}
			deleteList = parseDeleteList(raw)
			continue
		}
		dest := filepath.Join(destDir, name)
		if err = os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return nil, err
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, header.FileInfo().Mode())
		if err != nil {
			return nil, err
		}
		if _, err = io.Copy(out, tarReader); err != nil {
			_ = out.Close()
			return nil, err
		}
		if err = out.Close(); err != nil {
			return nil, err
		}
	}
	return deleteList, nil
}

// readZipEntry - 读取 zip 条目全部内容
func readZipEntry(entry *zip.File) ([]byte, error) {
	reader, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	return io.ReadAll(reader)
}

// extractZipEntry - 写入单个 zip 条目到目标路径（带目录创建与权限）
func extractZipEntry(entry *zip.File, destDir string, name string) error {

	dest := filepath.Join(destDir, name)
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	reader, err := entry.Open()
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, entry.Mode())
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, reader); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// safeArchivePath - 归档路径安全检查：Clean + 拒绝绝对路径与 `..` 逃逸
func safeArchivePath(name string) (string, error) {

	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == ".." ||
		strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", errors.New("非法归档路径：" + name)
	}
	return clean, nil
}

// parseDeleteList - 解析删除清单（每行一个相对路径，空行与 `#` 注释忽略）
func parseDeleteList(raw []byte) []string {

	var result []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		result = append(result, line)
	}
	return result
}

// copyFile - 复制文件（保留目标权限 0700 场景由调用方按需调整）
func copyFile(src string, dest string) error {

	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
