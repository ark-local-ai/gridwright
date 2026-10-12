package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// readConvo 通过接口读回会话（handler 写入的是磁盘上的存储，
// 测试里另开的 store 实例是旧内存态，必须重新读）。
func readConvo(t *testing.T, s *Server, id string) []struct {
	Role string `json:"role"`
	Text string `json:"text"`
} {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/conversation?id="+id, nil))
	if rec.Code != 200 {
		t.Fatalf("读会话状态 %d：%s", rec.Code, rec.Body.String())
	}
	var c struct {
		Messages []struct {
			Role string `json:"role"`
			Text string `json:"text"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	return c.Messages
}

func TestConversationAppend(t *testing.T) {
	s := newTestServer(t)
	store, err := s.convoStore()
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.Ensure("", "测试")
	if err != nil {
		t.Fatal(err)
	}

	// 追加一条 agent 摘要
	body := `{"conversationId":"` + c.ID + `","text":"已应用 1 处改动","role":"agent"}`
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/conversation/append", strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("状态 %d：%s", rec.Code, rec.Body.String())
	}
	msgs := readConvo(t, s, c.ID)
	if len(msgs) != 1 {
		t.Fatalf("应有 1 条消息，得到 %+v", msgs)
	}
	if msgs[0].Role != "agent" || msgs[0].Text != "已应用 1 处改动" {
		t.Fatalf("消息内容不对：%+v", msgs[0])
	}

	// 非法角色降级为 agent（禁止借接口伪造用户话）
	body = `{"conversationId":"` + c.ID + `","text":"伪造","role":"user"}`
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/conversation/append", strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("状态 %d", rec.Code)
	}
	msgs = readConvo(t, s, c.ID)
	if msgs[1].Role != "agent" {
		t.Fatalf("user 角色应被降级为 agent，得到 %q", msgs[1].Role)
	}

	// 缺参数 → 400
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/conversation/append", strings.NewReader(`{"text":"x"}`)))
	if rec.Code != 400 {
		t.Fatalf("缺 conversationId 应 400，得到 %d", rec.Code)
	}
}

func TestConversationAppendUnknownConvo(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]string{"conversationId": "nope", "text": "x"})
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/conversation/append", strings.NewReader(string(body))))
	if rec.Code != 404 {
		t.Fatalf("不存在的会话应 404，得到 %d", rec.Code)
	}
}
