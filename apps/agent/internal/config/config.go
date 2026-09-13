// Package config 读取 agent 运行时配置（config.yaml + 环境变量覆盖）。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 是 agent 的运行配置。
type Config struct {
	Workspace   string `yaml:"workspace"`    // 工作区 = 一个文件夹（spec §3）
	PollSeconds int    `yaml:"poll_seconds"` // fsnotify 之外的兜底轮询间隔
	SampleRows  int    `yaml:"sample_rows"`  // 发给脑的每表样例行数

	LLM    LLM    `yaml:"llm"`
	Notify Notify `yaml:"notify"`

	// workspaceSet：工作区是显式配过的（不是我们填的临时默认值）
	workspaceSet bool
}

// LLM 是"脑"的连接配置（OpenAI 兼容 API）。
type LLM struct {
	BaseURL        string `yaml:"base_url"` // 例如 https://api.deepseek.com/v1（以 /v1 结尾）
	APIKey         string `yaml:"api_key"`  // 也可用 env LLM_API_KEY
	Model          string `yaml:"model"`    // 例如 deepseek-chat
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
	Channel        string `yaml:"channel"` // console | serverchan | pushplus | webhook
	ServerChanSKey string `yaml:"serverchan_skey"`
	PushPlusToken  string `yaml:"pushplus_token"`
	WebhookURL     string `yaml:"webhook_url"`
}

// Load 读取配置并应用环境变量覆盖。
//
// 查找顺序（后者覆盖前者）：
//  1. 默认值
//  2. 用户级配置文件（UserConfigPath()，配一次就留在这里，更新/重装可复用）
//  3. path 指定的文件（若存在；通常由 -config 显式给出）
//  4. 环境变量
//
// 若 path 与用户配置路径相同则只读一次，不重复。
func Load(path string) (*Config, error) {
	c := &Config{PollSeconds: 5, SampleRows: 10}
	c.LLM.TimeoutSeconds = 60
	c.Notify.Channel = "console"

	// ① 用户级配置（不存在不算错——首次运行必然没有）
	userPath := UserConfigPath()
	if b, err := os.ReadFile(userPath); err == nil {
		if err := yaml.Unmarshal(b, c); err != nil {
			return nil, fmt.Errorf("解析用户配置 %s: %w", userPath, err)
		}
		if c.Workspace != "" {
			c.markWorkspaceSet()
		}
	}

	// ② 显式指定/默认的配置文件
	if !sameFile(path, userPath) {
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := yaml.Unmarshal(b, c); err != nil {
				return nil, fmt.Errorf("解析配置: %w", err)
			}
			if c.Workspace != "" {
				c.markWorkspaceSet()
			}
		case os.IsNotExist(err) && path == "config.yaml":
			// 默认路径不存在：正常（桌面版随包运行），继续
		default:
			return nil, fmt.Errorf("读取配置 %s: %w", path, err)
		}
	}

	// ③ 环境变量优先（密钥不落 config.yaml）
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
		c.markWorkspaceSet()
	}

	// 工作区**不是**启动前提：没配也要能起来，让用户在界面里选/新建工作区。
	// （原先没工作区就 log.Fatalf 退出——双击 exe 会一闪而过，用户完全不知道发生了什么。
	//  桌面单文件版尤其如此：它没有 config.yaml，用户也不可能先去写一个。）
	if c.Workspace == "" {
		c.Workspace = DefaultWorkspaceDir()
	}
	// 脑（LLM）不是启动前提：没有 key/网络时，只读能力（看表、体检、联动图、
	// 账目、预览）必须照常可用。只有"需要判断"的动作才要求配好脑。
	// 见 docs/agent-architecture/20-离线可用与桌面交付.md
	return c, nil
}

// sameFile 比较两个路径是否指向同一文件（大小写不敏感，Windows 友好）。
func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ca, err1 := filepath.Abs(a)
	cb, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return a == b
	}
	return strings.EqualFold(filepath.Clean(ca), filepath.Clean(cb))
}

// BrainReady 表示"脑"是否配好（有 base_url + api_key）。
// 未就绪时，agent 只能做确定性、只读的工作，改动类动作应明确报错而不是静默失败。
func (c *Config) BrainReady() bool {
	return c.LLM.BaseURL != "" && c.LLM.APIKey != ""
}

// DefaultWorkspaceDir 是"还没选工作区"时的临时落点：
// 放在用户目录下，先把服务起起来，让用户在界面里选或新建真正的工作区。
// 选了之后会写进配置，下次直接用那个。
func DefaultWorkspaceDir() string {
	if dir := os.Getenv("GRIDWRIGHT_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "workspace")
	}
	if cfg, err := os.UserConfigDir(); err == nil && cfg != "" {
		return filepath.Join(cfg, "gridwright", "workspace")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gridwright", "workspace")
}

// HasWorkspace 表示是否**显式**配过工作区（区别于临时默认目录）。
func (c *Config) HasWorkspace() bool { return c.Workspace != "" && c.workspaceSet }

// markWorkspaceSet 由 Load 在读到显式配置时调用。
func (c *Config) markWorkspaceSet() { c.workspaceSet = true }

// MarkWorkspaceChosen 供"用户在界面里选了工作区"时调用。
// 它同时置位并让保存把工作区写进配置。
func (c *Config) MarkWorkspaceChosen() { c.workspaceSet = true }
