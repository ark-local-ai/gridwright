package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/ark-local-ai/ark/apps/agent/internal/graph"
	"github.com/ark-local-ai/ark/apps/agent/internal/impact"
	"github.com/ark-local-ai/ark/apps/agent/internal/memory2"
)

// 影响面推断（见 docs/agent-architecture/23-影响面推断.md）。
//
//   POST /api/v1/impact        —— 给定改动点（可选类别）→ 推断相关表及优先级
//   POST /api/v1/impact/learn  —— **用户纠正**："还涉及 X 表" → 存成关系记忆
//
// 后者是学习闭环：第一次可能不全，人补一次就永久记住（且记账）。

type impactReq struct {
	Node string `json:"node"` // 改动点："文件!工作表" 或裸 sheet 名
	Kind string `json:"kind"` // 语义类别（收租/卖房/…）；空=未判定
}

// handleImpact POST /api/v1/impact
func (s *Server) handleImpact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req impactReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Node == "" {
		writeErr(w, http.StatusBadRequest, "缺少 node")
		return
	}
	_, layout, _, _ := s.cur()
	files, err := layout.DataFiles()
	if err != nil || len(files) == 0 {
		writeErr(w, http.StatusBadRequest, "工作区没有 .xlsx 表")
		return
	}
	g, err := graph.ScanWorkspace(layout.Root, files, graph.Options{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	mem, _ := s.memStore()
	res := impact.Infer(impact.Input{
		Graph: g, From: parseNode(req.Node), Kind: req.Kind, Memory: mem,
	})
	writeJSON(w, http.StatusOK, res)
}

type learnReq struct {
	Kind   string   `json:"kind"`   // 语义类别
	Tables []string `json:"tables"` // 用户指出还要看的表
	Note   string   `json:"note"`
}

// handleImpactLearn POST /api/v1/impact/learn —— 用户纠正后记下来
func (s *Server) handleImpactLearn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req learnReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Kind == "" || len(req.Tables) == 0 {
		writeErr(w, http.StatusBadRequest, "需要 kind 与 tables")
		return
	}
	mem, err := s.memStore()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// source=user-correction：人工纠正的关系权重更高，永不自动删
	if err := mem.AddRelation(memory2.Relation{
		Kind: req.Kind, Tables: req.Tables, Source: "user-correction", Note: req.Note,
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 记账：谁在什么时候补了这条关系（可审计）
	_, _, led, _ := s.cur()
	cfg, _, _, _ := s.cur()
	if led != nil {
		_ = led.Append(nowTs(), "", "关系记忆", "", "learn",
			"", req.Kind+" → "+joinComma(req.Tables), req.Note, "user-correction", "", cfg.LLM.Model, "ok")
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleRelations GET /api/v1/relations —— 列出已学到的关系记忆
func (s *Server) handleRelations(w http.ResponseWriter, r *http.Request) {
	mem, err := s.memStore()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"relations": mem.All().Relations})
}

func joinComma(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// nowTs 返回账目用的本地时间戳。
func nowTs() time.Time { return time.Now() }
