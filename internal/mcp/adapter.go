package mcp

import (
	"context"
	"encoding/json"

	"webagent/internal/tool"
)

// Adapter 把一个 MCP 工具包装成 tool.Tool，让 agent 像用内置工具一样用它。
// 这是 MCP 和 agent 之间唯一的接缝：Spec() 来自 tools/list，Execute() 转发 tools/call。
type Adapter struct {
	name   string
	desc   string
	schema json.RawMessage
	client *Client
}

func NewAdapter(c *Client, t Tool) *Adapter {
	schema := t.InputSchema
	if len(schema) == 0 || string(schema) == "null" {
		// 有些 server 不提供 inputSchema，补一个空 schema，避免模型收到 null
		schema = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return &Adapter{
		name:   t.Name,
		desc:   t.Description,
		schema: schema,
		client: c,
	}
}

func (a *Adapter) Spec() tool.Spec {
	return tool.Spec{
		Name:        a.name,
		Description: a.desc,
		Parameters:  a.schema,
	}
}

func (a *Adapter) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return a.client.CallTool(ctx, a.name, args)
}
