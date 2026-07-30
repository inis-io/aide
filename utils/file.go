package utils

import (
	"archive/zip"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/mholt/archives"
	"github.com/spf13/afero"
	"github.com/spf13/cast"
)

// fileHTTPRegexp - 判断路径是否为网络 URL（http/https）
var fileHTTPRegexp = regexp.MustCompile(`^https?://`)

// ============================================================
// FileClass 基础操作（File 单例，基于 afero）
// ============================================================

// FileClass - 文件系统类
type FileClass struct {
	Fs afero.Fs
}

// File - 文件系统单例（基于操作系统文件系统）
var File *FileClass

func init() {
	// 初始化文件实例
	File = &FileClass{
		Fs: afero.NewOsFs(),
	}
}

// Read - 读取文件内容
func (this *FileClass) Read(dest string) ([]byte, error) {

	// 一次性读取完整文件内容，避免单次 Read 短读
	content, err := afero.ReadFile(this.Fs, dest)
	if err != nil {
		return nil, fmt.Errorf("读取文件内容失败: %w", err)
	}

	return content, nil
}

// ReadString - 读取文件内容为字符串
func (this *FileClass) ReadString(dest string) (string, error) {

	content, err := this.Read(dest)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

// Write - 创建文件并写入内容
func (this *FileClass) Write(dest string, content []byte) error {

	// 确保父目录存在
	if err := this.Fs.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return fmt.Errorf("创建父目录失败: %w", err)
	}

	// 以写入模式打开文件（若不存在则创建，存在则覆盖）
	file, err := this.Fs.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("创建文件失败: %w", err)
	}
	// 延迟关闭文件
	defer func() { _ = file.Close() }()

	// 写入内容到文件
	if _, err = file.Write(content); err != nil {
		return fmt.Errorf("写入文件内容失败: %w", err)
	}

	return nil
}

// WriteString - 写入字符串内容到文件
func (this *FileClass) WriteString(dest, content string) error {
	return this.Write(dest, []byte(content))
}

// Append - 追加内容到文件末尾
func (this *FileClass) Append(dest string, content []byte) error {

	// 确保父目录存在
	if err := this.Fs.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return fmt.Errorf("创建父目录失败: %w", err)
	}

	// 以追加模式打开文件（若不存在则创建）
	file, err := this.Fs.OpenFile(dest, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("打开文件失败: %w", err)
	}
	defer func() { _ = file.Close() }()

	// 追加内容到文件末尾
	if _, err = file.Write(content); err != nil {
		return fmt.Errorf("追加文件内容失败: %w", err)
	}

	return nil
}

// WriteReader - 将数据流写入文件
func (this *FileClass) WriteReader(dest string, reader io.Reader) error {

	// 确保父目录存在
	if err := this.Fs.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return fmt.Errorf("创建父目录失败: %w", err)
	}

	// 以写入模式打开文件（若不存在则创建，存在则覆盖）
	file, err := this.Fs.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("创建文件失败: %w", err)
	}
	defer func() { _ = file.Close() }()

	// 将数据通过流的方式写入磁盘
	if _, err = io.Copy(file, reader); err != nil {
		return fmt.Errorf("写入文件内容失败: %w", err)
	}

	return nil
}

// Delete - 删除文件或目录
func (this *FileClass) Delete(dest string) error {
	return this.Fs.RemoveAll(dest)
}

// Exist - 检查文件或目录是否存在
func (this *FileClass) Exist(dest string) (ok bool) {
	ok, _ = afero.Exists(this.Fs, dest)
	return ok
}

// List - 查询目录下的目录和文件
func (this *FileClass) List(dest string) (files []FileInfo, err error) {

	// 打开指定目录
	dir, err := this.Fs.Open(dest)
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()

	// 获取目录下的所有文件信息
	infos, err := dir.Readdir(-1)
	if err != nil {
		return nil, err
	}

	for _, info := range infos {
		files = append(files, FileInfo{
			Path:    filepath.ToSlash(filepath.Join(dest, info.Name())),
			Name:    info.Name(),
			Size:    info.Size(),
			Mode:    info.Mode(),
			ModTime: info.ModTime(),
			IsDir:   info.IsDir(),
			Sys:     info.Sys(),
		})
	}

	return files, nil
}

// Stat - 获取单个文件或目录信息
func (this *FileClass) Stat(dest string) (FileInfo, error) {

	info, err := this.Fs.Stat(dest)
	if err != nil {
		return FileInfo{}, err
	}

	return FileInfo{
		Path:    filepath.ToSlash(dest),
		Name:    info.Name(),
		Size:    info.Size(),
		Mode:    info.Mode(),
		ModTime: info.ModTime(),
		IsDir:   info.IsDir(),
		Sys:     info.Sys(),
	}, nil
}

