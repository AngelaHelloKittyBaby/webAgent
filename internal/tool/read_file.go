package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// ReadFile 读取一个文本文件的内容。
// path 相对项目根目录（进程工作目录）或绝对路径均可——读不限制越界。
type ReadFile struct{}

func NewReadFile() *ReadFile { return &ReadFile{} }

func (t *ReadFile) Spec() Spec {
	return Spec{
		Name:        "read_file",
		Description: "读取项目内一个文本文件的完整内容。path 是文件路径（相对项目根目录或绝对路径）。读代码、读配置、读文档都用它。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "文件路径，相对项目根目录或绝对路径"}
			},
			"required": ["path"],
			"additionalProperties": false
		}`),
	}
}

type readFileArgs struct {
	Path string `json:"path"`
}

func (t *ReadFile) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var a readFileArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("read_file 参数解析失败: %w", err)
	}
	if a.Path == "" {
		return "", fmt.Errorf("read_file 缺少 path 参数")
	}
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return "", fmt.Errorf("读取失败: %w", err)
	}
	return truncateRunes(string(data), maxOutputRunes), nil
}