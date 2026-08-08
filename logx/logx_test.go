package logx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inis-io/aide/utils"
)

// readLogLines - 读取级别文件的全部日志行（文件不存在返回 nil）
func readLogLines(t *testing.T, root, name string) []map[string]any {
	t.Helper()

	path := filepath.Join(root, time.Now().Format("2006-01-02"), name+".log")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("读取日志文件失败：%v", err)
	}

	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if utils.Is.Empty(line) {
			continue
		}
		var item map[string]any
		if err = utils.Json.Unmarshal([]byte(line), &item); err != nil {
			t.Fatalf("日志行不是合法 JSON：%q", line)
		}
		lines = append(lines, item)
	}
	return lines
}

// TestNormConfig - 验证配置归一化：默认值补齐、Disable 零值为启用、Hash 自动计算
func TestNormConfig(t *testing.T) {

	conf := normConfig(Config{})
	if conf.Disable {
		t.Fatal("Disable 零值应为启用")
	}
	if conf.Root != "runtime/logs" || conf.Level != "debug" {
		t.Fatalf("默认根目录或级别不符：%+v", conf)
	}
	if conf.Size != 10 || conf.Age != 7 || conf.Backups != 20 {
		t.Fatalf("切割默认值不符：%+v", conf)
	}
	if conf.Hash == "" {
		t.Fatal("Hash 应自动计算")
	}
}

// TestLoggerWrite - 验证写入：级别文件精确收录、字段输出、空消息回退级别名
func TestLoggerWrite(t *testing.T) {

	root := t.TempDir()
	logger := New(Config{Root: root})
	t.Cleanup(logger.Close)

	logger.Info("user login", map[string]any{"uid": 1001})
	logger.Warn("slow query")
	logger.Error("pay failed")
	logger.Debug("trace")
	logger.Info("") // 空消息回退级别名
	logger.Sync()

	// info.log 精确收录两条 info（含空消息回退），不混其他级别
	infos := readLogLines(t, root, "info")
	if len(infos) != 2 {
		t.Fatalf("info.log 应有 2 条，实际 %d：%v", len(infos), infos)
	}
	if infos[0]["level"] != "info" || infos[0]["msg"] != "user login" {
		t.Fatalf("info 日志内容不符：%v", infos[0])
	}
	if infos[0]["uid"] != float64(1001) {
		t.Fatalf("字段输出不符：%v", infos[0])
	}
	if infos[1]["msg"] != "info" {
		t.Fatalf("空消息应回退级别名，实际 %v", infos[1]["msg"])
	}

	// 其余级别文件各自精确收录一条
	for name, msg := range map[string]string{"warn": "slow query", "error": "pay failed", "debug": "trace"} {
		lines := readLogLines(t, root, name)
		if len(lines) != 1 || lines[0]["msg"] != msg || lines[0]["level"] != name {
			t.Fatalf("%s.log 收录不符：%v", name, lines)
		}
	}

	// error 自动附带堆栈
	if stack, ok := readLogLines(t, root, "error")[0]["stacktrace"].(string); !ok || utils.Is.Empty(stack) {
		t.Fatal("error 日志应附带堆栈")
	}
}

// TestLoggerLevelThreshold - 验证最低级别：低于阈值的级别文件不创建
func TestLoggerLevelThreshold(t *testing.T) {

	root := t.TempDir()
	logger := New(Config{Root: root, Level: "warn"})
	t.Cleanup(logger.Close)

	logger.Debug("x")
	logger.Info("x")
	logger.Warn("y")
	logger.Error("z")
	logger.Sync()

	if readLogLines(t, root, "debug") != nil || readLogLines(t, root, "info") != nil {
		t.Fatal("低于阈值的级别文件不应创建")
	}
	if len(readLogLines(t, root, "warn")) != 1 || len(readLogLines(t, root, "error")) != 1 {
		t.Fatal("达到阈值的级别应正常收录")
	}
}

// TestLoggerDisable - 验证关闭日志：不产生任何落盘文件
func TestLoggerDisable(t *testing.T) {

	root := t.TempDir()
	logger := New(Config{Root: root, Disable: true})
	t.Cleanup(logger.Close)

	logger.Info("x")
	logger.Error("y")
	logger.Sync()

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("读取目录失败：%v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("Disable 时不应产生文件：%v", entries)
	}
}

// TestLoggerWith - 验证派生子实例：固定字段随行、原实例不受影响、多字段表合并覆盖
func TestLoggerWith(t *testing.T) {

	root := t.TempDir()
	base := New(Config{Root: root})
	t.Cleanup(base.Close)

	req := base.With(map[string]any{"traceId": "T-1"})
	req.Error("pay failed", map[string]any{"orderId": "O-1"})
	base.Error("plain")
	base.Info("merge", map[string]any{"a": 1}, map[string]any{"a": 2, "b": 3})
	base.Sync()

	errors := readLogLines(t, root, "error")
	if len(errors) != 2 {
		t.Fatalf("error.log 应有 2 条，实际 %d", len(errors))
	}
	if errors[0]["traceId"] != "T-1" || errors[0]["orderId"] != "O-1" {
		t.Fatalf("派生实例应带固定字段与调用字段：%v", errors[0])
	}
	if _, ok := errors[1]["traceId"]; ok {
		t.Fatal("原实例不应受派生字段影响")
	}

	infos := readLogLines(t, root, "info")
	if infos[0]["a"] != float64(2) || infos[0]["b"] != float64(3) {
		t.Fatalf("多字段表应按序合并覆盖：%v", infos[0])
	}
}

// TestLoggerCaller - 验证调用位置：指向业务调用方而非包装层
func TestLoggerCaller(t *testing.T) {

	root := t.TempDir()
	logger := New(Config{Root: root})
	t.Cleanup(logger.Close)
	logger.Info("where")
	logger.Sync()

	infos := readLogLines(t, root, "info")
	if len(infos) != 1 {
		t.Fatalf("info.log 应有 1 条，实际 %d", len(infos))
	}
	caller, ok := infos[0]["caller"].(string)
	if !ok || !strings.Contains(caller, "logx_test.go") {
		t.Fatalf("caller 应指向测试文件，实际 %v", infos[0]["caller"])
	}
}

// TestControllerInitAndReload - 验证控制器：注入配置、Hash 变化触发热重载、重载后落盘到新目录
func TestControllerInitAndReload(t *testing.T) {

	root1 := t.TempDir()
	root2 := t.TempDir()

	inst := &Controller{}
	inst.Init(Config{Root: root1})
	Log.Info("first")
	if len(readLogLines(t, root1, "info")) != 1 {
		t.Fatal("注入配置后应落盘到 root1")
	}

	// 配置未变化时不重载
	before := Log
	inst.ReloadIfChanged()
	if Log != before {
		t.Fatal("配置未变化不应重载")
	}

	// 配置变化后重载到新实例
	inst.ReloadIfChanged(Config{Root: root2})
	if Log == before {
		t.Fatal("配置变化后应重载")
	}
	Log.Info("second")
	if len(readLogLines(t, root2, "info")) != 1 {
		t.Fatal("重载后应落盘到 root2")
	}

	// 测试结束后恢复默认门面，避免影响其他用例
	inst.HasConfig = false
	inst.useDefault()
}
