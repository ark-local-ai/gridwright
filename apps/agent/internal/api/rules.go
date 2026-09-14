package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ark-local-ai/ark/apps/agent/internal/memory"
	"github.com/ark-local-ai/ark/apps/agent/internal/rules"
)

// rules.go 暴露"规则引擎"的只读视图与试跑（见 docs/agent-architecture/30-规则引擎.md）。
//
// 为什么要有这两个接口：
//   - 规则写在 rules.yaml 里，用户看不到"我这条规则到底能不能跑"——
//     一条 when 缺条件的规则会被当成"一直在生效"，其实每次都退回问模型。
//     这种"以为在跑"是最坏的，所以 GET 必须把**跑不起来的原因**说清楚。
//   - 规则会改表，改之前必须能**无损预演**：dry-run 只回报"会改哪些格、旧值→新值"，
//     不写文件、不记账。

// ruleView 是一条规则给界面看的样子。
type ruleView struct {
	Name    string   `json:"name"`
	Trigger string   `json:"trigger,omitempty"`
	Action  string   `json:"action,omitempty"`
	Forbid  []string `json:"forbid,omitempty"`
	// Kind 规则类别：edge（改表）| link（声明关联）| forbid（护栏）| hint（纯提示）。
	// 界面按它分组——三类东西"做完了没有"的判法不同，混在一起会误报。
	Kind string `json:"kind"`
	// Runnable=true 表示这条规则结构完整、引擎能直接用它。
	Runnable bool   `json:"runnable"`
	Reason   string `json:"reason,omitempty"`
	// Target 规则声明的目标表（空=需要模型分诊）。
	Target string `json:"target,omitempty"`
	// Summary 一句话说清这条规则干什么（给不识 yaml 的人看）。
	Summary string `json:"summary,omitempty"`
}

type rulesResp struct {
	Rules []ruleView `json:"rules"`
	// Path 规则文件位置（用户要能直接去编辑它）。
	Path string `json:"path"`
	// Raw 原文件内容（界面上直接可看/可改，不必去翻文件夹）。
	Raw string `json:"raw"`
	// Executable 可短路条数 / Total 总条数——界面顶部的读数。
	Executable int `json:"executable"`
	Total      int `json:"total"`
}

// handleRulesOrPut 按方法分发：GET 读、POST 存（让 /rules 一个路径既能读又能写）。
func (s *Server) handleRulesOrPut(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.handleRulesPut(w, r)
		return
	}
	s.handleRules(w, r)
}

// handleRules 处理 GET /api/v1/rules（读规则 + 可执行状态）。
func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	_, layout, _, _ := s.cur()
	rf, _, _, _, err := memory.Load(layout.Rules, layout.State)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "解析 rules.yaml 失败："+err.Error())
		return
	}
	raw := ""
	if b, rerr := os.ReadFile(layout.Rules); rerr == nil {
		raw = string(b)
	}
	ok, bad := rf.ExecutableRules()
	badReason := map[string]string{}
	for _, b := range bad {
		badReason[b.Name] = b.Reason
	}
	runnable := map[string]bool{}
	for _, r0 := range ok {
		runnable[r0.Name] = true
	}

	views := make([]ruleView, 0, len(rf.Rules))
	for _, rule := range rf.Rules {
		v := ruleView{
			Name: rule.Name, Trigger: rule.Trigger, Action: rule.Action,
			Forbid: rule.Forbid, Runnable: runnable[rule.Name],
			Kind:   rule.Kind(),
			Reason: badReason[rule.Name], Target: rules.Target(rule),
			Summary: describeRule(rule),
		}
		views = append(views, v)
	}
	writeJSON(w, http.StatusOK, rulesResp{
		Rules: views, Path: layout.Rules, Raw: raw,
		Executable: len(ok), Total: len(rf.Rules),
	})
}

