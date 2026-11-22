package tool

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func mustArgs(t *testing.T, m map[string]string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir) // 让相对路径以 dir 为基准
	if err := os.WriteFile("a.txt", []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := NewReadFile().Execute(context.Background(), mustArgs(t, map[string]string{"path": "a.txt"}))
	if err != nil || out != "hello" {
		t.Fatalf("相对路径读取不对: %q %v", out, err)
	}

	// 绝对路径
	abs := filepath.Join(dir, "a.txt")
	out, err = NewReadFile().Execute(context.Background(), mustArgs(t, map[string]string{"path": abs}))
	if err != nil || out != "hello" {
		t.Fatalf("绝对路径读取不对: %q %v", out, err)
	}

	if _, err := NewReadFile().Execute(context.Background(), mustArgs(t, map[string]string{"path": "nope.txt"})); err == nil {
		t.Fatal("不存在应报错")
	}
}

func TestListDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := NewListDir().Execute(context.Background(), mustArgs(t, map[string]string{"path": dir}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a.txt") || !strings.Contains(out, "sub/") {
		t.Fatalf("输出不对: %s", out)
	}

	// 空 path 默认当前目录
	if _, err := NewListDir().Execute(context.Background(), json.RawMessage(`{}`)); err != nil {
		t.Fatalf("空参数不应报错: %v", err)
	}
}

func TestWriteFile(t *testing.T) {
	root := t.TempDir()
	wf := NewWriteFile(root)

	// 相对路径 → 落在 root 下
	out, err := wf.Execute(context.Background(), mustArgs(t, map[string]string{"path": "a/b.txt", "content": "x"}))
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if !strings.Contains(out, "a/b.txt") {
		t.Fatalf("输出不对: %s", out)
	}
	if data, err := os.ReadFile(filepath.Join(root, "a", "b.txt")); err != nil || string(data) != "x" {
		t.Fatalf("写入不对: %q %v", data, err)
	}

	// 绝对路径 → 用户指定位置，直接写
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if _, err := wf.Execute(context.Background(), mustArgs(t, map[string]string{"path": outside, "content": "y"})); err != nil {
		t.Fatalf("绝对路径写入不应报错: %v", err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "y" {
		t.Fatalf("外部写入不对: %q %v", data, err)
	}
}

func TestExec(t *testing.T) {
	dir := t.TempDir()
	e := NewExec(dir)

	out, err := e.Execute(context.Background(), mustArgs(t, map[string]string{"command": "echo hello"}))
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if !strings.Contains(out, "hello") || !strings.Contains(out, "退出码 0") {
		t.Fatalf("输出不对: %s", out)
	}

	out, err = e.Execute(context.Background(), mustArgs(t, map[string]string{"command": "exit 3"}))
	if err != nil {
		t.Fatalf("非零退出不应作为 error: %v", err)
	}
	if !strings.Contains(out, "退出码 3") {
		t.Fatalf("应含退出码 3: %s", out)
	}
}

func TestReview(t *testing.T) {
	if _, err := exec.LookPath("gofmt"); err != nil {
		t.Skip("环境没有 gofmt，跳过")
	}
	dir := t.TempDir()
	t.Chdir(dir) // review 依赖工作目录
	if err := os.WriteFile("go.mod", []byte("module reviewtest\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 故意未格式化（该缩进没缩进）
	bad := "package main\n\nfunc main() {\nx := 1\nprintln(x)\n}\n"
	if err := os.WriteFile("main.go", []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := NewReview().Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if !strings.Contains(out, "代码审查报告") || !strings.Contains(out, "gofmt") {
		t.Fatalf("报告骨架不对: %.200s", out)
	}
	if !strings.Contains(out, "main.go") {
		t.Fatalf("gofmt 应检出 main.go: %.300s", out)
	}
}