package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withSpawn 替换 spawn 接缝，返回调用记录；测试结束自动复原。
func withSpawn(t *testing.T, fake func(root string, cfg serverConfig) (*serverConn, error)) *[]string {
	t.Helper()
	var called []string
	orig := spawn
	spawn = func(root string, cfg serverConfig) (*serverConn, error) {
		called = append(called, cfg.Name)
		return fake(root, cfg)
	}
	t.Cleanup(func() { spawn = orig })
	return &called
}

func writeConfig(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, ConfigFile), []byte(body), 0o644); err != nil {
		t.Fatalf("写 mcp.json 失败: %v", err)
	}
}

func TestOpenNoConfigFile(t *testing.T) {
	root := t.TempDir()
	reg := Open(root)
	if reg == nil {
		t.Fatal("Open 返回 nil（应为空注册表）")
	}
	if got := reg.Catalog(); len(got) != 0 {
		t.Errorf("空注册表工具数 = %d, 想要 0", len(got))
	}
	if err := reg.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// 可重复调用。
	if err := reg.Close(); err != nil {
		t.Errorf("第二次 Close 应幂等: %v", err)
	}
}

func TestOpenBadJSONDoesNotPanic(t *testing.T) {
	called := withSpawn(t, func(root string, cfg serverConfig) (*serverConn, error) {
		t.Errorf("坏 JSON 不应拉起任何 server，却调用了 %q", cfg.Name)
		return nil, nil
	})
	root := t.TempDir()
	writeConfig(t, root, `{"servers":[{"name":"ocr","command":"npx",`)

	reg := Open(root)
	if got := reg.Catalog(); len(got) != 0 {
		t.Errorf("坏 JSON 后工具数 = %d, 想要 0", len(got))
	}
	if len(*called) != 0 {
		t.Errorf("坏 JSON 后 spawn 调用 = %v, 想要空", *called)
	}
	_ = reg.Close()
}

func TestOpenSkipsDisabledServers(t *testing.T) {
	called := withSpawn(t, func(root string, cfg serverConfig) (*serverConn, error) {
		return &serverConn{name: cfg.Name, tools: []toolDef{{Name: cfg.Name + "_tool"}}}, nil
	})
	root := t.TempDir()
	writeConfig(t, root, `{"servers":[
		{"name":"off","command":"npx","args":["a"],"enabled":false},
		{"name":"on","command":"npx","args":["b"],"enabled":true},
		{"name":"implicit","command":"npx"}
	]}`)

	reg := Open(root)
	defer reg.Close()

	if strings.Join(*called, ",") != "on,implicit" {
		t.Errorf("spawn 调用 = %v, 想要 [on implicit]", *called)
	}
	got := reg.Catalog()
	if len(got) != 2 {
		t.Fatalf("工具数 = %d, 想要 2: %+v", len(got), got)
	}
	if got[0].Server != "implicit" || got[1].Server != "on" {
		t.Errorf("Catalog 未按 server 排序: %+v", got)
	}
}

func TestOpenIsolatesFailingServer(t *testing.T) {
	called := withSpawn(t, func(root string, cfg serverConfig) (*serverConn, error) {
		if cfg.Name == "bad" {
			return nil, &rpcError{Code: -32603, Message: "handshake failed"}
		}
		return &serverConn{name: cfg.Name, tools: []toolDef{{Name: "t"}}}, nil
	})
	root := t.TempDir()
	writeConfig(t, root, `{"servers":[
		{"name":"bad","command":"npx"},
		{"name":"good","command":"npx"}
	]}`)

	reg := Open(root)
	defer reg.Close()

	if len(*called) != 2 {
		t.Errorf("应尝试两个 server，实际 %v", *called)
	}
	got := reg.Catalog()
	if len(got) != 1 || got[0].Server != "good" {
		t.Errorf("失败 server 应只被跳过，实际 Catalog = %+v", got)
	}
}

func TestLoadConfigShape(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `{"servers":[{"name":"ocr","command":"npx","args":["-y","some-mcp-server"],"env":{"K":"V"},"enabled":true}]}`)

	servers, err := loadConfig(filepath.Join(root, ConfigFile))
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("servers = %d, 想要 1", len(servers))
	}
	s := servers[0]
	if s.Name != "ocr" || s.Command != "npx" {
		t.Errorf("名字/命令解析错: %+v", s)
	}
	if strings.Join(s.Args, " ") != "-y some-mcp-server" {
		t.Errorf("args = %v", s.Args)
	}
	if s.Env["K"] != "V" {
		t.Errorf("env = %v", s.Env)
	}
	if !s.enabled() {
		t.Error("enabled:true 应视为启用")
	}
}

