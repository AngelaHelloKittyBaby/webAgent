package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"webagent/internal/agent"
	"webagent/internal/config"
	"webagent/internal/llm"
	"webagent/internal/mcp"
	"webagent/internal/skill"
	"webagent/internal/store"
	"webagent/internal/tool"
)

const fallbackSystemPrompt = `你是 webagent，一个全栈 Web 开发助手。
用中文回答，简短，先给结论。你能读文件、列目录、写文件、执行命令。`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "启动失败:", err)
		os.Exit(1)
	}
}

func run() error {
	cfgPath := flag.String("config", "config.yaml", "配置文件路径（不存在则用默认值）")
	promptPath := flag.String("prompt", "prompts/system.md", "系统提示词文件")
	dataDir := flag.String("data", "data", "会话数据目录（存对话历史和摘要）")
	skillsDir := flag.String("skills", "skills", "技能目录（存放 *.md 技能说明）")
	cliMode := flag.Bool("cli", false, "使用命令行模式（默认 TUI）")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}

	logger, closeLog, err := newLogger()
	if err != nil {
		return err
	}
	defer closeLog()

	systemPrompt, err := loadSystemPrompt(*promptPath, logger)
	if err != nil {
		return err
	}

	// 启动时恢复上次会话，并把摘要注入系统提示（记忆）
	conv, err := store.LoadConversation(*dataDir)
	if err != nil {
		return err
	}
	if len(conv) > 0 {
		fmt.Printf("已恢复上次会话（%d 条消息）\n", len(conv))
	}
	if summary := store.LoadSummary(*dataDir); summary != "" {
		systemPrompt = "以下是之前会话的摘要，供你回忆上下文：\n\n" + summary + "\n\n---\n\n" + systemPrompt
	}

	// 技能机制（agent 内建，不走工具体系）：加载清单注入系统提示
	skillMgr := skill.NewManager(*skillsDir)
	if err := skillMgr.Load(); err != nil {
		logger.Warn("加载技能失败", "dir", *skillsDir, "err", err)
	}
	if catalog := skillMgr.Catalog(); catalog != "" {
		systemPrompt = systemPrompt + "\n\n" + catalog
	}

	client := llm.NewOpenAI(llm.OpenAIOptions{
		BaseURL: cfg.Model.BaseURL,
		APIKey:  cfg.Model.APIKey,
		Model:   cfg.Model.Model,
		Timeout: time.Duration(cfg.Model.TimeoutSeconds) * time.Second,
	})

	mcpCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	mcpTools := loadMCPTools(mcpCtx, cfg.MCP.Servers, logger)
	cancel()

	// 工具清单：技能接入（load_skill/install_skill）+ MCP 执行工具，统一走工具体系。
	tools := []tool.Tool{skill.NewLoadSkill(skillMgr), skill.NewInstallSkill(skillMgr)}
	tools = append(tools, mcpTools...)

	ag := &agent.Agent{
		Model:       client,
		System:      systemPrompt,
		MaxSteps:    cfg.Agent.MaxSteps,
		MaxHistory:  cfg.Agent.MaxHistory,
		Temperature: cfg.Model.Temperature,
		MaxTokens:   cfg.Model.MaxTokens,
		Root:        ".",
		Tools:       tools,
	}

	if *cliMode {
		return runREPL(cfg, client, ag, conv, *dataDir, logger)
	}
	return runTUI(cfg, client, ag, conv, *dataDir, logger)
}

// runREPL 是老的命令行模式：逐行读输入，事件打印到 stdout。
func runREPL(cfg config.Config, client llm.Client, ag *agent.Agent, conv []llm.Message, dataDir string, logger *slog.Logger) error {
	ag.Emit = replEmit(logger)
	ag.Confirm = replConfirm()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fmt.Printf("webagent v0.6 | 模型: %s | 最大步数: %d\n", cfg.Model.Model, cfg.Agent.MaxSteps)
	fmt.Println("输入内容回车发送，/exit 退出，Ctrl+C 中断")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 支持粘贴长文本

	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		switch {
		case input == "":
			continue
		case input == "/exit" || input == "/quit":
			saveSummary(ctx, client, dataDir, conv)
			return nil
		}

		conv = append(conv, llm.Message{Role: llm.RoleUser, Content: input})

		newConv, err := ag.Run(ctx, conv)
		conv = newConv // 失败也要接住，保证状态一致

		// 每轮结束都保存对话，Ctrl+C 也不丢历史
		if err := store.SaveConversation(dataDir, conv); err != nil {
			fmt.Fprintf(os.Stderr, "保存对话失败: %v\n", err)
		}

		if err != nil {
			if errors.Is(err, context.Canceled) {
				fmt.Println("\n已中断，退出。")
				return nil
			}
			fmt.Fprintf(os.Stderr, "本轮失败: %v\n", err)
		}
	}
	saveSummary(ctx, client, dataDir, conv)
	return nil
}

