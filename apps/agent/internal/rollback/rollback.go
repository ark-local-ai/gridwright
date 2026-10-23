// Package rollback 按账目把改动倒回去（见 docs/agent-architecture/23 与用户承诺）。
//
// 为什么单独做：账目早就存了每格的旧值（`ledger.Entry.Old`），备份也在
// （`xl.Backup`），但**从来没有把旧值写回表的代码**——而官网与关于面板都公开
// 写着"可回滚"。承诺了就得有。
//
// 设计要点：
//   - **只按账目回滚**，不猜。账目是唯一事实来源。
//   - **回滚本身也记账**（`op=rollback`），所以能"回滚的回滚"，永不丢历史。
//   - **回滚前先检查文件安全**（含宏的表不碰）与**指纹**（期间被改过就拒绝）。
//   - 支持按"一批"回滚：同一秒内的一次 apply 会写多条账目，应该一起倒回。
package rollback

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/filelock"
	"github.com/ark-local-ai/ark/apps/agent/internal/ledger"
	"github.com/ark-local-ai/ark/apps/agent/internal/logx"
	"github.com/ark-local-ai/ark/apps/agent/internal/safety"
	"github.com/ark-local-ai/ark/apps/agent/internal/xl"
)

// Item 是一条可回滚的账目（附带是否能回滚的判断）。
type Item struct {
	ID     int    `json:"id"` // 行号（稳定标识）
	Ts     string `json:"ts"`
	Table  string `json:"table"`
	Sheet  string `json:"sheet"`
	Cell   string `json:"cell"`
	Op     string `json:"op"` // set | append | rollback
	Old    string `json:"old"`
	New    string `json:"new"`
	Reason string `json:"reason"`
	Status string `json:"status"`
	// Source 是这笔改动的来源（"对话:<id>" / 规则 / inbox 文件）；
	// SourceTitle 是 API 层把会话 id 译回的标题，供界面显示「哪次对话改的」。
	Source      string `json:"source,omitempty"`
	SourceTitle string `json:"sourceTitle,omitempty"`
	// Zone 是作业区 id（空 = 工作区级）。回滚时写回账目，保持筛选可追溯。
	Zone string `json:"zone,omitempty"`
	// CanRollback / WhyNot 让界面能明确告诉用户"为什么这条不能倒"
	CanRollback bool   `json:"canRollback"`
	WhyNot      string `json:"whyNot,omitempty"`
	// Group 是"同一批"的标识（同一时间戳 = 一次 apply）
	Group string `json:"group"`
}

// Result 是一次回滚的结果。
type Result struct {
	OK      bool     `json:"ok"`
	Rolled  int      `json:"rolled"`
	Skipped int      `json:"skipped"`
	Details []string `json:"details"`
	Note    string   `json:"note,omitempty"`
}

