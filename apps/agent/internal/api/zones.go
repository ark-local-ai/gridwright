package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ark-local-ai/ark/apps/agent/internal/pathsafe"
	"github.com/ark-local-ai/ark/apps/agent/internal/zones"
)

// 作业区 Zone：工作区内部的“派单作用域”，不是新的工作区。
//
// 本阶段只提供后端契约（存储 / CRUD / 校验 / 账目筛选），**执行器与看板交互
// 留到前端讨论后接入**：在没定“一句话怎么落到哪个区、失败怎么呈现”之前，
// 先做一个会自动改表的 run 端点，风险大于收益。
//
// 安全：input.path 必须已通过 pathsafe 授权；target.file 只能是工作区内的文件名，
// 不允许路径分隔符（否则作业区就成了任意文件写入的入口）。

func (s *Server) zonesStore() *zones.Store {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.zones
}

// validateZone 校验调用方给的 Zone：存在性、目标文件、输入路径授权。
func (s *Server) validateZone(z zones.Zone) error {
	if z.Target.File != "" {
		if strings.ContainsAny(z.Target.File, `/\`) || z.Target.File != filepath.Base(z.Target.File) {
			return errText("target.file 只能是工作区内的文件名，不能带路径")
		}
		// 允许“还不存在的新表”（newTable 场景），但已存在的表必须在工作区里。
		if z.Target.NewTable == "" {
			_, layout, _, _ := s.cur()
			files, err := layout.DataFiles()
			if err == nil {
				found := false
				for _, f := range files {
					if strings.EqualFold(filepath.Base(f), z.Target.File) {
						found = true
						break
					}
				}
				if !found {
					return errText("工作区里没有这张表：" + z.Target.File)
				}
			}
		}
	}
	if z.Input.Type == zones.InputFolder && strings.TrimSpace(z.Input.Path) != "" {
		if s.Grants == nil {
			return nil
		}
		if err := s.Grants.Check(z.Input.Path, pathsafe.ModeRead); err != nil {
			return errText("输入目录尚未授权：" + err.Error())
		}
	}
	return nil
}

// errText 是一个可读错误（避免每处 fmt.Errorf 都引 fmt）。
type errText string

func (e errText) Error() string { return string(e) }

// handleZones GET/POST /api/v1/zones
func (s *Server) handleZones(w http.ResponseWriter, r *http.Request) {
	st := s.zonesStore()
	if st == nil {
		writeErr(w, http.StatusServiceUnavailable, "作业区存储不可用（工作区未就绪）")
		return
	}
	switch r.Method {
	case http.MethodGet:
		items := st.List()
		if items == nil {
			items = []zones.Zone{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		var z zones.Zone
		if err := json.NewDecoder(r.Body).Decode(&z); err != nil {
			writeErr(w, http.StatusBadRequest, "请求格式错误")
			return
		}
		if err := s.validateZone(z); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		saved, err := st.Put(z)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "zone": saved})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET/POST")
	}
}

// handleZoneDelete POST /api/v1/zones/delete {id}
func (s *Server) handleZoneDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	st := s.zonesStore()
	if st == nil {
		writeErr(w, http.StatusServiceUnavailable, "作业区存储不可用")
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.ID) == "" {
		writeErr(w, http.StatusBadRequest, "缺少 id")
		return
	}
	if err := st.Delete(req.ID); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleZoneRuns GET /api/v1/zones/runs?id=&limit=
func (s *Server) handleZoneRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	st := s.zonesStore()
	if st == nil {
		writeErr(w, http.StatusServiceUnavailable, "作业区存储不可用")
		return
	}
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	runs := st.Runs(r.URL.Query().Get("id"), limit)
	if runs == nil {
		runs = []zones.Run{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": runs})
}
