package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/ark-local-ai/ark/apps/agent/internal/agent"
	"github.com/ark-local-ai/ark/apps/agent/internal/convo"
)

// 会话接口（见 docs/agent-architecture/7-对话与自动化任务.md）。

// handleConversations GET /api/v1/conversations —— 列出本工作区的会话
func (s *Server) handleConversations(w http.ResponseWriter, r *http.Request) {
	store, err := s.convoStore()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": store.List()})
}

// handleConversation GET/DELETE /api/v1/conversation?id=
func (s *Server) handleConversation(w http.ResponseWriter, r *http.Request) {
	store, err := s.convoStore()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	id := r.URL.Query().Get("id")
	switch r.Method {
	case http.MethodGet:
		c := store.Get(id)
		if c == nil {
			writeErr(w, http.StatusNotFound, "会话不存在")
			return
		}
		writeJSON(w, http.StatusOK, c)
	case http.MethodDelete:
		if err := store.Delete(id); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET/DELETE")
	}
}

type chatReq struct {
	ConversationID string `json:"conversationId"`
	Message        string `json:"message"`
}

// handleChat POST /api/v1/chat —— 说一句话，得到回话 + 结构化建议
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req chatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
		writeErr(w, http.StatusBadRequest, "缺少 message")
		return
	}
	cfg, layout, led, _ := s.cur()
	if !cfg.BrainReady() {
		writeErr(w, http.StatusBadRequest, "还没配置模型（脑）：请在设置里填 base_url 与 api_key 后就能对话了")
		return
	}
	store, err := s.convoStore()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 取或建会话，先把用户的话记下来
	c, err := store.Ensure(req.ConversationID, "")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := store.Append(c.ID, convo.Message{Role: convo.RoleUser, Text: req.Message}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 问脑
	files, _ := layout.DataFiles()
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	ag := agent.New(cfg, layout, led, s.brainClient())
	reply, err := ag.Chat(r.Context(), req.Message, names)
	if err != nil {
		// 记下失败，避免对话看起来"没反应"
		_, _ = store.Append(c.ID, convo.Message{Role: convo.RoleSystem, Text: "出错：" + err.Error()})
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, err := store.Append(c.ID, *reply)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversationId": c.ID, "conversation": updated})
}

// convoStore 取当前工作区的会话存储（跟着工作区走）。
func (s *Server) convoStore() (*convo.Store, error) {
	_, layout, _, _ := s.cur()
	return convo.Open(layout.Root)
}
