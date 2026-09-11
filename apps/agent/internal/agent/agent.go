// Package agent 是"手"的编排核心（spec §4）：
// 一次运行 = 取新数据 → 组 prompt → 问脑 → 校验执行 edits → 备份写回 → 记账 → 移 done → 通知。
package agent

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/config"
	"github.com/ark-local-ai/ark/apps/agent/internal/ledger"
	"github.com/ark-local-ai/ark/apps/agent/internal/llm"
	"github.com/ark-local-ai/ark/apps/agent/internal/memory"
	"github.com/ark-local-ai/ark/apps/agent/internal/notify"
	"github.com/ark-local-ai/ark/apps/agent/internal/plan"
	"github.com/ark-local-ai/ark/apps/agent/internal/workspace"
	"github.com/ark-local-ai/ark/apps/agent/internal/xl"
)

// Agent 持有一次运行所需的全部依赖。
type Agent struct {
	Cfg     *config.Config
	Layout  *workspace.Layout
	Ledger  *ledger.Ledger
	Brain   *llm.Client
	SampleN int
}

// New 构造 agent。
func New(cfg *config.Config, layout *workspace.Layout, led *ledger.Ledger, brain *llm.Client) *Agent {
	return &Agent{Cfg: cfg, Layout: layout, Ledger: led, Brain: brain, SampleN: cfg.SampleRows}
}

// RunInboxFiles 处理 inbox 里的每个新文件（一次运行）。
func (a *Agent) RunInboxFiles(ctx context.Context) error {
	files, err := a.Layout.InboxFiles()
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}
	for _, f := range files {
		if err := a.RunInboxFile(ctx, f); err != nil {
			log.Printf("处理 %s 失败: %v", filepath.Base(f), err)
			// 失败的文件留在 inbox，下次重试（不静默丢）
			continue
		}
	}
	return nil
}

// RunInboxFile 处理单个 inbox 文件。
func (a *Agent) RunInboxFile(ctx context.Context, inboxFile string) error {
	source := filepath.Base(inboxFile)
	data, err := readInbox(inboxFile)
	if err != nil {
		return fmt.Errorf("读取新数据: %w", err)
	}

	// 1) 记忆文件
	rules, state, _, _, err := memory.Load(a.Layout.Rules, a.Layout.State)
	if err != nil {
		return err
	}
	forbid := rules.ForbidSet()

	// 2) 目标表（spec §5.4）：
	//    工作区只有 1 张 xlsx → 直接用；多张 → 先把所有表头发给脑分诊"数据该进哪张"。
	targets, err := a.Layout.DataFiles()
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return fmt.Errorf("工作区没有 .xlsx 表（%s）", a.Layout.Root)
	}
	target := targets[0]
	if len(targets) > 1 {
		triage, terr := a.triage(ctx, targets, source)
		if terr == nil && triage != "" {
			target = matchTable(targets, triage)
		} else {
			log.Printf("分诊表失败(%v)，默认用第一张 %s", terr, filepath.Base(targets[0]))
		}
	}

	// 3) 锁检测：目标表被 Excel 占用则跳过本次（spec §4）
	if workspace.Locked(target) {
		log.Printf("目标表 %s 被 Excel 占用（存在锁文件），跳过本次", filepath.Base(target))
		return nil
	}

	// 4) 读结构 + 组 prompt
	f, err := excelize.OpenFile(target)
	if err != nil {
		return fmt.Errorf("打开目标表: %w", err)
	}
	structures, err := a.collectStructures(f)
	if err != nil {
		f.Close()
		return err
	}
	recentLedger, _ := a.Ledger.Recent(20)

	prompt := assemblePrompt(a.Cfg, structures, source, data, recentLedger, rules, state)

	// 5) 问脑
	log.Printf("调用脑（%s）…", a.Brain.Model())
	p, err := a.Brain.Plan(ctx, prompt)
	if err != nil {
		f.Close()
		return fmt.Errorf("问脑: %w", err)
	}

	// 6) 校验 forbid 护栏：LLM 动了禁止列 → 该条 rejected
	headers := make(map[string][]string, len(structures))
	for _, s := range structures {
		headers[s.Sheet] = s.Header
	}
	guarded, blocked := guardEdits(p, forbid, headers)
	p.Edits = guarded

	// 7) 执行 edits（改内存工作簿）
	results := xl.ApplyEdits(f, p.Edits)

	// 8) 备份 + 写回（只有真正执行了 ok 的才保存）
	applied := 0
	for i, r := range results {
		if r.Status == "ok" {
			applied++
		}
		_ = i
	}
	// blocked 的也算需要记账（rejected），但没改工作簿 → 不需要保存
	if applied > 0 {
		if _, err := xl.Backup(target, 5); err != nil {
			log.Printf("备份失败（继续写回）: %v", err)
		}
		if err := f.SaveAs(target); err != nil {
			f.Close()
			return fmt.Errorf("写回失败: %w", err)
		}
	}
	f.Close()

	// 9) 记账（每格一条）+ 通知
	now := time.Now()
	table := filepath.Base(target)
	detail := []string{}
	for i, e := range p.Edits {
		st := "rejected"
		old, new, note := "", "", ""
		if i < len(results) {
			if results[i].Status == "ok" {
				st = "ok"
			}
			old = fmt.Sprintf("%v", results[i].Old)
			note = results[i].Note
		}
		if i >= len(results) { // 被护栏挡下的（没进 results）
			st = "rejected"
			note = "命中 forbid 护栏"
		}
		if st == "rejected" {
			detail = append(detail, fmt.Sprintf("%s %s: %s", e.Op, e.Cell, note))
		}
		_ = new
		a.Ledger.Append(now, table, e.Cell, e.Op, old, cellNew(e), e.Reason, source, ruleName(rules, e.Reason), a.Brain.Model(), st)
	}
	// 护栏挡下的单独补记账（它们不在 p.Edits 里）
	for _, b := range blocked {
		a.Ledger.Append(now, table, b.Cell, b.Op, "", "", b.Reason, source, "forbid护栏", a.Brain.Model(), "rejected")
		detail = append(detail, fmt.Sprintf("%s %s: forbid 护栏", b.Op, b.Cell))
	}

	// 10) 移 inbox → done
	if err := a.Layout.MoveToDone(inboxFile); err != nil {
		log.Printf("移入 done 失败: %v", err)
	}

	// 11) 通知
	notify.Send(a.Cfg.Notify, notify.Message{
		Table: table, Summary: p.Summary,
		Applied: applied, Rejected: len(blocked) + countRejected(results),
		RejectedDetail: detail,
	})
	return nil
}