// Size - 获取文件大小（字节）
func (this *FileClass) Size(dest string) (int64, error) {

	info, err := this.Fs.Stat(dest)
	if err != nil {
		return 0, err
	}

	return info.Size(), nil
}

// IsDir - 判断路径是否为目录
func (this *FileClass) IsDir(dest string) bool {

	info, err := this.Fs.Stat(dest)
	if err != nil {
		return false
	}

	return info.IsDir()
}

// IsFile - 判断路径是否为文件
func (this *FileClass) IsFile(dest string) bool {

	info, err := this.Fs.Stat(dest)
	if err != nil {
		return false
	}

	return !info.IsDir()
}

// Mkdir - 创建目录（含多级父目录）
func (this *FileClass) Mkdir(dest string) error {
	return this.Fs.MkdirAll(dest, 0755)
}

// Copy - 复制文件（保留源文件权限）
func (this *FileClass) Copy(src, dst string) error {

	// 打开源文件
	source, err := this.Fs.Open(src)
	if err != nil {
		return fmt.Errorf("打开源文件失败: %w", err)
	}
	defer func() { _ = source.Close() }()

	// 获取源文件权限
	info, err := source.Stat()
	if err != nil {
		return fmt.Errorf("获取源文件信息失败: %w", err)
	}

	// 确保目标父目录存在
	if err := this.Fs.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("创建目标父目录失败: %w", err)
	}

	// 创建目标文件，权限与源文件一致
	target, err := this.Fs.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return fmt.Errorf("创建目标文件失败: %w", err)
	}
	defer func() { _ = target.Close() }()

	// 复制文件内容
	if _, err = io.Copy(target, source); err != nil {
		return fmt.Errorf("复制文件内容失败: %w", err)
	}

	return nil
}

// Move - 移动文件或目录（跨设备时回退为复制后删除）
func (this *FileClass) Move(src, dst string) error {

	// 优先直接重命名（同设备时为原子操作）
	if err := this.Fs.Rename(src, dst); err == nil {
		return nil
	}

	// 重命名失败（如跨设备），回退为复制 + 删除
	if err := this.Copy(src, dst); err != nil {
		return err
	}

	return this.Delete(src)
}

// Chmod - 修改文件或目录权限
func (this *FileClass) Chmod(dest string, mode int64) error {
	return this.Fs.Chmod(dest, os.FileMode(mode))
}

// Download - 下载网络文件
func (this *FileClass) Download(url string) (fileName string, body io.ReadCloser, length int64, err error) {

	// 创建带有超时的 HTTP 客户端
	client := &http.Client{
		// 设置较长的超时时间
		Timeout: 10 * time.Minute,
	}

	// 创建 HTTP 请求
	request, err := http.NewRequestWithContext(context.Background(), "GET", url, nil)
	if err != nil {
		return "", nil, 0, err
	}

	// 发送请求
	resp, err := client.Do(request)
	if err != nil {
		return "", nil, 0, err
	}

	// 检查 HTTP 状态码
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return "", nil, 0, fmt.Errorf("HTTP请求失败: %s", resp.Status)
	}

	// 尝试从 URL 或响应头中提取文件名
	fileName = filepath.Base(url)
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if idx := strings.Index(cd, "filename="); idx != -1 {
			fileName = strings.Trim(cd[idx+9:], "\"' ")
		}
	}

	return fileName, resp.Body, resp.ContentLength, nil
}

