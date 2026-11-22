package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"webagent/internal/tool"
)

// TestFakeServerProcess 用 re-exec 技巧：当环境变量 MCP_FAKE=1 时，本测试二进制
// 充当假 MCP server（从 stdin 读 JSON-RPC、按 method 回响应）；正常情况下它是个空测试直接返回。
// 好处：不用引入任何外部程序，就能端到端测 stdio 传输。
func TestFakeServerProcess(t *testing.T) {
	if os.Getenv("MCP_FAKE") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var req struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			continue
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2024-11-05",
				"serverInfo":      map[string]any{"name": "fake", "version": "1.0"},
				"capabilities":    map[string]any{},
			}
			// 崩溃模式：只崩溃第一代（用标记文件区分代数）。
		// 握手完成后，若标记文件不存在则写完响应后退出，并留下标记；
		// 标记存在说明这是重连后的新进程，不崩溃。
		if os.Getenv("MCP_FAKE_CRASH") == "1" && req.Method == "initialize" {
			marker := os.Getenv("MCP_FAKE_CRASH_MARKER")
			if marker == "" {
				// 没指定标记文件：每次都崩（用于其它场景）
				b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
				fmt.Println(string(b))
				os.Exit(1)
			}
			if _, err := os.Stat(marker); os.IsNotExist(err) {
				b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
				fmt.Println(string(b))
				_ = os.WriteFile(marker, []byte("1"), 0o644)
				os.Exit(1)
			}
		}
		case "tools/list":
			result = map[string]any{"tools": []any{
				map[string]any{
					"name":        "echo",
					"description": "回显输入",
					"inputSchema": map[string]any{
						"type":       "object",
						"properties": map[string]any{"text": map[string]any{"type": "string"}},
					},
				},
			}}
		case "tools/call":
			var p struct {
				Params struct {
					Name string `json:"name"`
				} `json:"params"`
			}
			_ = json.Unmarshal(scanner.Bytes(), &p)
			if p.Params.Name != "echo" {
				result = map[string]any{
					"content": []any{map[string]any{"type": "text", "text": "工具不存在"}},
					"isError": true,
				}
			} else {
				result = map[string]any{
					"content": []any{map[string]any{"type": "text", "text": "hello-from-fake"}},
					"isError": false,
				}
			}
		}
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		fmt.Println(string(b))
	}
	os.Exit(0)
}

// TestAutoReconnect：server 在握手后崩溃（只崩第一代），下一次调用应自动重连并成功。
func TestAutoReconnect(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "crash.marker")
	c, err := startClient(os.Args[0],
		append(os.Environ(), "MCP_FAKE=1", "MCP_FAKE_CRASH=1", "MCP_FAKE_CRASH_MARKER="+marker),
		"-test.run=TestFakeServerProcess")
	if err != nil {
		t.Fatalf("启动假 server 失败: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	var reconnects atomic.Int32
	c.OnReconnect = func() { reconnects.Add(1) }
	ctx := context.Background()

	if _, _, err := c.Initialize(ctx); err != nil {
		t.Fatalf("首次握手失败: %v", err)
	}

	// 此时第一代 server 已崩溃。调用应触发重连（重启 + 重新握手）并成功。
	out, err := c.CallTool(ctx, "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("崩溃后调用应自动重连成功，实际: %v", err)
	}
	if out != "hello-from-fake" {
		t.Fatalf("结果不对: %q", out)
	}
	if n := reconnects.Load(); n < 1 {
		t.Fatalf("应有重连发生，实际 %d 次", n)
	}
}

// TestNoReconnectOnBusinessError：JSON-RPC 业务错误不是断线，不应触发重连。
func TestNoReconnectOnBusinessError(t *testing.T) {
	c := newFakeServerClient(t)
	var reconnects atomic.Int32
	c.OnReconnect = func() { reconnects.Add(1) }

	// fake server 对不存在的工具返回结果错误；这里直接断言"无工具"场景不重连：
	// 调用一个 fake server 不认识的工具，应返回错误且不重连。
	if _, err := c.CallTool(context.Background(), "no_such_tool", json.RawMessage(`{}`)); err == nil {
		t.Fatal("应报错")
	}
	if n := reconnects.Load(); n != 0 {
		t.Fatalf("业务错误不应触发重连，实际 %d 次", n)
	}
}

// checkAdapterInterface 编译期校验 Adapter 满足 tool.Tool。
func checkAdapterInterface() {
	var _ tool.Tool = (*Adapter)(nil)
}

// newFakeServerClient 启动自己（re-exec）作为假 MCP server，返回客户端。
func newFakeServerClient(t *testing.T) *Client {
	t.Helper()
	c, err := startClient(os.Args[0], append(os.Environ(), "MCP_FAKE=1"), "-test.run=TestFakeServerProcess")
	if err != nil {
		t.Fatalf("启动假 server 失败: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestClientRoundTrip(t *testing.T) {
	c := newFakeServerClient(t)
	ctx := context.Background()

	name, version, err := c.Initialize(ctx)
	if err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	if name != "fake" || version != "1.0" {
		t.Fatalf("server 信息不对: %q %q", name, version)
	}

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("列工具失败: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("工具清单不对: %+v", tools)
	}

	out, err := c.CallTool(ctx, "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("调用工具失败: %v", err)
	}
	if out != "hello-from-fake" {
		t.Fatalf("结果不对: %q", out)
	}
}

// TestAdapterWrapsMCPTool：验证 Adapter 满足 tool.Tool 接口，且能转发调用。
func TestAdapterWrapsMCPTool(t *testing.T) {
	c := newFakeServerClient(t)
	ctx := context.Background()

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	adapter := NewAdapter(c, tools[0])

	// 编译期保证 Adapter 实现了 tool.Tool
	var _ tool.Tool = adapter

	if adapter.Spec().Name != "echo" || adapter.Spec().Description != "回显输入" {
		t.Fatalf("Spec 不对: %+v", adapter.Spec())
	}
	if len(adapter.Spec().Parameters) == 0 {
		t.Fatalf("Parameters 应为 MCP 的 inputSchema")
	}

	out, err := adapter.Execute(ctx, json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("Execute 失败: %v", err)
	}
	if out != "hello-from-fake" {
		t.Fatalf("Execute 结果不对: %q", out)
	}
}
