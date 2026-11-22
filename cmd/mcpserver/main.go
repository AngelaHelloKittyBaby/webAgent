// Command mcpserver 是 webagent 的 MCP server：把 internal/tool 的工具
// 通过 stdio 暴露成 MCP 工具（read_file / list_dir / write_file / exec / review_code）。
//
// 本文件只是"传输壳"：工具逻辑全部在 internal/tool，这里负责注册和参数转发。
// 构建：go build -o mcpserver.exe ./cmd/mcpserver
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"webagent/internal/tool"
)

func main() {
	s := server.NewMCPServer("webagent-tools", "1.0.0")

	register(s, tool.NewReadFile())
	register(s, tool.NewListDir())
	register(s, tool.NewWriteFile("."))
	register(s, tool.NewExec("."))
	register(s, tool.NewReview())

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintln(os.Stderr, "mcpserver 退出:", err)
		os.Exit(1)
	}
}

// register 把一个 tool.Tool 注册成 MCP 工具：schema 用工具的 Parameters，
// handler 把原始参数转发给工具的 Execute。
func register(s *server.MCPServer, t tool.Tool) {
	spec := t.Spec()
	def := mcp.NewTool(spec.Name, mcp.WithDescription(spec.Description))
	// NewTool 默认会建一个空 InputSchema；设 RawInputSchema 前必须先清空，
	// 否则序列化时报 "has both InputSchema and RawInputSchema set"。
	def.InputSchema = mcp.ToolInputSchema{}
	def.RawInputSchema = spec.Parameters
	s.AddTool(def,
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			out, err := t.Execute(ctx, rawArgs(req))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(out), nil
		},
	)
}

// rawArgs 从 MCP 请求里取出参数并转成 json.RawMessage 交给工具。
func rawArgs(req mcp.CallToolRequest) json.RawMessage {
	switch v := req.GetRawArguments().(type) {
	case nil:
		return json.RawMessage(`{}`)
	case json.RawMessage:
		return v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return json.RawMessage(`{}`)
		}
		return b
	}
}