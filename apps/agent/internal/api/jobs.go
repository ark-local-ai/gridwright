package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ark-local-ai/ark/apps/agent/internal/jobs"
	"github.com/ark-local-ai/ark/apps/agent/internal/notify"
)

// 自动化任务（见 docs/agent-architecture/29-UI升级与剩余功能.md）。
//
//	GET/POST /api/v1/jobs        —— 列出 / 新建
//	PUT      /api/v1/jobs        —— 改（启停、改时间）
//	DELETE   /api/v1/jobs?id=    —— 删
//	POST     /api/v1/jobs/run    —— 立即跑一次
//	GET      /api/v1/jobs/runs   —— 运行历史
//
// 调度器在引擎里跑（见 StartScheduler）：每分钟检查一次，到点执行。

func (s *Server) jobStore() (*jobs.Store, error) {
	_, layout, _, _ := s.cur()
	return jobs.Open(layout.Root)
}

// handleJobs 路由分发（mux 不支持 method 区分，手工分）。
func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	st, err := s.jobStore()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	switch r.Method {
	case http.MethodGet:
		list := st.List()
		type jobView struct {
			jobs.Job
			When string `json:"when"`
		}
		view := make([]jobView, 0, len(list))
		for _, j := range list {
			view = append(view, jobView{Job: j, When: jobs.Describe(j.Schedule)})
		}
		if wantsText(r) {
			var b strings.Builder
			if len(list) == 0 {
				b.WriteString("还没有自动化任务\n")
			} else {
				for _, j := range list {
					state := "已暂停"
					if j.Enabled {
						state = "启用"
					}
					b.WriteString(line("[%s] %s · %s · 下次 %s", state, j.Name, jobs.Describe(j.Schedule), j.NextRun))
				}
			}
			writeText(w, b.String())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"jobs": view})
	case http.MethodPost:
		var req struct {
			Name     string `json:"name"`
			Schedule string `json:"schedule"`
			Kind     string `json:"kind"`
			Enabled  bool   `json:"enabled"`
			Source   string `json:"source"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求格式错误")
			return
		}
		j, err := st.Add(jobs.Job{
			Name: req.Name, Schedule: req.Schedule, Kind: req.Kind,
			Enabled: req.Enabled, Source: req.Source,
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job": j})
	case http.MethodPut:
		var req struct {
			ID       string  `json:"id"`
			Name     *string `json:"name"`
			Schedule *string `json:"schedule"`
			Enabled  *bool   `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
			writeErr(w, http.StatusBadRequest, "缺少 id")
			return
		}
		j, err := st.Update(req.ID, func(x *jobs.Job) {
			if req.Name != nil {
				x.Name = *req.Name
			}
			if req.Schedule != nil {
				x.Schedule = *req.Schedule
			}
			if req.Enabled != nil {
				x.Enabled = *req.Enabled
			}
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job": j})
	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if id == "" {
			writeErr(w, http.StatusBadRequest, "缺少 id")
			return
		}
		if err := st.Delete(id); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET/POST/PUT/DELETE")
	}
}

// handleJobRun POST /api/v1/jobs/run —— 立即跑一次（界面上的"现在跑"）。
func (s *Server) handleJobRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	st, err := s.jobStore()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var target *jobs.Job
	for _, j := range st.List() {
		if j.ID == req.ID {
			jj := j
			target = &jj
		}
	}
	if target == nil {
		writeErr(w, http.StatusNotFound, "任务不存在")
		return
	}
	run := s.ExecuteJob(r.Context(), st, *target)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "run": run})
}

// handleJobRuns GET /api/v1/jobs/runs —— 运行历史。
func (s *Server) handleJobRuns(w http.ResponseWriter, r *http.Request) {
	st, err := s.jobStore()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": st.RecentRuns(50)})
}

// ExecuteJob 执行一条任务并记录。目前只支持"只读体检"——
// **故意的**：定时任务默认应该只做安全的事；改表要走人确认。
func (s *Server) ExecuteJob(ctx context.Context, st *jobs.Store, j jobs.Job) jobs.Run {
	started := time.Now().Format("2006-01-02 15:04")
	run := jobs.Run{JobID: j.ID, Name: j.Name, Started: started}

	switch j.Kind {
	case jobs.KindScan, "":
		rep, err := s.runScan()
		if err != nil {
			run.OK = false
			run.Summary = "体检失败：" + err.Error()
		} else {
			errs, warns, _ := rep.CountBySeverity()
			run.OK = errs == 0
			run.Errors, run.Warns = errs, warns
			run.Summary = fmt.Sprintf("体检完成：%d 处错误、%d 处存疑", errs, warns)
		}
	default:
		run.OK = false
		run.Summary = "不支持的任务类型：" + j.Kind
	}

	_ = st.RecordRun(run)
	// 定时跑完也通知一声（用户要的是"下班前体检，有问题告诉我"）
	cfg, _, _, _ := s.cur()
	if err := notify.Send(cfg.Notify, notify.Message{
		Table: j.Name, Summary: run.Summary,
	}); err != nil {
		log.Printf("[jobs] 通知未发出: %v", err)
	}
	return run
}

// StartScheduler 启动调度器：每分钟检查一次到点任务。
// 在引擎启动时调用一次；ctx 取消即停。
func (s *Server) StartScheduler(ctx context.Context) {
	go func() {
		// 每分钟的第 0 秒对齐检查（误差 ≤1s）
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				st, err := s.jobStore()
				if err != nil {
					continue
				}
				for _, j := range st.Due(now) {
					log.Printf("[jobs] 触发：%s（%s）", j.Name, jobs.Describe(j.Schedule))
					s.ExecuteJob(ctx, st, j)
				}
			}
		}
	}()
}