// describeRule 用一句人话说明规则要做什么（不给用户看 yaml 字段名）。
func describeRule(r memory.Rule) string {
	switch r.Kind() {
	case memory.KindLink:
		s := "声明关系：改了「" + r.Link.To + "」要连带看「" + r.Link.From + "」"
		if r.Link.Note != "" {
			s += "（" + r.Link.Note + "）"
		}
		return s
	case memory.KindForbid:
		return "护栏：不许改动 " + strings.Join(r.Forbid, "、") + "（模型也动不了）"
	case memory.KindHint:
		if r.Trigger != "" || r.Action != "" {
			return "只作提示（没写 when/then，交给模型判断）"
		}
		return ""
	}
	if r.Then == nil {
		// 半条规则（有 when 无 then，或反过来）：说清它缺什么，别留白——
		// 用户需要知道这条为什么不会被自动执行。
		if r.When != nil {
			var conds []string
			if r.When.File != "" {
				conds = append(conds, "文件名含「"+r.When.File+"」")
			}
			if r.When.Format != "" {
				conds = append(conds, "格式为 "+r.When.Format)
			}
			if len(r.When.HasColumns) > 0 {
				conds = append(conds, "含列 "+strings.Join(r.When.HasColumns, "、"))
			}
			if len(conds) > 0 {
				return "当 " + strings.Join(conds, " 且 ") + "，但还没写 then（怎么改表），所以不会自动执行"
			}
		}
		return "还没写完整（when/then 缺一），不会自动执行"
	}
	var parts []string
	if r.When != nil {
		var conds []string
		if r.When.File != "" {
			conds = append(conds, "文件名含「"+r.When.File+"」")
		}
		if r.When.Format != "" {
			conds = append(conds, "格式为 "+r.When.Format)
		}
		if len(r.When.HasColumns) > 0 {
			conds = append(conds, "含列 "+strings.Join(r.When.HasColumns, "、"))
		}
		parts = append(parts, "当 "+strings.Join(conds, " 且 "))
	}
	var fieldCol, srcCol string
	for k, v := range r.Then.Field {
		fieldCol, srcCol = k, v
	}
	op := "写入"
	if strings.EqualFold(strings.TrimSpace(r.Then.Op), "add") {
		op = "累加进"
	}
	where := fieldCol
	if r.Then.Sheet != "" {
		where = r.Then.Sheet + " 的 " + fieldCol
	}
	parts = append(parts, "把「"+srcCol+"」"+op+" "+where)
	return strings.Join(parts, "，")
}

// dryRunResp 是试跑结果：会改什么，但不落盘。
type dryRunResp struct {
	File   string    `json:"file"`
	Target string    `json:"target"`
	Hits   []string  `json:"hits"`
	Items  []dryItem `json:"items"`
	Skips  []dryItem `json:"skips"`
	Note   string    `json:"note,omitempty"`
	// Wrote 永远是 false——这个接口不动文件。写出来是为了让界面明确显示。
	Wrote bool `json:"wrote"`
}

type dryItem struct {
	Rule  string `json:"rule,omitempty"`
	Sheet string `json:"sheet,omitempty"`
	Ref   string `json:"ref,omitempty"`
	Field string `json:"field,omitempty"`
	Old   string `json:"old,omitempty"`
	New   string `json:"new,omitempty"`
	Line  int    `json:"line,omitempty"`
	Why   string `json:"why,omitempty"`
}

// handleRulesDryRun 处理 POST /api/v1/rules/dry-run?file=<inbox 文件名>。
//
// 只计算、不写回、不记账。让用户"先看看规则会干什么"再决定开不开。
func (s *Server) handleRulesDryRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	_, layout, _, _ := s.cur()
	rf, _, _, _, err := memory.Load(layout.Rules, layout.State)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "解析 rules.yaml 失败："+err.Error())
		return
	}
	ok, _ := rf.ExecutableRules()
	if len(ok) == 0 {
		writeJSON(w, http.StatusOK, dryRunResp{
			Note: "没有可执行的规则（when/then 齐全的才算）。当前规则只会在问模型时当提示。",
		})
		return
	}

	// 选文件：优先 query 指定，否则 inbox 里唯一那份。
	files, _ := layout.InboxFiles()
	pick := ""
	if want := strings.TrimSpace(r.URL.Query().Get("file")); want != "" {
		for _, fp := range files {
			if strings.EqualFold(filepath.Base(fp), want) {
				pick = fp
				break
			}
		}
		if pick == "" {
			writeErr(w, http.StatusNotFound, "inbox 里没有这个文件："+want)
			return
		}
	} else if len(files) == 1 {
		pick = files[0]
	} else if len(files) == 0 {
		writeJSON(w, http.StatusOK, dryRunResp{Note: "inbox 是空的，没有可试跑的数据。"})
		return
	} else {
		writeErr(w, http.StatusBadRequest, "inbox 里有多个文件，请指明试跑哪一个（?file=）")
		return
	}

	data, err := rules.Load(pick)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读不了 "+filepath.Base(pick)+"："+err.Error())
		return
	}
	resp := dryRunResp{File: filepath.Base(pick)}
	for _, rule := range ok {
		if !rules.Match(rule, data) {
			continue
		}
		resp.Hits = append(resp.Hits, rule.Name)
		intents, skips := rules.Expand(rule, data)
		for _, sk := range skips {
			resp.Skips = append(resp.Skips, dryItem{Rule: sk.Rule, Line: sk.Line, Why: sk.Reason})
		}
		for _, in := range intents {
			resp.Items = append(resp.Items, dryItem{
				Rule: in.Rule, Sheet: in.Sheet, Field: in.Field,
				New:  in.Value + map[bool]string{true: "（累加）", false: ""}[in.Op == "add"],
				Line: in.Line, Why: describeIntent(in),
			})
		}
	}
	if len(resp.Hits) == 0 {
		resp.Note = "没有规则命中这份数据（" + data.File + " 的表头：" + strings.Join(data.Header, "、") + "）。"
	}
	writeJSON(w, http.StatusOK, resp)
}

