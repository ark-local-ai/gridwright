// Package agent 是"手"的编排核心（spec §4）：
// 一次运行 = 取新数据 → 组 prompt → 问脑 → 校验执行 edits → 备份写回 → 记账 → 移 done → 通知。
package agent

import (
	"context"
	"fmt"
	"log"
	"os"
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

// displayName 返回用户认识的文件名（进账目、进通知都用它）。
// 认领只换目录、不改名，所以这里就是基名——保留这个函数是为了让
// "账目里该显示什么名字"这件事有单一出处。
func displayName(p string) string { return filepath.Base(p) }

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
//
// **并发安全**：fsnotify 会为同一个文件连发 CREATE 和 WRITE 两个事件，
// 各触发一次运行；两次运行的间隔可能只有毫秒（规则短路时尤其如此），
// 于是同一份数据会被处理两遍。对"累加"类规则，重复执行=**重复入账**，
// 这是真金白银的错，必须在入口挡住。
//
// 挡法是"认领"：处理前把文件**原子改名**成带 .processing 后缀的名字。
// 改名成功=认领到；改名失败（文件已不在）=别人先认领了，跳过。
// 用改名而不是加锁，是因为它跨进程也成立（用户手动开两遍程序也一样安全）。
func (a *Agent) RunInboxFiles(ctx context.Context) error {
	files, err := a.Layout.InboxFiles()
	if err != nil {
		return err
	}
	for _, f := range files {
		claimed, err := claim(f)
		if err != nil {
			log.Printf("认领 %s 失败: %v", filepath.Base(f), err)
			continue
		}
		if claimed == "" {
			continue // 已经被另一次运行认领
		}
		if err := a.RunInboxFile(ctx, claimed); err != nil {
			log.Printf("处理 %s 失败: %v", displayName(claimed), err)
			// 失败：**释放锁**，下轮重试（不静默丢数据）
			if rerr := release(claimed); rerr != nil {
				log.Printf("释放认领锁 %s 失败: %v", displayName(claimed), rerr)
			}
			continue
		}
		// 成功：文件已移入 done，锁也该撤掉（否则会留下孤儿锁文件）
		if rerr := release(claimed); rerr != nil {
			log.Printf("释放认领锁 %s 失败: %v", displayName(claimed), rerr)
		}
	}
	// 顺带清理上次运行崩溃留下的认领残留（超过 10 分钟没动过的）
	a.reapStaleClaims()
	return nil
}

// claimSuffix 认领锁的后缀。
//
// 为什么不靠"改名认领"：**Windows 上 os.Rename 在目标已存在时会直接覆盖**
// （Go 用 MoveFileEx + REPLACE_EXISTING），于是并发改名会**全部成功**，
// 起不到互斥作用（这个坑真踩过：8 个并发全"认领成功"，账目写了两遍）。
// 用 O_EXCL 建锁文件才是跨平台原子的。
//
// 也不动数据文件本身：改名会把扩展名弄丢，后面按扩展名分流的读文件逻辑就瞎了。
const claimSuffix = ".claimlock"

// claim 尝试认领一个 inbox 文件：**原子地**创建独占锁文件。
// 拿到返回 "" 以外的标识（就是原路径）；已被别人认领返回 ""。
func claim(path string) (string, error) {
	lock := path + claimSuffix
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return "", nil // 已被另一次运行认领：正常情况
		}
		return "", err
	}
	f.Close()
	return path, nil
}

// release 释放认领（处理失败或需要重试时）。
func release(claimed string) error {
	err := os.Remove(claimed + claimSuffix)
	if err != nil && os.IsNotExist(err) {
		return nil
	}
	return err
}

// reapStaleClaims 清理超时的认领锁（上次运行在处理中途崩了，文件会一直卡住）。
// 只删锁、**不碰数据文件**——数据永远不丢，下轮会被重新认领处理。
func (a *Agent) reapStaleClaims() {
	entries, err := os.ReadDir(a.Layout.Inbox)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), claimSuffix) {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		if time.Since(info.ModTime()) < 10*time.Minute {
			continue // 可能正在处理中，别动
		}
		p := filepath.Join(a.Layout.Inbox, e.Name())
		if rerr := os.Remove(p); rerr != nil && !os.IsNotExist(rerr) {
			log.Printf("清理认领锁 %s 失败: %v", e.Name(), rerr)
			continue
		}
		log.Printf("释放超时认领锁 %s（数据将在下轮重新处理）",
			strings.TrimSuffix(e.Name(), claimSuffix))
	}
}