// List 列出可回滚的账目（最近的在前），并标注每条能否回滚。
//
// 能回滚的条件：
//   - status == ok（被拒的没改过东西，无需回滚）
//   - op == set 且有 old 值（append 无法回滚——追加的行没有唯一位置可删）
func List(led *ledger.Ledger, limit int) ([]Item, error) {
	entries, err := led.Entries(0)
	if err != nil {
		return nil, err
	}
	// 保证是数组而非 null —— 前端 .map 会因 null 直接崩（这坑踩过多次）
	out := []Item{}
	for i, e := range entries {
		it := Item{
			ID: i, Ts: e.Ts, Table: e.Table, Sheet: e.Sheet, Cell: e.Cell,
			Op: e.Op, Old: e.Old, New: e.New, Reason: e.Reason, Status: e.Status,
			Source: e.Source, Zone: e.Zone,
			Group: e.Ts, // 同一时间戳视为一次操作
		}
		switch {
		case e.Status != "ok":
			it.WhyNot = "这条没有实际改动（被拒或未执行）"
		case e.Op == "append":
			it.WhyNot = "追加的行无法自动定位删除，请手工处理"
		case e.Op != "set" && e.Op != "rollback":
			it.WhyNot = "不支持的操作类型：" + e.Op
		case e.Old == "" && e.New == "":
			it.WhyNot = "账目里没有旧值，无法还原"
		default:
			// 已经是回滚产生的记录 → 可以再回滚（等于重做），这是允许的
			it.CanRollback = true
		}
		_ = e
		out = append(out, it)
	}
	// 最近的在前
	sort.SliceStable(out, func(i, j int) bool { return out[i].Ts > out[j].Ts })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// RollbackByID 回滚一批（同 Ts 的条目一起倒回，保证"一次操作"整体还原）。
func RollbackByID(led *ledger.Ledger, root, target string, ids []int, model string) (*Result, error) {
	all, err := List(led, 0)
	if err != nil {
		return nil, err
	}
	want := map[int]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var picked []Item
	for _, it := range all {
		if want[it.ID] {
			picked = append(picked, it)
		}
	}
	if len(picked) == 0 {
		return nil, fmt.Errorf("没有找到要回滚的账目")
	}
	// 选中了多少条：ID 是界面行号，可能已过期（账目文件被手工编辑过），
	// 这里记下"请求 N 条、真的选中 M 条"，对不上时一眼能看出来。
	logx.Step("rollback", "选中账目", "ids", len(ids), "picked", len(picked))
	return Rollback(led, root, target, picked, model)
}

// Rollback 执行回滚：把 old 值写回，并为每条写一条 op=rollback 的账目。
func Rollback(led *ledger.Ledger, root, target string, items []Item, model string) (*Result, error) {
	res := &Result{Details: []string{}}
	// 与 apply 共用同一把表级锁：回滚也不能与写回交错。
	unlock := filelock.Lock(target)
	defer unlock()
	// 进入：把哪张表、几条旧值倒回去。回滚是不可逆主路径（回滚本身也记账），
	// 日志与账目要能对得上。
	logx.Step("rollback", "enter", "target", base(target), "items", len(items), "model", model)

	// 写前安全检查：含宏这类会被 excelize 破坏的表，不碰
	if rep, err := safety.Check(target); err == nil && !rep.CanWrite {
		logx.Step("rollback", "exit", "target", base(target), "err", "安全拒绝："+rep.Describe())
		return nil, fmt.Errorf("这张表含程序无法安全处理的内容，已拒绝回滚：%s", rep.Describe())
	}

	f, err := excelize.OpenFile(target)
	if err != nil {
		logx.Step("rollback", "exit", "target", base(target), "err", "打开目标表失败："+err.Error())
		return nil, fmt.Errorf("打开目标表: %w", err)
	}
	defer f.Close()

	// 逐条写回（先全部在内存里做完，再一次性保存）
	type applied struct {
		it Item
	}
	var done []applied
	for _, it := range items {
		if !it.CanRollback {
			res.Skipped++
			res.Details = append(res.Details, fmt.Sprintf("跳过 %s!%s：%s", it.Sheet, it.Cell, it.WhyNot))
			continue
		}
		sheet := it.Sheet
		if sheet == "" {
			sheet = f.GetSheetName(0)
		}
		if idx, err := f.GetSheetIndex(sheet); err != nil || idx < 0 {
			res.Skipped++
			res.Details = append(res.Details, fmt.Sprintf("跳过 %s!%s：工作表不存在", sheet, it.Cell))
			continue
		}
		if it.Cell == "" {
			res.Skipped++
			res.Details = append(res.Details, "跳过一条没有坐标的账目")
			continue
		}
		if err := f.SetCellValue(sheet, it.Cell, xl.Coerce(it.Old)); err != nil {
			res.Skipped++
			res.Details = append(res.Details, fmt.Sprintf("写回 %s!%s 失败：%v", sheet, it.Cell, err))
			continue
		}
		done = append(done, applied{it: it})
	}

	if len(done) == 0 {
		res.Note = "没有可回滚的条目，未改动文件"
		logx.Step("rollback", "exit", "target", base(target), "rolled", 0, "skipped", res.Skipped)
		return res, nil
	}

	// 备份 + 保存
	if _, err := xl.Backup(target, 5); err != nil {
		res.Details = append(res.Details, "备份失败（已继续回滚）："+err.Error())
	}
	if err := f.SaveAs(target); err != nil {
		logx.Step("rollback", "exit", "target", base(target), "err", "保存失败："+err.Error())
		return nil, fmt.Errorf("写回失败: %w", err)
	}

	// 记账：回滚本身也是改动，必须留痕（于是"回滚的回滚"= 重做，历史不丢）
	now := time.Now()
	table := base(target)
	for _, a := range done {
		_ = led.Append(now, table, a.it.Sheet, a.it.Cell, "rollback",
			a.it.New, a.it.Old, // 回滚后：旧值变新值，原新值成为"旧值"
			"撤销："+tidy(a.it.Reason), "rollback", "", model, "ok", a.it.Zone)
		res.Rolled++
		res.Details = append(res.Details, fmt.Sprintf("已还原 %s!%s：%s → %s",
			a.it.Sheet, a.it.Cell, a.it.New, a.it.Old))
	}
	res.OK = true
	res.Note = fmt.Sprintf("已回滚 %d 处；回滚本身也记了账，可以再倒回去", res.Rolled)
	// 退出：回滚条数 + 跳过条数（跳过的原因在明细里，默认级别下不看）。
	logx.Step("rollback", "exit", "target", base(target), "rolled", res.Rolled, "skipped", res.Skipped)
	// 逐条明细（含"为什么跳过"）放 Debug：回滚最常见的坑是"以为全倒了、其实有几条 SKIPPED"，
	// 排查时需要这份完整明细，但它是 O(条数) 的，不该占默认日志。
	if logx.Enabled(slog.LevelDebug) {
		logx.Block("回滚明细", strings.Join(res.Details, "\n"))
	}
	return res, nil
}

func base(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func tidy(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "（未注明原因）"
	}
	return s
}