// replConfirm 是命令行模式的审批：打印危险操作，读一行 y/n。
func replConfirm() func(ctx context.Context, tc llm.ToolCall) error {
	return func(ctx context.Context, tc llm.ToolCall) error {
		fmt.Fprintf(os.Stderr, "\n⚠ 允许执行 %s %s？[y 回车允许 / n 拒绝] ", tc.Name, string(tc.Arguments))
		var ans string
		_, err := fmt.Fscanln(os.Stdin, &ans)
		if err != nil {
			return fmt.Errorf("用户拒绝（无法读取确认）")
		}
		ans = strings.ToLower(strings.TrimSpace(ans))
		if ans == "n" || ans == "no" || ans == "拒绝" {
			return fmt.Errorf("用户拒绝")
		}
		return nil
	}
}

// replEmit 把 agent 事件打印到终端。
func replEmit(logger *slog.Logger) func(agent.Event) {
	return func(e agent.Event) {
		logger.Debug("event",
			"kind", string(e.Kind), "step", e.Step,
			"tool", e.ToolName, "dur_ms", e.Duration.Milliseconds())

		switch e.Kind {
		case agent.KindAssistantText:
			fmt.Println("\n" + e.Text)
		case agent.KindTextDelta:
			fmt.Print(e.Text) // 流式增量，不换行
		case agent.KindToolCall:
			fmt.Printf("\n[工具] %s %s\n", e.ToolName, e.ToolArgs)
		case agent.KindUsage:
			fmt.Printf("[token] 输入 %d / 输出 %d\n", e.Usage.InputTokens, e.Usage.OutputTokens)
		case agent.KindError:
			fmt.Fprintf(os.Stderr, "[错误] %v\n", e.Err)
		}
	}
}

// newLogger 把日志写到用户配置目录，绝不写 stdout。
func newLogger() (*slog.Logger, func(), error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return nil, nil, err
	}
	logDir := filepath.Join(base, "webagent", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(filepath.Join(logDir, "agent.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, err
	}
	logger := slog.New(slog.NewJSONHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
	return logger, func() { _ = f.Close() }, nil
}

func loadSystemPrompt(path string, logger *slog.Logger) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			logger.Warn("系统提示词文件不存在，使用内置兜底", "path", path)
			return fallbackSystemPrompt, nil
		}
		return "", err
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "", fmt.Errorf("系统提示词文件 %s 是空的", path)
	}
	return text, nil
}

// summarizeConversation 让模型把对话压缩成要点。
func summarizeConversation(ctx context.Context, client llm.Client, conv []llm.Message) (string, error) {
	resp, err := client.Chat(ctx, llm.Request{
		System:    "你是会话摘要助手。把下面的对话压缩成要点，保留任务目标、已完成的步骤、关键结论和未完成事项。用中文，简洁，不超过 200 字。",
		Messages:  conv,
		MaxTokens: 512,
	})
	if err != nil {
		return "", err
	}
	return resp.Message.Content, nil
}

// saveSummary 在正常退出时，让模型把本次会话压缩成要点，存到 data/summary.md。
func saveSummary(ctx context.Context, client llm.Client, dir string, conv []llm.Message) {
	if len(conv) == 0 {
		return
	}
	sum, err := summarizeConversation(ctx, client, conv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "生成摘要失败: %v\n", err)
		return
	}
	if err := store.SaveSummary(dir, sum); err != nil {
		fmt.Fprintf(os.Stderr, "保存摘要失败: %v\n", err)
		return
	}
	fmt.Printf("\n已保存会话摘要到 %s\n", filepath.Join(dir, "summary.md"))
}

// loadMCPTools 连接所有配置的 MCP server，把它们暴露的工具包装成 tool.Tool。
// 单个 server 失败不影响其它 server 和内置工具，只记一条 warning。
// 另外，若是本地可执行文件缺失，额外打印构建提示（工具全走 MCP 后这很关键）。
func loadMCPTools(ctx context.Context, servers []config.MCPServerConfig, logger *slog.Logger) []tool.Tool {
	var tools []tool.Tool
	for _, s := range servers {
		client, err := mcp.NewClient(s.Command, s.Args...)
		if err != nil {
			logger.Warn("MCP server 启动失败", "name", s.Name, "err", err)
			if _, statErr := os.Stat(s.Command); os.IsNotExist(statErr) {
				fmt.Fprintf(os.Stderr, "提示: 找不到 %s，请先构建 MCP server：go build -o %s ./cmd/mcpserver\n", s.Command, s.Command)
			}
			continue
		}
		// 断线自动重连时记一条日志
		client.OnReconnect = func() {
			logger.Info("MCP server 已自动重连", "server", s.Name)
		}
		name, version, err := client.Initialize(ctx)
		if err != nil {
			logger.Warn("MCP 握手失败", "server", s.Name, "err", err)
			_ = client.Close()
			continue
		}
		ts, err := client.ListTools(ctx)
		if err != nil {
			logger.Warn("MCP 列工具失败", "server", s.Name, "err", err)
			_ = client.Close()
			continue
		}
		logger.Info("MCP server 已连接", "server", name, "version", version, "tools", len(ts))
		for _, t := range ts {
			tools = append(tools, mcp.NewAdapter(client, t))
		}
	}
	return tools
}
