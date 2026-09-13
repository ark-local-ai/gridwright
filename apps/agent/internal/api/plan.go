package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/ark-local-ai/ark/apps/agent/internal/agent"
	"github.com/ark-local-ai/ark/apps/agent/internal/notify"
	"github.com/ark-local-ai/ark/apps/agent/internal/propose"
)

// 待确认闭环（见 docs/agent-architecture/19-界面设计.md 阶段 4）。
//
// 用户的规则：**看清单 → 你确认 → 才改**。
//   POST /plan  ：给一句指令（或新数据）→ 产出待改清单，**不落盘**
//   POST /apply ：带着清单 ID → 执行（备份 → 改 → 记账）
//
// 清单存在服务端，Apply 只认 ID：用户确认的是服务端算出来的那份，
// 客户端改不了；且文件在确认期间被改过会被指纹拦下。

type planReq struct {
	Instruction string `json:"instruction"` // 一句话指令
	File        string `json:"file"`        // 可选：目标文件
}

// handlePlan POST /api/v1/plan
func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req planReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Instruction == "" {
		writeErr(w, http.StatusBadRequest, "缺少 instruction")
		return
	}
	cfg, layout, led, _ := s.cur()
	if !cfg.BrainReady() {
		writeErr(w, http.StatusBadRequest,
			"还没配置模型（脑）：请在设置里填 base_url 与 api_key；只读的看表与体检不受影响")
		return
	}
	brain := s.brainClient()
	ag := agent.New(cfg, layout, led, brain)
	prop, err := ag.Plan(r.Context(), req.Instruction, agent.PlanOptions{File: req.File})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	id := s.proposals().Put(prop)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "proposal": prop})
}

type applyReq struct {
	ID string `json:"id"`
}

// handleApply POST /api/v1/apply
func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req applyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		writeErr(w, http.StatusBadRequest, "缺少清单 id")
		return
	}
	store := s.proposals()
	prop := store.Get(req.ID)
	if prop == nil {
		writeErr(w, http.StatusNotFound, "清单不存在或已过期（请重新计划）")
		return
	}
	_, _, led, _ := s.cur()
	cfg, _, _, _ := s.cur()
	results, err := propose.Apply(prop, led, cfg.LLM.Model)
	if err != nil {
		// 指纹不符 / 写回失败等：保留清单让用户重试或重新计划
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	store.Drop(req.ID) // 已应用，防重复
	applied, rejected := 0, 0
	touched := []string{}
	for _, x := range results {
		if x.Status == "ok" {
			applied++
			// 记下"已同步"的节点，供自检判断哪些表动过
			if x.Sheet != "" {
				touched = append(touched, propNodeBase(prop)+"!"+x.Sheet)
			}
		} else {
			rejected++
		}
	}

	// **改动后自动自检**：确认"该同步的表动了吗"（见 26-自检流程）
	// 自检失败不影响改动本身（改动已落盘），只是少一份报告
	var sc any
	if node := firstItemNode(prop); node != "" {
		if rep, err := s.runSelfCheck(node, "", touched); err == nil {
			sc = rep
		}
	}

	// 通知：改完就推（此前只有 inbox 自动路径发通知，界面确认后改表**不发**——
	// 主路径反而没有通知，是漏的）。
	cfg2, _, _, _ := s.cur()
	detail := []string{}
	for _, x := range results {
		if x.Status != "ok" {
			detail = append(detail, fmt.Sprintf("%s %s: %s", x.Ref, x.Field, x.Note))
		}
	}
	if perr := notify.Send(cfg2.Notify, notify.Message{
		Table: propNodeBase(prop), Summary: prop.Summary,
		Applied: applied, Rejected: rejected, RejectedDetail: detail,
	}); perr != nil {
		log.Printf("[apply] 通知未发出: %v", perr)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "applied": applied, "rejected": rejected,
		"results": results, "selfCheck": sc,
	})
}

// handleProposalGet GET /api/v1/plan?id= —— 回看一份清单（可选）
func (s *Server) handleProposalGet(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "缺少 id")
		return
	}
	prop := s.proposals().Get(id)
	if prop == nil {
		writeErr(w, http.StatusNotFound, "清单不存在或已过期")
		return
	}
	writeJSON(w, http.StatusOK, prop)
}

// handlePlanOrGet 让 GET /plan?id= 回看清单、POST /plan 产出清单。
func (s *Server) handlePlanOrGet(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.handleProposalGet(w, r)
		return
	}
	s.handlePlan(w, r)
}

// propNodeBase 取清单目标表的文件名（不带扩展名）。
func propNodeBase(p *propose.Proposal) string {
	b := filepath.Base(p.Target)
	return strings.TrimSuffix(b, ".xlsx")
}

// firstItemNode 取清单第一条改动所在的节点，作为自检的"改动点"。
func firstItemNode(p *propose.Proposal) string {
	if len(p.Items) == 0 {
		return ""
	}
	it := p.Items[0]
	if it.File == "" || it.Sheet == "" {
		return ""
	}
	return it.File + "!" + it.Sheet
}