// AutoExtract - 自动解压函数，支持本地和网络压缩包
func (this *FileClass) AutoExtract(sourceURL, dest string) (err error) {

	var (
		size   int64
		name   string
		reader io.ReadCloser
	)

	// 确保目标目录存在
	if err = os.MkdirAll(dest, 0755); err != nil {
		return fmt.Errorf("创建目标目录失败: %v", err)
	}

	// 判断是本地文件还是网络URL（通过正则表达式）
	if fileHTTPRegexp.MatchString(sourceURL) {

		name, reader, size, err = this.Download(sourceURL)
		if err != nil {
			return fmt.Errorf("下载压缩包失败: %v", err)
		}

		defer func(reader io.ReadCloser) { _ = reader.Close() }(reader)

	} else {

		// 处理本地文件
		name = filepath.Base(sourceURL)
		reader, err = os.Open(sourceURL)
		if err != nil {
			return fmt.Errorf("打开本地文件失败: %v", err)
		}

		defer func(reader io.ReadCloser) { _ = reader.Close() }(reader)

		// 获取文件大小
		if file, ok := reader.(*os.File); ok {
			if info, err := file.Stat(); err == nil {
				size = info.Size()
			}
		}
	}

	// 创建临时文件用于存储网络下载的内容（如果需要）
	if fileHTTPRegexp.MatchString(sourceURL) {

		// 创建临时文件
		temp, err := os.CreateTemp("", "archive-*"+filepath.Ext(name))
		if err != nil {
			return fmt.Errorf("创建临时文件失败: %v", err)
		}

		// 清理临时文件
		defer func(name string) { _ = os.Remove(name) }(temp.Name())
		defer func(temp *os.File) { _ = temp.Close() }(temp)

		// 显示下载进度
		progress := &ProgressReader{Reader: reader, Total: size}

		// 将内容复制到临时文件
		if _, err = io.Copy(temp, progress); err != nil {
			return fmt.Errorf("下载压缩包内容失败: %v", err)
		}

		// 重置文件指针到开始位置
		if _, err = temp.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("重置文件指针失败: %v", err)
		}

		// 使用临时文件作为输入
		reader = temp
	}

	// 自动识别文件格式
	format, stream, err := archives.Identify(context.Background(), name, reader)
	if err != nil {
		return fmt.Errorf("识别文件格式失败: %v", err)
	}

	// 检查是否为可提取的格式
	extractor, ok := format.(archives.Extractor)
	if !ok {
		return fmt.Errorf("不支持的压缩格式: %T", format)
	}

	// 创建目标目录
	if err = os.MkdirAll(dest, 0755); err != nil {
		return fmt.Errorf("创建目标目录失败: %v", err)
	}

	return extractor.Extract(context.Background(), stream, func(ctx context.Context, fileInfo archives.FileInfo) error {

		// 构建目标路径
		destPath := filepath.Join(dest, fileInfo.NameInArchive)

		// 防止 ZipSlip：解压后的路径必须位于目标目录内
		if !strings.HasPrefix(destPath, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("非法的压缩包路径: %s", fileInfo.NameInArchive)
		}

		// 处理目录
		if fileInfo.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}

		// 处理文件
		return this.WriteFile(destPath, fileInfo)
	})
}

// WriteFile - 写入文件内容（AutoExtract 内部使用）
func (this *FileClass) WriteFile(dest string, fileInfo archives.FileInfo) error {

	// 确保目标目录存在
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return fmt.Errorf("创建目标目录失败: %v", err)
	}

	// 从存档中打开源代码阅读器
	reader, err := fileInfo.Open()
	if err != nil {
		return fmt.Errorf("打开文件内容失败: %v", err)
	}
	defer func() { _ = reader.Close() }()

	// 在同一目录下创建一个临时文件，以避免跨设备重命名问题
	dir := filepath.Dir(dest)
	tmpFile, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %v", err)
	}
	// 如果在此之后有任何操作失败，请删除临时文件
	tmpName := tmpFile.Name()
	cleanupTmp := func() { _ = os.Remove(tmpName) }
	// 确保临时文件已关闭
	defer func() { _ = tmpFile.Close() }()

	// 将内容复制到临时文件
	if _, err = io.Copy(tmpFile, reader); err != nil {
		cleanupTmp()
		return fmt.Errorf("写入临时文件失败: %v", err)
	}

	// 刷新到磁盘
	if err = tmpFile.Sync(); err != nil {
		// 虽非致命，但创下纪录
		_ = tmpFile.Close()
		cleanupTmp()
		return fmt.Errorf("同步临时文件失败: %v", err)
	}

	// 重命名前关闭临时文件
	if err = tmpFile.Close(); err != nil {
		cleanupTmp()
		return fmt.Errorf("关闭临时文件失败: %v", err)
	}

	// 尝试将临时文件重命名为目标文件（在大多数系统上具有原子性）
	if err = os.Rename(tmpName, dest); err == nil {
		// 如果存档提供了权限，则设置权限
		if mode := fileInfo.Mode(); mode != 0 {
			_ = os.Chmod(dest, mode)
		} else {
			_ = os.Chmod(dest, 0755)
		}
		return nil
	}

	// 如果重命名失败（例如因为文件正忙），请尝试安全覆盖：打开目标文件并进行复制
	// 重新打开临时文件进行读取
	tmpRead, err2 := os.Open(tmpName)
	if err2 != nil {
		cleanupTmp()
		return fmt.Errorf("重命名失败: %v；且打开临时文件失败: %v", err, err2)
	}
	defer func() { _ = tmpRead.Close() }()

	// 尝试打开目标文件进行截断和写入
	dstFile, err3 := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err3 != nil {
		cleanupTmp()
		return fmt.Errorf("重命名失败: %v；且打开目标文件失败: %v", err, err3)
	}
	// 从tmp复制到目标位置
	if _, err = io.Copy(dstFile, tmpRead); err != nil {
		_ = dstFile.Close()
		cleanupTmp()
		return fmt.Errorf("重命名失败: %v；且覆盖目标文件失败: %v", err, err)
	}
	_ = dstFile.Close()

	// 如果存档提供了权限，则设置这些权限
	if mode := fileInfo.Mode(); mode != 0 {
		_ = os.Chmod(dest, mode)
	} else {
		_ = os.Chmod(dest, 0755)
	}

	// 删除临时文件
	cleanupTmp()

	return nil
}

