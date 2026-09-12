package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// 配置持久化（用户要求：配一次模型，留下配置文件在本地，以后更新可复用）。
//
// 落点选择：**放在用户的配置目录**（而非工作区、也非程序目录）——
//   - 不写程序目录：更新/重装应用不会覆盖或清掉
//   - 不写工作区：换工作区后配置还在，且不污染用户的数据文件夹
//
// 环境变量仍然优先（密钥可以用 LLM_API_KEY 给，不必写进文件）。

// UserConfigPath 返回用户级配置文件的路径。
// Windows: %APPDATA%\gridwright\config.yaml
// 其他：   $XDG_CONFIG_HOME 或 ~/.config/gridwright/config.yaml
func UserConfigPath() string {
	if dir := os.Getenv("GRIDWRIGHT_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "config.yaml")
	}
	if cfg, err := os.UserConfigDir(); err == nil && cfg != "" {
		return filepath.Join(cfg, "gridwright", "config.yaml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gridwright", "config.yaml")
}

// Save 把当前配置写回 path（保持 YAML，机器可读、人可改）。
// 只写该写的字段；密钥写进去是必要的（否则重装后还要重配），
// 但文件放在用户配置目录、不进版本库。
func (c *Config) Save(path string) error {
	if path == "" {
		path = UserConfigPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建配置目录: %w", err)
	}
	// 保留已有的其他字段（比如手写的注释之外的设置），先读回再合并
	existing := &Config{}
	if b, err := os.ReadFile(path); err == nil {
		_ = yaml.Unmarshal(b, existing)
	}
	merged := *existing
	merged.Workspace = c.Workspace
	merged.PollSeconds = c.PollSeconds
	merged.SampleRows = c.SampleRows
	merged.LLM = c.LLM
	merged.Notify = c.Notify

	b, err := yaml.Marshal(&merged)
	if err != nil {
		return fmt.Errorf("序列化配置: %w", err)
	}
	return os.WriteFile(path, b, 0o600) // 0600：含密钥，收紧权限
}

// Redacted 返回用于展示/回传的副本：API key 以是否配置表示，不回明文。
func (c *Config) Redacted() map[string]any {
	return map[string]any{
		"workspace":   c.Workspace,
		"baseUrl":     c.LLM.BaseURL,
		"model":       c.LLM.Model,
		"hasApiKey":   c.LLM.APIKey != "",
		"brainReady":  c.BrainReady(),
		"pollSeconds": c.PollSeconds,
		"configPath":  UserConfigPath(),
	}
}

// MaskKey 给密钥打码（仅在确实需要显示时用，例如日志；不用于 API 回传）。
func MaskKey(k string) string {
	if k == "" {
		return ""
	}
	if len(k) <= 8 {
		return strings.Repeat("*", len(k))
	}
	return k[:4] + strings.Repeat("*", 6) + k[len(k)-4:]
}