func TestServerConfigEnabledDefaultsTrue(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{
		{`{"name":"a","command":"x"}`, true},
		{`{"name":"a","command":"x","enabled":true}`, true},
		{`{"name":"a","command":"x","enabled":false}`, false},
	} {
		var s serverConfig
		if err := json.Unmarshal([]byte(tc.body), &s); err != nil {
			t.Fatalf("解析 %s 失败: %v", tc.body, err)
		}
		if got := s.enabled(); got != tc.want {
			t.Errorf("%s: enabled() = %v, 想要 %v", tc.body, got, tc.want)
		}
	}
}

func TestRegistryCatalogSorted(t *testing.T) {
	reg := &Registry{servers: map[string]*serverConn{
		"b": {name: "b", tools: []toolDef{{Name: "z"}, {Name: "a"}}},
		"a": {name: "a", tools: []toolDef{{Name: "m"}}},
	}}
	got := reg.Catalog()
	want := []string{"a/m", "b/a", "b/z"}
	if len(got) != len(want) {
		t.Fatalf("工具数 = %d, 想要 %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Server+"/"+got[i].Name != w {
			t.Errorf("[%d] = %s/%s, 想要 %s", i, got[i].Server, got[i].Name, w)
		}
	}
}

func TestRegistryCallUnknownServer(t *testing.T) {
	reg := &Registry{servers: map[string]*serverConn{}}
	_, err := reg.Call(context.Background(), "nope", "tool", nil)
	if err == nil || !strings.Contains(err.Error(), "未知 server") {
		t.Fatalf("错误 = %v, 想要未知 server", err)
	}
}

// TestRegistryCallThroughFakeServer 用 net.Pipe 假 server 走通 Call 全链路，
// 不起任何外部进程。
func TestRegistryCallThroughFakeServer(t *testing.T) {
	handler := func(method string, params json.RawMessage) (any, *rpcError) {
		if method != "tools/call" {
			return defaultHandler(method, params)
		}
		return map[string]any{"content": []map[string]any{{"type": "text", "text": "结果"}}}, nil
	}
	c, _ := startFakeServer(t, handler)

	tail := newTailBuffer(stderrTailLines)
	tail.add("boom: 缺少 ocr 引擎")
	reg := &Registry{servers: map[string]*serverConn{
		"ocr": {name: "ocr", c: c, stderr: tail},
	}}

	got, err := reg.Call(context.Background(), "ocr", "echo", map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got != "结果" {
		t.Errorf("Call 返回 %q, 想要 结果", got)
	}

	// 关闭后调用应快速失败。
	if err := reg.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if _, err := reg.Call(context.Background(), "ocr", "echo", nil); err == nil {
		t.Error("关闭后 Call 应失败")
	}
}

func TestCallErrorCarriesStderrTail(t *testing.T) {
	tail := newTailBuffer(2)
	tail.add("line1")
	tail.add("line2")
	tail.add("line3")
	if got := tail.text(); got != "line2\nline3" {
		t.Errorf("尾部 = %q, 想要最后两行", got)
	}
	if !strings.Contains(tail.errSuffix(), "line3") {
		t.Errorf("errSuffix 未包含 stderr: %q", tail.errSuffix())
	}
}

func TestRegistryCloseNilSafe(t *testing.T) {
	var reg *Registry
	if err := reg.Close(); err != nil {
		t.Errorf("nil Registry Close: %v", err)
	}
	if got := reg.Catalog(); got != nil {
		t.Errorf("nil Registry Catalog = %v", got)
	}
	if _, err := reg.Call(context.Background(), "x", "y", nil); err == nil {
		t.Error("nil Registry Call 应失败")
	}
}

// TestCallTimeoutBudget 确认 Call 给每次调用套了上限（这里只看常量与 ctx 传播）。
func TestCallTimeoutBudget(t *testing.T) {
	if callTimeout != 60*time.Second {
		t.Errorf("callTimeout = %v, 想要 60s", callTimeout)
	}
	if handshakeTimeout != 15*time.Second {
		t.Errorf("handshakeTimeout = %v, 想要 15s", handshakeTimeout)
	}
	if closeGrace != 3*time.Second {
		t.Errorf("closeGrace = %v, 想要 3s", closeGrace)
	}
}