// Watcher - 创建文件监听器
func (this *FileClass) Watcher(filePath string, callback func(event fsnotify.Event)) (*FileWatcher, error) {

	// 创建 fsnotify 监听器
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("创建文件监听器失败: %w", err)
	}

	// 获取文件的绝对路径和目录
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		if err := watcher.Close(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("获取文件绝对路径失败: %w", err)
	}

	// 获取文件所在目录（fsnotify 监听的是目录）
	dir := filepath.Dir(absPath)

	// 添加目录到监听列表
	err = watcher.Add(dir)
	if err != nil {
		if err := watcher.Close(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("添加监听目录失败: %w", err)
	}

	return &FileWatcher{
		watcher:  watcher,
		filePath: absPath,
		callback: callback,
		stopChan: make(chan bool),
		// 默认防抖时间
		debounce: 200 * time.Millisecond,
	}, nil
}

// ============================================================
// FileStruct 链式操作
// ============================================================

// File - 文件系统
func NewFile(request ...FileRequest) *FileStruct {

	if len(request) == 0 {
		request = append(request, FileRequest{})
	}

	if Is.Empty(request[0].Limit) {
		request[0].Limit = 10
	}

	if Is.Empty(request[0].Page) {
		request[0].Page = 1
	}

	if Is.Empty(request[0].Format) {
		request[0].Format = "network"
	}

	if Is.Empty(request[0].Sub) {
		// 已知怪癖：构造时默认递归，显式传入的 false 也会被重置为 true（兼容现有 List 递归行为）
		// 需要关闭递归时请使用链式设置器 .Sub(false)
		request[0].Sub = true
	}

	if Is.Empty(request[0].Ext) {
		request[0].Ext = "*"
	}

	return &FileStruct{
		request:  &request[0],
		response: &FileResponse{},
	}
}

// Path 设置文件路径(包含文件名，如：/tmp/test.txt)
func (this *FileStruct) Path(path any) *FileStruct {
	this.request.Path = cast.ToString(path)
	return this
}

// Dir 设置目录路径(不包含文件名，如：/tmp)
func (this *FileStruct) Dir(dir any) *FileStruct {
	this.request.Dir = cast.ToString(dir)
	return this
}

// Name 设置文件名(不包含路径，如：test.txt)
func (this *FileStruct) Name(name any) *FileStruct {
	this.request.Name = cast.ToString(name)
	return this
}

// Ext 设置文件后缀(如：.txt)
func (this *FileStruct) Ext(ext any) *FileStruct {
	this.request.Ext = cast.ToString(ext)
	return this
}

// Domain 设置域名(用于拼接文件路径)
func (this *FileStruct) Domain(domain any) *FileStruct {
	this.request.Domain = cast.ToString(domain)
	return this
}

// Prefix 设置前缀(用于过滤前缀)
func (this *FileStruct) Prefix(prefix any) *FileStruct {
	this.request.Prefix = cast.ToString(prefix)
	return this
}

// Limit 设置限制行数
func (this *FileStruct) Limit(limit any) *FileStruct {
	this.request.Limit = cast.ToInt(limit)
	return this
}

// Page 设置读取偏移量
func (this *FileStruct) Page(page any) *FileStruct {
	this.request.Page = cast.ToInt(page)
	return this
}

// Sub 设置是否包含子目录（File 构造时默认递归为 true，可用 .Sub(false) 关闭）
func (this *FileStruct) Sub(sub bool) *FileStruct {
	this.request.Sub = sub
	return this
}

// Save 保存文件
func (this *FileStruct) Save(reader io.Reader, path ...string) (result *FileResponse) {

	if len(path) != 0 {
		this.request.Path = path[0]
	}

	if Is.Empty(this.request.Path) {
		this.response.Error = errors.New("文件路径不能为空")
		return this.response
	}

	// 委托 File 流式写入磁盘
	if err := File.WriteReader(this.request.Path, reader); err != nil {
		this.response.Error = err
		return this.response
	}

	return this.response
}

// Remove 删除文件或目录
func (this *FileStruct) Remove(path ...any) (result *FileResponse) {

	if len(path) != 0 {
		this.request.Path = cast.ToString(path[0])
	}

	if Is.Empty(this.request.Path) {
		this.response.Error = errors.New("文件路径不能为空")
		return this.response
	}

	// 委托 File 删除（文件和目录均适用）
	if err := File.Delete(this.request.Path); err != nil {
		this.response.Error = err
		return this.response
	}

	return this.response
}