// RunInboxFile 处理单个 inbox 文件。
//
// 优先走**规则短路**：rules.yaml 里有 when/then 齐全的规则命中这批数据时，
// 直接按规则产出改动，不问模型（省 token、可复现、断网也能办事）。
// 没有规则命中才走"问脑"那条路。
func (a *Agent) RunInboxFile(ctx context.Context, inboxFile string) error {
	source := displayName(inboxFile)
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

	// 2) 试着用规则直接产出计划（短路）。命中就不必问脑了。
	rulePlan, ruleNote := a.runRulesOn(inboxFile, rules)
	if rulePlan != nil && len(rulePlan.Edits) > 0 {
		log.Printf("规则短路：%s", ruleNote)
	}

	// 3) 目标表（spec §5.4）：
	//    工作区只有 1 张 xlsx → 直接用；规则声明了 target_file → 用它；
	//    多张且没声明 → 先把所有表头发给脑分诊"数据该进哪张"。
	targets, err := a.Layout.DataFiles()
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return fmt.Errorf("工作区没有 .xlsx 表（%s）", a.Layout.Root)
	}
	target := targets[0]
	// 规则声明过目标表就直接用——省掉一次模型调用，也避免分诊猜错表。
	if t := declaredTarget(rules); t != "" {
		target = matchTable(targets, t)
		log.Printf("规则声明目标表：%s", filepath.Base(target))
	} else if len(targets) > 1 {
		triage, terr := a.triage(ctx, targets, source)
		if terr == nil && triage != "" {
			target = matchTable(targets, triage)
		} else {
			log.Printf("分诊表失败(%v)，默认用第一张 %s", terr, filepath.Base(targets[0]))
		}
	}

	// 4) 锁检测：目标表被 Excel 占用则跳过本次（spec §4）
	if workspace.Locked(target) {
		log.Printf("目标表 %s 被 Excel 占用（存在锁文件），跳过本次", filepath.Base(target))
		return nil
	}

	// 5) 读结构 + 组 prompt
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

	// 6) 出计划：规则命中就用规则的计划；否则问脑。
	var p *plan.Plan
	if rulePlan != nil && len(rulePlan.Edits) > 0 {
		p = rulePlan
	} else {
		// 没配模型又没规则命中：明确跳过（数据留在 inbox，配好模型后会重试），
		// 不要发一个必然 401 的请求，也不要静默把数据当"处理过了"。
		if a.Brain == nil || !a.Brain.Ready() {
			f.Close()
			return fmt.Errorf("没有规则命中，且未配置模型：%s 留在 inbox 待处理", source)
		}
		prompt := assemblePrompt(a.Cfg, structures, source, data, recentLedger, rules, state)
		log.Printf("调用脑（%s）…", a.Brain.Model())
		p, err = a.Brain.Plan(ctx, prompt)
		if err != nil {
			f.Close()
			return fmt.Errorf("问脑: %w", err)
		}
	}

	// 7) 校验 forbid 护栏：动到禁止列 → 该条 rejected（**规则和模型一视同仁**）
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
	// 记账里的"谁干的"：规则短路写 "rule"，否则写模型名。
	// **不能笼统写模型名**——那次改动根本没调模型，账目写"mock/deepseek"会误导审计。
	byWhom := a.Brain.Model()
	if rulePlan != nil && len(rulePlan.Edits) > 0 {
		byWhom = "rule"
	}
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
		a.Ledger.Append(now, table, e.Sheet, e.Cell, e.Op, old, cellNew(e), e.Reason, source, ruleName(rules, e.Reason), byWhom, st)
	}
	// 护栏挡下的单独补记账（它们不在 p.Edits 里）
	for _, b := range blocked {
		a.Ledger.Append(now, table, b.Sheet, b.Cell, b.Op, "", "", b.Reason, source, "forbid护栏", byWhom, "rejected")
		detail = append(detail, fmt.Sprintf("%s %s: forbid 护栏", b.Op, b.Cell))
	}

	// 规则层面的跳过（缺列/空值/定不到行）单独记账：**必须让人看见**。
	// 规则说"处理了 5 行"却漏了 3 行，如果只在日志里，用户永远不知道——
	// 对记账类工作，漏记比记错更难发现，所以进账目（rejected）并进通知。
	for _, s := range p.SkipReasons {
		a.Ledger.Append(now, table, "", "", "skip", "", "", s, source, ruleName(rules, s), byWhom, "rejected")
		detail = append(detail, s)
	}

	// 10) 移 inbox → done
	if err := a.Layout.MoveToDone(inboxFile); err != nil {
		log.Printf("移入 done 失败: %v", err)
	}

	// 11) 通知
	notify.Send(a.Cfg.Notify, notify.Message{
		Table: table, Summary: p.Summary,
		Applied:        applied,
		Rejected:       len(blocked) + countRejected(results) + len(p.SkipReasons),
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
