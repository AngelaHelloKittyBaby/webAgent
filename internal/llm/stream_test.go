package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sseChunk 把一个结构体编码成一行 SSE data，避免手写转义出错。
func sseChunk(w io.Writer, v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(w, "data: %s\n\n", b)
}

func TestStreamContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"stream":true`) {
			t.Errorf("请求体缺少 stream: true: %s", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")

		delta := func(content string) map[string]any {
			return map[string]any{
				"choices": []any{map[string]any{
					"delta":         map[string]any{"content": content},
					"finish_reason": nil,
				}},
			}
		}
		sseChunk(w, delta("你"))
		sseChunk(w, delta("好"))
		sseChunk(w, map[string]any{
			"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 9, "completion_tokens": 3},
		})
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := NewOpenAI(OpenAIOptions{BaseURL: srv.URL, APIKey: "k", Model: "m", HTTP: srv.Client()})

	var deltas []string
	resp, err := c.Stream(context.Background(), Request{System: "s", Messages: []Message{{Role: RoleUser, Content: "hi"}}}, func(d string) {
		deltas = append(deltas, d)
	})
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if len(deltas) != 2 || deltas[0] != "你" || deltas[1] != "好" {
		t.Fatalf("增量不对: %v", deltas)
	}
	if resp.Message.Content != "你好" || resp.FinishReason != "stop" {
		t.Fatalf("结果不对: %+v", resp)
	}
	if resp.Usage.InputTokens != 9 || resp.Usage.OutputTokens != 3 {
		t.Fatalf("usage 不对: %+v", resp.Usage)
	}
}

func TestStreamToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")

		// 工具调用：第一片带 id + name + 空参数，第二片带参数
		sseChunk(w, map[string]any{
			"choices": []any{map[string]any{
				"delta": map[string]any{
					"tool_calls": []any{map[string]any{
						"index": 0, "id": "c1",
						"function": map[string]any{"name": "read_file", "arguments": ""},
					}},
				},
				"finish_reason": nil,
			}},
		})
		sseChunk(w, map[string]any{
			"choices": []any{map[string]any{
				"delta": map[string]any{
					"tool_calls": []any{map[string]any{
						"index":    0,
						"function": map[string]any{"arguments": `{"path":"a.txt"}`},
					}},
				},
				"finish_reason": nil,
			}},
		})
		sseChunk(w, map[string]any{
			"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": "tool_calls"}},
		})
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := NewOpenAI(OpenAIOptions{BaseURL: srv.URL, APIKey: "k", Model: "m", HTTP: srv.Client()})

	resp, err := c.Stream(context.Background(), Request{}, nil)
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("应累积出 1 个工具调用: %+v", resp.Message.ToolCalls)
	}
	tc := resp.Message.ToolCalls[0]
	if tc.ID != "c1" || tc.Name != "read_file" || string(tc.Arguments) != `{"path":"a.txt"}` {
		t.Fatalf("工具调用不对: %+v", tc)
	}
	if resp.FinishReason != "tool_calls" {
		t.Fatalf("finish_reason 不对: %q", resp.FinishReason)
	}
}
