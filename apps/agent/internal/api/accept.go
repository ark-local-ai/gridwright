package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ark-local-ai/ark/apps/agent/internal/jobs"
)

// 采纳一条提案（见 docs/agent-architecture/29-UI升级与剩余功能.md 第 6 项）。
//
//	POST /api/v1/conversation/accept {conversationId, index}
//
// 提案以前只能看、点不了——模型说"每天下班前体检一次"，用户点头了也没有下文。
// 这里把它落成真的东西：
//   - task          → 一条**默认暂停**的定时任务（改文件的开关必须由人亲自打开）
//   - rule          → 只标记已采纳；规则要写成 rules.yaml 的结构化 when/then，
//     不自动落盘，免得把模型的一句自然语言当成可执行规则
//   - tool_request  → 同上，只标记
//
// 无论哪种，Accepted 都会落盘：采纳是一个决定，重启后要还在。
func (s *Server) handleConversationAccept(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req struct {
		ConversationID string `json:"conversationId"`
		Index          int    `json:"index"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ConversationID == "" {
		writeErr(w, http.StatusBadRequest, "缺少 conversationId")
		return
	}
	store, err := s.convoStore()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	p, err := store.Accept(req.ConversationID, req.Index)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	resp := map[string]any{"ok": true, "kind": p.Kind}
	switch p.Kind {
	case "task":
		if strings.TrimSpace(p.Schedule) == "" {
			writeErr(w, http.StatusBadRequest, "这条任务提案里没有时间（cron），无法落成定时任务")
			return
		}
		st, jerr := s.jobStore()
		if jerr != nil {
			writeErr(w, http.StatusInternalServerError, jerr.Error())
			return
		}
		j, aerr := st.Add(jobs.Job{
			Name: p.Title, Schedule: p.Schedule, Kind: jobs.KindScan,
			Enabled: false, Source: "对话采纳",
		})
		if aerr != nil {
			writeErr(w, http.StatusBadRequest, aerr.Error())
			return
		}
		resp["job"] = j
		resp["note"] = "已建为定时任务（默认暂停，去「定时任务」打开开关）"
	case "rule":
		resp["note"] = "已采纳。规则要落成 rules.yaml，请到「规则」里确认草稿"
	case "tool_request":
		resp["note"] = "已采纳。工具申请已记下"
	default:
		resp["note"] = "已采纳"
	}
	writeJSON(w, http.StatusOK, resp)
}
