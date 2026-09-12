// Package config 读取 agent 运行时配置（config.yaml + 环境变量覆盖）。
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 是 agent 的运行配置。
type Config struct {
	Workspace   string `yaml:"workspace"`   // 工作区 = 一个文件夹（spec §3）
	PollSeconds int    `yaml:"poll_seconds"` // fsnotify 之外的兜底轮询间隔
	SampleRows  int    `yaml:"sample_rows"`  // 发给脑的每表样例行数

	LLM    LLM    `yaml:"llm"`
	Notify Notify `yaml:"notify"`
}

// LLM 是"脑"的连接配置（OpenAI 兼容 API）。
type LLM struct {
	BaseURL        string `yaml:"base_url"`        // 例如 https://api.deepseek.com/v1（以 /v1 结尾）
	APIKey         string `yaml:"api_key"`         // 也可用 env LLM_API_KEY
	Model          string `yaml:"model"`           // 例如 deepseek-chat
	TimeoutSeconds int    `yaml:"timeout_seconds"`
}

// Timeout 返回 LLM 请求超时（默认 60s）。
func (l *LLM) Timeout() time.Duration {
	if l.TimeoutSeconds <= 0 {
		return 60 * time.Second
	}
	return time.Duration(l.TimeoutSeconds) * time.Second
}

// Notify 是通知出口配置（M1 仅 console；M3 接 serverchan/pushplus/webhook）。
type Notify struct {
	Channel        string `yaml:"channel"`         // console | serverchan | pushplus | webhook
	ServerChanSKey string `yaml:"serverchan_skey"`
	PushPlusToken  string `yaml:"pushplus_token"`
	WebhookURL     string `yaml:"webhook_url"`
}

// Load 读取 path 处的 config.yaml 并应用环境变量覆盖。
func Load(path string) (*Config, error) {
	c := &Config{PollSeconds: 5, SampleRows: 10}
	c.LLM.TimeoutSeconds = 60
	c.Notify.Channel = "console"

	// 配置文件是**可选**的：桌面版随包运行、没有 config.yaml，全靠环境变量。
	// 只有"显式指定了配置路径却读不到"才算错误。
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(b, c); err != nil {
			return nil, fmt.Errorf("解析配置: %w", err)
		}
	case os.IsNotExist(err) && path == "config.yaml":
		// 默认路径不存在：正常（桌面版），继续用默认值 + 环境变量
	default:
		return nil, fmt.Errorf("读取配置 %s: %w", path, err)
	}

	// 环境变量优先（密钥不落 config.yaml）
	if v := os.Getenv("LLM_API_KEY"); v != "" {
		c.LLM.APIKey = v
	}
	if v := os.Getenv("LLM_BASE_URL"); v != "" {
		c.LLM.BaseURL = v
	}
	if v := os.Getenv("LLM_MODEL"); v != "" {
		c.LLM.Model = v
	}
	if v := os.Getenv("WORKSPACE"); v != "" {
		c.Workspace = v
	}

	if c.Workspace == "" {
		return nil, fmt.Errorf("未配置工作区（config.yaml 的 workspace 或 env WORKSPACE）")
	}
	// 脑（LLM）不是启动前提：没有 key/网络时，只读能力（看表、体检、联动图、
	// 账目、预览）必须照常可用。只有"需要判断"的动作才要求配好脑。
	// 见 docs/agent-architecture/20-离线可用与桌面交付.md
	return c, nil
}

// BrainReady 表示"脑"是否配好（有 base_url + api_key）。
// 未就绪时，agent 只能做确定性、只读的工作，改动类动作应明确报错而不是静默失败。
func (c *Config) BrainReady() bool {
	return c.LLM.BaseURL != "" && c.LLM.APIKey != ""
}
