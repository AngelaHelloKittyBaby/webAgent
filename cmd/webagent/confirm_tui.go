package main

import (
	"context"
	"encoding/json"
	"fmt"

	"webagent/internal/llm"
)

// pendingConfirm 表示一次等待用户审批的工具调用。
type pendingConfirm struct {
	tc    llm.ToolCall
	brief string // 展示用的摘要（命令或路径）
}

// confirmRequest 是 agent 的 Confirm 回调发给 TUI 主循环的审批请求。
type confirmRequest struct {
	tc llm.ToolCall
}

// confirmResultMsg 是用户按键后的审批结果。
type confirmResultMsg struct {
	ok bool
}

// toolCallBrief 从工具参数里提取一行摘要，用于审批提示。
func toolCallBrief(tc llm.ToolCall) string {
	var kv map[string]string
	if err := json.Unmarshal(tc.Arguments, &kv); err == nil {
		if kv["command"] != "" {
			return kv["command"]
		}
		if kv["path"] != "" {
			return kv["path"]
		}
	}
	return string(tc.Arguments)
}

// makeTuiConfirm 构造 agent 的 Confirm 回调。
// 它把请求发给 TUI 主循环（confirmCh），然后阻塞等用户按键结果（confirmResult）。
// ctx 取消或通道关闭时返回错误，表示"本次调用被中断"。
func makeTuiConfirm(m *tuiModel) func(ctx context.Context, tc llm.ToolCall) error {
	return func(ctx context.Context, tc llm.ToolCall) error {
		select {
		case m.confirmCh <- confirmRequest{tc: tc}:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case res := <-m.confirmResult:
			if !res {
				return fmt.Errorf("用户拒绝了 %s 的调用", tc.Name)
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// confirmBrief 生成审批提示文本（不含按键说明）。
func confirmBrief(req confirmRequest) string {
	return fmt.Sprintf("⚠ 允许执行：%s %s", req.tc.Name, toolCallBrief(req.tc))
}