package api

import (
	"encoding/json"
	"errors"
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
//   - task + zoneId + 无 schedule → 立即提交一个作业区任务（排队、一次只跑一个）
//   - task + schedule             → 一条**默认暂停**的定时任务
//   - rule                        → 只标记已采纳；不自动落盘
//   - tool_request                → 同上，只标记
//
// 顺序要紧：**先校验/入队，后写 Accepted**。以前先 Accept 再校验，
// 校验失败时提案已经显示“已采纳”，用户以为事情成了、实际什么都没发生。
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
	c := store.Get(req.ConversationID)
	if c == nil {
		writeErr(w, http.StatusNotFound, "会话不存在："+req.ConversationID)
		return
	}
	if req.Index < 0 || req.Index >= len(c.Messages) || c.Messages[req.Index].Proposal == nil {
		writeErr(w, http.StatusBadRequest, "这条消息上没有可采纳的提案")
		return
	}
	p := c.Messages[req.Index].Proposal

	resp := map[string]any{"ok": true, "kind": p.Kind}

	switch {
	case p.Kind == "task" && strings.TrimSpace(p.ZoneID) != "":
		// 作业区任务：立即入队跑一次。dryRun 优先——只有明确 mode=apply 才真改。
		t, terr := s.submitZoneTask(p.ZoneID, strings.EqualFold(p.Mode, "") || strings.EqualFold(p.Mode, "dryRun"))
		if terr != nil {
			if errors.Is(terr, errZoneNotFound) {
				writeErr(w, http.StatusNotFound, "提案里的作业区不存在："+p.ZoneID)
				return
			}
			writeErr(w, http.StatusBadRequest, terr.Error())
			return
		}
		if _, aerr := store.Accept(req.ConversationID, req.Index); aerr != nil {
			writeErr(w, http.StatusInternalServerError, "任务已入队，但采纳记录写入失败："+aerr.Error())
			return
		}
		resp["task"] = t
		resp["note"] = "已加入任务队列（一次只跑一个，看板会显示进度）"
		if t.DryRun {
			resp["note"] = "已加入任务队列（试运行：只扫描与去重，不改任何文件）"
		}
		writeJSON(w, http.StatusOK, resp)
		return
	case p.Kind == "task":
		if strings.TrimSpace(p.Schedule) == "" {
			writeErr(w, http.StatusBadRequest, "这条任务提案既没有 zoneId，也没有时间（cron）")
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
		if _, aerr := store.Accept(req.ConversationID, req.Index); aerr != nil {
			writeErr(w, http.StatusInternalServerError, "任务已建，但采纳记录写入失败："+aerr.Error())
			return
		}
		resp["job"] = j
		resp["note"] = "已建为定时任务（默认暂停，去「定时任务」打开开关）"
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// 其余类型：先写 Accepted，再按类型给回执。
	if _, aerr := store.Accept(req.ConversationID, req.Index); aerr != nil {
		writeErr(w, http.StatusBadRequest, aerr.Error())
		return
	}
	switch p.Kind {
	case "rule":
		resp["note"] = "已采纳。规则要落成 rules.yaml，请到「规则」里确认草稿"
	case "tool_request":
		resp["note"] = "已采纳。工具申请已记下"
	default:
		resp["note"] = "已采纳"
	}
	writeJSON(w, http.StatusOK, resp)
}
