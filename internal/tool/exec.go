package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// execTimeout 单条命令的超时上限。常驻命令（开发服务器）不会自己退出，
// 会被这个超时杀掉；设短一点，避免 agent 整轮卡在这里“转圈不动”。
const execTimeout = 20 * time.Second

// Exec 在项目根目录执行一条 shell 命令，返回退出码和输出。
// 带三层防护：固定工作目录、单条命令超时、输出截断。
type Exec struct {
	dir     string
	timeout time.Duration
}

func NewExec(dir string) *Exec {
	if dir == "" {
		dir = "."
	}
	return &Exec{dir: dir, timeout: execTimeout}
}

func (t *Exec) Spec() Spec {
	return Spec{
		Name:        "exec",
		Description: "在项目根目录执行一条 shell 命令，返回退出码和输出，20 秒超时、输出过长会截断。只能跑一次性命令：编译（go build ./...）、测试（go test ./...）、看 git。禁止运行常驻命令——go run 主程序、pnpm dev、npm run dev、vite 等开发服务器不会退出，会一直阻塞直到超时；验证编译请用 go build，验证测试请用 go test。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"command": {"type": "string", "description": "要执行的一次性命令，如 go build ./...、go test ./...、git status。不要传 go run main.go、pnpm dev 等会一直运行的命令。"}
			},
			"required": ["command"],
			"additionalProperties": false
		}`),
	}
}

type execArgs struct {
	Command string `json:"command"`
}

func (t *Exec) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var a execArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("exec 参数解析失败: %w", err)
	}
	if strings.TrimSpace(a.Command) == "" {
		return "", fmt.Errorf("exec 缺少 command 参数")
	}

	ctx2, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx2, "cmd", "/C", a.Command)
	} else {
		cmd = exec.CommandContext(ctx2, "sh", "-c", a.Command)
	}
	cmd.Dir = t.dir

	out, runErr := cmd.CombinedOutput()
	output := truncateRunes(string(out), maxOutputRunes)

	if runErr != nil {
		// 1) 上层取消或我们的超时：超时返回已捕获的部分输出
		if ctx2.Err() != nil {
			if ctx2.Err() == context.DeadlineExceeded {
				// 超时几乎都是常驻命令（go run / pnpm dev / npm run dev 等开发服务器）：
				// 它们不会自己退出。给模型一个明确的、可操作的反馈，让它换思路，
				// 而不是傻傻重试同一条命令又一轮卡住。
				return fmt.Sprintf("（命令超过 %v 未退出，已终止。这通常是开发服务器等常驻命令（go run、pnpm dev、npm run dev、vite）——exec 只能跑一次性命令，不要用它启动服务器，也不要重试同一条命令。验证编译请改用 `go build ./...`，验证测试请改用 `go test ./...`。）\n%s", t.timeout, output), nil
			}
			return "", ctx2.Err()
		}
		// 2) 非零退出：结果（不是错误），退出码 + 输出回给模型
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return fmt.Sprintf("（退出码 %d）\n%s", exitErr.ExitCode(), output), nil
		}
		// 3) 命令没跑起来（找不到命令等）
		return "", fmt.Errorf("执行命令失败: %w", runErr)
	}
	return fmt.Sprintf("（退出码 0）\n%s", output), nil
}