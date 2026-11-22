package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Model    ModelConfig `yaml:"model"`
	Agent    AgentConfig `yaml:"agent"`
	MCP      MCPConfig   `yaml:"mcp"`
	LogLevel string      `yaml:"log_level"`
}
type ModelConfig struct {
	BaseURL        string  `yaml:"base_url"`
	APIKey         string  `yaml:"api_key"` // 强烈建议留空，用环境变量
	Model          string  `yaml:"model"`
	Temperature    float64 `yaml:"temperature"`
	MaxTokens      int     `yaml:"max_tokens"`
	TimeoutSeconds int     `yaml:"timeout_seconds"`
}
type AgentConfig struct {
	MaxSteps   int `yaml:"max_steps"`
	MaxHistory int `yaml:"max_history"` // 滚动摘要：对话超过该条数就压缩旧消息，0 关闭
}
type MCPConfig struct {
	Servers []MCPServerConfig `yaml:"servers"`
}
type MCPServerConfig struct {
	Name    string   `yaml:"name"`
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
}

func Default() Config {
	return Config{
		Model: ModelConfig{
			BaseURL:        "https://api.deepseek.com",
			Model:          "deepseek-chat",
			Temperature:    0.3, // agent 任务要稳，不要创意
			MaxTokens:      4096,
			TimeoutSeconds: 120,
		},
		Agent:    AgentConfig{MaxSteps: 20, MaxHistory: 20},
		LogLevel: "info",
	}
}

// Load 按 默认值 → 文件 → 环境变量 三层合并。
// path 为空或文件不存在时，只用默认值 + 环境变量（方便测试和首次运行）。
func Load(path string) (Config, error) {
	cfg := Default()

	if path != "" {
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				return cfg, fmt.Errorf("解析配置文件 %s: %w", path, err)
			}
		case errors.Is(err, fs.ErrNotExist):
			// 没有配置文件是允许的
		default:
			return cfg, fmt.Errorf("读取配置文件 %s: %w", path, err)
		}
	}

	if err := applyEnv(&cfg); err != nil {
		return cfg, err
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func applyEnv(c *Config) error {
	if v := os.Getenv("WEBAGENT_BASE_URL"); v != "" {
		c.Model.BaseURL = v
	}
	if v := os.Getenv("WEBAGENT_API_KEY"); v != "" {
		c.Model.APIKey = v
	}
	if v := os.Getenv("WEBAGENT_MODEL"); v != "" {
		c.Model.Model = v
	}
	if v := os.Getenv("WEBAGENT_MAX_STEPS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("WEBAGENT_MAX_STEPS 必须是整数: %w", err)
		}
		c.Agent.MaxSteps = n
	}
	if v := os.Getenv("WEBAGENT_TIMEOUT_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("WEBAGENT_TIMEOUT_SECONDS 必须是整数: %w", err)
		}
		c.Model.TimeoutSeconds = n
	}
	return nil
}

func (c Config) Validate() error {
	var errs []error
	if c.Model.BaseURL == "" {
		errs = append(errs, errors.New("model.base_url 不能为空"))
	}
	if c.Model.Model == "" {
		errs = append(errs, errors.New("model.model 不能为空"))
	}
	if c.Model.APIKey == "" {
		errs = append(errs, errors.New("缺少 API key：请设置环境变量 WEBAGENT_API_KEY"))
	}
	if c.Model.MaxTokens <= 0 {
		errs = append(errs, errors.New("model.max_tokens 必须大于 0"))
	}
	if c.Model.TimeoutSeconds <= 0 {
		errs = append(errs, errors.New("model.timeout_seconds 必须大于 0"))
	}
	if c.Agent.MaxSteps <= 0 {
		errs = append(errs, errors.New("agent.max_steps 必须大于 0"))
	}
	return errors.Join(errs...)
}
