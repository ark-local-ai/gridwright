package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/ark-local-ai/ark/apps/agent/internal/tasks"
)

// 重试失败文件（见 .workbuddy/handoff-zone2.md 第 5 项）。
//
//	POST /api/v1/tasks/retry {id}
//
// 只重试**这一轮失败的那几个文件**，不整批重跑。
//
// 为什么不能整批重跑：作业区的一批数据往往只错了一份（比如某行缺列被规则跳过、
// 或某张表被 Excel 占着）。整批重投会把已经出过清单、甚至已经确认写表的文件再
// 算一遍，累加类规则（add）就会**重复入账**——这是账目上最难发现、最伤人的错。
//
// 幂等键 "retry:<原任务id>"：用户连点两下「重试」，只会有一次真的跑。
func (s *Server) handleTasksRetry(w http.ResponseWriter, r *http.Request) {
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
	orig, ok := q.Get(req.ID)
	if !ok {
		writeErr(w, http.StatusNotFound, "任务不存在："+req.ID)
		return
	}
	if len(orig.FailedFiles) == 0 {
		// 没失败文件就别给按钮留活路：重试一条已经成功的任务只会制造重复账。
		writeErr(w, http.StatusConflict, "这条任务没有失败文件，无需重试")
		return
	}
	if zs := s.zonesStore(); zs != nil {
		if _, ok := zs.Get(orig.ZoneID); !ok {
			writeErr(w, http.StatusNotFound, "作业区已不存在："+orig.ZoneID)
			return
		}
	}

	t, err := q.Submit(tasks.Task{
		ZoneID:         orig.ZoneID,
		Title:          fmt.Sprintf("重试失败 %d 个（%s）", len(orig.FailedFiles), orig.Title),
		Source:         orig.Source,
		ConversationID: orig.ConversationID,
		DryRun:         orig.DryRun,
		Files:          append([]string(nil), orig.FailedFiles...),
		IdempotencyKey: "retry:" + orig.ID,
		Steps:          zoneTaskSteps(),
		Counts:         map[string]int{},
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "task": t})
}
