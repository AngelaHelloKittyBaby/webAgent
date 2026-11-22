package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// install 下载一份 skill markdown，净化文件名后安装到 skills/ 目录。
func (m *Manager) install(ctx context.Context, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var a struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("install_skill 参数解析失败: %w", err)
	}
	rawURL := strings.TrimSpace(a.URL)
	if rawURL == "" {
		return "", fmt.Errorf("install_skill 缺少 url 参数")
	}

	data, err := m.fetch(ctx, rawURL)
	if err != nil {
		return "", err
	}

	// 技能名：优先参数，其次 URL 文件名；都做净化（只留字母数字-_，防路径穿越）
	name := sanitizeSkillName(a.Name)
	if name == "" {
		name = sanitizeSkillName(filepath.Base(urlPath(rawURL)))
	}
	if name == "" {
		return "", fmt.Errorf("无法从 URL 推断技能名，请提供 name 参数")
	}

	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return "", fmt.Errorf("创建技能目录失败: %w", err)
	}
	dst := filepath.Join(m.dir, name+".md")
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return "", fmt.Errorf("写入技能文件失败: %w", err)
	}
	return fmt.Sprintf("已安装 skill %q（%d 字节）到 %s。重启 webagent 后生效。", name, len(data), dst), nil
}

// fetch 下载内容并做大小校验。
func (m *Manager) fetch(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		return nil, fmt.Errorf("下载失败 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// 多读 1 字节以检测超限
	data, err := io.ReadAll(io.LimitReader(resp.Body, m.maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	if int64(len(data)) > m.maxBytes {
		return nil, fmt.Errorf("内容超过 %d 字节上限，拒绝安装", m.maxBytes)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("下载内容为空")
	}
	return data, nil
}

// sanitizeSkillName 只保留字母、数字、下划线、连字符，去掉一切路径相关字符。
// 防止 URL 文件名里的 ../、\、: 等造成路径穿越或非法文件。
func sanitizeSkillName(s string) string {
	s = strings.TrimSpace(strings.TrimSuffix(s, ".md"))
	var b strings.Builder
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_'
		if ok {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// urlPath 取 URL 的路径部分；解析失败时返回原字符串。
func urlPath(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return path.Base(u.Path)
}