package skill

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestInstallSkill 构造 Manager 并把 HTTP 客户端指到 srv，返回 install_skill 工具。
func newTestInstallSkill(t *testing.T, dir string, srv *httptest.Server) *InstallSkill {
	t.Helper()
	m := NewManager(dir)
	m.client = srv.Client()
	return NewInstallSkill(m)
}

func TestInstallSkill(t *testing.T) {
	dir := t.TempDir()
	const good = "---\nname: deploy\ndescription: 部署\n---\n\n# 步骤\n1. build"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		io.WriteString(w, good)
	}))
	defer srv.Close()

	it := newTestInstallSkill(t, dir, srv)

	out, err := it.Execute(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`/skills/deploy.md"}`))
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if !strings.Contains(out, "deploy") {
		t.Fatalf("输出不对: %s", out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "deploy.md"))
	if err != nil {
		t.Fatalf("文件未写入: %v", err)
	}
	if string(data) != good {
		t.Fatalf("内容不对: %q", data)
	}
}

func TestInstallSkillSanitizesName(t *testing.T) {
	// 恶意文件名：../、反斜杠、冒号都要被净化，防路径穿越
	if got := sanitizeSkillName("../evil"); got != "evil" {
		t.Fatalf("期望 evil，实际 %q", got)
	}
	if got := sanitizeSkillName(`C:\windows\evil`); got != "Cwindowsevil" {
		t.Fatalf("净化不对: %q", got)
	}
	if got := sanitizeSkillName("my.skill.md"); got != "myskill" {
		t.Fatalf("净化不对: %q", got)
	}
}

func TestInstallSkillHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, "not found")
	}))
	defer srv.Close()

	it := newTestInstallSkill(t, t.TempDir(), srv)

	_, err := it.Execute(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`/x.md"}`))
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("应报 HTTP 错误，实际: %v", err)
	}
}

func TestInstallSkillTooBig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Repeat("a", 100))
	}))
	defer srv.Close()

	it := newTestInstallSkill(t, t.TempDir(), srv)
	it.mgr.maxBytes = 10

	if _, err := it.Execute(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`/x.md"}`)); err == nil {
		t.Fatal("超限应报错")
	}
}

func TestInstallSkillNameParam(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "---\nname: x\ndescription: x\n---\n\nbody")
	}))
	defer srv.Close()

	it := newTestInstallSkill(t, dir, srv)

	// URL 路径是 "/"，推断不出名字，但 name 参数给了
	if _, err := it.Execute(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`/","name":"my-skill"}`)); err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "my-skill.md")); err != nil {
		t.Fatalf("应写入 my-skill.md: %v", err)
	}
}