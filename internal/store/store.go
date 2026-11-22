// Package store 负责会话的持久化：对话历史存 JSON，摘要存 Markdown。
// 只做文件读写，不掺和任何模型逻辑。
package store

import (
	"encoding/json"
	"os"
	"path/filepath"

	"webagent/internal/llm"
)

const (
	conversationFile = "conversation.json"
	summaryFile      = "summary.md"
)

// conversation 是落盘格式。llm.Message 自带 json tag，直接复用，不用再写一套映射。
type conversation struct {
	Messages []llm.Message `json:"messages"`
}

// LoadConversation 读取对话历史；文件不存在时返回空切片（不算错误）。
func LoadConversation(dir string) ([]llm.Message, error) {
	data, err := os.ReadFile(filepath.Join(dir, conversationFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c conversation
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return c.Messages, nil
}

// SaveConversation 把对话历史写成 JSON（目录不存在会自动创建）。
func SaveConversation(dir string, conv []llm.Message) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(conversation{Messages: conv}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, conversationFile), data, 0o644)
}

// LoadSummary 读取摘要；文件不存在返回空字符串。
func LoadSummary(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, summaryFile))
	if err != nil {
		return ""
	}
	return string(data)
}

// SaveSummary 把摘要写成 Markdown。
func SaveSummary(dir, text string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, summaryFile), []byte(text), 0o644)
}
