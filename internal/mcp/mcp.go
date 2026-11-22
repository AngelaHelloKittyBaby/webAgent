// Package mcp 实现一个最小的 MCP（Model Context Protocol）stdio 客户端。
// 只依赖标准库：spawn 一个 MCP server 进程，通过它的 stdin/stdout 走 JSON-RPC 2.0。
// 手写而不直接用 mark3labs/mcp-go，是为了看清协议本身；生产环境可换 SDK。
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// ---------- JSON-RPC 2.0 消息 ----------

type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ---------- MCP 协议类型 ----------

// Tool 是 server 暴露的一个工具（对应 tools/list 返回的每一项）。
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type listToolsResult struct {
	Tools []Tool `json:"tools"`
}

type callToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// contentBlock 是工具结果里的一个内容块。MCP 用"内容块数组"表达结果。
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type callToolResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError"`
}

// ---------- 客户端 ----------

// errConnLost 表示底层连接坏了（写 stdin 失败 / server 进程退出）。
// call 看到它就知道该触发自动重连；JSON-RPC 错误、业务错误不是它，不重连。
var errConnLost = errors.New("MCP 连接已断开")

// Client 是连接单个 MCP server 的 stdio 客户端。
type Client struct {
	command string   // 启动命令（重连时用它重建进程）
	args    []string // 启动参数
	env     []string // 启动环境变量

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
	mu     sync.Mutex // 保护请求写入、id 递增和连接重建
	nextID int64

	// OnReconnect 在自动重连成功后回调（可为 nil，用于日志/观察）。
	OnReconnect func()
}

// NewClient 启动 server 进程并建立 stdio 管道。command 是要执行的程序，args 是参数。
func NewClient(command string, args ...string) (*Client, error) {
	return startClient(command, os.Environ(), args...)
}

// startClient 同 NewClient，但允许为子进程指定环境变量（测试时用来标记假 server）。
func startClient(command string, env []string, args ...string) (*Client, error) {
	cmd := exec.Command(command, args...)
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("打开 stdin 管道失败: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("打开 stdout 管道失败: %w", err)
	}
	cmd.Stderr = os.Stderr // server 的报错直接透传到终端，便于排查

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 MCP server 失败: %w", err)
	}
	return &Client{
		command: command,
		args:    args,
		env:     env,
		cmd:     cmd,
		stdin:   stdin,
		stdout:  bufio.NewScanner(stdout),
	}, nil
}

// call 发送一条 JSON-RPC 请求，若连接已断开则自动重连并重试一次。
func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	err := c.callOnce(ctx, method, params, result)
	if err == nil || !errors.Is(err, errConnLost) {
		return err // 成功，或非断线错误（JSON-RPC 报错、业务错误）——不重连
	}
	if cerr := c.reconnect(ctx); cerr != nil {
		return fmt.Errorf("%w（自动重连也失败: %v）", err, cerr)
	}
	return c.callOnce(ctx, method, params, result)
}

// callOnce 发送一次请求并读取匹配 id 的响应（不做重连）。
// 底层 IO 失败（写管道失败、server 退出导致 EOF）会包装成 errConnLost。
func (c *Client) callOnce(ctx context.Context, method string, params any, result any) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	c.nextID++
	id := c.nextID
	req := request{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	data, err := json.Marshal(req)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	data = append(data, '\n') // NDJSON：一行一条消息
	if _, err := c.stdin.Write(data); err != nil {
		c.mu.Unlock()
		return fmt.Errorf("%w: 写入请求失败: %v", errConnLost, err)
	}
	c.mu.Unlock()

	// 读响应：server 可能夹带通知（notification，无 id）或其它请求的响应，跳过它们
	for c.stdout.Scan() {
		var resp response
		if err := json.Unmarshal(c.stdout.Bytes(), &resp); err != nil {
			continue // 忽略非 JSON 行
		}
		if resp.ID != id {
			continue // 不是发给我的响应
		}
		if resp.Error != nil {
			return fmt.Errorf("MCP 错误 %d: %s", resp.Error.Code, resp.Error.Message)
		}
		if result != nil {
			if err := json.Unmarshal(resp.Result, result); err != nil {
				return fmt.Errorf("解析结果失败: %w", err)
			}
		}
		return nil
	}
	return fmt.Errorf("%w: server 关闭了 stdout", errConnLost)
}

