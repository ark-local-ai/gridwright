package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/ark-local-ai/ark/apps/agent/internal/logx"
	"github.com/ark-local-ai/ark/apps/agent/internal/tasks"
	"github.com/ark-local-ai/ark/apps/agent/internal/workspace"
	"github.com/ark-local-ai/ark/apps/agent/internal/zoneingest"
	"github.com/ark-local-ai/ark/apps/agent/internal/zones"
)

// 任务队列与作业区执行（见 internal/tasks、internal/zoneingest）。
//
// 语义：
//   - 点一次运行 / 对话里采纳一次 = 提交一个任务；队列**串行**，一次只跑一个。
//   - dryRun（作业区 mode=dryRun 或显式要求）= 只扫描与去重，不投递、不处理。
//   - apply = 把输入目录的新文件投递到 inbox，然后**停下等人确认**，
//     不在任务里调用 agent.ProcessInboxFile。
//   - 看板最多展示 Snapshot 的 6 条；更多在 History 里查。
//
// 为什么停在这里（而不是投递后自动处理）：规则命中会直接写表，
// “改表必须人确认”。现有收件箱面板的「现在处理」就是那个确认动作——
// 人点一下才走 agent 的既有路径（认领锁、规则短路、备份、指纹、记账、移 done）。
// 未来若要做“合并待确认清单”，入口也应从这一步接：任务只负责投递与报数，
// 处理统一由确认动作触发，不要在这里再长出一条自动处理分支。

var errZoneNotFound = errors.New("zone 不存在")

// tasksQueue 返回当前工作区的任务队列（切换工作区时会换指针）。
func (s *Server) tasksQueue() *tasks.Queue {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tasks
}

// newTaskQueue 为某个工作区建队列。runner 捕获 layout，避免切区后
// 正在跑的任务把文件投到新工作区。
func (s *Server) newTaskQueue(layout *workspace.Layout) *tasks.Queue {
	if layout == nil {
		return nil
	}
	var q *tasks.Queue
	var err error
	q, err = tasks.New(layout.Root, func(ctx context.Context, t *tasks.Task) error {
		return s.runZoneTask(ctx, layout, q, t)
	})
	if err != nil {
		logx.Step("tasks", "打开队列失败", "err", err.Error())
		return nil
	}
	return q
}

// zoneTaskSteps 是卡片上那条链路。与 runner 的更新点一一对应。
func zoneTaskSteps() []tasks.Step {
	return []tasks.Step{
		{Key: "scan", Label: "收料", Status: tasks.StepPending},
		{Key: "dedupe", Label: "去重", Status: tasks.StepPending},
		{Key: "deliver", Label: "投递", Status: tasks.StepPending},
		{Key: "process", Label: "处理", Status: tasks.StepPending},
		{Key: "done", Label: "归档", Status: tasks.StepPending},
	}
}

func setStep(q *tasks.Queue, id, key, status, detail string) {
	q.Update(id, func(x *tasks.Task) {
		for i := range x.Steps {
			if x.Steps[i].Key == key {
				x.Steps[i].Status = status
				x.Steps[i].Detail = detail
				return
			}
		}
	})
}

func setCounts(q *tasks.Queue, id string, kv map[string]int) {
	q.Update(id, func(x *tasks.Task) {
		if x.Counts == nil {
			x.Counts = map[string]int{}
		}
		for k, v := range kv {
			x.Counts[k] = v
		}
	})
}

// submitZoneTask 校验作业区并提交一个任务。dryRun 为 true 时强制演习；
// 作业区自身 mode != apply 时也强制演习（不会因为按钮参数就变成真改）。
func (s *Server) submitZoneTask(zoneID string, dryRun bool, source, conversationID string) (tasks.Task, error) {
	zs := s.zonesStore()
	if zs == nil {
		return tasks.Task{}, fmt.Errorf("作业区存储不可用")
	}
	z, ok := zs.Get(zoneID)
	if !ok {
		return tasks.Task{}, errZoneNotFound
	}
	q := s.tasksQueue()
	if q == nil {
		return tasks.Task{}, fmt.Errorf("任务队列不可用")
	}
	dry := dryRun || z.Mode != zones.ModeApply
	title := "运行：" + z.Name
	if dry {
		title = "试运行：" + z.Name
	}
	t, err := q.Submit(tasks.Task{
		ZoneID:         z.ID,
		Title:          title,
		Source:         source,
		ConversationID: conversationID,
		DryRun:         dry,
		Steps:          zoneTaskSteps(),
		Counts:         map[string]int{},
	})
	if err != nil {
		return tasks.Task{}, err
	}
	// 状态先落到 zone 上，卡片在任务真正开始前就能显示“排队中”。
	_ = zs.MarkStatus(z.ID, "queued", "")
	return t, nil
}

