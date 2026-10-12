package api

import (
	"encoding/json"
	"net/http"

	"github.com/ark-local-ai/ark/apps/agent/internal/agent"
	"github.com/ark-local-ai/ark/apps/agent/internal/convo"
	"github.com/ark-local-ai/ark/apps/agent/internal/prompts"
)

// 快捷任务模板接口（见 prompts 包注释）。
//
// 与 /rules 的区别：规则命中即短路、不调模型；快捷任务只是一个入口，
// 触发后仍走 /chat → /plan → 人工确认的同一条路。所以这里**不做执行**，
// 只负责存/取/总结。

type promptReq struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	When     string `json:"when"`
	Template string `json:"template"`
	Note     string `json:"note"`
	Source   string `json:"source"`
}

// handlePromptsOrPut 让 GET /prompts 读、POST /prompts 存。
func (s *Server) handlePromptsOrPut(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.handlePromptsPut(w, r)
		return
	}
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET/POST")
		return
	}
	st, err := s.promptStore()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	list := st.All()
	if list == nil {
		list = []prompts.Prompt{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"prompts": list, "path": st.Path()})
}

// handlePromptsPut POST /api/v1/prompts —— 存一条模板（同 id 更新）。
func (s *Server) handlePromptsPut(w http.ResponseWriter, r *http.Request) {
	var req promptReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	st, err := s.promptStore()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	p, err := st.Save(prompts.Prompt{
		ID: req.ID, Name: req.Name, When: req.When,
		Template: req.Template, Note: req.Note, Source: req.Source,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "prompt": p})
}

// handlePromptsDelete POST /api/v1/prompts/delete —— 删一条。
func (s *Server) handlePromptsDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		writeErr(w, http.StatusBadRequest, "缺少 id")
		return
	}
	st, err := s.promptStore()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := st.Delete(req.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handlePromptsUse POST /api/v1/prompts/use —— 记一次使用（供排序）。
func (s *Server) handlePromptsUse(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		writeErr(w, http.StatusBadRequest, "缺少 id")
		return
	}
	st, err := s.promptStore()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	st.Bump(req.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handlePromptsDistill POST /api/v1/prompts/distill?id= —— 从一段对话总结快捷任务候选。
// 只读：返回候选，不落盘；落盘走 POST /api/v1/prompts。
// handlePromptsBuiltin GET /api/v1/prompts/builtin —— 随包内置的通用模板骨架。
// 离线、可审计；不是已保存的快捷任务，只是起点。
func (s *Server) handlePromptsBuiltin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": prompts.Builtins()})
}

// handlePromptsDistill POST /api/v1/prompts/distill
//
// 从一段对话（或**用户勾选的那几条消息**）总结快捷任务候选。只读：返回候选，不落盘。
//   - 带 messages：只总结勾选的消息（界面里勾选哪几条就总结哪几条）；
//   - 只带 conversationId（或 ?id=）：总结整条对话。
func (s *Server) handlePromptsDistill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	cfg, layout, led, _ := s.cur()
	var req struct {
		ConversationID string `json:"conversationId"`
		Messages       []struct {
			Role string `json:"role"`
			Text string `json:"text"`
		} `json:"messages"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // body 可空
	id := req.ConversationID
	if id == "" {
		id = r.URL.Query().Get("id")
	}

	var c *convo.Conversation
	if len(req.Messages) > 0 {
		// 勾选的消息：直接组一段对话，不读整条会话
		msgs := make([]convo.Message, 0, len(req.Messages))
		for _, m := range req.Messages {
			r := m.Role
			if r != convo.RoleUser && r != convo.RoleAgent && r != convo.RoleSystem {
				r = convo.RoleUser
			}
			msgs = append(msgs, convo.Message{Role: r, Text: m.Text})
		}
		c = &convo.Conversation{ID: id, Title: "选中的消息", Messages: msgs}
	} else {
		if id == "" {
			writeErr(w, http.StatusBadRequest, "需要 conversationId 或 messages")
			return
		}
		store, err := s.convoStore()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		c = store.Get(id)
		if c == nil {
			writeErr(w, http.StatusNotFound, "会话不存在")
			return
		}
	}
	if !cfg.BrainReady() {
		writeErr(w, http.StatusBadRequest, "还没配置模型（脑）：沉淀快捷任务需要模型总结")
		return
	}
	ag := agent.New(cfg, layout, led, s.brainClient())
	cands, err := ag.DistillPrompt(r.Context(), c)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cands == nil {
		cands = []agent.PromptCandidate{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"conversationId": c.ID,
		"title":          c.Title,
		"candidates":     cands,
	})
}

// promptStore 取当前工作区的快捷任务表。
func (s *Server) promptStore() (*prompts.Store, error) {
	_, layout, _, _ := s.cur()
	return prompts.Open(layout.Root)
}