// reconnect 杀掉旧进程、重启 server、重新握手。成功后回调 OnReconnect。
// 注意：重建进程的管道替换要在 mu 内原子完成；握手调用 callOnce/notify
// 会自己加锁，所以必须在 mu 之外执行，否则同一把非重入锁会死锁。
func (c *Client) reconnect(ctx context.Context) error {
	// ① 收掉旧进程并重建管道（原子替换）
	c.mu.Lock()
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait() // 回收僵尸进程
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}

	cmd, stdin, stdout, err := c.buildProcess()
	if err != nil {
		c.mu.Unlock()
		return err
	}
	c.cmd, c.stdin, c.stdout = cmd, stdin, stdout
	c.mu.Unlock()

	// ② 新进程不认识我们，必须重新握手（锁外执行）
	if _, _, err := c.rawInitialize(ctx); err != nil {
		return fmt.Errorf("重连握手失败: %w", err)
	}
	if c.OnReconnect != nil {
		c.OnReconnect()
	}
	return nil
}

// buildProcess 用最初保存的参数新建一个 server 子进程，返回其管道。
func (c *Client) buildProcess() (*exec.Cmd, io.WriteCloser, *bufio.Scanner, error) {
	cmd := exec.Command(c.command, c.args...)
	cmd.Env = c.env
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("重建 stdin 失败: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, fmt.Errorf("重建 stdout 失败: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, nil, nil, fmt.Errorf("重启 MCP server 失败: %w", err)
	}
	return cmd, stdin, bufio.NewScanner(stdout), nil
}

// notify 发送一条无 id 的通知（notification），不等待响应。
func (c *Client) notify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
	}
	_, err = c.stdin.Write(append(data, '\n'))
	return err
}

// rawInitialize 是一次完整的握手：initialize + initialized 通知。
// 用 callOnce（不重连），供首次启动和 reconnect 复用。
func (c *Client) rawInitialize(ctx context.Context) (name, version string, err error) {
	params := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "webagent", "version": "0.7.0"},
	}
	var res struct {
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := c.callOnce(ctx, "initialize", params, &res); err != nil {
		return "", "", err
	}
	// MCP 规定握手完成后，客户端要发 initialized 通知，否则有的 server 不响应后续请求
	if err := c.notify("notifications/initialized", map[string]any{}); err != nil {
		return "", "", err
	}
	return res.ServerInfo.Name, res.ServerInfo.Version, nil
}

// Initialize 握手，返回 server 的名字和版本。
func (c *Client) Initialize(ctx context.Context) (name, version string, err error) {
	return c.rawInitialize(ctx)
}

// ListTools 列出 server 暴露的所有工具。
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	var res listToolsResult
	if err := c.call(ctx, "tools/list", map[string]any{}, &res); err != nil {
		return nil, err
	}
	return res.Tools, nil
}

// CallTool 调用一个工具，返回拼接后的文本内容。
func (c *Client) CallTool(ctx context.Context, name string, arguments json.RawMessage) (string, error) {
	if len(arguments) == 0 || string(arguments) == "null" {
		arguments = json.RawMessage(`{}`) // 无参工具补空对象，别发 null
	}
	var res callToolResult
	if err := c.call(ctx, "tools/call", callToolParams{Name: name, Arguments: arguments}, &res); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, block := range res.Content {
		if block.Type == "text" {
			b.WriteString(block.Text)
		}
	}
	out := b.String()
	if res.IsError {
		return out, fmt.Errorf("工具返回错误: %s", out)
	}
	return out, nil
}

// Close 关闭 stdin 并等待进程退出。
func (c *Client) Close() error {
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		return c.cmd.Wait()
	}
	return nil
}
