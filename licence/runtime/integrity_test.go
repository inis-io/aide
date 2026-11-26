package runtime

import (
	"os"
	"testing"
)

// selfBinaryBytes - 读取当前测试二进制内容（伪造发布物数据用，其 SHA-256 即本机锚点哈希）
func selfBinaryBytes(t *testing.T) []byte {

	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatalf("获取可执行文件路径失败: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取可执行文件失败: %v", err)
	}
	return data
}

// TestBinaryIntegrityOK - 当前版本清单发布物哈希与本机锚点一致 → OK
func TestBinaryIntegrityOK(t *testing.T) {

	platform := newFakePlatform(t)
	withUpdate(platform, "2.4.0", selfBinaryBytes(t))

	options := testOptions(platform, t.TempDir())
	options.Version = "2.4.0"
	client, err := New(options)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	if _, err = client.CheckUpdate(t.Context(), "linux/amd64"); err != nil {
		t.Fatalf("CheckUpdate 失败: %v", err)
	}
	state, expected, actual := client.BinaryIntegrity()
	if state != BinaryIntegrityOK {
		t.Fatalf("锚点一致应为 OK: state=%v expected=%s actual=%s", state, expected, actual)
	}
	if expected == "" || expected != actual {
		t.Fatalf("比对双方哈希应非空且一致: expected=%s actual=%s", expected, actual)
	}
}

// TestBinaryIntegrityViolation - 发布物哈希与本机锚点不一致（二进制被 patch）→ Violation
func TestBinaryIntegrityViolation(t *testing.T) {

	platform := newFakePlatform(t)
	withUpdate(platform, "2.4.0", []byte("official-release-bytes-not-this-binary"))

	options := testOptions(platform, t.TempDir())
	options.Version = "2.4.0"
	client, err := New(options)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	if _, err = client.CheckUpdate(t.Context(), "linux/amd64"); err != nil {
		t.Fatalf("CheckUpdate 失败: %v", err)
	}
	state, expected, actual := client.BinaryIntegrity()
	if state != BinaryIntegrityViolation {
		t.Fatalf("锚点不一致应为 Violation: state=%v expected=%s actual=%s", state, expected, actual)
	}
	if expected == "" || actual == "" || expected == actual {
		t.Fatalf("比对双方哈希应非空且不一致: expected=%s actual=%s", expected, actual)
	}
}

// TestBinaryIntegrityUnknownWithoutAnchor - 无匹配已发布版本（无锚）→ 保持 Unknown 且 fail-open
func TestBinaryIntegrityUnknownWithoutAnchor(t *testing.T) {

	platform := newFakePlatform(t)
	withUpdate(platform, "2.4.0", []byte("fake-artifact-bytes"))

	options := testOptions(platform, t.TempDir())
	options.Version = "1.0.0" // 平台无该版本的已发布记录 → 不下发 currentManifest
	client, err := New(options)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	if _, err = client.CheckUpdate(t.Context(), "linux/amd64"); err != nil {
		t.Fatalf("CheckUpdate 失败: %v", err)
	}
	state, expected, actual := client.BinaryIntegrity()
	if state != BinaryIntegrityUnknown {
		t.Fatalf("无锚应为 Unknown: state=%v", state)
	}
	if expected != "" || actual != "" {
		t.Fatalf("Unknown 不应有比对记录: expected=%s actual=%s", expected, actual)
	}
}

// TestBinaryIntegrityTamperedCurrentManifest - 当前版本清单被篡改 → 验签失败（fail-closed 报错）
func TestBinaryIntegrityTamperedCurrentManifest(t *testing.T) {

	platform := newFakePlatform(t)
	withUpdate(platform, "2.4.0", selfBinaryBytes(t))
	platform.mu.Lock()
	platform.tamperManifest = true
	platform.mu.Unlock()

	options := testOptions(platform, t.TempDir())
	options.Version = "2.4.0"
	client, err := New(options)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	if _, err = client.CheckUpdate(t.Context(), "linux/amd64"); err == nil {
		t.Fatalf("篡改清单必须验签失败")
	}
}
