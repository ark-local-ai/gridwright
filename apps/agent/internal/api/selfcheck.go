package api

import (
	"encoding/json"
	"net/http"

	"github.com/ark-local-ai/ark/apps/agent/internal/graph"
	"github.com/ark-local-ai/ark/apps/agent/internal/selfcheck"
)

// 自检接口（见 docs/agent-architecture/26-自检流程.md）。

type selfcheckReq struct {
	Node  string   `json:"node"`  // 本次改动点 "文件!工作表"
	Kind  string   `json:"kind"`  // 语义类别
	Files []string `json:"files"` // 本次实际动过的节点ID（算"已同步"）
}

// handleSelfCheck POST /api/v1/selfcheck
func (s *Server) handleSelfCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req selfcheckReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Node == "" {
		writeErr(w, http.StatusBadRequest, "缺少 node")
		return
	}
	rep, err := s.runSelfCheck(req.Node, req.Kind, req.Files)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// runSelfCheck 供 handleSelfCheck 与 apply 后自动调用。
func (s *Server) runSelfCheck(nodeID, kind string, touched []string) (*selfcheck.Report, error) {
	_, layout, _, _ := s.cur()
	files, err := layout.DataFiles()
	if err != nil {
		return nil, err
	}
	g, err := graph.ScanWorkspace(layout.Root, files, graph.Options{})
	if err != nil {
		return nil, err
	}
	mem, _ := s.memStore()
	return selfcheck.Run(selfcheck.Input{
		Root:    layout.Root,
		Changed: parseNode(nodeID),
		Kind:    kind,
		Touched: touched,
		Files:   files,
		Graph:   g,
		Memory:  mem,
	}), nil
}
