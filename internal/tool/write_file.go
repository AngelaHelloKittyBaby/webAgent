package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WriteFile 创建或覆盖一个文本文件。
// root 是默认基准（项目根目录）：相对 path 拼到 root 下（默认写根目录内/output/）；
// 绝对路径直接写入用户指定位置（越界授权由 agent 侧的确认机制承担）。
type WriteFile struct {
	root string
}

func NewWriteFile(root string) *WriteFile {
	if root == "" {
		root = "."
	}
	return &WriteFile{root: root}
}

func (t *WriteFile) Spec() Spec {
	return Spec{
		Name:        "write_file",
		Description: "创建或覆盖一个文本文件，父目录会自动创建。path 相对项目根目录（如 output/x.txt）或用户指定的绝对路径；content 是完整内容。改代码、写配置、写文档都用它。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "文件路径，相对项目根目录或绝对路径"},
				"content": {"type": "string", "description": "文件的完整内容"}
			},
			"required": ["path", "content"],
			"additionalProperties": false
		}`),
	}
}

type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// resolveTarget 把 path 解析成实际写入路径：相对拼 root，绝对直接用。
func (t *WriteFile) resolveTarget(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(t.root, path)
}

func (t *WriteFile) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var a writeFileArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("write_file 参数解析失败: %w", err)
	}
	if a.Path == "" {
		return "", fmt.Errorf("write_file 缺少 path 参数")
	}
	full := t.resolveTarget(a.Path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", fmt.Errorf("创建父目录失败: %w", err)
	}
	if err := os.WriteFile(full, []byte(a.Content), 0o644); err != nil {
		return "", fmt.Errorf("写入失败: %w", err)
	}
	return fmt.Sprintf("已写入 %s（%d 字节）", a.Path, len(a.Content)), nil
}