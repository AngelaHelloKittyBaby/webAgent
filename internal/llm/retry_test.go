package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient 统一造一个指向假服务器、且重试等待只有 1ms 的客户端。
// BackoffBase 设成 1ms 是必须的：否则重试两次就要白等 1.5 秒，测试套件会越来越慢。
func newTestClient(u string) *OpenAIClient {
	return NewOpenAI(OpenAIOptions{
		BaseURL:     u,
		APIKey:      "k",
		Model:       "m",
		HTTP:        &http.Client{},
		BackoffBase: time.Millisecond,
	})
}

// TestParseErrorNotRetried：响应解析不了，说明请求本身是通的，
// 重试一百次也是同样的垃圾数据 —— 必须只请求一次。
func TestParseErrorNotRetried(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, `这不是 JSON`)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)

	if _, err := c.Chat(context.Background(), Request{}); err == nil {
		t.Fatal("应当报错")
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("解析失败不该重试，实际请求了 %d 次", n)
	}
}

// TestNoRetryOn401：密钥错了，重试三次 = 让用户多等三倍时间，还毫无意义。
func TestNoRetryOn401(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"invalid api key"}}`)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)

	if _, err := c.Chat(context.Background(), Request{}); err == nil {
		t.Fatal("应当报错")
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("401 不该重试，实际请求了 %d 次", n)
	}
}

// TestRetryOn500ThenOK：5xx 是服务端临时抽风，重试才有意义。
func TestRetryOn500ThenOK(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, `{"error":{"message":"boom"}}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)

	resp, err := c.Chat(context.Background(), Request{})
	if err != nil {
		t.Fatalf("应当重试成功: %v", err)
	}
	if resp.Message.Content != "ok" {
		t.Fatalf("内容不对: %q", resp.Message.Content)
	}
	if n := calls.Load(); n != 3 {
		t.Fatalf("期望请求 3 次，实际 %d 次", n)
	}
}

// TestRetryDisabled：MaxRetry=-1 必须真的关掉重试（0 值有歧义，所以要显式约定）。
func TestRetryDisabled(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewOpenAI(OpenAIOptions{
		BaseURL: srv.URL, APIKey: "k", Model: "m",
		HTTP: &http.Client{}, MaxRetry: -1, BackoffBase: time.Millisecond,
	})

	if _, err := c.Chat(context.Background(), Request{}); err == nil {
		t.Fatal("应当报错")
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("MaxRetry=-1 时不该重试，实际请求了 %d 次", n)
	}
}