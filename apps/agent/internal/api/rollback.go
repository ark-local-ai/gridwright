package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ark-local-ai/ark/apps/agent/internal/rollback"
)

// 回滚接口（见 docs/agent-architecture/29 与对外承诺"可回滚"）。
//
//	GET  /api/v1/rollback        —— 列出可回滚的账目（带"能不能倒"的判断）
//	POST /api/v1/rollback        —— 按账目行号回滚一批
//
// 回滚本身也记账（op=rollback），所以可以再倒回去（=重做）。

// handleRollbackList GET /api/v1/rollback?limit=
func (s *Server) handleRollbackList(w http.ResponseWriter, r *http.Request) {
	_, _, led, _ := s.cur()
	items, err := rollback.List(led, 200)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if wantsText(r) {
		var b strings.Builder
		if len(items) == 0 {
			b.WriteString("账目里还没有改动，没有可回滚的\n")
		} else {
			b.WriteString(line("可回滚的改动（最近 %d 条）：", len(items)))
			for _, it := range items {
				mark := "可回滚"
				if !it.CanRollback {
					mark = "不可回滚：" + it.WhyNot
				}
				b.WriteString(line("  #%d %s %s!%s  %s → %s  [%s]",
					it.ID, it.Ts, it.Table, it.Cell, it.New, it.Old, mark))
			}
		}
		writeText(w, b.String())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type rollbackReq struct {
	IDs   []int  `json:"ids"`   // 要回滚的账目行号
	Table string `json:"table"` // 目标表文件名（可空=工作区第一张）
}

// handleRollback POST /api/v1/rollback
func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req rollbackReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.IDs) == 0 {
		writeErr(w, http.StatusBadRequest, "缺少要回滚的账目 ids")
		return
	}
	target, err := s.pickTable(req.Table)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg, _, led, _ := s.cur()
	res, err := rollback.RollbackByID(led, "", target, req.IDs, cfg.LLM.Model)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleRollbackOrList 让 GET 列、POST 执行。
func (s *Server) handleRollbackOrList(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.handleRollback(w, r)
		return
	}
	s.handleRollbackList(w, r)
}
