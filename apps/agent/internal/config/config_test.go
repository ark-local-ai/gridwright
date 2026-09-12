package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOfflineNoConfigFile 桌面版没有 config.yaml：只要能拿到工作区就应可启动。
// 这是"没网络也能用 xls"的前提——引擎不能因为没有配置文件/没有 key 就拒绝启动。
func TestNoConfigFileUsesEnv(t *testing.T) {
	t.Setenv("WORKSPACE", filepath.Join(t.TempDir(), "ws"))
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("LLM_BASE_URL", "")

	// 默认路径不存在是允许的（桌面版随包运行，不带 config.yaml）
	c, err := Load("config.yaml")
	if err != nil {
		t.Fatalf("默认配置缺失不应报错，得到: %v", err)
	}
	if c.Workspace == "" {
		t.Fatal("应读到 WORKSPACE 环境变量")
	}
	if c.BrainReady() {
		t.Fatal("没配 key 时 BrainReady 应为 false")
	}
	// 离线时也该有合理默认，供只读能力使用
	if c.PollSeconds <= 0 || c.SampleRows <= 0 {
		t.Errorf("应有默认值，得到 poll=%d sample=%d", c.PollSeconds, c.SampleRows)
	}
}

// TestExplicitMissingConfigIsError 显式指定了配置文件却读不到 → 应报错（不静默）。
func TestExplicitMissingConfigIsError(t *testing.T) {
	t.Setenv("WORKSPACE", t.TempDir())
	if _, err := Load("does-not-exist.yaml"); err == nil {
		t.Fatal("显式指定的配置不存在时应报错")
	}
}

// TestConfigFileRead 正常读配置文件 + env 覆盖。
func TestConfigFileRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := `
workspace: ` + filepath.ToSlash(dir) + `
poll_seconds: 7
llm:
  base_url: https://api.example.com/v1
  api_key: file-key
  model: some-model
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.PollSeconds != 7 {
		t.Errorf("poll_seconds 应为 7，得到 %d", c.PollSeconds)
	}
	if !c.BrainReady() {
		t.Error("配了 base_url + api_key 后 BrainReady 应为 true")
	}
	if c.LLM.Model != "some-model" {
		t.Errorf("model=%q", c.LLM.Model)
	}

	// env 覆盖文件
	t.Setenv("LLM_MODEL", "env-model")
	c2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c2.LLM.Model != "env-model" {
		t.Errorf("env 应覆盖文件，得到 %q", c2.LLM.Model)
	}
}

// TestMissingWorkspaceIsError 工作区是硬要求（没有它无从下手）。
func TestMissingWorkspaceIsError(t *testing.T) {
	t.Setenv("WORKSPACE", "")
	if _, err := Load("config.yaml"); err == nil {
		t.Fatal("没有工作区应报错")
	}
}
