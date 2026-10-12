package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPromptsCRUD(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()

	// 初始为空
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/prompts", nil))
	if rec.Code != 200 {
		t.Fatalf("GET /prompts 状态 %d", rec.Code)
	}
	var list struct {
		Prompts []struct {
			ID string `json:"id"`
		} `json:"prompts"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Prompts) != 0 {
		t.Fatalf("初始应为空，得到 %d 条", len(list.Prompts))
	}
	if !strings.HasSuffix(list.Path, "prompts.yaml") {
		t.Fatalf("路径应是工作区里的 prompts.yaml，得到 %s", list.Path)
	}

	// 存一条：槽位必须由模板派生
	body := `{"name":"收租录账","when":"收到租金实收时","template":"记一笔：{铺位} 收到 {月份} 租金 {金额}"}`
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/prompts", strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("POST /prompts 状态 %d：%s", rec.Code, rec.Body.String())
	}
	var saved struct {
		OK     bool `json:"ok"`
		Prompt struct {
			ID    string   `json:"id"`
			Slots []string `json:"slots"`
		} `json:"prompt"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.OK || saved.Prompt.ID == "" || len(saved.Prompt.Slots) != 3 {
		t.Fatalf("保存结果不对：%+v", saved)
	}

	// 再列出应有 1 条
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/prompts", nil))
	list.Prompts = nil
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Prompts) != 1 {
		t.Fatalf("保存后应有 1 条，得到 %d", len(list.Prompts))
	}

	// 用一次记命中
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/prompts/use",
		strings.NewReader(`{"id":"`+saved.Prompt.ID+`"}`)))
	if rec.Code != 200 {
		t.Fatalf("POST /prompts/use 状态 %d", rec.Code)
	}

	// 缺 template → 400
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/prompts", strings.NewReader(`{"name":"x"}`)))
	if rec.Code != 400 {
		t.Fatalf("缺 template 应 400，得到 %d", rec.Code)
	}

	// 删除
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/prompts/delete",
		strings.NewReader(`{"id":"`+saved.Prompt.ID+`"}`)))
	if rec.Code != 200 {
		t.Fatalf("delete 状态 %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/prompts", nil))
	list.Prompts = nil
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Prompts) != 0 {
		t.Fatalf("删除后应为空，得到 %d", len(list.Prompts))
	}
}

func TestBuiltinPromptsEndpoint(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/prompts/builtin", nil))
	if rec.Code != 200 {
		t.Fatalf("状态 %d：%s", rec.Code, rec.Body.String())
	}
	var r struct {
		Templates []struct {
			Name     string   `json:"name"`
			Template string   `json:"template"`
			Slots    []string `json:"slots"`
		} `json:"templates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Templates) < 5 {
		t.Fatalf("内置模板应至少 5 个，得到 %d", len(r.Templates))
	}
	for _, tp := range r.Templates {
		if tp.Name == "" || tp.Template == "" || len(tp.Slots) == 0 {
			t.Fatalf("内置模板不完整：%+v", tp)
		}
	}
}

// 勾选消息的 distill 路径：带 messages 时不该去读会话（未配模型应报"还没配置模型"，
// 而不是"会话不存在"）。
func TestPromptsDistillFromMessagesNoBrain(t *testing.T) {
	s := newTestServer(t)
	body := `{"messages":[{"role":"user","text":"记一笔 A03 收到 8 月租金 10000"}]}`
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/prompts/distill", strings.NewReader(body)))
	if rec.Code != 400 {
		t.Fatalf("未配模型应 400，得到 %d：%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "模型") {
		t.Fatalf("错误应是缺模型而非缺会话：%s", rec.Body.String())
	}

	// 既无 messages 也无 id → 400
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/prompts/distill", strings.NewReader(`{}`)))
	if rec.Code != 400 {
		t.Fatalf("缺参数应 400，得到 %d", rec.Code)
	}
}
