package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/tasks"
	"github.com/ark-local-ai/ark/apps/agent/internal/zones"
)

// 作业区 apply 的确认闭环：投递到 staged → 出待确认清单 → 人确认才写表。
//
// 这条链路的三个承诺，一个都不能松：
//  1. 任务结束时目标表**没变**（只算不写）；
//  2. 任务带 proposalIds（前端据此给「查看待确认」），文件在 inbox/staged/<taskId>；
//  3. /plan?id= + /apply 之后表才变，staged 文件归档进 done（去重才认得它）。
func TestZoneApplyStagingThenConfirm(t *testing.T) {
	s := newTestServer(t)
	_, layout, _, _ := s.cur()
	target := filepath.Join(layout.Root, "测试表.xlsx")
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}

	// 规则短路：没有模型也能出清单（实收金额按物业位置累加进「本月实收」）。
	rules := `rules:
  - name: 记本月实收
    trigger: 有物业位置和实收金额
    action: 按物业位置把实收金额累加进本月实收
    when:
      has_columns: [物业位置, 实收金额]
    then:
      target_file: 测试表
      sheet: Sheet1
      key:   {物业位置: 物业位置}
      field: {本月实收: 实收金额}
      op: add
`
	if err := os.WriteFile(layout.Rules, []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}
	inDir := filepath.Join(layout.Root, "输入")
	if err := os.MkdirAll(inDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const csvName = "实收日报.csv"
	if err := os.WriteFile(filepath.Join(inDir, csvName), []byte("物业位置,实收金额\nA03,1000\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 建 folder 区（mode=apply 才会真投递）。
	zbody, _ := json.Marshal(map[string]any{
		"name":   "实收区",
		"input":  map[string]any{"type": "folder", "path": inDir, "watch": "manual"},
		"target": map[string]any{"file": "测试表.xlsx", "sheet": "Sheet1"},
		"mode":   "apply",
	})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/zones", bytes.NewReader(zbody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("创建 zone = %d：%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Zone zones.Zone `json:"zone"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	// POST /tasks/run → 轮询到终态。
	runBody, _ := json.Marshal(map[string]any{"zoneId": created.Zone.ID})
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/tasks/run", bytes.NewReader(runBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("tasks/run = %d：%s", rec.Code, rec.Body.String())
	}
	var runRes struct {
		Task tasks.Task `json:"task"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &runRes)
	taskID := runRes.Task.ID
	done := waitTaskTerminal(t, s, taskID)
	if done.Status != tasks.StatusOK {
		t.Fatalf("任务应成功，得到 %s（%s）", done.Status, done.Error)
	}
	if len(done.ProposalIDs) == 0 {
		t.Fatalf("任务应带 proposalIds，得到 %+v", done)
	}
	if len(done.FailedFiles) != 0 {
		t.Fatalf("不该有失败文件，得到 %v", done.FailedFiles)
	}
	staged := filepath.Join(layout.Inbox, "staged", taskID)
	ents, err := os.ReadDir(staged)
	if err != nil || len(ents) == 0 {
		t.Fatalf("staged 里应有文件：%v %v", ents, err)
	}

	// 关键证据 1：只算清单，表没动；也没记账。
	if after, rerr := os.ReadFile(target); rerr != nil || !bytes.Equal(after, before) {
		t.Fatalf("出清单阶段不该改目标表（%v）", rerr)
	}
	if recent, _ := s.Ledger.Recent(20); len(recent) != 0 {
		t.Fatalf("出清单阶段不该记账：%v", recent)
	}

	// GET /plan?id= → 清单带着 staged 文件路径。
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/plan?id="+done.ProposalIDs[0], nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET plan = %d：%s", rec.Code, rec.Body.String())
	}
	var prop struct {
		Sources []string `json:"sources"`
		Items   []struct {
			Ref string `json:"ref"`
			New any    `json:"new"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &prop); err != nil {
		t.Fatal(err)
	}
	if len(prop.Sources) == 0 || !strings.Contains(prop.Sources[0], staged) {
		t.Fatalf("清单来源应是 staged 文件，得到 %v", prop.Sources)
	}
	if len(prop.Items) == 0 {
		t.Fatalf("清单应有条目，得到 %+v", prop)
	}

	// POST /apply → 表变了，staged 文件进 done。
	applyBody, _ := json.Marshal(map[string]string{"id": done.ProposalIDs[0]})
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/apply", bytes.NewReader(applyBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("apply = %d：%s", rec.Code, rec.Body.String())
	}
	if after, rerr := os.ReadFile(target); rerr != nil || bytes.Equal(after, before) {
		t.Fatalf("确认后目标表必须变（%v）", rerr)
	}
	f, err := excelize.OpenFile(target)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := f.GetCellValue("Sheet1", "E2")
	f.Close()
	if v != "20354.02" {
		t.Fatalf("E2 应是 20354.02（19354.02+1000），得到 %q", v)
	}
	if ents, _ := os.ReadDir(staged); len(ents) != 0 {
		t.Fatalf("确认后 staged 应被清空，仍有 %v", ents)
	}
	if _, err := os.Stat(filepath.Join(layout.Done, csvName)); err != nil {
		t.Fatalf("确认后文件应进 done：%v", err)
	}
}

// 重试：没有失败文件 → 409；有 → 只带失败文件、继承原任务上下文、幂等。
func TestTaskRetryOnlyFailedFiles(t *testing.T) {
	s := newTestServer(t)
	zs := s.zonesStore()
	if zs == nil {
		t.Skip("作业区存储不可用")
	}
	z, err := zs.Put(zones.Zone{Name: "重试区", Input: zones.Input{Type: zones.InputManual}, Mode: zones.ModeDryRun})
	if err != nil {
		t.Fatal(err)
	}
	q := s.tasksQueue()
	h := s.Handler()

	// 无失败文件 → 409。
	okTask, err := q.Submit(tasks.Task{ZoneID: z.ID, Title: "正常任务", DryRun: true, Steps: zoneTaskSteps(), Counts: map[string]int{}})
	if err != nil {
		t.Fatal(err)
	}
	waitTaskTerminal(t, s, okTask.ID)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/tasks/retry",
		strings.NewReader(`{"id":"`+okTask.ID+`"}`)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("无失败文件应 409，得到 %d：%s", rec.Code, rec.Body.String())
	}

	// 未知 id → 404。
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/tasks/retry", strings.NewReader(`{"id":"t_nope"}`)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知任务应 404，得到 %d", rec.Code)
	}

	// 有失败文件 → 新任务只带那两个文件。
	failedTask, err := q.Submit(tasks.Task{
		ZoneID: z.ID, Title: "半成功", Source: "chat", ConversationID: "c_1", DryRun: true,
		FailedFiles: []string{"甲.csv", "乙.csv"}, Steps: zoneTaskSteps(), Counts: map[string]int{},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitTaskTerminal(t, s, failedTask.ID)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/tasks/retry",
		strings.NewReader(`{"id":"`+failedTask.ID+`"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("retry = %d：%s", rec.Code, rec.Body.String())
	}
	var res struct {
		Task tasks.Task `json:"task"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if len(res.Task.Files) != 2 || res.Task.Files[0] != "甲.csv" {
		t.Fatalf("新任务应只带失败文件，得到 %v", res.Task.Files)
	}
	if res.Task.IdempotencyKey != "retry:"+failedTask.ID {
		t.Fatalf("幂等键应为 retry:<原 id>，得到 %q", res.Task.IdempotencyKey)
	}
	if !strings.Contains(res.Task.Title, "重试失败 2 个") {
		t.Fatalf("标题应写明重试失败数，得到 %q", res.Task.Title)
	}
	if res.Task.DryRun != true || res.Task.Source != "chat" || res.Task.ConversationID != "c_1" {
		t.Fatalf("应继承原任务的演习/来源/会话，得到 %+v", res.Task)
	}

	// 连点两次 → 同一条任务（不新建）。
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/tasks/retry",
		strings.NewReader(`{"id":"`+failedTask.ID+`"}`)))
	var res2 struct {
		Task tasks.Task `json:"task"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &res2)
	if res2.Task.ID != res.Task.ID {
		t.Fatalf("重复重试应返回同一条任务：%s != %s", res2.Task.ID, res.Task.ID)
	}
	// 等这条重试任务真的结束，否则临时工作区可能正被 worker 写 tasks.json。
	waitTaskTerminal(t, s, res.Task.ID)
}

// waitTaskTerminal 轮询到任务进终态。
func waitTaskTerminal(t *testing.T, s *Server, id string) tasks.Task {
	t.Helper()
	q := s.tasksQueue()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if tk, ok := q.Get(id); ok {
			switch tk.Status {
			case tasks.StatusOK, tasks.StatusFailed, tasks.StatusCanceled:
				return tk
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("任务 %s 未在预期时间内结束", id)
	return tasks.Task{}
}
