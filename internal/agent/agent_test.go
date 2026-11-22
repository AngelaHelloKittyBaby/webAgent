package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"webagent/internal/llm"
	"webagent/internal/tool"
)

// fakeClient 按脚本返回响应，并记录收到的每个请求。
// 它满足 llm.Client，测试里塞进 Agent.Model，全程不碰网络。
type fakeClient struct {
	steps []fakeStep
	got   []llm.Request
	calls int
}

type fakeStep struct {
	resp *llm.Response
	err  error
}

func (f *fakeClient) Chat(ctx context.Context, req llm.Request) (*llm.Response, error) {
	f.got = append(f.got, req)
	if f.calls >= len(f.steps) {
		return nil, errors.New("fakeClient 没有更多预设响应")
	}
	s := f.steps[f.calls]
	f.calls++
	return s.resp, s.err
}

// fakeTool 是测试用的工具，返回固定内容。
type fakeTool struct {
	name string
	out  string
}

func (f *fakeTool) Spec() tool.Spec {
	return tool.Spec{Name: f.name, Description: f.name, Parameters: json.RawMessage(`{"type":"object"}`)}
}

func (f *fakeTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return f.out, nil
}

func stopResp(content string) *llm.Response {
	return &llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: content}, FinishReason: "stop"}
}

func toolCallResp(id, name, args string) *llm.Response {
	return &llm.Response{
		Message:      llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: name, Arguments: json.RawMessage(args)}}},
		FinishReason: "tool_calls",
	}
}

// TestRunToolThenStop：模型先调工具、再结束，验证整条循环和事件序列。
func TestRunToolThenStop(t *testing.T) {
	c := &fakeClient{steps: []fakeStep{
		{resp: toolCallResp("c1", "echo", `{"x":"1"}`)},
		{resp: stopResp("完成了")},
	}}

	var kinds []EventKind
	ag := &Agent{
		Model: c,
		Tools: []tool.Tool{&fakeTool{name: "echo", out: "ok"}},
		Emit:  func(e Event) { kinds = append(kinds, e.Kind) },
	}

	conv, err := ag.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}

	// conv 应该是 [assistant(tool_calls), tool, assistant(stop)]
	if len(conv) != 3 {
		t.Fatalf("期望 3 条消息，实际 %d", len(conv))
	}
	if conv[1].Role != llm.RoleTool || conv[1].ToolCallID != "c1" || conv[1].Content != "ok" {
		t.Fatalf("tool 消息不对: %+v", conv[1])
	}
	if conv[2].Content != "完成了" {
		t.Fatalf("最终回复不对: %+v", conv[2])
	}

	// 关键事件都应出现
	for _, k := range []EventKind{KindToolCall, KindToolResult, KindAssistantText, KindTurnEnd} {
		if !containsKind(kinds, k) {
			t.Fatalf("缺少事件 %s，实际: %v", k, kinds)
		}
	}
}

// TestRunPassesTools：请求里必须带上注册的工具清单（这是之前漏掉的那一行）。
func TestRunPassesTools(t *testing.T) {
	c := &fakeClient{steps: []fakeStep{{resp: stopResp("hi")}}}
	ag := &Agent{Model: c, Tools: []tool.Tool{&fakeTool{name: "echo"}}}

	if _, err := ag.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(c.got) != 1 || len(c.got[0].Tools) != 1 || c.got[0].Tools[0].Name != "echo" {
		t.Fatalf("工具没传给模型: %+v", c.got)
	}
}

// TestRunMaxSteps：模型一直要调工具、永不结束，应在上限处返回 ErrMaxSteps。
func TestRunMaxSteps(t *testing.T) {
	var steps []fakeStep
	for i := 0; i < 100; i++ {
		steps = append(steps, fakeStep{resp: toolCallResp("c", "echo", `{}`)})
	}
	c := &fakeClient{steps: steps}
	ag := &Agent{Model: c, MaxSteps: 2, Tools: []tool.Tool{&fakeTool{name: "echo", out: "ok"}}}

	_, err := ag.Run(context.Background(), nil)
	if !errors.Is(err, ErrMaxSteps) {
		t.Fatalf("期望 ErrMaxSteps，实际: %v", err)
	}
	if c.calls != 2 {
		t.Fatalf("期望调用 2 次，实际 %d", c.calls)
	}
}

