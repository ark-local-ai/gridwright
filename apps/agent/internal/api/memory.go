package api

import (
	"encoding/json"
	"net/http"

	"github.com/ark-local-ai/ark/apps/agent/internal/memory2"
)

// 记忆接口（见 docs/agent-architecture/21-记忆设计.md）。
//
// 设计要点：**LLM 只能"建议"一条记忆，落盘要人点确认**——记忆是长期资产，
// 写错会污染以后所有判断，所以和工具权限走同一个模式（提议 → 人授权）。

// handleMemory GET /api/v1/memory?q=&sheet=&key=
//   - 无参数：返回全部记忆（供记忆页展示）
//   - 带 q：按文本检索（供界面"这条跟什么相关"）
func (s *Server) handleMemory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	st, err := s.memStore()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if q := r.URL.Query().Get("q"); q != "" {
		rel := st.RetrieveByText(q)
		writeJSON(w, http.StatusOK, map[string]any{"filtered": rel, "query": q})
		return
	}
	f := st.All()
	facts, decisions, stale := st.Counts()
	writeJSON(w, http.StatusOK, map[string]any{
		"memory": f, "counts": map[string]int{"facts": facts, "decisions": decisions, "stale": stale},
		"path": st.Path(),
	})
}

// handleMemoryPut POST /api/v1/memory —— 人批准后写入一条记忆（事实或决策）
func (s *Server) handleMemoryPut(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req struct {
		Kind    string            `json:"kind"` // fact | decision
		ID      string            `json:"id"`
		Key     map[string]string `json:"key"`
		Value   string            `json:"value"` // fact
		Unit    string            `json:"unit"`
		Text    string            `json:"text"` // decision
		Source  string            `json:"source"`
		Approve bool              `json:"approve"` // 必须显式为 true（人点确认）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if !req.Approve {
		writeErr(w, http.StatusBadRequest, "写入记忆需要人明确批准（approve=true）")
		return
	}
	st, err := s.memStore()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	switch req.Kind {
	case "fact":
		if req.ID == "" || req.Value == "" {
			writeErr(w, http.StatusBadRequest, "事实记忆需要 id 与 value")
			return
		}
		err = st.PutFact(memory2.Fact{
			ID: req.ID, Key: req.Key, Value: req.Value, Unit: req.Unit, Source: req.Source,
		})
	case "decision":
		if req.ID == "" || req.Text == "" {
			writeErr(w, http.StatusBadRequest, "决策记忆需要 id 与 text")
			return
		}
		err = st.PutDecision(memory2.Decision{
			ID: req.ID, Key: req.Key, Text: req.Text, Source: req.Source, By: "user",
		})
	default:
		writeErr(w, http.StatusBadRequest, "kind 只能是 fact 或 decision")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleMemoryStale POST /api/v1/memory/stale —— 标记/清除"可能过时"
func (s *Server) handleMemoryStale(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req struct {
		Action string `json:"action"` // mark | clear
		ID     string `json:"id"`
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		writeErr(w, http.StatusBadRequest, "缺少 id")
		return
	}
	st, err := s.memStore()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	switch req.Action {
	case "clear":
		err = st.RemoveStale(req.ID)
	default:
		err = st.MarkStale(memory2.Stale{ID: req.ID, Note: req.Note})
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// memStore 取当前工作区的记忆存储。
func (s *Server) memStore() (*memory2.Store, error) {
	_, layout, _, _ := s.cur()
	return memory2.Open(layout.Root)
}

// handleMemoryOrPut 让 GET /memory 读、POST /memory 写。
func (s *Server) handleMemoryOrPut(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.handleMemoryPut(w, r)
		return
	}
	s.handleMemory(w, r)
}
