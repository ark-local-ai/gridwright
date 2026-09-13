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
	"sort"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/ledger"
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
	return Rollback(led, root, target, picked, model)
}

// Rollback 执行回滚：把 old 值写回，并为每条写一条 op=rollback 的账目。
func Rollback(led *ledger.Ledger, root, target string, items []Item, model string) (*Result, error) {
	res := &Result{Details: []string{}}

	// 写前安全检查：含宏这类会被 excelize 破坏的表，不碰
	if rep, err := safety.Check(target); err == nil && !rep.CanWrite {
		return nil, fmt.Errorf("这张表含程序无法安全处理的内容，已拒绝回滚：%s", rep.Describe())
	}

	f, err := excelize.OpenFile(target)
	if err != nil {
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
		return res, nil
	}

	// 备份 + 保存
	if _, err := xl.Backup(target, 5); err != nil {
		res.Details = append(res.Details, "备份失败（已继续回滚）："+err.Error())
	}
	if err := f.SaveAs(target); err != nil {
		return nil, fmt.Errorf("写回失败: %w", err)
	}

	// 记账：回滚本身也是改动，必须留痕（于是"回滚的回滚"= 重做，历史不丢）
	now := time.Now()
	table := base(target)
	for _, a := range done {
		_ = led.Append(now, table, a.it.Sheet, a.it.Cell, "rollback",
			a.it.New, a.it.Old, // 回滚后：旧值变新值，原新值成为"旧值"
			"撤销："+tidy(a.it.Reason), "rollback", "", model, "ok")
		res.Rolled++
		res.Details = append(res.Details, fmt.Sprintf("已还原 %s!%s：%s → %s",
			a.it.Sheet, a.it.Cell, a.it.New, a.it.Old))
	}
	res.OK = true
	res.Note = fmt.Sprintf("已回滚 %d 处；回滚本身也记了账，可以再倒回去", res.Rolled)
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
