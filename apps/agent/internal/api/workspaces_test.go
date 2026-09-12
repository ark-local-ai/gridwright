package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ark-local-ai/ark/apps/agent/internal/workspace"
)

func newTestServerWithRegistry(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	layout, err := workspace.LayoutOf(dir)
	if err != nil {
		t.Fatal(err)
	}
	led, err := ledgerOpen(layout.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	writeMinimalXlsx(t, filepath.Join(dir, "测试表.xlsx"))
	cfg := configForTest(dir)
	reg, err := workspace.OpenRegistry(filepath.Join(t.TempDir(), "reg.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(cfg, layout, led, "127.0.0.1:0").WithRegistry(reg)
	return s, dir
}

func TestWorkspaceCreateAndList(t *testing.T) {
	s, _ := newTestServerWithRegistry(t)
	h := s.Handler()
	base := t.TempDir()

	// 新建一个工作区
	body, _ := json.Marshal(map[string]string{"name": "松茂御龙湾", "base": base})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/workspace/create", bytes.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("create 状态 %d：%s", rec.Code, rec.Body.String())
	}
	var created struct {
		OK   bool   `json:"ok"`
		Root string `json:"root"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !created.OK {
		t.Fatal("create 应返回 ok")
	}
	if _, err := os.Stat(created.Root); err != nil {
		t.Fatalf("工作区目录应被创建: %v", err)
	}

	// 列表里应能看到它，且标记为当前
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil))
	if rec.Code != 200 {
		t.Fatalf("workspaces 状态 %d", rec.Code)
	}
	var list struct {
		Current string `json:"current"`
		Items   []struct {
			Path    string `json:"path"`
			Current bool   `json:"current"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) == 0 {
		t.Fatal("列表应至少有一项")
	}
	if !samePath(list.Current, created.Root) {
		t.Errorf("current=%q 期望 %q", list.Current, created.Root)
	}
}

func TestWorkspaceOpenExistingDir(t *testing.T) {
	s, _ := newTestServerWithRegistry(t)
	// 另一个已有文件夹
	other := t.TempDir()
	writeMinimalXlsx(t, filepath.Join(other, "别的表.xlsx"))

	body, _ := json.Marshal(map[string]string{"dir": other})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/workspace/open", bytes.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("open 状态 %d：%s", rec.Code, rec.Body.String())
	}
	// 切换后 /workspace 应指向新目录，且能看到那张表
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/workspace", nil))
	var info struct {
		Root   string `json:"root"`
		Tables int    `json:"tables"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if !samePath(info.Root, other) {
		t.Errorf("切换后 root=%q 期望 %q", info.Root, other)
	}
	if info.Tables != 1 {
		t.Errorf("切换后应看到 1 张表，得到 %d", info.Tables)
	}
}

func TestWorkspaceOpenMissingDir(t *testing.T) {
	s, _ := newTestServerWithRegistry(t)
	body, _ := json.Marshal(map[string]string{"dir": filepath.Join(t.TempDir(), "nope")})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/workspace/open", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("不存在的目录应返回 400，得到 %d", rec.Code)
	}
}

func TestWorkspaceForgetDoesNotDeleteDir(t *testing.T) {
	s, _ := newTestServerWithRegistry(t)
	base := t.TempDir()
	body, _ := json.Marshal(map[string]string{"name": "要移除的", "base": base})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/workspace/create", bytes.NewReader(body)))
	var created struct{ Root string }
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	// 移除登记
	body, _ = json.Marshal(map[string]string{"dir": created.Root})
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/workspace/forget", bytes.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("forget 状态 %d", rec.Code)
	}
	// 关键：磁盘上的文件夹必须还在（绝不删用户数据）
	if _, err := os.Stat(created.Root); err != nil {
		t.Fatal("forget 不应删除磁盘目录")
	}
}

func TestSettingsGetPut(t *testing.T) {
	s, _ := newTestServerWithRegistry(t)
	h := s.Handler()

	// 初始：没配脑
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil))
	var got struct {
		HasAPIKey  bool `json:"hasApiKey"`
		BrainReady bool `json:"brainReady"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.HasAPIKey || got.BrainReady {
		t.Fatal("初始不应有 key")
	}

	// 配置脑
	body, _ := json.Marshal(map[string]string{
		"baseUrl": "https://api.example.com/v1", "apiKey": "sk-test", "model": "m1",
	})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewReader(body)))
	var put struct{ BrainReady bool }
	_ = json.Unmarshal(rec.Body.Bytes(), &put)
	if !put.BrainReady {
		t.Fatal("配好后 brainReady 应为 true")
	}

	// 再读：密钥不回明文，只回 hasApiKey
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil))
	var after struct {
		BaseURL    string `json:"baseUrl"`
		Model      string `json:"model"`
		HasAPIKey  bool   `json:"hasApiKey"`
		APIKeyLeak string `json:"apiKey"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &after)
	if !after.HasAPIKey {
		t.Error("hasApiKey 应为 true")
	}
	if after.APIKeyLeak != "" {
		t.Error("GET 不应回传 API key 明文")
	}
	if after.BaseURL != "https://api.example.com/v1" || after.Model != "m1" {
		t.Errorf("配置未生效: %+v", after)
	}
}
