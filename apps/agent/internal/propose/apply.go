package propose

import (
	"fmt"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/ledger"
	"github.com/ark-local-ai/ark/apps/agent/internal/safety"
	"github.com/ark-local-ai/ark/apps/agent/internal/xl"
)

// ApplyResult 是一条改动的执行结果。
type ApplyResult struct {
	Ref    string `json:"ref"`
	Sheet  string `json:"sheet"`
	Field  string `json:"field"`
	Old    any    `json:"old"`
	New    any    `json:"new"`
	Status string `json:"status"` // ok | rejected
	Note   string `json:"note,omitempty"`
}

// Apply 执行一份已确认的清单：备份 → 逐条写入 → 记账。
//
// 关键约束：
//   - 只写清单里的格（用户确认过的），不扩散。
//   - 写前校验文件指纹：确认期间文件被改过 → 整份拒绝，提示重新体检/重新计划。
//   - 每条都记账（含旧值），失败条目记 rejected 而不是静默跳过。
//   - 先全部算好、再一次性保存：避免写一半失败留下半成品。
func Apply(p *Proposal, led *ledger.Ledger, model string) ([]ApplyResult, error) {
	if p == nil {
		return nil, fmt.Errorf("清单不存在或已过期")
	}
	// 防盲改：确认期间文件被换过就不动
	if Changed(p.Target, p.Finger) {
		return nil, fmt.Errorf("目标表在确认期间被改动过，为避免覆盖你的修改，已取消本次操作；请重新体检再改")
	}

	// 写入安全：含宏这类 excelize 会破坏的内容，**默认拒绝写**
	// （见 docs/agent-architecture/24-语义映射与安全边界.md）
	if rep, err := safety.Check(p.Target); err == nil {
		if !rep.CanWrite {
			return nil, fmt.Errorf("这张表含程序无法安全处理的内容，已拒绝写入：%s", rep.Describe())
		}
	}

	f, err := excelize.OpenFile(p.Target)
	if err != nil {
		return nil, fmt.Errorf("打开目标表: %w", err)
	}
	defer f.Close()

	results := make([]ApplyResult, 0, len(p.Items))
	applied := 0
	for _, it := range p.Items {
		r := ApplyResult{Ref: it.Ref, Sheet: it.Sheet, Field: it.Field, Old: it.Old, New: it.New}
		sheet := it.Sheet
		if sheet == "" {
			sheet = f.GetSheetName(0)
		}
		if idx, err := f.GetSheetIndex(sheet); err != nil || idx < 0 {
			r.Status = "rejected"
			r.Note = "工作表不存在：" + sheet
			results = append(results, r)
			continue
		}
		// 取"当前实际旧值"，与清单里的 Old 不一致说明文件变了该格
		cur, _ := f.GetCellValue(sheet, it.Ref)
		if it.Op == "set" && cur != fmt.Sprint(it.Old) {
			// 仅提示，不阻断：以实际为准记账（旧值记实际值，保证可回滚）
			r.Old = cur
		}
		if err := f.SetCellValue(sheet, it.Ref, it.New); err != nil {
			r.Status = "rejected"
			r.Note = "写入失败：" + err.Error()
			results = append(results, r)
			continue
		}
		r.Status = "ok"
		applied++
		results = append(results, r)
	}

	// 一条都没成功：不备份、不保存（避免留下无意义备份）
	if applied == 0 {
		recordAll(led, p, results, model)
		return results, nil
	}

	if _, err := xl.Backup(p.Target, 5); err != nil {
		// 备份失败照常写（但要记账说明）
		results = append(results, ApplyResult{Status: "rejected", Note: "备份失败（已继续写回）：" + err.Error()})
	}
	if err := f.SaveAs(p.Target); err != nil {
		return results, fmt.Errorf("写回失败: %w", err)
	}
	recordAll(led, p, results, model)
	return results, nil
}

// recordAll 逐条记账（每格一条，含旧值 → 可回滚）。
func recordAll(led *ledger.Ledger, p *Proposal, results []ApplyResult, model string) {
	if led == nil {
		return
	}
	now := time.Now()
	table := p.Target
	if i := lastSlash(table); i >= 0 {
		table = table[i+1:]
	}
	for _, r := range results {
		status := r.Status
		if status == "" {
			status = "rejected"
		}
		_ = led.Append(now, table, r.Sheet, r.Ref, "set",
			fmt.Sprint(r.Old), fmt.Sprint(r.New),
			r.Note, "confirmed-plan", "", model, status)
	}
}

func lastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' || s[i] == '\\' {
			return i
		}
	}
	return -1
}
