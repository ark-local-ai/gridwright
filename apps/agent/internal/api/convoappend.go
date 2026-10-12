package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ark-local-ai/ark/apps/agent/internal/convo"
)

// handleConversationAppend POST /api/v1/conversation/append
//
// 把一条**非用户**消息追加进会话。用途：应用改动之后，把"这次做了什么"如实写回
// 产生这份清单的那条对话，而不是让用户去翻账目/看板才知道有没有改成。
//
// 只允许 agent / system 两个角色：用户话必须走 /chat（那里才会真的问模型）。
// 否则界面可以伪造一条"用户说过 X"，污染会话历史与之后 /plan 的上下文。
func (s *Server) handleConversationAppend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req struct {
		ConversationID string `json:"conversationId"`
		Text           string `json:"text"`
		Role           string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if strings.TrimSpace(req.ConversationID) == "" || strings.TrimSpace(req.Text) == "" {
		writeErr(w, http.StatusBadRequest, "需要 conversationId 与 text")
		return
	}
	role := req.Role
	if role != convo.RoleAgent && role != convo.RoleSystem {
		role = convo.RoleAgent
	}
	store, err := s.convoStore()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	c, err := store.Append(req.ConversationID, convo.Message{Role: role, Text: req.Text})
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "conversation": c})
}
