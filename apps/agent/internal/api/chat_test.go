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

// TestConvoRename 改标题：记录要能翻回来，就得能起个记得住的名字。
func TestConvoRename(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	store, _ := s.convoStore()
	c, err := store.Ensure("", "")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = store.Append(c.ID, convo.Message{Role: convo.RoleUser, Text: "帮我看下这个"})

	// 默认标题取首句——常常是"帮我看下这个"这种没能耐的名字
	body, _ := json.Marshal(map[string]string{"title": "御龙湾 9 月对账"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/conversation?id="+c.ID, bytes.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("rename 状态 %d：%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/conversation?id="+c.ID, nil))
	var full convo.Conversation
	_ = json.Unmarshal(rec.Body.Bytes(), &full)
	if full.Title != "御龙湾 9 月对账" {
		t.Errorf("标题没改成，得到 %q", full.Title)
	}

	// 空标题要拒：列表里不该出现无名记录
	body, _ = json.Marshal(map[string]string{"title": "   "})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/conversation?id="+c.ID, bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("空标题应 400，得到 %d", rec.Code)
	}
}

// TestDistillNeedsBrain 没配模型时 /conversation/distill 明确报错，不崩。
//
// 为什么单独测这条：提炼是"记录 → 记忆"的闸门，它出问题时不能表现得像
// "这段对话没有可沉淀的东西"——那会让人以为提炼过了、只是没内容。
// 必须明确说"要配模型"。
func TestDistillNeedsBrain(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	store, _ := s.convoStore()
	c, _ := store.Ensure("", "")
	_, _ = store.Append(c.ID, convo.Message{Role: convo.RoleUser, Text: "B31 收到 8 月租金 23540"})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/conversation/distill?id="+c.ID, nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未配模型应 400，得到 %d：%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("模型")) {
		t.Errorf("报错应说清是模型没配，得到 %s", rec.Body.String())
	}
}

// TestDistillMissingConvo 记录不存在时报 404，而不是空候选。
func TestDistillMissingConvo(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/conversation/distill?id=不存在", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("不存在的记录应 404（未配模型时也应先说模型），得到 %d：%s", rec.Code, rec.Body.String())
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