// Download 下载文件
/**
 * @param path1 远程文件路径（下载地址）
 * @param path2 本地文件路径（保存路径，包含文件名）
 * @return *FileResponse
 * @example：
 * 1. item := utils.NewFile().Download("https://inis.cn/name.zip", "public/test.zip")
 * 2. item := utils.NewFile().Dir("public").Name("test.zip").Download("https://inis.cn/name.zip")
 * 3. item := utils.NewFile(utils.FileRequest{
	Path: "https://inis.cn/name.zip",
	Name: "test.zip",
	Dir: "public",
}).Download()
*/
func (this *FileStruct) Download(path ...any) (result *FileResponse) {

	if len(path) != 0 {
		this.request.Path = cast.ToString(path[0])
	}

	if Is.Empty(this.request.Path) {
		this.response.Error = errors.New("文件路径不能为空")
		return this.response
	}

	// 创建一个HTTP GET请求
	req, err := http.NewRequest("GET", this.request.Path, nil)
	if err != nil {
		this.response.Error = err
		return this.response
	}

	// 发送HTTP请求并获取响应
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		this.response.Error = err
		return this.response
	}
	defer func() { _ = resp.Body.Close() }()

	if Is.Empty(this.request.Name) {
		this.request.Name = filepath.Base(this.request.Path)
	}

	var savePath string
	if len(path) > 1 {
		savePath = cast.ToString(path[1])
	} else {
		savePath = filepath.Join(this.request.Dir, this.request.Name)
	}

	// 如果目录不存在，需要创建
	dir := filepath.Dir(savePath)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0755); err != nil {
			this.response.Error = err
			return this.response
		}
	}
	// 创建本地文件并将HTTP响应的Body写入本地文件
	saveFile, err := os.Create(savePath)
	if err != nil {
		this.response.Error = err
		return this.response
	}
	defer func() { _ = saveFile.Close() }()

	_, err = io.Copy(saveFile, resp.Body)
	if err != nil {
		this.response.Error = err
		return this.response
	}

	return this.response
}

// Byte 获取文件字节
func (this *FileStruct) Byte(path ...any) (result *FileResponse) {

	if len(path) != 0 {
		this.request.Path = cast.ToString(path[0])
	}

	if Is.Empty(this.request.Path) {
		this.response.Error = errors.New("文件路径不能为空")
		return this.response
	}

	// 委托 File 一次性读取完整内容（顺带修复小于50MB分支单次 Read 的短读问题）
	bytes, err := File.Read(this.request.Path)
	if err != nil {
		this.response.Error = err
		return this.response
	}

	this.response.Byte = bytes
	this.response.Text = string(bytes)
	this.response.Result = bytes

	return this.response
}

// List 获取指定目录下的所有文件
func (this *FileStruct) List(path ...any) (result *FileResponse) {

	if len(path) != 0 {
		this.request.Path = cast.ToString(path[0])
	}

	if Is.Empty(this.request.Dir) {
		this.response.Error = errors.New("目录路径不能为空")
		return this.response
	}

	var slice []string
	this.response.Error = filepath.Walk(this.request.Dir, func(path string, info os.FileInfo, err error) error {
		// 遍历出错（如权限不足）时跳过该条目，避免 info 为 nil 崩溃
		if err != nil {
			return nil
		}
		// 忽略当前目录
		if info.IsDir() {
			return nil
		}
		// 忽略子目录
		if !this.request.Sub && filepath.Dir(path) != filepath.Clean(this.request.Dir) {
			return nil
		}
		// []string 转 []any
		var exts []any
		// this.request.Ext 逗号分隔的字符串 转 []string
		for _, val := range strings.Split(this.request.Ext, ",") {
			// 忽略空字符串
			if Is.Empty(val) {
				continue
			}
			// 去除空格
			exts = append(exts, strings.TrimSpace(val))
		}
		// 忽略指定后缀
		if !InArray("*", exts) && !InArray[any](filepath.Ext(path), exts) {
			return nil
		}
		slice = append(slice, path)
		return nil
	})

	// 转码为网络路径
	if this.request.Format == "network" {
		for key, val := range slice {
			slice[key] = filepath.ToSlash(val)
			if !Is.Empty(this.request.Domain) {
				slice[key] = this.request.Domain + slice[key][len(this.request.Prefix):]
			}
		}
	}

	for _, val := range slice {
		this.response.Slice = append(this.response.Slice, val)
	}
	this.response.Result = slice
	this.response.Text = strings.Join(slice, ",")
	this.response.Byte = []byte(this.response.Text)

	return this.response
}

