package skill

import (
	"context"
	"encoding/json"

	"webagent/internal/tool"
)

// LoadSkill 是 load_skill 工具：按名加载某个技能的完整说明。
// 技能接入能力统一放工具体系，模型把它们当普通工具调用。
type LoadSkill struct {
	mgr *Manager
}

func NewLoadSkill(m *Manager) *LoadSkill { return &LoadSkill{mgr: m} }

func (t *LoadSkill) Spec() tool.Spec {
	return tool.Spec{
		Name:        "load_skill",
		Description: "加载一个技能的完整说明。先在系统提示的\"可用技能\"清单里找到技能名，再调用它获取详细步骤。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {"type": "string", "description": "技能名，见系统提示里的可用技能清单"}
			},
			"required": ["name"],
			"additionalProperties": false
		}`),
	}
}

func (t *LoadSkill) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return t.mgr.load(ctx, args)
}

// InstallSkill 是 install_skill 工具：从 URL 下载安装一份技能 markdown 到 skills/。
type InstallSkill struct {
	mgr *Manager
}

func NewInstallSkill(m *Manager) *InstallSkill { return &InstallSkill{mgr: m} }

func (t *InstallSkill) Spec() tool.Spec {
	return tool.Spec{
		Name:        "install_skill",
		Description: "从 URL 下载并安装一个 skill（markdown 文件，含 frontmatter）到 skills/ 目录。用户要求安装某个技能、或给出了 skill 链接时用。安装后需重启才生效。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"url": {"type": "string", "description": "skill markdown 文件的下载地址（HTTP/HTTPS）"},
				"name": {"type": "string", "description": "可选：安装后的技能名（不含 .md），默认取 URL 文件名"}
			},
			"required": ["url"],
			"additionalProperties": false
		}`),
	}
}

func (t *InstallSkill) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	return t.mgr.install(ctx, args)
}