package api

import (
	"encoding/json"
	"net/http"

	"github.com/ark-local-ai/ark/apps/agent/internal/terms"
)

// 语义映射接口（见 docs/agent-architecture/24-语义映射与安全边界.md、25）。

// handleTerms GET /api/v1/terms —— 列出术语（含提问模板）
func (s *Server) handleTerms(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	st, err := s.termStore()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"terms":   st.All(),
		"prompts": st.Prompts(),
		"path":    st.Path(),
	})
}

type termReq struct {
	Word   string            `json:"word"`
	Sheet  string            `json:"sheet"`
	Field  string            `json:"field"`
	Kind   string            `json:"kind"`
	Key    map[string]string `json:"key"`
	Note   string            `json:"note"`
	Manual bool              `json:"manual"` // true=人手写（优先，且不被覆盖）
	Text   string            `json:"text"`   // 也可给一段文本，返回命中的映射
}

// handleTermsPut POST /api/v1/terms —— 存一条映射
//
//	带 word+指向 = 保存；只带 text = 查询命中的映射
func (s *Server) handleTermsPut(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req termReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	st, err := s.termStore()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 只给 text：查询
	if req.Word == "" && req.Text != "" {
		writeJSON(w, http.StatusOK, map[string]any{"hits": st.Resolve(req.Text)})
		return
	}
	src := "clarified"
	if req.Manual {
		src = "manual"
	}
	if err := st.Save(terms.Term{
		Word:     req.Word,
		Resolves: terms.Target{Sheet: req.Sheet, Field: req.Field, Kind: req.Kind, Key: req.Key},
		Note:     req.Note,
		Source:   src,
	}); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleTermsDelete POST /api/v1/terms/delete —— 删一条
func (s *Server) handleTermsDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Word string `json:"word"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Word == "" {
		writeErr(w, http.StatusBadRequest, "缺少 word")
		return
	}
	st, err := s.termStore()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := st.Delete(req.Word); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// termStore 取当前工作区的术语表。
func (s *Server) termStore() (*terms.Store, error) {
	_, layout, _, _ := s.cur()
	return terms.Open(layout.Root)
}

// handleTermsOrPut 让 GET /terms 读、POST /terms 写或查。
func (s *Server) handleTermsOrPut(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.handleTermsPut(w, r)
		return
	}
	s.handleTerms(w, r)
}
