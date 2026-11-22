package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileByPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 相对路径：解析到 root 下
	out, err := readFileByPath(dir, "a.txt")
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if out != "hello" {
		t.Fatalf("内容不对: %q", out)
	}
	// 绝对路径：直接用（读不限制越界）
	abs := filepath.Join(dir, "a.txt")
	out2, err := readFileByPath("其它根", abs)
	if err != nil || out2 != "hello" {
		t.Fatalf("绝对路径读取不对: %q %v", out2, err)
	}
	if _, err := readFileByPath(dir, "nope.txt"); err == nil {
		t.Fatal("不存在应报错")
	}
}

func TestListDirByPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := listDirByPath(dir, ".")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a.txt") || !strings.Contains(out, "sub/") {
		t.Fatalf("输出不对: %s", out)
	}
}

func TestWriteFileByPath(t *testing.T) {
	dir := t.TempDir()
	// 相对路径：写 root 下
	out, err := writeFileByPath(dir, "a/b/c.txt", "x")
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if !strings.Contains(out, "a/b/c.txt") {
		t.Fatalf("输出不对: %s", out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "a", "b", "c.txt"))
	if err != nil || string(data) != "x" {
		t.Fatalf("写入内容不对: %q %v", data, err)
	}

	// 绝对路径：写 root 之外（授权由 agent Confirm 承担，这里直接写）
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if _, err := writeFileByPath(dir, outside, "y"); err != nil {
		t.Fatalf("绝对路径写入不应报错: %v", err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "y" {
		t.Fatalf("越界写入内容不对: %q %v", data, err)
	}
}

func TestExecCmd(t *testing.T) {
	dir := t.TempDir()

	out, err := execCmd(dir, "echo hello")
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if !strings.Contains(out, "hello") || !strings.Contains(out, "退出码 0") {
		t.Fatalf("输出不对: %s", out)
	}

	// 非零退出：返回文本而非 error
	out, err = execCmd(dir, exitCommand(3))
	if err != nil {
		t.Fatalf("非零退出不应作为 error: %v", err)
	}
	if !strings.Contains(out, "退出码 3") {
		t.Fatalf("应含退出码 3: %s", out)
	}
}

// exitCommand 生成"退出码为 code"的命令，按平台选择。
func exitCommand(code int) string {
	if code == 3 {
		return "exit 3"
	}
	return "exit"
}