// triage 把"新数据该进哪张表"交给脑（多表时，spec §5.4 第 2 步）。
func (a *Agent) triage(ctx context.Context, targets []string, source string) (string, error) {
	var b strings.Builder
	b.WriteString("下面是工作区里所有表的结构，新数据来自 " + source + "。\n")
	for _, t := range targets {
		f, err := excelize.OpenFile(t)
		if err != nil {
			continue
		}
		s, err := xl.ReadStructure(f, f.GetSheetName(0), 0)
		f.Close()
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "表「%s」列头: %s\n", filepath.Base(t), strings.Join(s.Header, " | "))
	}
	b.WriteString("问：这批新数据应该更新到哪张表？只回答表名（文件名，不含扩展名），不要解释。\n")
	p, err := a.Brain.Plan(ctx, b.String())
	_ = p
	if err != nil {
		return "", err
	}
	// 复用 summary 承载表名（triage 调用约定：summary=表名）
	return strings.TrimSpace(p.Summary), nil
}

func matchTable(targets []string, name string) string {
	name = strings.TrimSuffix(name, ".xlsx")
	name = strings.ToLower(strings.TrimSpace(name))
	for _, t := range targets {
		base := strings.ToLower(strings.TrimSuffix(filepath.Base(t), ".xlsx"))
		if base == name || strings.Contains(base, name) || strings.Contains(name, base) {
			return t
		}
	}
	return targets[0]
}

// collectStructures 读工作簿里所有 sheet 的结构（M1 直接全带；表多时 M4 再做两阶段分诊）。
func (a *Agent) collectStructures(f *excelize.File) ([]*xl.Structure, error) {
	names := f.GetSheetList()
	out := make([]*xl.Structure, 0, len(names))
	for _, name := range names {
		s, err := xl.ReadStructure(f, name, a.SampleN)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func countRejected(results []plan.Result) int {
	n := 0
	for _, r := range results {
		if r.Status == "rejected" {
			n++
		}
	}
	return n
}
