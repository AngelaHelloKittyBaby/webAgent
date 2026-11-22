package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type OpenAIOptions struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration

	// MaxRetry 是"首次失败后最多再试几次"。
	//   0  → 用默认值 2
	//  -1  → 完全不重试
	MaxRetry int

	// BackoffBase 是第一次重试前的等待时间，之后每次翻倍。
	//   0  → 默认 500ms。测试里设成 1ms，整个测试套件才不会跑几秒钟。
	BackoffBase time.Duration

	HTTP *http.Client // 可为 nil，测试时注入
}

type OpenAIClient struct {
	opts     OpenAIOptions
	http     *http.Client
	maxRetry int
}

// 编译期断言：OpenAIClient 必须实现 Client。
// 以后谁改了 Chat 的方法名或签名，这一行立刻编译报错，
// 而不是等到 agent 包调用时才发现。
var _ Client = (*OpenAIClient)(nil)

// StreamClient 断言：OpenAIClient 也支持流式。
var _ StreamClient = (*OpenAIClient)(nil)

func NewOpenAI(o OpenAIOptions) *OpenAIClient {
	if o.Timeout <= 0 {
		o.Timeout = 120 * time.Second
	}
	switch {
	case o.MaxRetry < 0:
		o.MaxRetry = 0 // 显式关闭重试
	case o.MaxRetry == 0:
		o.MaxRetry = 2 // 未指定，用默认值
	}
	if o.BackoffBase <= 0 {
		o.BackoffBase = 500 * time.Millisecond
	}
	h := o.HTTP
	if h == nil {
		h = &http.Client{Timeout: o.Timeout}
	}
	return &OpenAIClient{opts: o, http: h, maxRetry: o.MaxRetry}
}

func (c *OpenAIClient) endpoint() string {
	return strings.TrimRight(c.opts.BaseURL, "/") + "/chat/completions"
}

func (c *OpenAIClient) Chat(ctx context.Context, req Request) (*Response, error) {
	payload, err := c.buildBody(req)
	if err != nil {
		return nil, err
	}

	// 单次调用的总超时，覆盖所有重试
	ctx, cancel := context.WithTimeout(ctx, c.opts.Timeout)
	defer cancel()

	var lastErr error
	for attempt := 0; attempt <= c.maxRetry; attempt++ {
		if attempt > 0 {
			wait := c.backoff(attempt, lastErr)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}

		resp, err := c.do(ctx, payload)
		if err == nil {
			return resp, nil
		}
		lastErr = err

		// fail-closed：只有明确标记为"可重试"的错误才重试。
		// 未知错误（解析失败、模型返回错误、空 choices 等）一律不重试——
		// 重试的成本（时间 + 费用）是确定的，收益是猜的。
		var apiErr *APIError
		if !errors.As(err, &apiErr) || !apiErr.Retryable {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("模型调用失败（已重试 %d 次）: %w", c.maxRetry, lastErr)
}

func (c *OpenAIClient) backoff(attempt int, err error) time.Duration {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
		return apiErr.RetryAfter // 服务端说了算
	}

	base := c.opts.BackoffBase << uint(attempt-1) // 500ms, 1s, 2s...
	if base > 8*time.Second {
		base = 8 * time.Second
	}
	if base < time.Millisecond {
		return base // 太小就不抖动了，避免 rand.Int63n(0) 直接 panic
	}
	// ±25% 抖动：多个进程同时重试时不要一起打服务端
	jitter := time.Duration(rand.Int63n(int64(base)/2)) - base/4
	return base + jitter
}

func (c *OpenAIClient) do(ctx context.Context, payload []byte) (*Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.opts.APIKey)

	res, err := c.http.Do(httpReq)
	if err != nil {
		return nil, &APIError{Retryable: true, Body: err.Error()}
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, &APIError{Retryable: true, Body: err.Error()}
	}

	if res.StatusCode >= 400 {
		ae := &APIError{
			StatusCode: res.StatusCode,
			Body:       truncate(string(body), 500),
			Retryable:  res.StatusCode == 408 || res.StatusCode == 429 || res.StatusCode >= 500,
		}
		if v := res.Header.Get("Retry-After"); v != "" {
			if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
				ae.RetryAfter = time.Duration(secs) * time.Second
			}
		}
		return nil, ae
	}

	var ar apiResponse
	if err := json.Unmarshal(body, &ar); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w（原始内容: %s）", err, truncate(string(body), 300))
	}
	if ar.Error != nil {
		return nil, fmt.Errorf("模型返回错误: %s", ar.Error.Message)
	}
	if len(ar.Choices) == 0 {
		return nil, errors.New("模型返回了空的 choices")
	}

	choice := ar.Choices[0]
	msg := Message{Role: RoleAssistant, Content: choice.Message.Content}
	for _, tc := range choice.Message.ToolCalls {
		args := tc.Function.Arguments
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(args),
		})
	}
	return &Response{
		Message:      msg,
		FinishReason: choice.FinishReason,
		Usage:        Usage{InputTokens: ar.Usage.PromptTokens, OutputTokens: ar.Usage.CompletionTokens},
	}, nil
}

