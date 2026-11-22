package skill

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"webagent/internal/llm"
)

const sampleSkill = `---
name: go-build
description: 编译和测试 Go 项目
---

# 步骤
1. go build ./...
2. go test ./...
`

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go-build.md"), []byte(sampleSkill), 0o644); err != nil {
		t.Fatal(err)
	}

	skills, err := Load(dir)
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("应加载 1 个技能，实际 %d", len(skills))
	}
	s := skills[0]
	if s.Name != "go-build" || s.Description != "编译和测试 Go 项目" {
		t.Fatalf("frontmatter 解析不对: %+v", s)
	}
	if !strings.Contains(s.Content, "go build ./...") {
		t.Fatalf("正文不对: %q", s.Content)
	}
}

func TestLoadMissingDir(t *testing.T) {
	skills, err := Load(filepath.Join(t.TempDir(), "no-such"))
	if err != nil {
		t.Fatalf("目录不存在不应报错: %v", err)
	}
	if skills != nil {
		t.Fatalf("应返回 nil，实际 %+v", skills)
	}
}

func TestManagerLoadSkill(t *testing.T) {
	m := NewManager("")
	m.skills = []Skill{{Name: "go-build", Description: "x", Content: "内容"}}

	// Specs 对模型可见
	specs := m.Specs()
	if len(specs) != 2 || specs[0].Name != "load_skill" || specs[1].Name != "install_skill" {
		t.Fatalf("specs 不对: %+v", specs)
	}

	out, err := m.Handle(context.Background(), llm.ToolCall{Name: "load_skill", Arguments: json.RawMessage(`{"name":"go-build"}`)})
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if out != "内容" {
		t.Fatalf("内容不对: %q", out)
	}

	if _, err := m.Handle(context.Background(), llm.ToolCall{Name: "load_skill", Arguments: json.RawMessage(`{"name":"nope"}`)}); err == nil {
		t.Fatal("不存在的技能应当报错")
	}

	// 其它名字：未知技能操作（agent 会落到常规工具查找）
	if _, err := m.Handle(context.Background(), llm.ToolCall{Name: "exec", Arguments: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("未知技能操作应报错")
	}
}

func TestCatalog(t *testing.T) {
	c := Catalog([]Skill{{Name: "go-build", Description: "编译"}})
	if !strings.Contains(c, "go-build") || !strings.Contains(c, "编译") {
		t.Fatalf("清单不对: %q", c)
	}
	if Catalog(nil) != "" {
		t.Fatalf("空技能应返回空串")
	}
}
