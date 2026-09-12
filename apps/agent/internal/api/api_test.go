package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ark-local-ai/ark/apps/agent/internal/config"
	"github.com/ark-local-ai/ark/apps/agent/internal/ledger"
	"github.com/ark-local-ai/ark/apps/agent/internal/workspace"
)

// newTestServer 建一个指向临时工作区的 server（不含真实表），并放一张最小 xlsx。
func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	layout, err := workspace.LayoutOf(dir)
	if err != nil {
		t.Fatal(err)
	}
	led, err := ledger.Open(layout.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Workspace: dir}
	// 放一张表，让 workspace/graph/scan 有东西可用
	writeMinimalXlsx(t, filepath.Join(dir, "测试表.xlsx"))
	return New(cfg, layout, led, "127.0.0.1:0")
}

func TestHealthAndWorkspace(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()

	// health
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	if rec.Code != 200 {
		t.Fatalf("health 状态 %d", rec.Code)
	}

	// workspace
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/workspace", nil))
	if rec.Code != 200 {
		t.Fatalf("workspace 状态 %d：%s", rec.Code, rec.Body.String())
	}
	var info struct {
		Tables int `json:"tables"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Tables != 1 {
		t.Fatalf("期望 1 张表，得到 %d", info.Tables)
	}
}

func TestGraphAndLedgerEndpoints(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()

	// graph：节点现在是 {file, sheet}，且扫整个工作区
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/graph", nil))
	if rec.Code != 200 {
		t.Fatalf("graph 状态 %d：%s", rec.Code, rec.Body.String())
	}
	var g struct {
		Files []string `json:"files"`
		Nodes []struct {
			File  string `json:"file"`
			Sheet string `json:"sheet"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) == 0 {
		t.Error("graph 应返回至少一个 sheet 节点")
	}
	if len(g.Files) == 0 {
		t.Error("graph 应返回文件清单")
	}
	// 节点应带文件前缀（跨文件不撞车的前提）
	if g.Nodes[0].File == "" {
		t.Error("节点应带文件名")
	}

	// 带 ?node= 时附传播闭包，且是数组不是 null
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/graph?node=nonexistent!sheet", nil))
	if rec.Code != 200 {
		t.Fatalf("graph?node= 状态 %d", rec.Code)
	}
	if !contains(string(rec.Body.Bytes()), `"to":[]`) {
		t.Errorf("propagate.to 应为空数组而非 null：%s", rec.Body.String())
	}

	// ledger（空账目也应是 200 + 空数组）
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/ledger?limit=10", nil))
	if rec.Code != 200 {
		t.Fatalf("ledger 状态 %d：%s", rec.Code, rec.Body.String())
	}
}

func TestScanEndpointReadOnly(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	path := filepath.Join(s.Layout.Root, "测试表.xlsx")
	before := hashFile(t, path)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/scan/run", nil))
	if rec.Code != 200 {
		t.Fatalf("scan/run 状态 %d：%s", rec.Code, rec.Body.String())
	}
	// 关键：体检绝不能改文件
	if after := hashFile(t, path); after != before {
		t.Fatal("体检改动了文件！必须只读")
	}
}

func TestMethodGuards(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	// 只读端点不接受 POST
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/workspace", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("workspace 应拒绝 POST，得到 %d", rec.Code)
	}
}

func TestMissingWorkspaceReturnsError(t *testing.T) {
	// 空工作区（没有表）→ 明确报错，而不是 500 静默
	dir := t.TempDir()
	layout, _ := workspace.LayoutOf(dir)
	led, _ := ledger.Open(layout.Ledger)
	s := New(&config.Config{Workspace: dir}, layout, led, "127.0.0.1:0")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/graph", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("空工作区应返回 400，得到 %d：%s", rec.Code, rec.Body.String())
	}
}

func hashFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// 简单校验和即可，无需加密强度
	var sum int64
	for _, x := range b {
		sum = sum*31 + int64(x)
	}
	return string(rune(sum))
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