// runZoneTask 在 tasks 的 worker 里执行。一次只跑一个任务由 Queue 保证。
func (s *Server) runZoneTask(ctx context.Context, layout *workspace.Layout, q *tasks.Queue, t *tasks.Task) error {
	if q == nil || layout == nil {
		return fmt.Errorf("任务队列未就绪")
	}
	zs, err := zones.Open(layout.Root)
	if err != nil {
		return fmt.Errorf("打开作业区失败：%w", err)
	}
	z, ok := zs.Get(t.ZoneID)
	if !ok {
		return fmt.Errorf("作业区已不存在：%s", t.ZoneID)
	}
	_ = zs.MarkStatus(z.ID, "running", "")
	setStep(q, t.ID, "scan", tasks.StepRunning, "")
	logx.Step("task", "enter", "id", t.ID, "zone", z.Name, "dryRun", t.DryRun)

	finish := func(status, errMsg string, applied, rejected int, summary string) error {
		// 任务本体也要写 summary/error：卡片与历史读的是 Task，不是 zones.Run。
		q.Update(t.ID, func(x *tasks.Task) {
			x.Summary = summary
			x.Error = errMsg
		})
		if errMsg == "" {
			_ = zs.MarkStatus(z.ID, status, "")
		} else {
			_ = zs.MarkStatus(z.ID, status, errMsg)
		}
		zs.RecordRun(zones.Run{
			ZoneID: z.ID, Started: t.StartedAt, OK: status == "ok",
			Applied: applied, Rejected: rejected, Summary: summary, Err: errMsg,
		})
		logx.Step("task", "exit", "id", t.ID, "status", status, "applied", applied, "rejected", rejected)
		if errMsg != "" {
			return errors.New(errMsg)
		}
		return nil
	}

	// 手工区：入口就是 inbox，不经过“输入目录 → 投递”。
	if z.Input.Type == zones.InputManual {
		setStep(q, t.ID, "scan", tasks.StepOK, "从收件箱取件")
		setStep(q, t.ID, "dedupe", tasks.StepSkipped, "收件箱已由认领锁去重")
		setStep(q, t.ID, "deliver", tasks.StepSkipped, "手工区不投递")
		names, _ := inboxNames(layout)
		if t.DryRun {
			setStep(q, t.ID, "process", tasks.StepSkipped, fmt.Sprintf("演习：%d 个文件待处理", len(names)))
			setStep(q, t.ID, "done", tasks.StepSkipped, "")
			setCounts(q, t.ID, map[string]int{"candidates": len(names)})
			return finish("ok", "", 0, 0, fmt.Sprintf("试运行：收件箱 %d 个文件，未处理", len(names)))
		}
		// apply 也停下等确认：这里只报数，不处理 inbox。
		setCounts(q, t.ID, map[string]int{"candidates": len(names), "pending": len(names)})
		setStep(q, t.ID, "process", tasks.StepSkipped, fmt.Sprintf("待确认：%d 个文件在收件箱", len(names)))
		setStep(q, t.ID, "done", tasks.StepSkipped, "确认处理后会自动移入 inbox/done")
		return finish("ok", "", 0, 0, fmt.Sprintf("已投递 %d 个，等待你在收件箱确认", len(names)))
	}
	if z.Input.Type != zones.InputFolder {
		setStep(q, t.ID, "scan", tasks.StepFailed, "暂不支持该输入类型："+z.Input.Type)
		return finish("failed", "暂不支持该输入类型："+z.Input.Type, 0, 0, "")
	}
	if strings.TrimSpace(z.Input.Path) == "" {
		setStep(q, t.ID, "scan", tasks.StepFailed, "作业区没有配置输入目录")
		return finish("failed", "作业区没有配置输入目录", 0, 0, "")
	}
	if s.Grants != nil {
		if err := s.Grants.Check(z.Input.Path, "read"); err != nil {
			setStep(q, t.ID, "scan", tasks.StepFailed, "目录未授权")
			return finish("failed", "输入目录未授权："+err.Error(), 0, 0, "")
		}
	}

	opt := zoneingest.Options{
		SourceDir: z.Input.Path,
		Pattern:   z.Input.Pattern,
		InboxDir:  layout.Inbox,
		DoneDir:   layout.Done,
	}
	res, err := zoneingest.Run(opt, true) // 先只扫描，拿到准确计数再决定是否投递
	if err != nil {
		setStep(q, t.ID, "scan", tasks.StepFailed, err.Error())
		return finish("failed", err.Error(), 0, 0, "")
	}
	setCounts(q, t.ID, map[string]int{
		"candidates": res.Matched,
		"duplicates": res.Duplicates,
	})
	setStep(q, t.ID, "scan", tasks.StepOK, fmt.Sprintf("%d 个候选", res.Matched))
	setStep(q, t.ID, "dedupe", tasks.StepOK, fmt.Sprintf("%d 个重复跳过", res.Duplicates))

	if t.DryRun {
		setStep(q, t.ID, "deliver", tasks.StepSkipped, "演习：未投递")
		setStep(q, t.ID, "process", tasks.StepSkipped, "")
		setStep(q, t.ID, "done", tasks.StepSkipped, "")
		return finish("ok", "", 0, 0,
			fmt.Sprintf("试运行：%d 个候选，%d 个重复，未改动任何文件", res.Matched, res.Duplicates))
	}

	// 真正投递。res 是 dryRun 的结果，重新跑一次非 dryRun 才复制文件。
	res, err = zoneingest.Run(opt, false)
	if err != nil {
		setStep(q, t.ID, "deliver", tasks.StepFailed, err.Error())
		return finish("failed", err.Error(), 0, 0, "")
	}
	setStep(q, t.ID, "deliver", tasks.StepOK, fmt.Sprintf("%d 个进入收件箱", res.Delivered))
	names := make([]string, 0, res.Delivered)
	for _, c := range res.Items {
		if c.Reason == "已投递" {
			names = append(names, c.Name)
		}
	}
	// 投递完成即止：不调用 agent.ProcessInboxFile，等人在收件箱面板点「现在处理」。
	setCounts(q, t.ID, map[string]int{"delivered": len(names), "pending": len(names)})
	setStep(q, t.ID, "process", tasks.StepSkipped, fmt.Sprintf("待确认：%d 个文件在收件箱", len(names)))
	setStep(q, t.ID, "done", tasks.StepSkipped, "确认处理后会自动移入 inbox/done")
	return finish("ok", "", 0, 0, fmt.Sprintf("已投递 %d 个，等待你在收件箱确认", len(names)))
}

