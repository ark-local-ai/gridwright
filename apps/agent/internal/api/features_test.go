package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ark-local-ai/ark/apps/agent/internal/zones"
)

// 收件箱：状态、失败记录、跳过。
func TestInboxListAndSkip(t *testing.T) {
	s := newTestServer(t)
	_, layout, _, _ := s.cur()
	name := "新数据.csv"
	if err := os.WriteFile(filepath.Join(layout.Inbox, name), []byte("a,b\n1,2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := s.Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/inbox", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET inbox = %d：%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Items []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"items"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Count != 1 || got.Items[0].Status != "waiting" {
		t.Fatalf("应有 1 个 waiting，得到 %+v", got)
	}

	// 跳过 → 移到 inbox/skipped，不再出现在队列里，且不是 done。
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/inbox/skip",
		strings.NewReader(`{"file":"`+name+`","reason":"测试跳过"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("skip = %d：%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(layout.Done, name)); !os.IsNotExist(err) {
		t.Fatal("跳过不应把文件放进 done（它没有被处理）")
	}
	ents, err := os.ReadDir(layout.Skipped)
	if err != nil || len(ents) == 0 {
		t.Fatalf("skipped 里应有文件：%v %v", ents, err)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/inbox", nil))
	if !strings.Contains(rec.Body.String(), `"count":0`) {
		t.Fatalf("跳过后队列应为空：%s", rec.Body.String())
	}
}

// 处理失败要留下结构化原因（不是只进日志），并把文件留在 inbox。
func TestInboxFailureRecorded(t *testing.T) {
	s := newTestServer(t)
	_, layout, _, _ := s.cur()
	name := "无法处理.csv"
	if err := os.WriteFile(filepath.Join(layout.Inbox, name), []byte("a,b\n1,2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 没配模型、没有规则命中 → RunInboxFile 失败。
	h := s.Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/inbox/run",
		strings.NewReader(`{"file":"`+name+`"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("run = %d：%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"ok":false`) {
		t.Fatalf("无模型时应返回 ok=false：%s", rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(layout.Inbox, name)); err != nil {
		t.Fatal("失败后文件必须留在 inbox（不能静默丢）")
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/inbox", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `"status":"failed"`) || !strings.Contains(body, `"attempts":1`) {
		t.Fatalf("应显示 failed + attempts=1：%s", body)
	}
}

// 认领锁的两种状态：未超时=processing，超时=claimStale（可重试）。
func TestInboxClaimState(t *testing.T) {
	s := newTestServer(t)
	_, layout, _, _ := s.cur()
	name := "被认领.csv"
	p := filepath.Join(layout.Inbox, name)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := p + ".claimlock"
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/inbox", nil))
	if !strings.Contains(rec.Body.String(), `"status":"processing"`) {
		t.Fatalf("新锁应显示 processing：%s", rec.Body.String())
	}
	old := time.Now().Add(-11 * time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Skipf("改 mtime 失败（平台限制）：%v", err)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/inbox", nil))
	if !strings.Contains(rec.Body.String(), `"claimStale":true`) {
		t.Fatalf("超时锁应标记 claimStale：%s", rec.Body.String())
	}
}

// 账目导出：BOM、表头、公式注入转义、筛选。
func TestLedgerExportCSV(t *testing.T) {
	s := newTestServer(t)
	_, _, led, _ := s.cur()
	if err := led.Append(time.Now(), "测试表.xlsx", "Sheet1", "A1", "set",
		"1", "=HYPERLINK(\"http://x\")", "测试", "对话:abc", "", "", "ok"); err != nil {
		t.Fatal(err)
	}
	if err := led.Append(time.Now(), "别的表.xlsx", "Sheet1", "B2", "set",
		"2", "3", "别动", "", "", "", "ok"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/v1/ledger/export?table=测试表&status=ok", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("export = %d：%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.Bytes()
	if len(body) < 3 || body[0] != 0xEF || body[1] != 0xBB || body[2] != 0xBF {
		t.Fatal("导出必须以 UTF-8 BOM 开头（Excel 中文不乱码）")
	}
	text := string(body)
	if !strings.Contains(text, "ts,table,sheet,cell,op,old,new,reason,source,rule,model,status,zone") {
		t.Fatalf("表头不对：%s", text)
	}
	if !strings.Contains(text, "'=HYPERLINK") {
		t.Fatalf("公式注入没有被转义：%s", text)
	}
	if strings.Contains(text, "别的表") {
		t.Fatalf("筛选没生效：%s", text)
	}
	if !strings.Contains(text, "对话:abc") {
		t.Fatalf("source 没导出：%s", text)
	}
}

// 作业区：CRUD + 未知 zone 在 /plan 里 404（不静默回退）。
func TestZonesCRUDAndPlanRejectsUnknown(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()

	body := `{"name":"发票","input":{"type":"folder","path":"","watch":"manual"},"target":{"file":"测试表.xlsx"},"mode":"dryRun"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/zones", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("创建 zone = %d：%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Zone zones.Zone `json:"zone"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Zone.ID == "" || created.Zone.Mode != "dryRun" {
		t.Fatalf("zone 字段不对：%+v", created.Zone)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/zones", nil))
	if !strings.Contains(rec.Body.String(), "发票") {
		t.Fatalf("列表里没有新建的 zone：%s", rec.Body.String())
	}

	// 未知 zone：必须在调模型之前就 404。
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/plan",
		strings.NewReader(`{"instruction":"x","zoneId":"z_nope"}`)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知 zone 应 404，得到 %d：%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/zones/delete",
		strings.NewReader(`{"id":"`+created.Zone.ID+`"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("删除 zone = %d：%s", rec.Code, rec.Body.String())
	}
}

// 任务来源：对话采纳 = chat，手动接口 = manual。看板据此只展示 chat。
func TestTaskSourceChatVsManual(t *testing.T) {
	s := newTestServer(t)
	zs := s.zonesStore()
	if zs == nil {
		t.Skip("作业区存储不可用")
	}
	z, err := zs.Put(zones.Zone{Name: "手工区", Input: zones.Input{Type: zones.InputManual}, Mode: zones.ModeDryRun})
	if err != nil {
		t.Fatal(err)
	}

	tc, err := s.submitZoneTask(z.ID, true, "chat", "c_test")
	if err != nil {
		t.Fatal(err)
	}
	if tc.Source != "chat" || tc.ConversationID != "c_test" {
		t.Fatalf("对话任务来源不对：%+v", tc)
	}
	tm, err := s.submitZoneTask(z.ID, true, "manual", "")
	if err != nil {
		t.Fatal(err)
	}
	if tm.Source != "manual" {
		t.Fatalf("手动任务来源不对：%+v", tm)
	}

	q := s.tasksQueue()
	// 等两个任务都离开队列（queue 单 worker，dryRun 很快）。
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, okc := q.Get(tc.ID)
		m, okm := q.Get(tm.ID)
		if okc && okm && (c.Status == "ok" || c.Status == "failed") && (m.Status == "ok" || m.Status == "failed") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("任务未在预期时间内结束")
}