func (c *OpenAIClient) buildBody(req Request) ([]byte, error) {
	msgs := make([]apiMessage, 0, len(req.Messages)+1)
	if strings.TrimSpace(req.System) != "" {
		msgs = append(msgs, apiMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		if m.Role == "system" {
			continue // system 统一走 req.System，避免重复
		}
		am := apiMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			args := string(tc.Arguments)
			if args == "" {
				args = "{}"
			}
			am.ToolCalls = append(am.ToolCalls, apiToolCall{
				ID:       tc.ID,
				Type:     "function",
				Function: apiFunction{Name: tc.Name, Arguments: args},
			})
		}
		msgs = append(msgs, am)
	}

	payload := apiRequest{
		Model:       c.opts.Model,
		Messages:    msgs,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      req.Stream,
	}
	for _, t := range req.Tools {
		params := t.Parameters
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		payload.Tools = append(payload.Tools, apiTool{
			Type:     "function",
			Function: apiToolFunc{Name: t.Name, Description: t.Description, Parameters: params},
		})
	}
	return json.Marshal(payload)
}

// Stream 用 SSE 流式请求，内容到达时逐个回调 onDelta。
// 注意：流式不重试——没法在输出到一半时安全重放，失败直接返回错误。
func (c *OpenAIClient) Stream(ctx context.Context, req Request, onDelta func(string)) (*Response, error) {
	req.Stream = true
	payload, err := c.buildBody(req)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.opts.Timeout)
	defer cancel()
	return c.stream(ctx, payload, onDelta)
}

// stream 发送请求并按 SSE 逐行解析，累积内容、工具调用、usage 后组装成完整 Response。
func (c *OpenAIClient) stream(ctx context.Context, payload []byte, onDelta func(string)) (*Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.opts.APIKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	res, err := c.http.Do(httpReq)
	if err != nil {
		return nil, &APIError{Retryable: true, Body: err.Error()}
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
		return nil, &APIError{
			StatusCode: res.StatusCode,
			Body:       truncate(string(body), 500),
			Retryable:  res.StatusCode == 408 || res.StatusCode == 429 || res.StatusCode >= 500,
		}
	}

	var content strings.Builder
	var finishReason string
	usage := Usage{}
	toolAcc := map[int]*streamToolCallAcc{}

	scanner := bufio.NewScanner(res.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20) // 单行 chunk 可能较大
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue // 跳过空行和非数据行
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var chunk apiStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, fmt.Errorf("解析流式响应失败: %w（原始: %s）", err, truncate(data, 300))
		}
		if chunk.Usage != nil {
			usage = Usage{InputTokens: chunk.Usage.PromptTokens, OutputTokens: chunk.Usage.CompletionTokens}
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				content.WriteString(choice.Delta.Content)
				if onDelta != nil {
					onDelta(choice.Delta.Content)
				}
			}
			for _, tc := range choice.Delta.ToolCalls {
				acc, ok := toolAcc[tc.Index]
				if !ok {
					acc = &streamToolCallAcc{}
					toolAcc[tc.Index] = acc
				}
				if tc.ID != "" {
					acc.id = tc.ID
				}
				if tc.Function.Name != "" {
					acc.name = tc.Function.Name
				}
				acc.args.WriteString(tc.Function.Arguments)
			}
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				finishReason = *choice.FinishReason
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取流式响应失败: %w", err)
	}

	msg := Message{Role: RoleAssistant, Content: content.String()}
	indices := make([]int, 0, len(toolAcc))
	for i := range toolAcc {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	for _, i := range indices {
		acc := toolAcc[i]
		args := acc.args.String()
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: acc.id, Name: acc.name, Arguments: json.RawMessage(args)})
	}
	return &Response{Message: msg, FinishReason: finishReason, Usage: usage}, nil
}

// ---------- 线上协议（wire format），只在本文件可见 ----------

type apiFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // 注意：字符串，不是对象
}

type apiToolCall struct {
	ID       string      `json:"id"`
	Type     string      `json:"type"`
	Function apiFunction `json:"function"`
}

type apiToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type apiTool struct {
	Type     string      `json:"type"`
	Function apiToolFunc `json:"function"`
}

type apiMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content,omitempty"`
	ToolCalls  []apiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type apiRequest struct {
	Model       string       `json:"model"`
	Messages    []apiMessage `json:"messages"`
	Tools       []apiTool    `json:"tools,omitempty"`
	Temperature float64      `json:"temperature,omitempty"`
	MaxTokens   int          `json:"max_tokens,omitempty"`
	Stream      bool         `json:"stream,omitempty"`
}

type apiResponse struct {
	Choices []struct {
		Message      apiMessage `json:"message"`
		FinishReason string     `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// apiStreamChunk 是流式（SSE）响应里的一个 data 块。
type apiStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string             `json:"content"`
			ToolCalls []apiStreamToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// apiStreamToolCall 是流式里的工具调用分片：参数会分多次到达，按 index 拼。
type apiStreamToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// streamToolCallAcc 累积同一个 index 下的工具调用分片。
type streamToolCallAcc struct {
	id   string
	name string
	args strings.Builder
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
