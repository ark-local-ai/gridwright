package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/ark-local-ai/ark/apps/agent/internal/agent"
	"github.com/ark-local-ai/ark/apps/agent/internal/convo"
	"github.com/ark-local-ai/ark/apps/agent/internal/llm"
	"github.com/ark-local-ai/ark/apps/agent/internal/logx"
	"github.com/ark-local-ai/ark/apps/agent/internal/notify"
	"github.com/ark-local-ai/ark/apps/agent/internal/propose"
	"github.com/ark-local-ai/ark/apps/agent/internal/trace"
	"github.com/ark-local-ai/ark/apps/agent/internal/zones"
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
	// ConversationID 产生这条指令的会话（可选）。带上它，账目就能回答
	// "这次改动是哪次对话让我做的"。
	ConversationID string `json:"conversationId,omitempty"`
	// Images 是随指令附的图（data URL）。举例："按 D 列名字把 E、F 列填进销售明细表"——
	// 数据本身就在截图里。不把图传到"出清单"这一步，模型只能反问"数据来自哪张表"，
	// 把一件能做的事停成澄清（实测发生过）。
	Images []string `json:"images,omitempty"`
	// ZoneID 是本次指令所属的作业区。显式给错 → 404，不静默回退到当前区：
	// “以为在改 A 区、实际改了 B 区”比直接报错危险得多。
	ZoneID string `json:"zoneId,omitempty"`
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
	// 接口层 enter/exit 只记轻量参数：细节步骤由 agent.Plan 自己记（flow=plan）。
	// 两端都用 flow + step 两个固定字段，? 查日志时一条 grep 就能拼出一次完整请求。
	logx.Step("api.plan", "enter", "chars", len([]rune(req.Instruction)), "file", req.File,
		"conversation", req.ConversationID, "images", len(req.Images), "zone", req.ZoneID)
	cfg, layout, led, _ := s.cur()
	// 作业区：校验存在性；target.file 作为默认目标表（用户显式给了 file 则以用户为准）。
	zoneID := ""
	if req.ZoneID != "" {
		st := s.zonesStore()
		var z zones.Zone
		var ok bool
		if st != nil {
			z, ok = st.Get(req.ZoneID)
		}
		if !ok {
			writeErr(w, http.StatusNotFound, "zone 不存在："+req.ZoneID)
			return
		}
		zoneID = z.ID
		if req.File == "" && z.Target.File != "" {
			req.File = z.Target.File
		}
	}
	if !cfg.BrainReady() {
		logx.Step("api.plan", "exit", "err", "未配置模型")
		writeErr(w, http.StatusBadRequest,
			"还没配置模型（脑）：请在设置里填 base_url 与 api_key；只读的看表与体检不受影响")
		return
	}
	// trace 收「这一步做了什么」，随响应回给界面（运行详情）。
	ctx, tr := trace.New(r.Context(), "plan")
	brain := s.brainClient()
	ag := agent.New(cfg, layout, led, brain)
	// 图只放行 data:image/ 前缀（与 /chat 同一道校验）：不信任客户端传来的任意 URL，
	// 否则等于让对方通过我们的密钥去访问任意地址。
	var imgs []llm.ImageInput
	for _, d := range imgsForStore(req.Images) {
		imgs = append(imgs, llm.ImageInput{DataURL: d})
	}
	// 带上产生这条指令的对话：复核反馈（"那行改成累加"）离开上下文就不知道
	// 在纠正哪一条。读不到就退回"只给指令"——上下文是增强，不是必需。
	var history []convo.Message
	if req.ConversationID != "" {
		if store, serr := s.convoStore(); serr == nil {
			if c := store.Get(req.ConversationID); c != nil {
				history = c.Messages
			}
		}
	}
	prop, err := ag.Plan(ctx, req.Instruction, agent.PlanOptions{File: req.File, Images: imgs, History: history})
	if err != nil {
		logx.Step("api.plan", "exit", "err", err.Error())
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 把会话与作业区标在清单上（Apply 时写进账目）。
	prop.Conversation = req.ConversationID
	prop.Zone = zoneID
	id := s.proposals().Put(prop)
	logx.Step("api.plan", "exit", "id", id, "items", len(prop.Items), "blocked", len(prop.Blocked))
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "proposal": prop, "trace": tr})
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
	logx.Step("api.apply", "enter", "id", req.ID)
	store := s.proposals()
	prop := store.Get(req.ID)
	if prop == nil {
		logx.Step("api.apply", "exit", "id", req.ID, "err", "清单不存在或已过期")
		writeErr(w, http.StatusNotFound, "清单不存在或已过期（请重新计划）")
		return
	}
	cfg, layout, led, _ := s.cur()
	results, err := propose.Apply(prop, led, cfg.LLM.Model)
	if err != nil {
		// 指纹不符 / 写回失败等：保留清单让用户重试或重新计划
		logx.Step("api.apply", "exit", "id", req.ID, "err", err.Error())
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	// 作业区链路：清单来自 inbox/staged/<taskId> 的暂存文件。写表成功后它们
	// 才算真的「处理过」，归档到 done（去重也靠 done 认得它们）；单文件失败
	// 不影响已经落盘的改动，也不回滚清单。
	for _, src := range prop.Sources {
		if strings.TrimSpace(src) == "" {
			continue
		}
		if err := layout.MoveToDone(src); err != nil {
			log.Printf("[apply] 归档暂存文件失败 %s: %v", src, err)
		}
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

	// 让模型把"这次改了什么"总结成**一句话**（≤20 字）：
	//   · 写进工作表笔记 → 看板的"上次改动"显示人话，而不是 B2、C2；
	//   · 随响应回给界面 → 对话里的回执也用这句。
	// 结合对话上下文总结，才能写出"销售明细表补充按揭信息和日期"。
	// 没配模型 / 调用失败都不影响改动本身（已落盘）。
	var summaries []agent.SheetChangeSummary
	if cfg.BrainReady() {
		var conv *convo.Conversation
		if prop.Conversation != "" {
			if cs, cerr := s.convoStore(); cerr == nil {
				conv = cs.Get(prop.Conversation)
			}
		}
		ag := agent.New(cfg, layout, led, s.brainClient())
		if ss, serr := ag.SummarizeChange(r.Context(), conv, results); serr == nil {
			summaries = ss
			if notes := s.notesStore(); notes != nil {
				at := nowTs().Format("2006-01-02 15:04")
				for _, sm := range ss {
					_ = notes.PutLastChange(nodeKeyForFile(filepath.Base(prop.Target), sm.Sheet), sm.Text, at, cfg.LLM.Model)
				}
			}
		} else {
			log.Printf("[apply] 改动总结未生成: %v", serr)
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
		"results": results, "selfCheck": sc, "summaries": summaries,
	})
	// exit 放在写响应之后：这两个数就是这次接口的最终交代（与 flow=apply 的退出行一致）。
	logx.Step("api.apply", "exit", "id", req.ID, "applied", applied, "rejected", rejected)
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