// TestConfirmWriteFileInsideRoot：write_file 目标在项目根内（相对路径）不触发审批。
func TestConfirmWriteFileInsideRoot(t *testing.T) {
	c := &fakeClient{steps: []fakeStep{
		{resp: toolCallResp("c1", "write_file", `{"path":"output/x.txt","content":"hi"}`)},
		{resp: stopResp("完成")},
	}}
	var called bool
	confirmCalled := false
	ag := &Agent{
		Model: c,
		Root:  t.TempDir(),
		Tools: []tool.Tool{&trackingTool{name: "write_file", out: "ok", called: &called}},
		Confirm: func(ctx context.Context, tc llm.ToolCall) error {
			confirmCalled = true
			return nil
		},
	}
	if _, err := ag.Run(context.Background(), nil); err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if !called {
		t.Fatal("根内写文件应正常执行")
	}
	if confirmCalled {
		t.Fatal("根内写文件不应触发审批")
	}
}

// TestConfirmWriteFileOutsideRoot：write_file 目标在项目根外（绝对路径）应触发审批。
func TestConfirmWriteFileOutsideRoot(t *testing.T) {
	c := &fakeClient{steps: []fakeStep{
		{resp: toolCallResp("c1", "write_file", `{"path":"C:\\outside\\x.txt","content":"hi"}`)},
		{resp: stopResp("完成")},
	}}
	var called bool
	confirmCalled := false
	ag := &Agent{
		Model: c,
		Root:  t.TempDir(),
		Tools: []tool.Tool{&trackingTool{name: "write_file", out: "ok", called: &called}},
		Confirm: func(ctx context.Context, tc llm.ToolCall) error {
			confirmCalled = true
			return nil
		},
	}
	if _, err := ag.Run(context.Background(), nil); err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if !confirmCalled {
		t.Fatal("根外写文件应触发审批")
	}
	if !called {
		t.Fatal("允许后应执行工具")
	}
}

// fakeSkills 是 SkillProvider 的测试替身。
type fakeSkills struct {
	handled []string
}

func (f *fakeSkills) Specs() []llm.ToolSpec {
	return []llm.ToolSpec{{Name: "load_skill", Description: "load", Parameters: json.RawMessage(`{"type":"object"}`)}}
}

func (f *fakeSkills) Handle(ctx context.Context, tc llm.ToolCall) (string, error) {
	f.handled = append(f.handled, tc.Name)
	return "skill-out:" + tc.Name, nil
}

// TestRunSkillDispatch：load_skill/install_skill 由 Skills 处理，不走工具体系。
func TestRunSkillDispatch(t *testing.T) {
	c := &fakeClient{steps: []fakeStep{
		{resp: toolCallResp("c1", "load_skill", `{"name":"go-build"}`)},
		{resp: stopResp("完成")},
	}}
	skills := &fakeSkills{}
	ag := &Agent{Model: c, Skills: skills}

	conv, err := ag.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if len(conv) < 2 || !strings.Contains(conv[1].Content, "skill-out:load_skill") {
		t.Fatalf("技能结果不对: %+v", conv)
	}
	if len(skills.handled) != 1 || skills.handled[0] != "load_skill" {
		t.Fatalf("Skills 应处理 load_skill: %v", skills.handled)
	}
}

// TestRunPassesSkillSpecs：技能 spec 附加在模型可见的工具清单里。
func TestRunPassesSkillSpecs(t *testing.T) {
	c := &fakeClient{steps: []fakeStep{{resp: stopResp("hi")}}}
	ag := &Agent{Model: c, Skills: &fakeSkills{}}

	if _, err := ag.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(c.got) != 1 {
		t.Fatalf("应有一次请求: %d", len(c.got))
	}
	found := false
	for _, spec := range c.got[0].Tools {
		if spec.Name == "load_skill" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("工具清单里应含 load_skill: %+v", c.got[0].Tools)
	}
}

func containsKind(kinds []EventKind, k EventKind) bool {
	for _, x := range kinds {
		if x == k {
			return true
		}
	}
	return false
}

