package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"github.com/ark-local-ai/ark/apps/agent/internal/workspace"
)

// 工作区管理（见 docs/agent-architecture/19-界面设计.md 阶段 2）：
// 工作区 = 一个文件夹，一一对应。支持列出/打开/新建/切换。

type workspaceListItem struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Tables  int    `json:"tables"`
	Opened  string `json:"opened"`
	Current bool   `json:"current"`
}

type workspaceListResp struct {
	Current string              `json:"current"`
	Items   []workspaceListItem `json:"items"`
}

// handleWorkspaces GET /api/v1/workspaces —— 列出登记过的工作区
func (s *Server) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	_, _, _, reg := s.cur()
	if reg == nil {
		writeJSON(w, http.StatusOK, workspaceListResp{Items: []workspaceListItem{}})
		return
	}
	cur := reg.Current()
	items := []workspaceListItem{}
	for _, e := range reg.List() {
		// 刷新表数量（目录可能已变化）
		n := e.Tables
		if abs, err := filepath.Abs(e.Path); err == nil {
			if l, err := workspace.LayoutOf(abs); err == nil {
				if fs, err := l.DataFiles(); err == nil {
					n = len(fs)
				}
			}
		}
		items = append(items, workspaceListItem{
			Path: e.Path, Name: e.Name, Tables: n,
			Opened: e.Opened, Current: samePath(e.Path, cur),
		})
	}
	writeJSON(w, http.StatusOK, workspaceListResp{Current: cur, Items: items})
}

type openReq struct {
	Dir string `json:"dir"`
}

// handleWorkspaceOpen POST /api/v1/workspace/open {dir} —— 就地把已有文件夹当工作区
func (s *Server) handleWorkspaceOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req openReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Dir == "" {
		writeErr(w, http.StatusBadRequest, "缺少 dir")
		return
	}
	if fi, err := os.Stat(req.Dir); err != nil || !fi.IsDir() {
		writeErr(w, http.StatusBadRequest, "目录不存在或不可用："+req.Dir)
		return
	}
	if err := s.SwitchWorkspace(req.Dir); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, layout, _, _ := s.cur()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "root": layout.Root})
}

type createReq struct {
	Name string `json:"name"` // 工作区名（文件夹名）
	Base string `json:"base"` // 建在哪；空=应用数据目录下的 workspaces/
}

// handleWorkspaceCreate POST /api/v1/workspace/create {name, base} —— 新建工作区文件夹
func (s *Server) handleWorkspaceCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeErr(w, http.StatusBadRequest, "缺少 name")
		return
	}
	base := req.Base
	if base == "" {
		base = s.defaultWorkspacesDir()
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, "无法创建目录："+err.Error())
		return
	}
	dir := workspace.SuggestDir(base, req.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, "无法创建工作区："+err.Error())
		return
	}
	if err := s.SwitchWorkspace(dir); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, layout, _, _ := s.cur()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "root": layout.Root})
}

// handleWorkspaceForget POST /api/v1/workspace/forget {dir} —— 从列表移除（不删文件夹）
func (s *Server) handleWorkspaceForget(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req openReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Dir == "" {
		writeErr(w, http.StatusBadRequest, "缺少 dir")
		return
	}
	_, _, _, reg := s.cur()
	if reg == nil {
		writeErr(w, http.StatusBadRequest, "未启用工作区注册表")
		return
	}
	if err := reg.Forget(req.Dir); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleSettings GET/PUT /api/v1/settings —— 脑（LLM）等配置
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, _, _, _ := s.cur()
		writeJSON(w, http.StatusOK, map[string]any{
			"baseUrl": cfg.LLM.BaseURL,
			"model":   cfg.LLM.Model,
			// 密钥不回传明文，只回"是否已配"
			"hasApiKey":   cfg.LLM.APIKey != "",
			"brainReady":  cfg.BrainReady(),
			"workspace":   cfg.Workspace,
			"pollSeconds": cfg.PollSeconds,
		})
	case http.MethodPut:
		var req struct {
			BaseURL string `json:"baseUrl"`
			APIKey  string `json:"apiKey"`
			Model   string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求格式错误")
			return
		}
		s.mu.Lock()
		if req.BaseURL != "" {
			s.Cfg.LLM.BaseURL = req.BaseURL
		}
		if req.APIKey != "" { // 空表示不改（避免前端回传掩码覆盖真 key）
			s.Cfg.LLM.APIKey = req.APIKey
		}
		if req.Model != "" {
			s.Cfg.LLM.Model = req.Model
		}
		ready := s.Cfg.BrainReady()
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "brainReady": ready})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET/PUT")
	}
}

// defaultWorkspacesDir 新建工作区的默认落点（应用数据目录下 workspaces/）。
func (s *Server) defaultWorkspacesDir() string {
	// 用当前工作区的父目录作为兄弟目录，避免引入平台相关的 appdata 探测
	_, layout, _, _ := s.cur()
	if layout != nil && layout.Root != "" {
		return filepath.Join(filepath.Dir(layout.Root), "workspaces")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "gridwright", "workspaces")
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ca, err1 := filepath.Abs(a)
	cb, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return a == b
	}
	return equalFold(filepath.Clean(ca), filepath.Clean(cb))
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 32
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}
