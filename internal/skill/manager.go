package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"webagent/internal/llm"
)

// Manager 是技能机制的实现者：持有技能列表，处理 load_skill / install_skill 调用。
// agent 通过 SkillProvider 接口使用它；它不实现 tool.Tool，不属于"执行工具"。
type Manager struct {
	dir      string
	skills   []Skill
	client   *http.Client
	maxBytes int64
}

func NewManager(dir string) *Manager {
	return &Manager{
		dir:      dir,
		client:   &http.Client{Timeout: 10 * time.Second},
		maxBytes: 1 << 20, // 1 MiB
	}
}

// Load 从 skills 目录加载技能列表。
func (m *Manager) Load() error {
	skills, err := Load(m.dir)
	if err != nil {
		return err
	}
	m.skills = skills
	return nil
}

// Catalog 返回技能清单文本（注入系统提示用）。
func (m *Manager) Catalog() string { return Catalog(m.skills) }

// Specs 返回两个内建技能操作的 LLM 层 spec。
// 它们对模型可见（模型要能发起调用），但执行由 agent 分派给 Handle，不走 tool 体系。
func (m *Manager) Specs() []llm.ToolSpec {
	return []llm.ToolSpec{
		{
			Name: "load_skill",
			Description: "加载一个技能的完整说明。先在系统提示的\"可用技能\"清单里找到技能名，再调用它获取详细步骤。",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "技能名，见系统提示里的可用技能清单"}
				},
				"required": ["name"],
				"additionalProperties": false
			}`),
		},
		{
			Name: "install_skill",
			Description: "从 URL 下载并安装一个 skill（markdown 文件，含 frontmatter）到 skills/ 目录。" +
				"用户要求安装某个技能、或给出了 skill 链接时用。安装后需重启才生效。",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"url": {"type": "string", "description": "skill markdown 文件的下载地址（HTTP/HTTPS）"},
					"name": {"type": "string", "description": "可选：安装后的技能名（不含 .md），默认取 URL 文件名"}
				},
				"required": ["url"],
				"additionalProperties": false
			}`),
		},
	}
}

// Handle 分派一次技能调用（load_skill / install_skill）。
// 其它名字返回错误，agent 会落到常规工具查找。
func (m *Manager) Handle(ctx context.Context, tc llm.ToolCall) (string, error) {
	switch tc.Name {
	case "load_skill":
		return m.load(ctx, tc.Arguments)
	case "install_skill":
		return m.install(ctx, tc.Arguments)
	default:
		return "", fmt.Errorf("未知技能操作 %q", tc.Name)
	}
}

func (m *Manager) load(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var a struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("load_skill 参数解析失败: %w", err)
	}
	for _, s := range m.skills {
		if s.Name == a.Name {
			return s.Content, nil
		}
	}
	return "", fmt.Errorf("技能 %q 不存在", a.Name)
}