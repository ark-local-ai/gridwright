package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestMemoryRequiresApproval 记忆写入必须人明确批准——不给 approve 就拒。
func TestMemoryRequiresApproval(t *testing.T) {
	s := newTestServer(t)
	body, _ := json.Marshal(map[string]any{
		"kind": "decision", "id": "d1", "text": "含运费", "approve": false,
	})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/memory", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未批准应 400，得到 %d：%s", rec.Code, rec.Body.String())
	}
}

// TestMemoryWriteAndRetrieve 批准后写入，并能按文本检索到。
func TestMemoryWriteAndRetrieve(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()

	// 批准写一条决策
	body, _ := json.Marshal(map[string]any{
		"kind": "decision", "id": "d1", "key": map[string]string{"铺位": "B45-1"},
		"text": "按 455 计入（含运费，不含税）", "source": "conversation:c1", "approve": true,
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/memory", bytes.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("写入应成功，得到 %d：%s", rec.Code, rec.Body.String())
	}

	// 全部记忆：应有 1 条决策
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/memory", nil))
	var all struct {
		Counts map[string]int `json:"counts"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &all)
	if all.Counts["decisions"] != 1 {
		t.Fatalf("应有 1 条决策，得到 %+v", all.Counts)
	}

	// 按文本检索：提到 B45-1 应命中
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/memory?q=B45-1%20收租", nil))
	var rel struct {
		Filtered struct {
			Decisions []struct{ ID string } `json:"decisions"`
		} `json:"filtered"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &rel)
	if len(rel.Filtered.Decisions) != 1 {
		t.Fatalf("按文本应命中该决策，得到 %s", rec.Body.String())
	}

	// 检索无关内容：应为空（不给噪音）
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/memory?q=完全不相关的内容XYZ", nil))
	var rel2 struct {
		Filtered struct {
			Decisions []struct{ ID string } `json:"decisions"`
		} `json:"filtered"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &rel2)
	if len(rel2.Filtered.Decisions) != 0 {
		t.Fatalf("无关检索不该命中，得到 %s", rec.Body.String())
	}
}

// TestMemoryStale 过时标记与清除。
func TestMemoryStale(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	body, _ := json.Marshal(map[string]string{"action": "mark", "id": "f1", "note": "租金将调整"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/memory/stale", bytes.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("标记过时应成功，得到 %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/memory", nil))
	var all struct {
		Counts map[string]int `json:"counts"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &all)
	if all.Counts["stale"] != 1 {
		t.Fatalf("应有 1 条过时标记，得到 %+v", all.Counts)
	}
}
