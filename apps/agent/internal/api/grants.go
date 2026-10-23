package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ark-local-ai/ark/apps/agent/internal/config"
	"github.com/ark-local-ai/ark/apps/agent/internal/pathsafe"
)

// 路径白名单（见 apps/agent/internal/pathsafe）。
//
// 与 token 的分工：token 证明“调用者是本机界面”；白名单限定“能碰哪些目录”。
// 自动监听、连接器、作业区输入路径都必须先过白名单；用户一次性打开/导入是显式动作，
// 由对应接口自行授权（workspace/open 会 Allow 所选目录）。

// handleGrants GET/POST /api/v1/grants
// GET  → {items:[Grant]}
// POST {path, mode?} → 授权一个根目录（mode: rw|read，默认 rw）
func (s *Server) handleGrants(w http.ResponseWriter, r *http.Request) {
	if s.Grants == nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": []pathsafe.Grant{}, "enabled": false})
		return
	}
	switch r.Method {
	case http.MethodGet:
		items := s.Grants.List()
		if items == nil {
			items = []pathsafe.Grant{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "enabled": true})
	case http.MethodPost:
		var req struct {
			Path string `json:"path"`
			Mode string `json:"mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Path) == "" {
			writeErr(w, http.StatusBadRequest, "缺少 path")
			return
		}
		mode := pathsafe.ModeRW
		if strings.EqualFold(req.Mode, "read") {
			mode = pathsafe.ModeRead
		}
		g, err := s.Grants.AllowWithMode(req.Path, "界面授权", mode)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "grant": g})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET/POST")
	}
}

// handleGrantRevoke POST /api/v1/grants/revoke {path}
// 只删授权，不删数据；撤销后自动监听/连接器会拒绝该目录。
func (s *Server) handleGrantRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	if s.Grants == nil {
		writeErr(w, http.StatusBadRequest, "路径白名单未启用")
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Path) == "" {
		writeErr(w, http.StatusBadRequest, "缺少 path")
		return
	}
	if err := s.Grants.Revoke(req.Path); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// grantsPath 是白名单文件的默认落点（应用数据目录，跟工作区无关）。
func grantsPath() string { return config.UserConfigDir() + "/grants.json" }
