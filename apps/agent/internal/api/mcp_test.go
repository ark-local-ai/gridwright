package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ark-local-ai/ark/apps/agent/internal/mcp"
)

// TestToolRunnerNoConfig 没配 mcp.json 是常态：toolRunner 返回 nil（对话照常），
// 接口返回空数组而不是 null/500。
func TestToolRunnerNoConfig(t *testing.T) {
	s := newTestServer(t)
	if got := s.toolRunner(); got != nil {
		t.Fatalf("没有 mcp.json 时应返回 nil，得到 %#v", got)
	}

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tools/mcp", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("状态 %d：%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v（原文 %s）", err, rec.Body.String())
	}
	if body.Items == nil {
		t.Error("应返回空数组 []，不是 null")
	}
	if len(body.Items) != 0 {
		t.Errorf("无工具时 items 应为空，得到 %d 项", len(body.Items))
	}
}

// TestToolRunnerBadConfig 配置坏了也只是没工具，不报错。
func TestToolRunnerBadConfig(t *testing.T) {
	s := newTestServer(t)
	if err := os.WriteFile(filepath.Join(s.Layout.Root, mcp.ConfigFile), []byte("{不是 JSON"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := s.toolRunner(); got != nil {
		t.Fatalf("配置解析失败时应返回 nil，得到 %#v", got)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tools/mcp", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("状态 %d", rec.Code)
	}
}

// TestToolRunnerCachesByRoot 同一个工作区只打开一次（避免每次对话重拉子进程）。
// 没工具时也应记住"这个 root 打开过"，所以两次拿到 nil 且不重复记日志。
func TestToolRunnerCachesByRoot(t *testing.T) {
	s := newTestServer(t)
	s.toolRunner()
	root := s.mcpRoot
	if root == "" {
		t.Fatal("第一次打开后应记住 root（否则每次对话都会重读配置、重拉进程）")
	}
	if got := s.toolRunner(); got != nil {
		t.Fatal("空注册表也应复用，不该变成非 nil")
	}
	if s.mcpRoot != root {
		t.Errorf("root 缓存应保持 %q，得到 %q", root, s.mcpRoot)
	}
}

// TestMCPToolsRejectsNonGet 只读接口：别的方法直接拒。
func TestMCPToolsRejectsNonGet(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/tools/mcp", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST 应 405，得到 %d", rec.Code)
	}
}
