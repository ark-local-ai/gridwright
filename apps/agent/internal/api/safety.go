package api

import (
	"net/http"

	"github.com/ark-local-ai/ark/apps/agent/internal/safety"
)

// handleSafety GET /api/v1/safety?file= —— 写入前风险评估
// （见 docs/agent-architecture/24-语义映射与安全边界.md）
// 让界面在用户确认前就能看到"这张表能不能安全改"。
func (s *Server) handleSafety(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	target, err := s.pickTable(r.URL.Query().Get("file"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rep, err := safety.Check(target)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"report":   rep,
		"advice":   rep.Advice,
		"canWrite": rep.CanWrite,
	})
}
