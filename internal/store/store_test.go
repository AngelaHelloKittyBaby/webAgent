package store

import (
	"testing"

	"webagent/internal/llm"
)

func TestSaveLoadConversation(t *testing.T) {
	dir := t.TempDir()
	conv := []llm.Message{
		{Role: llm.RoleUser, Content: "你好"},
		{Role: llm.RoleAssistant, Content: "你好，有什么可以帮你？"},
	}

	if err := SaveConversation(dir, conv); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConversation(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Content != "你好" || got[1].Role != llm.RoleAssistant {
		t.Fatalf("加载结果不对: %+v", got)
	}
}

func TestLoadConversationMissing(t *testing.T) {
	// 空目录（没有 conversation.json）：应返回 nil 而非报错
	got, err := LoadConversation(t.TempDir())
	if err != nil {
		t.Fatalf("文件不存在不应报错: %v", err)
	}
	if got != nil {
		t.Fatalf("应返回 nil，实际 %+v", got)
	}
}

func TestSaveLoadSummary(t *testing.T) {
	dir := t.TempDir()
	if err := SaveSummary(dir, "要点：写了一个 agent"); err != nil {
		t.Fatal(err)
	}
	if got := LoadSummary(dir); got != "要点：写了一个 agent" {
		t.Fatalf("摘要不对: %q", got)
	}
}
