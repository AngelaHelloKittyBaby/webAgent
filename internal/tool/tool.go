// Package tool 定义 agent 可用的工具接口。
// agent 只依赖这个接口，具体工具各自一个文件，加新工具 = 加文件 + main 里注册一行。
package tool

import (
	"context"
	"encoding/json"
)

// Tool 是一个可以被模型调用的工具。
// 换工具、加工具都只在这里实现，agent 层一行不动。
type Tool interface {
	// Spec 是"广告给模型的说明书"：名称、描述、参数 JSON Schema。
	Spec() Spec
	// Execute 执行一次工具调用。args 是模型返回的参数（JSON 对象，可能非法）。
	// 返回的字符串会作为 tool 消息内容回给模型。
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

// Spec 是工具对外的元信息。
type Spec struct {
	Name        string          // 唯一名，模型发起调用时就用它
	Description string          // 写清楚：什么时候用、参数什么意思，直接影响模型选工具的准确率
	Parameters  json.RawMessage // 参数的 JSON Schema，必须是合法 schema
}