// TestCompressHistory：对话超过 MaxHistory 时，旧消息被压缩进 Memory，只保留最近部分。
func TestCompressHistory(t *testing.T) {
	c := &fakeClient{steps: []fakeStep{{resp: stopResp("【摘要】这是压缩后的内容")}}}
	ag := &Agent{Model: c, MaxHistory: 4}

	conv := []llm.Message{
		{Role: llm.RoleUser, Content: "1"},
		{Role: llm.RoleAssistant, Content: "1"},
		{Role: llm.RoleUser, Content: "2"},
		{Role: llm.RoleAssistant, Content: "2"},
		{Role: llm.RoleUser, Content: "3"},
		{Role: llm.RoleAssistant, Content: "3"},
	}

	got, err := ag.compressHistory(context.Background(), conv)
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("应保留最近 4 条，实际 %d", len(got))
	}
	if ag.Memory != "【摘要】这是压缩后的内容" {
		t.Fatalf("Memory 没设置: %q", ag.Memory)
	}
	if c.calls != 1 {
		t.Fatalf("应只调用一次摘要，实际 %d", c.calls)
	}
}

// TestConfirmDenied：危险工具（exec/write_file）被用户拒绝时，工具不执行，结果回"已拒绝"。
func TestConfirmDenied(t *testing.T) {
	c := &fakeClient{steps: []fakeStep{
		{resp: toolCallResp("c1", "exec", `{"command":"rm -rf /"}`)},
		{resp: stopResp("好的")},
	}}
	executed := false
	ag := &Agent{
		Model: c,
		Tools: []tool.Tool{&fakeTool{name: "exec", out: "不该执行"}},
		Confirm: func(ctx context.Context, tc llm.ToolCall) error {
			return errors.New("用户拒绝")
		},
	}
	// 用自定义工具记录 execute 是否被调用：fakeTool.Execute 里置标志
	execTool := &trackingTool{name: "exec", out: "不该执行", called: &executed}
	ag.Tools = []tool.Tool{execTool}

	conv, err := ag.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	// conv: [assistant(tool_call), tool(已拒绝), assistant(stop)]
	if len(conv) < 3 {
		t.Fatalf("消息数不对: %d", len(conv))
	}
	if !strings.Contains(conv[1].Content, "用户拒绝") {
		t.Fatalf("应回已拒绝: %q", conv[1].Content)
	}
	if executed {
		t.Fatal("被拒绝的工具不应执行")
	}
}

// TestConfirmAllow：exec 被用户同意时正常执行。
func TestConfirmAllow(t *testing.T) {
	c := &fakeClient{steps: []fakeStep{
		{resp: toolCallResp("c1", "exec", `{"command":"echo hi"}`)},
		{resp: stopResp("完成")},
	}}
	var called bool
	ag := &Agent{
		Model: c,
		Tools: []tool.Tool{&trackingTool{name: "exec", out: "ok", called: &called}},
		Confirm: func(ctx context.Context, tc llm.ToolCall) error {
			return nil // 同意
		},
	}
	conv, err := ag.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if !called {
		t.Fatal("同意后应执行工具")
	}
	if !strings.Contains(conv[1].Content, "ok") {
		t.Fatalf("应含工具结果: %q", conv[1].Content)
	}
}

// TestConfirmSkipReadTool：只读工具（read_file）不应触发审批。
func TestConfirmSkipReadTool(t *testing.T) {
	c := &fakeClient{steps: []fakeStep{
		{resp: toolCallResp("c1", "read_file", `{"path":"a.txt"}`)},
		{resp: stopResp("完成")},
	}}
	var called bool
	confirmCalled := false
	ag := &Agent{
		Model: c,
		Tools: []tool.Tool{&trackingTool{name: "read_file", out: "内容", called: &called}},
		Confirm: func(ctx context.Context, tc llm.ToolCall) error {
			confirmCalled = true
			return nil
		},
	}
	_, err := ag.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if !called {
		t.Fatal("只读工具应正常执行")
	}
	if confirmCalled {
		t.Fatal("只读工具不应触发审批")
	}
}

// trackingTool 和 fakeTool 一样，但会记录 Execute 是否被调用。
type trackingTool struct {
	name  string
	out   string
	called *bool
}

func (f *trackingTool) Spec() tool.Spec {
	return tool.Spec{Name: f.name, Description: f.name, Parameters: json.RawMessage(`{"type":"object"}`)}
}

func (f *trackingTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	*f.called = true
	return f.out, nil
}