func describeIntent(in rules.Intent) string {
	var keys []string
	for k, v := range in.Key {
		keys = append(keys, k+"="+v)
	}
	s := "改 " + in.Sheet + " 里 " + strings.Join(keys, "、")
	if in.Month != "" {
		s += "，" + in.Month
	}
	return s + " 的 " + in.Field
}

// rulesPutReq 是保存规则的请求体。
//
// 两种写法都收：**Raw 优先**（用户在界面里直接编 yaml 文本），
// 否则用 Rules 数组（界面用表单改，不碰文本）。给 raw 时先解析一遍，
// 解析不过就拒绝——坏文件不能被写进去，它直接决定改表行为。
type rulesPutReq struct {
	Rules []memory.Rule `json:"rules"`
	Raw   string        `json:"raw"`
}

// handleRulesPut 处理 POST /api/v1/rules（保存规则）。
//
// 门禁与记忆/术语一致：**写要人拍板**。规则直接决定改表行为，
// 机器不能自己往里写一条然后照着它改表。
func (s *Server) handleRulesPut(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req rulesPutReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	_, layout, _, _ := s.cur()

	var rf memory.RulesFile
	if strings.TrimSpace(req.Raw) != "" {
		parsed, perr := memory.Parse(req.Raw)
		if perr != nil {
			writeErr(w, http.StatusBadRequest, "规则解析失败，未写入："+perr.Error())
			return
		}
		rf = parsed
	} else {
		rf = memory.RulesFile{Rules: req.Rules}
	}
	// 重名规则会让"命中哪条"变得不可预测；账目/回滚也靠规则名认人。
	if dup := dupRuleNames(rf.Rules); dup != "" {
		writeErr(w, http.StatusBadRequest, "规则名重复："+dup+"（规则名要唯一）")
		return
	}
	if err := rf.Save(layout.Rules); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 回读确认真的落盘，并把可执行情况一并回报。
	back, _, _, _, err := memory.Load(layout.Rules, layout.State)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "写入后回读失败："+err.Error())
		return
	}
	ok, bad := back.ExecutableRules()
	writeJSON(w, http.StatusOK, map[string]any{
		"saved": true, "total": len(back.Rules),
		"executable": len(ok), "problems": bad, "path": layout.Rules,
	})
}

// handleRulesValidate 处理 POST /api/v1/rules/validate——只校验不写入。
// 界面里用户边写边看"这条能不能跑起来"。
func (s *Server) handleRulesValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req rulesPutReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	var rf memory.RulesFile
	if strings.TrimSpace(req.Raw) != "" {
		parsed, perr := memory.Parse(req.Raw)
		if perr != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": perr.Error()})
			return
		}
		rf = parsed
	} else {
		rf = memory.RulesFile{Rules: req.Rules}
	}
	ok, bad := rf.ExecutableRules()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "total": len(rf.Rules),
		"executable": len(ok), "problems": bad,
		"dup": dupRuleNames(rf.Rules),
	})
}

func dupRuleNames(rules []memory.Rule) string {
	seen := map[string]bool{}
	for _, r := range rules {
		n := strings.TrimSpace(r.Name)
		if n == "" {
			continue
		}
		if seen[n] {
			return n
		}
		seen[n] = true
	}
	return ""
}
