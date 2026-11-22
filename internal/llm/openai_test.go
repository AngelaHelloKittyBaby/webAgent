package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestBuildBodyShape 锁死线上协议格式：字段名、顺序、嵌套、omitempty。
// 这个测试不联网，跑得飞快。它红了 = 你发出去的 JSON 和 API 要求的不一致。
func TestBuildBodyShape(t *testing.T) {
	c := NewOpenAI(OpenAIOptions{Model: "m"})

	raw, err := c.buildBody(Request{
		System:      "你是助手",
		Messages:    []Message{{Role: RoleUser, Content: "hi"}},
		Tools:       []ToolSpec{{Name: "read_file", Description: "读文件", Parameters: json.RawMessage(`{"type":"object"}`)}},
		MaxTokens:   100,
		Temperature: 0.3,
	})
	if err != nil {
		t.Fatal(err)
	}

	want := `{"model":"m","messages":[{"role":"system","content":"你是助手"},{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"read_file","description":"读文件","parameters":{"type":"object"}}}],"temperature":0.3,"max_tokens":100}`

	if string(raw) != want {
		t.Fatalf("线上格式不对\n实际: %s\n期望: %s", raw, want)
	}
}

// TestToAPIMessagesToolCall 验证"双重编码"有没有正确还原：
// 内部的 RawMessage（对象）必须变成线上的 string。
func TestToAPIMessagesToolCall(t *testing.T) {
	c := NewOpenAI(OpenAIOptions{Model: "m"})

	raw, err := c.buildBody(Request{Messages: []Message{
		{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				{ID: "c1", Name: "read_file", Arguments: json.RawMessage(`{"path":"a.txt"}`)},
			},
		},
		{Role: RoleTool, ToolCallID: "c1", Content: "文件内容"},
	}})
	if err != nil {
		t.Fatal(err)
	}

	// arguments 在线上必须是字符串，而不是对象
	want := `{"model":"m","messages":[{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"a.txt\"}"}}]},{"role":"tool","content":"文件内容","tool_call_id":"c1"}]}`
	if string(raw) != want {
		t.Fatalf("双重编码还原不对\n实际: %s\n期望: %s", raw, want)
	}
}

// TestChatOK 用假服务器验证整条链路：请求头、路径、响应解析、usage。
func TestChatOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("期望 POST，实际 %s", r.Method)
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("路径不对: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization 不对: %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type 不对: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"你好"},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":3}}`)
	}))
	defer srv.Close()

	c := NewOpenAI(OpenAIOptions{
		BaseURL: srv.URL,
		APIKey:  "sk-test",
		Model:   "m",
		HTTP:    srv.Client(),
	})

	got, err := c.Chat(context.Background(), Request{
		System:   "sys",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if got.Message.Content != "你好" {
		t.Fatalf("内容不对: %q", got.Message.Content)
	}
	if got.FinishReason != "stop" {
		t.Fatalf("finish_reason 不对: %q", got.FinishReason)
	}
	if got.Usage.InputTokens != 9 || got.Usage.OutputTokens != 3 {
		t.Fatalf("usage 没解出来: %+v", got.Usage)
	}
}