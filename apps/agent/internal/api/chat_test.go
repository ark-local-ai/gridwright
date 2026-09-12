package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/ark-local-ai/ark/apps/agent/internal/convo"
)

// TestConversationsPersistence 会话落盘在工作区里，切换后仍能读回。
func TestConversationsLifecycle(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()

	// 初始为空
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/conversations", nil))
	if rec.Code != 200 {
		t.Fatalf("list 状态 %d", rec.Code)
	}
	var list struct {
		Items []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 0 {
		t.Fatalf("初始应为空，得到 %d", len(list.Items))
	}

	// 直接往存储里写一条（绕开模型），验证读回
	store, err := s.convoStore()
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.Ensure("", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(c.ID, convo.Message{Role: convo.RoleUser, Text: "每天下班前体检一次"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(c.ID, convo.Message{
		Role: convo.RoleAgent, Text: "好，我理解为每天 17:30 做只读体检。",
		Proposal: &convo.Proposal{Kind: "task", Title: "工作日体检", Schedule: "30 17 * * 1-5"},
	}); err != nil {
		t.Fatal(err)
	}

	// 列表里应出现，且标题取自首句
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/conversations", nil))
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Items) != 1 {
		t.Fatalf("应有 1 条会话，得到 %d", len(list.Items))
	}
	if list.Items[0].Title != "每天下班前体检一次" {
		t.Errorf("标题应取自首句，得到 %q", list.Items[0].Title)
	}

	// 读详情：消息与 proposal 都在
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/conversation?id="+c.ID, nil))
	var full convo.Conversation
	if err := json.Unmarshal(rec.Body.Bytes(), &full); err != nil {
		t.Fatal(err)
	}
	if len(full.Messages) != 2 {
		t.Fatalf("应有 2 条消息，得到 %d", len(full.Messages))
	}
	if full.Messages[1].Proposal == nil || full.Messages[1].Proposal.Kind != "task" {
		t.Fatalf("第二条应带 task 提案，得到 %+v", full.Messages[1])
	}

	// 删除
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/conversation?id="+c.ID, nil))
	if rec.Code != 200 {
		t.Fatalf("delete 状态 %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/conversations", nil))
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Items) != 0 {
		t.Fatalf("删除后应为空，得到 %d", len(list.Items))
	}
}

// TestChatWithoutBrain 没配模型时 /chat 明确提示，不崩。
func TestChatWithoutBrain(t *testing.T) {
	s := newTestServer(t)
	body, _ := json.Marshal(map[string]string{"message": "每天下班前体检一次"})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/chat", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未配模型应 400，得到 %d：%s", rec.Code, rec.Body.String())
	}
}

// TestConvoStoredInWorkspace 会话文件应落在工作区里（跟着工作区走，含切换后）。
func TestConvoStoredInWorkspace(t *testing.T) {
	s := newTestServer(t)
	store, _ := s.convoStore()
	if _, err := store.Ensure("", ""); err != nil {
		t.Fatal(err)
	}
	// 文件应出现在当前工作区根目录
	_ = store
	_, layout, _, _ := s.cur()
	path := layout.Root + "/conversations.json"
	if !fileExists(path) {
		t.Fatalf("会话文件应落在工作区：%s", path)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
