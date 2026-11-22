// Package skill 提供"技能"能力：skills 目录下的 markdown 文件，
// 每个文件是一份可复用任务说明（何时用 + 具体步骤）。
// 启动时把技能清单注入系统提示，模型需要时用 load_skill 工具加载完整说明。
package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"webagent/internal/tool"
)

// Skill 是一份可复用的任务说明。
type Skill struct {
	Name        string // 唯一名（来自 frontmatter 或文件名）
	Description string // 一句话：什么时候用
	Content     string // 完整正文（何时用 + 步骤）
}

// frontmatter 是 markdown 文件顶部的元信息。
type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// Load 扫描 dir 下所有 .md 文件，解析 frontmatter 和正文。
// dir 不存在时返回空切片（不算错误）。
func Load(dir string) ([]Skill, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var skills []Skill
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		s, err := loadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("解析 skill %s: %w", e.Name(), err)
		}
		skills = append(skills, s)
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills, nil
}

func loadFile(path string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	text := string(data)

	// 默认名 = 文件名（去扩展名）
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	desc := ""
	content := strings.TrimSpace(text)

	// 解析 frontmatter：第一行 "---"，到下一个 "---" 之间是 YAML
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		end := -1
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				end = i
				break
			}
		}
		if end > 0 {
			var fm frontmatter
			if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &fm); err != nil {
				return Skill{}, err
			}
			if fm.Name != "" {
				name = fm.Name
			}
			desc = fm.Description
			content = strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
		}
	}
	return Skill{Name: name, Description: desc, Content: content}, nil
}

// Catalog 生成技能清单文本，用于注入系统提示。
func Catalog(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## 可用技能\n")
	for _, s := range skills {
		sb.WriteString(fmt.Sprintf("- %s: %s\n", s.Name, s.Description))
	}
	sb.WriteString("\n需要某项技能的详细步骤时，调用 load_skill 工具加载它的完整说明。\n")
	return sb.String()
}

// LoadTool 是 load_skill 工具：让模型按名加载某个技能的完整说明。
type LoadTool struct {
	skills map[string]Skill
}

func NewLoadTool(skills []Skill) *LoadTool {
	m := make(map[string]Skill, len(skills))
	for _, s := range skills {
		m[s.Name] = s
	}
	return &LoadTool{skills: m}
}

func (t *LoadTool) Spec() tool.Spec {
	return tool.Spec{
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
	}
}

type loadSkillArgs struct {
	Name string `json:"name"`
}

func (t *LoadTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var a loadSkillArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("load_skill 参数解析失败: %w", err)
	}
	s, ok := t.skills[a.Name]
	if !ok {
		return "", fmt.Errorf("技能 %q 不存在", a.Name)
	}
	return s.Content, nil
}
