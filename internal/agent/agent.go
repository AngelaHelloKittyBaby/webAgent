package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"webagent/internal/llm"
	"webagent/internal/tool"
)

// ErrMaxSteps 表示循环达到上限仍未结束。用错误变量而不是字符串，
// 上层可以 errors.Is(err, agent.ErrMaxSteps) 精确判断。
var ErrMaxSteps = errors.New("达到最大步数上限，任务未完成")

type EventKind string

const (
	KindStepStart     EventKind = "step_start"
	KindAssistantText EventKind = "assistant_text"
	KindTextDelta     EventKind = "text_delta" // 流式增量
	KindToolCall      EventKind = "tool_call"
	KindToolResult    EventKind = "tool_result"
	KindUsage         EventKind = "usage"
	KindError         EventKind = "error"
	KindTurnEnd       EventKind = "turn_end"
)

// Event 是 agent 对外唯一的信息通道。
// 现在是"一次性文本"，第 6 步做流式时加 TextDelta 即可，不改结构。
type Event struct {
	Kind     EventKind
	Step     int
	Text     string
	ToolName string
	ToolArgs string
	Usage    llm.Usage
	Duration time.Duration
	Err      error
}

type Agent struct {
	Model       llm.Client
	System      string // 系统提示，每轮重新传给模型
	MaxSteps    int
	Temperature float64
	MaxTokens   int

	// Memory 是滚动摘要：对话过长时，旧消息被压缩成这段文字，每轮拼进 System。
	Memory string
	// MaxHistory 超过这个数量就触发压缩；<=0 表示关闭滚动摘要。
	MaxHistory int

	// Root 是项目根目录（用于判断 write_file 是否越界）。空值视为 "."。
	Root string

	// Skills 是技能机制（load_skill/install_skill）的实现者。
	// 技能不属于"执行工具"，agent 直接分派给它，不走 tool 体系。
	Skills SkillProvider

	// Confirm 是工具调用前的安全审批：危险命令/越界写文件先问用户。
	// 返回 nil 放行，非 nil 表示用户拒绝。为 nil 时所有工具直接放行（不审批）。
	Confirm func(ctx context.Context, tc llm.ToolCall) error

	// Tools 是本 agent 注册的工具清单。每轮都会把它们的 Spec 广告给模型。
	Tools []tool.Tool

	// Emit 为 nil 时静默。TUI 来了以后把它接到 channel 上。
	Emit func(Event)
}

// SkillProvider 是技能机制的抽象：提供对模型可见的 spec，并处理技能调用。
// skill.Manager 实现它。
type SkillProvider interface {
	Specs() []llm.ToolSpec
	Handle(ctx context.Context, tc llm.ToolCall) (string, error)
}

// requireConfirm 决定一次工具调用是否需要用户审批。
//   - exec：始终审批（命令没有"默认区域"概念）；
//   - write_file：目标在项目根外（用户指定了外部位置）才审批，根内（含 output/）直接写；
//   - 读取类工具：一律放行。
func (a *Agent) requireConfirm(tc llm.ToolCall) bool {
	switch tc.Name {
	case "exec":
		return true
	case "write_file":
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(tc.Arguments, &args); err != nil || args.Path == "" {
			return true // 参数解析失败/缺路径：保守起见要确认
		}
		root := a.Root
		if root == "" {
			root = "."
		}
		return !writeInRoot(root, args.Path)
	default:
		return false
	}
}

