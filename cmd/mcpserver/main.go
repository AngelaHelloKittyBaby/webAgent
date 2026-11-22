// Command mcpserver 是 webagent 的 MCP server：提供全部执行类工具
// （read_file / list_dir / write_file / exec）。
//
// webagent 通过 stdio 连接它，把它的工具接进 agent；agent 不再内置这些工具，
// 全部能力来自本进程（"全部接上 MCP"）。
//
// 构建：go build -o mcpserver.exe ./cmd/mcpserver
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// root 是所有路径的基准（项目根目录，server 的工作目录）。
const root = "."

// execTimeout 单条命令的超时上限。
const execTimeout = 30 * time.Second

// maxOutputRunes 限制返回给模型的内容长度，防止塞爆上下文。
const maxOutputRunes = 32000

func main() {
	s := server.NewMCPServer("webagent-tools", "1.0.0")

	s.AddTool(
		mcp.NewTool("read_file",
			mcp.WithDescription("读取项目内一个文本文件的完整内容"),
			mcp.WithString("path", mcp.Required(), mcp.Description("相对项目根目录的文件路径")),
		),
		readFileHandler,
	)
	s.AddTool(
		mcp.NewTool("list_dir",
			mcp.WithDescription("列出项目内一个目录下的文件和子目录，目录项带 / 后缀"),
			mcp.WithString("path", mcp.Description("相对项目根目录的目录路径，默认根目录")),
		),
		listDirHandler,
	)
	s.AddTool(
		mcp.NewTool("write_file",
			mcp.WithDescription("创建或覆盖项目内一个文本文件，父目录不存在会自动创建"),
			mcp.WithString("path", mcp.Required(), mcp.Description("相对项目根目录的文件路径")),
			mcp.WithString("content", mcp.Required(), mcp.Description("文件的完整内容")),
		),
		writeFileHandler,
	)
	s.AddTool(
		mcp.NewTool("exec",
			mcp.WithDescription("在项目根目录执行一条 shell 命令，返回退出码和输出，30 秒超时"),
			mcp.WithString("command", mcp.Required(), mcp.Description("要执行的命令，如 go build ./...")),
		),
		execHandler,
	)

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintln(os.Stderr, "mcpserver 退出:", err)
		os.Exit(1)
	}
}

// ---------- 工具逻辑（纯函数，便于测试） ----------

// resolvePath 把 path 解析成实际写入/读取的路径：
// 相对路径拼到项目根下；绝对路径直接用（用户指定 root 之外的位置）。
// 注意：这里不做越界拦截——读不破坏；写文件在 agent 侧调用前会经用户
// Confirm 审批（用户指定哪就写哪，包括项目根之外）。
func resolvePath(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}

// readFileByPath 读取一个文件的内容（相对 root 或绝对路径均可）。
func readFileByPath(root, path string) (string, error) {
	full := resolvePath(root, path)
	data, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("读取失败: %w", err)
	}
	return truncateRunes(string(data), maxOutputRunes), nil
}

// listDirByPath 列出一个目录（相对 root 或绝对路径均可），目录项带 / 后缀。
func listDirByPath(root, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	full := resolvePath(root, path)
	entries, err := os.ReadDir(full)
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
	return fmt.Sprintf("%s 下共 %d 项：\n%s", path, len(names), strings.Join(names, "\n")), nil
}

// writeFileByPath 写一个文件（相对 root 或绝对路径均可），父目录自动创建。
// 越界写入的授权由 agent 侧的 Confirm 审批承担，这里不拦。
func writeFileByPath(root, path, content string) (string, error) {
	full := resolvePath(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", fmt.Errorf("创建父目录失败: %w", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("写入失败: %w", err)
	}
	return fmt.Sprintf("已写入 %s（%d 字节）", path, len(content)), nil
}

// execCmd 在 root 内执行一条命令，返回退出码 + 输出（非零退出也算"结果"，
// 只有命令起不来/超时才返回 error）。
func execCmd(root, command string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), execTimeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	cmd.Dir = root // 工作目录限定项目根

	out, runErr := cmd.CombinedOutput()
	output := truncateRunes(string(out), maxOutputRunes)
	if runErr != nil {
		if ctx.Err() != nil {
			// 上层取消或我们自己的超时
			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Sprintf("（命令超时，已终止）\n%s", output), nil
			}
			return "", ctx.Err()
		}
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return fmt.Sprintf("（退出码 %d）\n%s", exitErr.ExitCode(), output), nil
		}
		return "", fmt.Errorf("执行命令失败: %w", runErr)
	}
	return fmt.Sprintf("（退出码 0）\n%s", output), nil
}

// ---------- MCP handler（薄壳：解析参数 → 调纯函数 → 映射错误） ----------

func readFileHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	out, err := readFileByPath(root, path)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(out), nil
}

func listDirHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path := "."
	if v, ok := req.GetArguments()["path"].(string); ok && strings.TrimSpace(v) != "" {
		path = v
	}
	out, err := listDirByPath(root, path)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(out), nil
}

func writeFileHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	content, err := req.RequireString("content")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	out, err := writeFileByPath(root, path, content)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(out), nil
}

func execHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	command, err := req.RequireString("command")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	out, err := execCmd(root, command)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(out), nil
}

// ---------- 小工具 ----------

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "\n…（输出过长，已截断）"
}