// Exist 判断文件或目录是否存在
func (this *FileStruct) Exist(path ...any) (ok bool) {

	if len(path) != 0 {
		this.request.Path = cast.ToString(path[0])
	}

	// Path 为空时回退检查 Dir（.Dir(p).Exist() 用于判断目录是否存在）
	target := this.request.Path
	if Is.Empty(target) {
		target = this.request.Dir
	}

	if Is.Empty(target) {
		return false
	}

	// 委托 File 判断文件或目录是否存在
	return File.Exist(target)
}

// Line 按行读取文件
func (this *FileStruct) Line(path ...any) (result *FileResponse) {

	if len(path) != 0 {
		this.request.Path = cast.ToString(path[0])
	}

	if Is.Empty(this.request.Path) {
		this.response.Error = errors.New("文件路径不能为空")
		return this.response
	}

	// 读取指定区间的行
	file, err := os.Open(this.request.Path)
	if err != nil {
		this.response.Error = err
		return this.response
	}
	defer func() { _ = file.Close() }()

	end := this.request.Page * this.request.Limit
	start := end - this.request.Limit + 1

	// 顺序扫描文件，只保留 [start, end] 区间的行
	lines := make([]string, 0)
	scanner := bufio.NewScanner(file)
	current := 0
	for scanner.Scan() {
		current++
		if current < start {
			continue
		}
		if current > end {
			break
		}
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		this.response.Error = err
		return this.response
	}

	this.response.Result = lines
	this.response.Text = Json.Encode(this.response.Result)
	this.response.Byte = []byte(this.response.Text)

	for _, v := range lines {
		this.response.Slice = append(this.response.Slice, v)
	}

	return this.response
}

// DirInfo 获取目录信息
func (this *FileStruct) DirInfo(dir ...any) (result *FileResponse) {

	if len(dir) != 0 {
		this.request.Dir = cast.ToString(dir[0])
	}

	if Is.Empty(this.request.Dir) {
		this.request.Dir = "./"
	}

	// 如果目录不是以 / 结尾，则补上
	if this.request.Dir[len(this.request.Dir)-1:] != "/" {
		this.request.Dir += "/"
	}

	// 获取目录信息（目录不存在时直接返回错误）
	fileInfo, err := os.Stat(this.request.Dir)
	if err != nil {
		this.response.Error = err
		return this.response
	}

	var dirs []string
	var files []string

	// 只获取当前目录下的文件夹和文件 - 忽略子目录
	fileList, err := os.ReadDir(this.request.Dir)
	if err != nil {
		this.response.Error = err
		return this.response
	}
	for _, file := range fileList {
		path := filepath.Join(this.request.Dir, file.Name())
		// path 转网络路径
		path = filepath.ToSlash(path)
		// 替换 this.request.Dir 为空字符串
		path = strings.Replace(path, this.request.Dir, "", 1)

		if file.IsDir() {
			dirs = append(dirs, path)
		} else {
			files = append(files, path)
		}
	}

	// 获取目录信息
	this.response.Result = map[string]any{
		"info":  fileInfo,
		"dirs":  dirs,
		"files": files,
	}
	this.response.Text = Json.Encode(this.response.Result)
	this.response.Byte = []byte(this.response.Text)

	return this.response
}

