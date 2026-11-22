package llm

import (
	"context"
	"encoding/json"
)

// Role 是消息角色。注意：system 不放在 Messages 里，而是走 Request.System。
// 这样做是为了后面每轮都能重新组装系统提示（记忆、skill 目录、工具清单都会变）。
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolCall 是模型发起的一次工具调用。
// Arguments 故意用 RawMessage：模型返回的参数是"字符串形式的 JSON"，可能非法。
// 先原样保留，校验/修复的责任交给 tools 层。
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolSpec 是广告给模型的工具定义。Parameters 必须是 JSON Schema。
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type Request struct {
	System      string
	Messages    []Message
	Tools       []ToolSpec
	MaxTokens   int
	Temperature float64
	Stream      bool // 是否流式返回（仅 StreamClient 使用）
}
type Usage struct {
	InputTokens  int
	OutputTokens int
}
type Response struct {
	Message      Message
	FinishReason string
	Usage        Usage
}

// Client 是唯一的模型接口。换厂商只改实现，agent 包一行都不动。
type Client interface {
	Chat(ctx context.Context, req Request) (*Response, error)
}

// StreamClient 是支持流式输出的客户端。Agent 检测到它就用流式，
// 否则回退到 Client.Chat 一次性返回。
// Stream 在内容到达时逐个回调 onDelta（增量文本），最终仍返回完整 Response。
type StreamClient interface {
	Client
	Stream(ctx context.Context, req Request, onDelta func(string)) (*Response, error)
}