func inboxNames(layout *workspace.Layout) ([]string, error) {
	files, err := layout.InboxFiles()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, filepathBase(f))
	}
	return out, nil
}

func filepathBase(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

func statusOf(okN, failN int) string {
	if failN == 0 {
		return tasks.StepOK
	}
	if okN == 0 {
		return tasks.StepFailed
	}
	return tasks.StepOK // 部分成功仍算完成，失败数在 detail 里
}

// ---------- HTTP ----------

// handleTasks GET /api/v1/tasks —— 看板卡片（最多 6 条）
func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	q := s.tasksQueue()
	if q == nil {
		writeJSON(w, http.StatusOK, map[string]any{"runningId": "", "items": []tasks.Task{}, "queued": 0})
		return
	}
	writeJSON(w, http.StatusOK, q.Snapshot())
}

// handleTasksHistory GET /api/v1/tasks/history?limit=
func (s *Server) handleTasksHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	q := s.tasksQueue()
	if q == nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": []tasks.Task{}})
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	items := q.History(limit)
	if items == nil {
		items = []tasks.Task{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleTasksRun POST /api/v1/tasks/run {zoneId, dryRun?}
func (s *Server) handleTasksRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req struct {
		ZoneID string `json:"zoneId"`
		DryRun bool   `json:"dryRun"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.ZoneID) == "" {
		writeErr(w, http.StatusBadRequest, "缺少 zoneId")
		return
	}
	t, err := s.submitZoneTask(req.ZoneID, req.DryRun, "manual", "")
	if err != nil {
		if errors.Is(err, errZoneNotFound) {
			writeErr(w, http.StatusNotFound, "zone 不存在："+req.ZoneID)
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "task": t})
}

// handleTasksCancel POST /api/v1/tasks/cancel {id}
func (s *Server) handleTasksCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	q := s.tasksQueue()
	if q == nil {
		writeErr(w, http.StatusServiceUnavailable, "任务队列不可用")
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.ID) == "" {
		writeErr(w, http.StatusBadRequest, "缺少 id")
		return
	}
	if err := q.Cancel(req.ID); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