// EnZip 压缩文件
/**
 * @return *FileResponse
 * @example：
 * 1. item := utils.NewFile().Dir("public").Name("name.zip").EnZip()
 * 2. item := utils.NewFile().Dir("public").Path("public/name.zip").EnZip()
 * 3. item := utils.NewFile(utils.FileRequest{
	Path: "public/name.zip",
	Dir: "public",
}).EnZip()
*/
func (this *FileStruct) EnZip() (result *FileResponse) {

	if Is.Empty(this.request.Dir) {
		this.response.Error = errors.New("压缩目录不能为空")
		return this.response
	}

	if Is.Empty(this.request.Path) && !Is.Empty(this.request.Name) {

		// 判断 Dir 是否以 / 结尾
		if this.request.Dir[len(this.request.Dir)-1:] != "/" {
			this.request.Dir += "/"
		}

		// 判断 Name 是否以 .zip 结尾
		if this.request.Name[len(this.request.Name)-4:] != ".zip" {
			this.request.Name += ".zip"
		}

		this.request.Path = this.request.Dir + this.request.Name
	}

	if Is.Empty(this.request.Path) {
		this.response.Error = errors.New("压缩后的文件路径不能为空")
		return this.response
	}

	// 判断目录是否存在
	if _, err := os.Stat(this.request.Dir); os.IsNotExist(err) {
		this.response.Error = err
		return this.response
	}

	var files []string
	err := filepath.Walk(this.request.Dir, func(path string, info os.FileInfo, err error) error {
		// 遍历出错（如权限不足）时返回错误，终止遍历
		if err != nil {
			return err
		}
		if !info.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		this.response.Error = err
		return this.response
	}

	zipFile, err := os.Create(this.request.Path)
	if err != nil {
		this.response.Error = err
		return this.response
	}
	// 兜底关闭，提前返回时不泄漏句柄
	defer func() { _ = zipFile.Close() }()

	write := zip.NewWriter(zipFile)

	for _, file := range files {

		item, err := os.Open(file)
		if err != nil {
			this.response.Error = err
			return this.response
		}

		info, err := item.Stat()
		if err != nil {
			_ = item.Close()
			this.response.Error = err
			return this.response
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			_ = item.Close()
			this.response.Error = err
			return this.response
		}

		header.Name = file
		header.Method = zip.Deflate

		writer, err := write.CreateHeader(header)
		if err != nil {
			_ = item.Close()
			this.response.Error = err
			return this.response
		}

		_, err = io.Copy(writer, item)
		// 每次迭代显式关闭，避免句柄累积到函数结束
		_ = item.Close()
		if err != nil {
			this.response.Error = err
			return this.response
		}
	}

	// 显式关闭 zip 写入器，确保中央目录完整写入
	if err := write.Close(); err != nil {
		this.response.Error = err
		return this.response
	}

	// 显式关闭文件（defer 兜底会再次关闭，返回的 ErrClosed 被忽略）
	if err := zipFile.Close(); err != nil {
		this.response.Error = err
		return this.response
	}

	this.response.Text = "1"
	this.response.Result = true
	this.response.Byte = []byte{1}

	return this.response
}

// UnZip 解压文件
/**
 * @return *FileResponse
 * @example：
 * 1. item := utils.NewFile().Dir("public").Name("name.zip").UnZip()
 * 2. item := utils.NewFile().Dir("public").Path("public/name.zip").UnZip()
 * 3. item := utils.NewFile(utils.FileRequest{
	Path: "public/name.zip",
	Dir: "public",
}).UnZip()
*/
func (this *FileStruct) UnZip() (result *FileResponse) {

	if Is.Empty(this.request.Dir) {
		this.response.Error = errors.New("解压路径不能为空")
		return this.response
	}

	if Is.Empty(this.request.Path) && !Is.Empty(this.request.Name) {

		// 判断 Dir 是否以 / 结尾
		if this.request.Dir[len(this.request.Dir)-1:] != "/" {
			this.request.Dir += "/"
		}

		// 判断 Name 是否以 .zip 结尾
		if this.request.Name[len(this.request.Name)-4:] != ".zip" {
			this.request.Name += ".zip"
		}

		this.request.Path = this.request.Dir + this.request.Name
	}

	if Is.Empty(this.request.Path) {
		this.response.Error = errors.New("压缩包路径不能为空")
		return this.response
	}

	// 判断压缩包是否存在
	if _, err := os.Stat(this.request.Path); os.IsNotExist(err) {
		this.response.Error = err
		return this.response
	}

	// 读取压缩包
	read, err := zip.OpenReader(this.request.Path)
	if err != nil {
		this.response.Error = err
		return this.response
	}
	defer func() { _ = read.Close() }()

	for _, file := range read.File {
		// 解压文件
		err := this.extract(file, this.request.Dir)
		if err != nil {
			this.response.Error = err
			return this.response
		}
	}

	this.response.Text = "1"
	this.response.Result = true
	this.response.Byte = []byte{1}

	return this.response
}

// 提取文件
func (this *FileStruct) extract(file *zip.File, dir string) (err error) {

	read, err := file.Open()
	if err != nil {
		return err
	}
	defer func() { _ = read.Close() }()

	path := filepath.Join(dir, file.Name)

	// 防止 ZipSlip：解压后的路径必须位于目标目录内
	if !strings.HasPrefix(path, filepath.Clean(dir)+string(os.PathSeparator)) {
		return errors.New("非法的压缩包路径: " + file.Name)
	}

	if file.FileInfo().IsDir() {

		err := os.MkdirAll(path, file.Mode())
		if err != nil {
			return err
		}

	} else {

		err := os.MkdirAll(filepath.Dir(path), os.ModePerm)
		if err != nil {
			return err
		}
		item, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())

		if err != nil {
			return err
		}
		defer func() { _ = item.Close() }()

		_, err = io.Copy(item, read)
		if err != nil {
			return err
		}
	}

	return nil
}

