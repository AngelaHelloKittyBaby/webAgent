package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Review 对项目做一次静态代码审查（review_code）。
// 运行 gofmt -l（未格式化文件）、go vet ./...（静态检查）、go build ./...（编译），
// 汇总每项的退出码和输出。发现问题后模型再 read_file 相关文件并给出修复建议。
type Review struct{}

func NewReview() *Review { return &Review{} }

func (t *Review) Spec() Spec {
	return Spec{
		Name: "review_code",
		Description: "对项目做一次静态代码审查：运行 gofmt -l 找未格式化文件、go vet ./... 静态检查、go build ./... 编译，" +
			"汇总退出码和输出。发现问题后用 read_file 查看对应文件并给出修复建议。无参数。",
		Parameters: json.RawMessage(`{"type":"object","properties":{}}`),
	}
}

// check 是审查清单里的一项。
type check struct {
	title string
	cmd   string
	args  []string
}

func (t *Review) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// gofmt 需要收集项目自己的 .go 文件，排除隐藏目录（.git/.gopath）和
	// vendor/node_modules，否则会把依赖缓存的第三方源码也扫进来。
	goFiles, err := collectGoFiles(".")
	if err != nil {
		return "", fmt.Errorf("收集 Go 文件失败: %w", err)
	}
	gofmtArgs := append([]string{"-l"}, goFiles...)

	checks := []check{
		{title: "gofmt（格式检查）", cmd: "gofmt", args: gofmtArgs},
		{title: "go vet（静态检查）", cmd: "go", args: []string{"vet", "./..."}},
		{title: "go build（编译）", cmd: "go", args: []string{"build", "./..."}},
	}

	var b strings.Builder
	b.WriteString("# 代码审查报告\n")
	found := false

	for _, c := range checks {
		ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
		out, runErr := runCmd(ctx2, c.cmd, c.args...)
		cancel()

		fmt.Fprintf(&b, "\n## %s\n", c.title)
		switch {
		case runErr == nil && strings.TrimSpace(out) == "":
			fmt.Fprintln(&b, "✅ 通过")
		case runErr == nil:
			found = true
			fmt.Fprintln(&b, truncateRunes(out, maxOutputRunes))
		default:
			found = true
			if strings.TrimSpace(out) != "" {
				fmt.Fprintf(&b, "❌ %s\n", truncateRunes(out, maxOutputRunes))
			} else {
				fmt.Fprintf(&b, "❌ %v\n", runErr)
			}
		}
	}

	if !found {
		b.WriteString("\n未发现格式、静态检查或编译问题。\n")
	}
	return b.String(), nil
}

// collectGoFiles 递归收集 root 下的 .go 文件，跳过隐藏目录、vendor 和 node_modules。
func collectGoFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "vendor" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".go") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// runCmd 执行命令并返回综合输出（不区分 stdout/stderr）。
func runCmd(ctx context.Context, name string, arg ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, arg...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}