package api

import (
	"encoding/json"
	"net/http"

	"github.com/ark-local-ai/ark/apps/agent/internal/agent"
)

// 收件箱队列（见 apps/agent/internal/agent/inboxstatus.go）。
//
// 与 /workspace/files 的区别：那个只列文件；这里回答"每个文件现在是什么状态、
// 为什么没被处理"。界面据此展示等待/处理中/失败，并能手动重试或跳过。
//
// 处理动作复用 Agent 的认领锁：界面点「处理」与后台 fsnotify 同时触发时，
// 只有一方能拿到锁，另一方得到 claimed=false（不是错误）。

func (s *Server) inboxAgent() *agent.Agent {
	cfg, layout, led, _ := s.cur()
	return agent.New(cfg, layout, led, s.brainClient())
}

// handleInbox GET /api/v1/inbox
func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	items, err := s.inboxAgent().InboxItems()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"count": len(items),
	})
}

// handleInboxRun POST /api/v1/inbox/run {file?}
// 不给 file 就处理整个 inbox；给了就只处理那一个。
func (s *Server) handleInboxRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req struct {
		File string `json:"file"`
	}
	// 空 body 合法（= 处理全部）。
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	ag := s.inboxAgent()
	if req.File != "" {
		claimed, err := ag.ProcessInboxFile(r.Context(), req.File)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok": false, "file": req.File, "claimed": claimed, "error": err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "file": req.File, "claimed": claimed,
		})
		return
	}
	if err := ag.RunInboxFiles(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items, _ := ag.InboxItems()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": items, "count": len(items)})
}

// handleInboxSkip POST /api/v1/inbox/skip {file, reason?}
// 移到 inbox/skipped，**不是 done**：它没有被处理，不应进已完成统计。
func (s *Server) handleInboxSkip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req struct {
		File   string `json:"file"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.File == "" {
		writeErr(w, http.StatusBadRequest, "缺少 file")
		return
	}
	if err := s.inboxAgent().SkipInboxFile(req.File, req.Reason); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "file": req.File})
}
