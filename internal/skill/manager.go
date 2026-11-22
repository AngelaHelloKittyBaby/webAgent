package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Manager 承载技能数据与操作（加载清单、load、install）。
// 它本身不是 tool.Tool；对外提供能力的 LoadSkill / InstallSkill 工具
// （见 tools.go）包装它，放进统一的工具清单。
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