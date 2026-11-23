package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"webagent/internal/agent"
	"webagent/internal/config"
	"webagent/internal/llm"
	"webagent/internal/store"
)

const inputHeight = 3

var statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Padding(0, 1)

// runResult 是 agent.Run 完成后的结果。
type runResult struct {
	conv []llm.Message
	err  error
}

// tuiModel 是 bubbletea 的 Model：持有 agent、对话、事件通道和渲染状态。
type tuiModel struct {
	ag      *agent.Agent
	client  llm.Client
	ctx     context.Context
	cancel  context.CancelFunc
	dataDir string

	conv []llm.Message

	viewport viewport.Model
	input    textarea.Model

	lines     []string // 渲染行
	streaming bool     // 是否正在追加一个助手的流式文本行
	running   bool     // agent 是否在跑
	step      int

	eventCh chan agent.Event
	doneCh  chan runResult

	confirmCh    chan confirmRequest // agent 发来的审批请求
	confirmResult chan bool          // 用户按键结果送回 agent
	pending      *pendingConfirm     // 正在等待审批的调用

	lastContent  string // 上次渲染的内容（内容没变就不重设 viewport）
	followBottom bool   // 是否自动跟随最新内容（用户滚动过则为 false）

	width  int
	height int
}

// runTUI 启动 bubbletea 界面。核心：把 agent 的 Emit 接到一个 channel，
// TUI 从 channel 读事件渲染——agent 完全不知道自己在什么界面里跑。
func runTUI(cfg config.Config, client llm.Client, ag *agent.Agent, conv []llm.Message, dataDir string, logger *slog.Logger) error {
	m := newTUIModel(ag, client, conv, dataDir)
	ag.Emit = func(e agent.Event) {
		m.eventCh <- e
	}
	ag.Confirm = makeTuiConfirm(m) // 危险工具（exec/write_file）先问用户
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

func newTUIModel(ag *agent.Agent, client llm.Client, conv []llm.Message, dataDir string) *tuiModel {
	ta := textarea.New()
	ta.Placeholder = "输入内容，回车发送；Ctrl+C 退出…"
	ta.ShowLineNumbers = false
	ta.SetHeight(inputHeight)

	ctx, cancel := context.WithCancel(context.Background())

	m := &tuiModel{
		ag:       ag,
		client:   client,
		ctx:      ctx,
		cancel:   cancel,
		dataDir:  dataDir,
		conv:     conv,
		viewport: viewport.New(0, 0),
		input:    ta,
		eventCh:  make(chan agent.Event, 256),
		doneCh:   make(chan runResult, 1),
		confirmCh:    make(chan confirmRequest, 1),
		confirmResult: make(chan bool, 1),
		followBottom:  true,
	}
	m.renderHistory(conv)
	m.greet()
	return m
}

// greet 在启动时显示一行 AI 自我介绍（只渲染，不进入对话历史）。
func (m *tuiModel) greet() {
	m.lines = append(m.lines,
		"助手> 你好！我是 webagent，运行在你终端里的 AI 开发助手。",
		"  我可以读取/修改项目文件、执行命令、审查代码，还能按需加载技能（load_skill）。",
		"  在下方输入框输入内容回车发送；PgUp/PgDown 或鼠标滚轮滚动对话。",
	)
}

// pendingLine 是待审批时显示的提示行。
func (m *tuiModel) pendingLine() string {
	if m.pending == nil {
		return ""
	}
	return fmt.Sprintf("  ⚠ 允许执行 %s %s？ [y 允许 / n 拒绝]", m.pending.tc.Name, m.pending.brief)
}

func (m *tuiModel) Init() tea.Cmd {
	m.input.Focus()
	return tea.Batch(textarea.Blink, waitMsg(m.eventCh, m.doneCh, m.confirmCh))
}

// waitMsg 同时监听事件、完成、审批三个通道，哪个先到返回哪个。
// 用单个 select 而不是多个独立命令，避免"处理一个消息时丢掉另一个"的坑。
func waitMsg(events chan agent.Event, done chan runResult, confirmCh chan confirmRequest) tea.Cmd {
	return func() tea.Msg {
		select {
		case e := <-events:
			return e
		case d := <-done:
			return d
		case r := <-confirmCh:
			return r
		}
	}
}

func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout()
		return m, nil

	case confirmRequest:
		// 收到审批请求：进入待审批态，等用户按键
		m.pending = &pendingConfirm{tc: msg.tc, brief: toolCallBrief(msg.tc)}
		m.updateViewport()
		return m, nil

	case agent.Event:
		m.handleEvent(msg)
		m.updateViewport()
		return m, waitMsg(m.eventCh, m.doneCh, m.confirmCh)

	case runResult:
		m.handleDone(msg)
		m.updateViewport()
		return m, waitMsg(m.eventCh, m.doneCh, m.confirmCh)

	case tea.MouseMsg:
		// 鼠标滚轮滚动对话区
		m.viewport, _ = m.viewport.Update(msg)
		m.afterScroll()
		return m, nil

	case tea.KeyMsg:
		return m, m.handleKey(msg)
	}

	// 其它消息（光标闪烁等）交给输入框
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *tuiModel) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		return m.quit()
	case tea.KeyEnter:
		if !m.running {
			return m.submit()
		}
		return nil // 运行中忽略回车
	case tea.KeyRunes:
		if m.pending != nil {
			// y = 允许，n = 拒绝；只认这两个字母
			switch msg.String() {
			case "y", "Y":
				return m.resolveConfirm(true)
			case "n", "N":
				return m.resolveConfirm(false)
			}
		}
	case tea.KeyPgUp, tea.KeyPgDown:
		// 翻页滚动对话区（运行中也能翻历史）
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		m.afterScroll()
		return cmd
	}
	if m.running || m.pending != nil {
		return nil // 运行中或待审批时忽略其它输入
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}

