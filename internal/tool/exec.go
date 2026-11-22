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

// execTimeout 单条命令的超时上限。
const execTimeout = 30 * time.Second

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
		Description: "在项目根目录执行一条 shell 命令，返回退出码和输出，30 秒超时、输出过长会截断。编译（go build）、跑测试（go test）、看 git 都用它。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"command": {"type": "string", "description": "要执行的命令，如 go build ./...、go test ./...、git status"}
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
				return fmt.Sprintf("（命令超时，已终止）\n%s", output), nil
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