package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/graph"
	"github.com/ark-local-ai/ark/apps/agent/internal/memory"
	"github.com/ark-local-ai/ark/apps/agent/internal/propose"
	"github.com/ark-local-ai/ark/apps/agent/internal/rules"
)

// planFromRules 是"规则短路"：命中 when 的规则直接产出待确认清单，不问模型。
//
// 与模型路径的**唯一区别是"谁决定改哪些格"**（规则 vs 模型）。
// 之后的每一步都共用同一条路：locate 定位 → 算旧值 → forbid 护栏 → 待确认 → 记账。
// 这是刻意的：两条路若各写一套定位逻辑，迟早会算出不同结果，那才是真危险的。
//
// 返回 (nil, nil) 表示"没有规则命中" → 调用方接着走模型。
func (a *Agent) planFromRules(instruction string, opts PlanOptions) (*propose.Proposal, error) {
	rf, _, _, _, err := memory.Load(a.Layout.Rules, a.Layout.State)
	if err != nil {
		return nil, err
	}
	ok := rf.PlanRules()
	if len(ok) == 0 {
		return nil, nil // 没有可短路的规则
	}

	// 规则要从"结构化数据"里取值，所以得有一个**数据文件**当输入。
	// 两种来源：对话里给的指令指向 inbox 文件，或 inbox 里恰好只有一份待处理数据。
	data, _, err := a.pickRuleInput(instruction, rf)
	if err != nil || data == nil {
		return nil, nil // 没有可用的数据输入 → 交给模型去理解自然语言
	}

	// 逐条匹配（一条数据可以被多条规则命中，各管各的字段）
	fileList, err := a.Layout.DataFiles()
	if err != nil || len(fileList) == 0 {
		return nil, nil
	}
	target := fileList[0]
	var intents []rules.Intent
	var skips []rules.Skip
	var hits []string

	for _, r := range ok {
		if !rules.Match(r, data) {
			continue
		}
		hits = append(hits, r.Name)
		// 规则声明了目标表就用它（省一次分诊调用）；否则退回第一张表。
		if t := rules.Target(r); t != "" {
			target = matchTable(fileList, t)
		}
		in, sk := rules.Expand(r, data)
		intents = append(intents, in...)
		skips = append(skips, sk...)
	}
	if len(hits) == 0 {
		return nil, nil // 有规则，但没有一条命中这批数据 → 走模型
	}

	// 命中但没有产出（例如所有行都缺列）：仍要返回一份清单说明原因，
	// 而不是"悄悄回退到模型"——用户需要知道规则为什么没帮上忙。
	prop := &propose.Proposal{Root: a.Layout.Root, Target: target}
	prop.Summary = fmt.Sprintf("规则「%s」命中 %s：%d 处改动", strings.Join(hits, "、"), data.File, len(intents))

	f, err := excelize.OpenFile(target)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	for _, in := range intents {
		// 复用模型路径的定位/取值（含 add 的累加语义）——单一实现，两条路结果必然一致。
		item, blocked := resolveEdit(f, target, semanticEdit{
			Sheet: in.Sheet, Key: in.Key, Month: in.Month, Field: in.Field,
			Op: in.Op, Value: in.Value, Reason: in.Reason,
		})
		if blocked != nil {
			// 把"哪条规则、哪一行"补进去（resolveEdit 不知道规则这层）
			b := *blocked
			b.Reason = fmt.Sprintf("规则「%s」：%s", in.Rule, b.Reason)
			prop.Blocked = append(prop.Blocked, b)
			continue
		}
		item.Reason = in.Reason
		prop.Items = append(prop.Items, *item)
	}

	// 规则层面的跳过（缺列/空值）单独列出：它们不在 resolveEdit 的视野里，
	// 不给用户看的话，"规则处理了 5 行，其实漏了 3 行"会被完全掩盖。
	for _, s := range skips {
		prop.Blocked = append(prop.Blocked, propose.Blocked{
			Reason: fmt.Sprintf("规则「%s」：%s", s.Rule, s.Reason),
		})
	}

	// 联动面（和模型路径一样：改这里会牵动谁）
	if fileList != nil {
		if g, gerr := graph.ScanWorkspace(a.Layout.Root, fileList, graph.Options{}); gerr == nil && g != nil {
			for i := range prop.Items {
				prop.Items[i].Affects = g.Propagate(graph.Node{File: filepath.Base(target), Sheet: prop.Items[i].Sheet})
			}
		}
	}

	// 标记来源，界面/账目据此显示"这条是规则办的"
	prop.Source = fmt.Sprintf("rule:%s · %s", strings.Join(hits, "+"), data.File)

	if fp, err := propose.Fingerprint(target); err == nil {
		prop.Finger = fp
	}
	if len(prop.Items) == 0 && len(prop.Blocked) == 0 {
		return nil, nil // 真的什么都没产出 → 让模型兜底，别给空清单
	}
	return prop, nil
}

// pickRuleInput 找出规则该处理的那份"结构化数据"。
//
// 只在**明确指向一个** inbox 文件时才短路：规则是按列名办事的，
// 若有好几份数据不知该用哪份，就不该猜（猜错会改错表）。这时交给模型理解指令。
func (a *Agent) pickRuleInput(instruction string, rf memory.RulesFile) (*rules.Data, string, error) {
	files, err := a.Layout.InboxFiles()
	if err != nil || len(files) == 0 {
		return nil, "", err
	}
	// 指令里点名了哪个 inbox 文件 → 用它（支持部分匹配，如"实收汇总"）
	named := []string{}
	lower := strings.ToLower(instruction)
	for _, fp := range files {
		base := strings.TrimSuffix(filepath.Base(fp), filepath.Ext(fp))
		if base != "" && strings.Contains(lower, strings.ToLower(base)) {
			named = append(named, fp)
		}
	}
	pick := ""
	switch {
	case len(named) == 1:
		pick = named[0]
	case len(files) == 1:
		pick = files[0]
	default:
		// 多份且没点名：只有当**所有**可执行规则都带 has_columns 条件时，
		// 才尝试用"哪份表头能命中规则"来消歧；否则不猜。
		pick = disambiguate(files, rf)
	}
	if pick == "" {
		return nil, "", nil
	}
	d, err := rules.Load(pick)
	if err != nil {
		return nil, "", err
	}
	return d, pick, nil
}

// disambiguate 在多份 inbox 文件里挑出能被规则处理的那一份。
// 只有"恰好一份"能满足某条规则的列条件时才返回它——两份都能满足就说明有歧义，不猜。
func disambiguate(files []string, rf memory.RulesFile) string {
	ok := rf.PlanRules()
	cands := map[string]bool{}
	for _, fp := range files {
		d, err := rules.Load(fp)
		if err != nil {
			continue
		}
		for _, r := range ok {
			if r.When != nil && len(r.When.HasColumns) > 0 && rules.Match(r, d) {
				cands[fp] = true
				break
			}
		}
	}
	if len(cands) == 1 {
		for fp := range cands {
			return fp
		}
	}
	return ""
}
