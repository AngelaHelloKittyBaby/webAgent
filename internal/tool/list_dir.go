package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// ListDir 列出目录下的文件和子目录，目录项带 "/" 后缀。
// path 相对项目根目录或绝对路径均可。
type ListDir struct{}

func NewListDir() *ListDir { return &ListDir{} }

func (t *ListDir) Spec() Spec {
	return Spec{
		Name:        "list_dir",
		Description: "列出目录下的文件和子目录，目录项带 / 后缀。path 是目录路径（相对项目根目录或绝对路径），留空或 \".\" 表示项目根。想摸清项目结构时用它。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "目录路径，默认 \".\""}
			},
			"additionalProperties": false
		}`),
	}
}

type listDirArgs struct {
	Path string `json:"path"`
}

func (t *ListDir) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var a listDirArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("list_dir 参数解析失败: %w", err)
	}
	if strings.TrimSpace(a.Path) == "" {
		a.Path = "."
	}
	entries, err := os.ReadDir(a.Path)
	if err != nil {
		return "", fmt.Errorf("列目录失败: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return fmt.Sprintf("%s 下共 %d 项：\n%s", a.Path, len(names), strings.Join(names, "\n")), nil
}