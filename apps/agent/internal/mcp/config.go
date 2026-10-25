package mcp

import (
	"encoding/json"
	"fmt"
	"os"
)

// serverConfig 是 mcp.json 里的一项。Enabled 用指针区分「没写」与「写了 false」：
// 没写等于启用。
type serverConfig struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	Enabled *bool             `json:"enabled"`
}

// fileConfig 是 mcp.json 的顶层结构。
type fileConfig struct {
	Servers []serverConfig `json:"servers"`
}

func (s serverConfig) enabled() bool {
	return s.Enabled == nil || *s.Enabled
}

// loadConfig 读取并解析配置文件。
// 文件不存在时返回 os.ErrNotExist 包装的错误，由调用方转成「空注册表」。
func loadConfig(path string) ([]serverConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg fileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	if cfg.Servers == nil {
		return nil, nil
	}
	return cfg.Servers, nil
}