// writeInRoot 判断目标路径是否在项目根（默认区域）内。
// 相对路径视为根内（模型按约定用相对路径时落到根下/output/）；
// 绝对路径只有真的在根内才算根内，否则视为越界（需用户确认）。
func writeInRoot(root, path string) bool {
	if !filepath.IsAbs(path) {
		return true
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// toolSpecs 把内部工具清单 + 技能 spec 翻译成模型层认识的 ToolSpec。
func (a *Agent) toolSpecs() []llm.ToolSpec {
	specs := make([]llm.ToolSpec, 0, len(a.Tools)+4)
	for _, t := range a.Tools {
		s := t.Spec()
		specs = append(specs, llm.ToolSpec{
			Name:        s.Name,
			Description: s.Description,
			Parameters:  s.Parameters,
		})
	}
	if a.Skills != nil {
		specs = append(specs, a.Skills.Specs()...)
	}
	return specs
}

// executeTool 执行一次工具调用，返回要给模型看的 tool 消息内容。
// 约定：工具执行失败不致命，把错误文本回给模型，让模型转告用户或换思路。
func (a *Agent) executeTool(ctx context.Context, tc llm.ToolCall) string {
	// 技能调用（load_skill/install_skill）由 Skills 处理，不走工具体系
	if a.Skills != nil && (tc.Name == "load_skill" || tc.Name == "install_skill") {
		out, err := a.Skills.Handle(ctx, tc)
		if err != nil {
			return fmt.Sprintf("错误：%v", err)
		}
		return out
	}
	for _, t := range a.Tools {
		if t.Spec().Name == tc.Name {
			out, err := t.Execute(ctx, tc.Arguments)
			if err != nil {
				return fmt.Sprintf("错误：%v", err)
			}
			return out
		}
	}
	return fmt.Sprintf("错误：工具 %q 不存在。", tc.Name)
}

func (a *Agent) emit(e Event) {
	if a.Emit != nil {
		a.Emit(e)
	}
}

// chat 优先走流式（如果模型支持），否则回退到一次性 Chat。
// 返回 streamed 表示是否用了流式：流式时内容已通过 KindTextDelta 逐段输出，
// 上层就不应再重复发 KindAssistantText 了。
func (a *Agent) chat(ctx context.Context, step int, req llm.Request) (*llm.Response, bool, error) {
	if sc, ok := a.Model.(llm.StreamClient); ok {
		resp, err := sc.Stream(ctx, req, func(delta string) {
			a.emit(Event{Kind: KindTextDelta, Step: step, Text: delta})
		})
		return resp, true, err
	}
	resp, err := a.Model.Chat(ctx, req)
	return resp, false, err
}

// summarize 让模型把一段消息压缩成要点。用独立系统提示，和主任务隔开。
func (a *Agent) summarize(ctx context.Context, msgs []llm.Message) (string, error) {
	resp, err := a.Model.Chat(ctx, llm.Request{
		System:    "你是会话摘要助手。把下面的对话压缩成要点，保留任务目标、已完成的步骤、关键结论和未完成事项。用中文，简洁。",
		Messages:  msgs,
		MaxTokens: 512,
	})
	if err != nil {
		return "", err
	}
	return resp.Message.Content, nil
}

// compressHistory 当对话超过 MaxHistory 时，把最旧的一部分压缩进 Memory，
// 只保留最近 MaxHistory 条原文。压缩失败时返回原对话（宁可超长也不丢内容）。
func (a *Agent) compressHistory(ctx context.Context, conv []llm.Message) ([]llm.Message, error) {
	if a.MaxHistory <= 0 || len(conv) <= a.MaxHistory {
		return conv, nil
	}
	keep := a.MaxHistory
	old := conv[:len(conv)-keep]
	recent := conv[len(conv)-keep:]

	summary, err := a.summarize(ctx, old)
	if err != nil {
		return conv, err
	}
	a.Memory = summary
	return recent, nil
}

// Run 驱动一次完整的任务循环。
// 传入完整对话（不含 system），返回更新后的对话——即使出错也返回，便于上层持久化。
func (a *Agent) Run(ctx context.Context, conv []llm.Message) ([]llm.Message, error) {
	if a.Model == nil {
		return conv, errors.New("agent: 未设置 Model")
	}
	maxSteps := a.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 20
	}

	for step := 1; step <= maxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return conv, err
		}

		// 滚动摘要：对话过长就先压缩旧消息
		newConv, err := a.compressHistory(ctx, conv)
		if err != nil {
			return conv, err
		}
		conv = newConv

		start := time.Now()
		a.emit(Event{Kind: KindStepStart, Step: step})

		// 每轮重新组装系统提示：滚动摘要（记忆）+ 基础提示
		system := a.System
		if a.Memory != "" {
			system = "之前对话的摘要：\n" + a.Memory + "\n\n---\n\n" + system
		}

		resp, streamed, err := a.chat(ctx, step, llm.Request{
			System:      system,
			Messages:    conv,
			Tools:       a.toolSpecs(),
			MaxTokens:   a.MaxTokens,
			Temperature: a.Temperature,
		})
		if err != nil {
			a.emit(Event{Kind: KindError, Step: step, Err: err, Duration: time.Since(start)})
			return conv, err
		}

		a.emit(Event{Kind: KindUsage, Step: step, Usage: resp.Usage})
		conv = append(conv, resp.Message)

		switch resp.FinishReason {
		case "tool_calls":
			if len(resp.Message.ToolCalls) == 0 {
				err := errors.New("模型声明要调用工具，但未返回任何工具调用")
				a.emit(Event{Kind: KindError, Step: step, Err: err})
				return conv, err
			}
			for _, tc := range resp.Message.ToolCalls {
				if err := ctx.Err(); err != nil {
					return conv, err // 取消就别再回 tool 消息了
				}
				a.emit(Event{Kind: KindToolCall, Step: step, ToolName: tc.Name, ToolArgs: string(tc.Arguments)})

				// 危险操作（exec、越界写文件）先问用户；拒绝则该次调用返回"已拒绝"
				refused := ""
				if a.Confirm != nil && a.requireConfirm(tc) {
					if err := a.Confirm(ctx, tc); err != nil {
						refused = fmt.Sprintf("用户拒绝了该操作：%v", err)
					}
				}

				var out string
				if refused != "" {
					out = refused
				} else {
					out = a.executeTool(ctx, tc)
				}
				conv = append(conv, llm.Message{Role: llm.RoleTool, ToolCallID: tc.ID, Content: out})
				a.emit(Event{Kind: KindToolResult, Step: step, ToolName: tc.Name, Text: out})
			}
			continue

		case "length":
			if resp.Message.Content != "" && !streamed {
				a.emit(Event{Kind: KindAssistantText, Step: step, Text: resp.Message.Content})
			}
			err := errors.New("输出被 max_tokens 截断，请调大 model.max_tokens")
			a.emit(Event{Kind: KindError, Step: step, Err: err})
			return conv, err

		default: // "stop" 以及厂商返回的任何未知值
			if resp.Message.Content != "" && !streamed {
				// 流式时内容已通过 KindTextDelta 逐段输出，这里不再重复
				a.emit(Event{Kind: KindAssistantText, Step: step, Text: resp.Message.Content})
			}
			a.emit(Event{Kind: KindTurnEnd, Step: step, Duration: time.Since(start)})
			return conv, nil
		}
	}

	err := fmt.Errorf("%w（上限 %d 步）", ErrMaxSteps, maxSteps)
	a.emit(Event{Kind: KindError, Err: err})
	return conv, err
}