// resolveConfirm 把用户决定送回 agent，结束待审批态。
func (m *tuiModel) resolveConfirm(ok bool) tea.Cmd {
	m.pending = nil
	m.confirmResult <- ok
	m.updateViewport()
	return waitMsg(m.eventCh, m.doneCh, m.confirmCh)
}

func (m *tuiModel) submit() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return nil
	}
	m.input.Reset()

	m.conv = append(m.conv, llm.Message{Role: llm.RoleUser, Content: text})
	m.lines = append(m.lines, "你> "+text)
	m.running = true

	conv := m.conv // 捕获当前快照，避免 goroutine 读到后续修改
	go func() {
		newConv, err := m.ag.Run(m.ctx, conv)
		m.doneCh <- runResult{conv: newConv, err: err}
	}()

	m.updateViewport()
	return waitMsg(m.eventCh, m.doneCh, m.confirmCh)
}

func (m *tuiModel) handleEvent(e agent.Event) {
	switch e.Kind {
	case agent.KindStepStart:
		m.step = e.Step
	case agent.KindTextDelta:
		if !m.streaming {
			m.lines = append(m.lines, "助手> "+e.Text)
			m.streaming = true
		} else {
			m.lines[len(m.lines)-1] += e.Text
		}
	case agent.KindAssistantText:
		m.streaming = false
		m.lines = append(m.lines, "助手> "+e.Text)
	case agent.KindToolCall:
		m.streaming = false
		m.lines = append(m.lines, fmt.Sprintf("  🔧 %s %s", e.ToolName, e.ToolArgs))
	case agent.KindToolResult:
		m.lines = append(m.lines, "  ↳ "+truncate(e.Text, 300))
	case agent.KindUsage:
		m.lines = append(m.lines, fmt.Sprintf("  [token] 输入 %d / 输出 %d", e.Usage.InputTokens, e.Usage.OutputTokens))
	case agent.KindError:
		m.streaming = false
		m.lines = append(m.lines, "  ✗ "+e.Err.Error())
	case agent.KindTurnEnd:
		m.streaming = false
	}
}

func (m *tuiModel) handleDone(r runResult) {
	m.running = false
	m.streaming = false
	m.conv = r.conv
	if r.err != nil && !errors.Is(r.err, context.Canceled) {
		m.lines = append(m.lines, "  ✗ "+r.err.Error())
	}
	if err := store.SaveConversation(m.dataDir, r.conv); err != nil {
		m.lines = append(m.lines, "  ✗ 保存失败: "+err.Error())
	}
}

func (m *tuiModel) quit() tea.Cmd {
	m.cancel()
	_ = store.SaveConversation(m.dataDir, m.conv)
	// 生成会话摘要（独立超时，静默写文件，不干扰 TUI 屏幕）
	if len(m.conv) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		sum, err := summarizeConversation(ctx, m.client, m.conv)
		cancel()
		if err == nil {
			_ = store.SaveSummary(m.dataDir, sum)
		}
	}
	return tea.Quit
}

// renderHistory 把已恢复的历史转成渲染行。
func (m *tuiModel) renderHistory(conv []llm.Message) {
	for _, msg := range conv {
		switch msg.Role {
		case llm.RoleUser:
			m.lines = append(m.lines, "你> "+msg.Content)
		case llm.RoleAssistant:
			if msg.Content != "" {
				m.lines = append(m.lines, "助手> "+msg.Content)
			}
			for _, tc := range msg.ToolCalls {
				m.lines = append(m.lines, fmt.Sprintf("  🔧 %s %s", tc.Name, string(tc.Arguments)))
			}
		case llm.RoleTool:
			m.lines = append(m.lines, "  ↳ "+truncate(msg.Content, 200))
		}
	}
}

func (m *tuiModel) layout() {
	m.viewport.Width = m.width
	m.viewport.Height = m.height - inputHeight - 3
	if m.viewport.Height < 3 {
		m.viewport.Height = 3
	}
	m.input.SetWidth(m.width - 2)
}

func (m *tuiModel) updateViewport() {
	content := strings.Join(m.lines, "\n")
	if content == m.lastContent {
		return // 内容没变：不动 viewport，保留用户的滚动位置
	}
	m.lastContent = content
	m.viewport.SetContent(content)
	if m.followBottom {
		m.viewport.GotoBottom()
	}
}

// afterScroll 在用户滚动后刷新"是否自动跟随"状态：
// 滚到最底才恢复跟随最新，在中间/顶部则停在原地（不再被新内容拉走）。
func (m *tuiModel) afterScroll() {
	m.followBottom = m.viewport.AtBottom()
}

func (m *tuiModel) View() string {
	if m.width == 0 {
		return "加载中…"
	}
	m.updateViewport()
	parts := []string{m.viewport.View()}
	if line := m.pendingLine(); line != "" {
		parts = append(parts, line)
	}
	parts = append(parts, m.statusBar(), m.input.View())
	return lipgloss.JoinVertical(lipgloss.Top, parts...)
}

func (m *tuiModel) statusBar() string {
	if m.running {
		return statusStyle.Render(fmt.Sprintf(" ● 运行中（step %d）", m.step))
	}
	return statusStyle.Render(" ● 就绪 · 回车发送 · Ctrl+C 退出")
}

// truncate 按 rune 截断，避免切碎中文。
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