// Rename 重命名文件
func (this *FileStruct) Rename(path ...any) (result *FileResponse) {

	if len(path) != 0 {
		this.request.Path = cast.ToString(path[0])
	}

	if Is.Empty(this.request.Path) {
		this.response.Error = errors.New("文件路径不能为空")
		return this.response
	}

	if Is.Empty(this.request.Name) {
		this.response.Error = errors.New("文件名不能为空")
		return this.response
	}

	// 判断文件是否存在
	if _, err := os.Stat(this.request.Path); os.IsNotExist(err) {
		this.response.Error = err
		return this.response
	}

	// 重命名文件 - 放在同一个目录下
	err := os.Rename(this.request.Path, filepath.Dir(this.request.Path)+"/"+this.request.Name)
	if err != nil {
		this.response.Error = err
		return this.response
	}

	this.response.Text = "1"
	this.response.Result = true
	this.response.Byte = []byte{1}

	return this.response
}

// ============================================================
// 公共类型
// ============================================================

// FileStruct - File 结构体
type FileStruct struct {
	request  *FileRequest
	response *FileResponse
}

// FileRequest - File 请求
type FileRequest struct {
	// 文件名
	Name string
	// 文件路径（包含文件名）
	Path string
	// 目录路径（不包含文件名）
	Dir string
	// 文件后缀
	Ext string
	// 限制行数
	Limit int
	// 读取偏移量
	Page int
	// 返回结果格式
	Format string
	// 是否包含子目录（构造时默认递归为 true，显式传入 false 也会被重置，可用 .Sub(false) 关闭）
	Sub bool
	// 域名 - 用于拼接文件路径
	Domain string
	// 前缀 - 用于过滤前缀
	Prefix string
}

// FileResponse - File 响应
type FileResponse struct {
	Error  error
	Result any
	Text   string
	Byte   []byte
	Slice  []any
}

// FileInfo - 文件信息结构体
type FileInfo struct {
	Path    string      `json:"path"`
	Name    string      `json:"name"`
	Size    int64       `json:"size"`
	Mode    fs.FileMode `json:"mode"`
	ModTime time.Time   `json:"modTime"`
	IsDir   bool        `json:"isDir"`
	Sys     any         `json:"sys"`
}

// ProgressReader - 进度读取器，用于显示下载进度
type ProgressReader struct {
	Reader     io.Reader
	Total      int64
	Reading    int64
	OnProgress func(read int64)
	LastUpdate time.Time
}

// Read 读取数据并统计进度
func (this *ProgressReader) Read(b []byte) (n int, err error) {

	n, err = this.Reader.Read(b)
	this.Reading += int64(n)

	// 每500毫秒更新一次进度，避免刷屏
	if time.Since(this.LastUpdate) > 500*time.Millisecond {
		if this.OnProgress != nil {
			this.OnProgress(this.Reading)
		}
		this.LastUpdate = time.Now()
	}

	return
}

// FileWatcher - 文件监听器
type FileWatcher struct {
	watcher  *fsnotify.Watcher
	filePath string
	callback func(event fsnotify.Event)
	stopChan chan bool
	// 防抖相关
	mu        sync.Mutex
	timer     *time.Timer
	lastEvent fsnotify.Event
	debounce  time.Duration
	// 停止标记 - 保证 Stop 幂等
	stopped bool
}

// Start 开始监听文件变更
func (this *FileWatcher) Start() {
	go func() {
		for {
			select {
			case event, ok := <-this.watcher.Events:
				if !ok {
					return
				}
				// 只处理目标文件的变更事件
				if filepath.Clean(event.Name) == this.filePath {
					// 过滤掉一些不需要的事件，比如访问事件
					if event.Op.Has(fsnotify.Write) || event.Op.Has(fsnotify.Remove) || event.Op.Has(fsnotify.Rename) || event.Op.Has(fsnotify.Create) {
						// 使用防抖：如果在短时间内收到多个事件，只执行一次回调
						this.mu.Lock()
						this.lastEvent = event
						if this.timer != nil {
							this.timer.Stop()
						}
						// 创建一个新的定时器，定时器触发时调用回调
						this.timer = time.AfterFunc(this.debounce, func() {
							this.mu.Lock()
							ev := this.lastEvent
							this.mu.Unlock()
							this.callback(ev)
						})
						this.mu.Unlock()
					}
				}
			case _, ok := <-this.watcher.Errors:
				if !ok {
					return
				}
			case <-this.stopChan:
				return
			}
		}
	}()
}

// Stop 停止监听（幂等，可重复调用，未调用 Start 时也不会阻塞）
func (this *FileWatcher) Stop() {

	this.mu.Lock()
	if this.stopped {
		this.mu.Unlock()
		return
	}
	this.stopped = true
	this.mu.Unlock()

	// 关闭通道通知监听协程退出（替代发送，避免未 Start 时永久阻塞）
	close(this.stopChan)

	if this.timer != nil {
		this.timer.Stop()
	}
	_ = this.watcher.Close()
}
