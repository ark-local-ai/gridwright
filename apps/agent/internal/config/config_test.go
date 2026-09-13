package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOfflineNoConfigFile 桌面版没有 config.yaml：只要能拿到工作区就应可启动。
// 这是"没网络也能用 xls"的前提——引擎不能因为没有配置文件/没有 key 就拒绝启动。
func TestNoConfigFileUsesEnv(t *testing.T) {
	isolateConfig(t)
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
	isolateConfig(t)
	t.Setenv("WORKSPACE", t.TempDir())
	if _, err := Load("does-not-exist.yaml"); err == nil {
		t.Fatal("显式指定的配置不存在时应报错")
	}
}

// TestConfigFileRead 正常读配置文件 + env 覆盖。
func TestConfigFileRead(t *testing.T) {
	isolateConfig(t)
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

// TestMissingWorkspaceFallsBack 没配工作区**不该报错**——要能起服务，
// 让用户在界面里选。原先这里要求报错，结果双击 exe 一闪而过（真实踩到）。
func TestMissingWorkspaceFallsBack(t *testing.T) {
	isolateConfig(t)
	t.Setenv("WORKSPACE", "")
	c, err := Load("config.yaml")
	if err != nil {
		t.Fatalf("没配工作区也应能启动（让用户去选），得到: %v", err)
	}
	if c.Workspace == "" {
		t.Fatal("应给一个临时默认目录，服务才能起来")
	}
	if c.HasWorkspace() {
		t.Error("临时默认目录不该被当作\"用户已选\"")
	}
	// 用户选了之后才算已选
	c.MarkWorkspaceChosen()
	if !c.HasWorkspace() {
		t.Error("标记后应算已选")
	}
}

// TestSaveAndReload 配一次 → 落盘 → 重启后能读回（"更新可复用"的核心保证）。
func TestSaveAndReload(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GRIDWRIGHT_CONFIG_DIR", dir)
	t.Setenv("WORKSPACE", filepath.Join(dir, "ws"))
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("LLM_BASE_URL", "")

	// 首次启动：没有配置文件也能起
	c, err := Load("config.yaml")
	if err != nil {
		t.Fatalf("首次启动不应报错: %v", err)
	}
	// 配模型并保存
	c.LLM.BaseURL = "https://api.example.com/v1"
	c.LLM.APIKey = "sk-abc"
	c.LLM.Model = "m1"
	if err := c.Save(UserConfigPath()); err != nil {
		t.Fatal(err)
	}

	// 模拟"重启应用"：重新 Load（不给任何 LLM env）
	c2, err := Load("config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !c2.BrainReady() {
		t.Error("重启后应读回模型配置")
	}
	if c2.LLM.Model != "m1" || c2.LLM.BaseURL != "https://api.example.com/v1" {
		t.Errorf("配置未复用：%+v", c2.LLM)
	}
}

// TestSavePreservesWorkspace 保存设置不应把工作区弄丢（合并而非覆盖）。
func TestSavePreservesOtherFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GRIDWRIGHT_CONFIG_DIR", dir)
	path := UserConfigPath()

	c := &Config{Workspace: filepath.Join(dir, "ws"), PollSeconds: 9, SampleRows: 7}
	c.LLM.Model = "first"
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	// 只改模型再存（模拟只动设置页的一项）
	c2 := &Config{Workspace: filepath.Join(dir, "ws"), PollSeconds: 9, SampleRows: 7}
	c2.LLM.Model = "second"
	if err := c2.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load("config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got.LLM.Model != "second" {
		t.Errorf("模型应更新为 second，得到 %q", got.LLM.Model)
	}
	if got.PollSeconds != 9 || got.SampleRows != 7 {
		t.Errorf("其他字段应保留：poll=%d sample=%d", got.PollSeconds, got.SampleRows)
	}
}

// isolateConfig 把用户配置目录指到临时目录，避免测试读到开发机上真实的
// %APPDATA%\gridwright\config.yaml（否则测试结果依赖本机状态）。
func isolateConfig(t *testing.T) {
	t.Helper()
	t.Setenv("GRIDWRIGHT_CONFIG_DIR", t.TempDir())
}
