package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ark-local-ai/ark/apps/agent/internal/convo"
	"github.com/ark-local-ai/ark/apps/agent/internal/tasks"
	"github.com/ark-local-ai/ark/apps/agent/internal/zones"
)

// waitTaskDone 等任务进终态。
//
// 必须等：worker 还在往工作区写 zones.json/tasks.json，测试提前返回会让
// t.TempDir 的清理撞上未完成的写入（"directory is not empty"），失败随机出现在
// 别的用例名下，极难查。
func waitTaskDone(t *testing.T, s *Server, id string) {
	t.Helper()
	q := s.tasksQueue()
	if q == nil {
		return
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		cur, ok := q.Get(id)
		if ok && (cur.Status == tasks.StatusOK || cur.Status == tasks.StatusFailed || cur.Status == tasks.StatusCanceled) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("任务 %s 超时未结束", id)
}

// TestChatZoneCommandWithoutBrain 没配模型也能用「@区名 试运行」派单。
//
// 这是需求的核心：对话是作业区任务的唯一入口，指令语义又是确定的，
// 不该因为模型没配就整条路走不通。
func TestChatZoneCommandWithoutBrain(t *testing.T) {
	s := newTestServer(t)
	zs := s.zonesStore()
	if zs == nil {
		t.Skip("作业区存储不可用")
	}
	z, err := zs.Put(zones.Zone{Name: "发票目录", Input: zones.Input{Type: zones.InputManual}, Mode: zones.ModeDryRun})
	if err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]string{"message": "@发票目录 试运行"})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/chat", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("未配模型也应 200，得到 %d：%s", rec.Code, rec.Body.String())
	}
	var out struct {
		ConversationID string `json:"conversationId"`
		Conversation   struct {
			Messages []convo.Message `json:"messages"`
		} `json:"conversation"`
		Task struct {
			ID             string `json:"id"`
			ZoneID         string `json:"zoneId"`
			Source         string `json:"source"`
			ConversationID string `json:"conversationId"`
			DryRun         bool   `json:"dryRun"`
		} `json:"task"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Task.ID == "" || out.Task.Source != "chat" || out.Task.ZoneID != z.ID {
		t.Fatalf("任务不对：%+v", out.Task)
	}
	if out.Task.ConversationID != out.ConversationID {
		t.Errorf("任务应挂在这条会话上：task=%s convo=%s", out.Task.ConversationID, out.ConversationID)
	}
	if !out.Task.DryRun {
		t.Error("试运行应为 dryRun")
	}
	// 会话里既要有用户原话，也要有 agent 的回话（否则刷新后就"没反应"）。
	var hasUser, hasAgent bool
	for _, m := range out.Conversation.Messages {
		if m.Role == convo.RoleUser && m.Text == "@发票目录 试运行" {
			hasUser = true
		}
		if m.Role == convo.RoleAgent && strings.Contains(m.Text, "已加入任务队列") {
			hasAgent = true
		}
	}
	if !hasUser || !hasAgent {
		t.Fatalf("会话消息不全：user=%v agent=%v %+v", hasUser, hasAgent, out.Conversation.Messages)
	}
	waitTaskDone(t, s, out.Task.ID)
}

// TestChatZoneCommandUnknown 区名对不上时 404，且错误里带上是哪个名字。
func TestChatZoneCommandUnknown(t *testing.T) {
	s := newTestServer(t)
	body, _ := json.Marshal(map[string]string{"message": "@不存在的区 运行"})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/chat", bytes.NewReader(body)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知区应 404，得到 %d：%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "没有匹配的作业区") {
		t.Errorf("错误应说清没有匹配的作业区，得到 %s", rec.Body.String())
	}
}

// TestChatZoneCommandByID 按 zone id 派单也认；动作「跑一次」= 真跑（但区自身
// 是 dryRun 时仍强制演习，安全默认优先）。
func TestChatZoneCommandByID(t *testing.T) {
	s := newTestServer(t)
	zs := s.zonesStore()
	if zs == nil {
		t.Skip("作业区存储不可用")
	}
	z, err := zs.Put(zones.Zone{Name: "Rent Ledger", Input: zones.Input{Type: zones.InputManual}, Mode: zones.ModeApply})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"message": "  @" + z.ID + " 跑一次  "})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/chat", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("按 ID 派单应 200，得到 %d：%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Task struct {
			ID     string `json:"id"`
			ZoneID string `json:"zoneId"`
			Source string `json:"source"`
			DryRun bool   `json:"dryRun"`
		} `json:"task"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Task.ZoneID != z.ID || out.Task.Source != "chat" || out.Task.DryRun {
		t.Fatalf("任务不对：%+v", out.Task)
	}
	waitTaskDone(t, s, out.Task.ID)
}
