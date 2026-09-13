// Package xl 用 excelize 读/改 xlsx（spec §4）：
// 读表结构（表头+样例）、执行 edits、备份原文件（保留最近 N 份）。
package xl

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/plan"
)

// Structure 是发给脑的表结构（表头 + 前 N 行样例，spec §4 组装 prompt ①）。
type Structure struct {
	Sheet  string   `json:"sheet"`
	Header []string `json:"header"`
	Sample [][]any  `json:"sample"`
}

// ReadStructure 读一个工作表的表头（第 1 行）与前 n 行样例。
func ReadStructure(f *excelize.File, sheet string, n int) (*Structure, error) {
	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, fmt.Errorf("读取工作表 %s: %w", sheet, err)
	}
	s := &Structure{Sheet: sheet}
	if len(rows) == 0 {
		return s, nil
	}
	s.Header = rows[0]
	if n > 0 {
		for i := 1; i < len(rows) && len(s.Sample) < n; i++ {
			s.Sample = append(s.Sample, toAny(rows[i]))
		}
	}
	return s, nil
}

func toAny(in []string) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

// Describe 把结构格式化成发给脑的多行文本。
func (s *Structure) Describe(sampleRows int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "工作表「%s」列头: %s\n", s.Sheet, strings.Join(s.Header, " | "))
	limit := sampleRows
	if limit <= 0 || limit > len(s.Sample) {
		limit = len(s.Sample)
	}
	for i := 0; i < limit; i++ {
		b.WriteString("  " + strings.Join(vals(s.Sample[i]), " | ") + "\n")
	}
	return b.String()
}

func vals(a []any) []string {
	out := make([]string, len(a))
	for i, v := range a {
		out[i] = fmt.Sprintf("%v", v)
	}
	return out
}

// ApplyEdits 在内存工作簿上执行 edits，逐条返回结果。
// 不写盘、不记账——只改 *excelize.File，由 agent 决定备份/保存/记账。
func ApplyEdits(f *excelize.File, edits []plan.Edit) []plan.Result {
	results := make([]plan.Result, 0, len(edits))
	for _, e := range edits {
		switch strings.ToLower(e.Op) {
		case "set":
			results = append(results, applySet(f, e))
		case "append":
			results = append(results, applyAppend(f, e))
		default:
			results = append(results, plan.Result{Status: "rejected", Note: "未知操作类型: " + e.Op})
		}
	}
	return results
}

func sheetOK(f *excelize.File, name string) (string, bool) {
	if name == "" {
		return f.GetSheetName(0), true
	}
	idx, err := f.GetSheetIndex(name)
	return name, err == nil && idx >= 0
}

func applySet(f *excelize.File, e plan.Edit) plan.Result {
	if e.Cell == "" {
		return plan.Result{Status: "rejected", Note: "set 缺少 cell 坐标"}
	}
	sheet, ok := sheetOK(f, e.Sheet)
	if !ok {
		return plan.Result{Status: "rejected", Note: fmt.Sprintf("工作表 %s 不存在", e.Sheet)}
	}
	old, _ := f.GetCellValue(sheet, e.Cell)
	if err := f.SetCellValue(sheet, e.Cell, e.Value); err != nil {
		return plan.Result{Status: "rejected", Note: "写入失败: " + err.Error()}
	}
	return plan.Result{Status: "ok", Old: old}
}

// applyAppend 按表头列名把 row 写入下一空行；任何列不在表头中 → 整条拒绝（不猜列）。
func applyAppend(f *excelize.File, e plan.Edit) plan.Result {
	sheet, ok := sheetOK(f, e.Sheet)
	if !ok {
		return plan.Result{Status: "rejected", Note: fmt.Sprintf("工作表 %s 不存在", e.Sheet)}
	}
	rows, err := f.GetRows(sheet)
	if err != nil {
		return plan.Result{Status: "rejected", Note: "读取失败: " + err.Error()}
	}
	header := []string{}
	if len(rows) > 0 {
		header = rows[0]
	}
	// 先校验所有列都在表头里，再写（避免写一半）
	colIdx := make(map[string]int, len(e.Row))
	for name := range e.Row {
		idx := -1
		for i, h := range header {
			if strings.TrimSpace(h) == strings.TrimSpace(name) {
				idx = i
				break
			}
		}
		if idx < 0 {
			return plan.Result{Status: "rejected", Note: fmt.Sprintf("列「%s」不在表头中", name)}
		}
		colIdx[name] = idx
	}
	newRow := len(rows) + 1
	for name, v := range e.Row {
		cellName, _ := excelize.CoordinatesToCellName(colIdx[name]+1, newRow)
		if err := f.SetCellValue(sheet, cellName, coerce(v)); err != nil {
			return plan.Result{Status: "rejected", Note: "写入 " + name + " 失败: " + err.Error()}
		}
	}
	return plan.Result{Status: "ok"}
}

// Coerce 把纯数字字符串转成 number，其余原样（excelize 对数字/文本更友好）。
// 导出以便回滚同样按此规则写回（保持与写入时一致的数值/文本语义）。
func Coerce(v any) any { return coerce(v) }

// coerce 把纯数字字符串转成 number，其余原样（excelize 对数字/文本更友好）。
func coerce(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	t := strings.TrimSpace(s)
	if t == "" {
		return s
	}
	if n, err := strconv.ParseFloat(t, 64); err == nil && strconv.FormatFloat(n, 'f', -1, 64) == t {
		return n
	}
	return s
}

// Backup 复制 path 到同目录 <name>.bak-<unixnano>，并只保留最近 keep 份。
func Backup(path string, keep int) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	dir, base := filepath.Dir(abs), filepath.Base(abs)
	prefix := base + ".bak-"
	entries, _ := os.ReadDir(dir)
	var baks []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			baks = append(baks, e.Name())
		}
	}
	sort.Strings(baks) // 时间戳升序，最旧在前
	for len(baks) >= keep {
		os.Remove(filepath.Join(dir, baks[0]))
		baks = baks[1:]
	}
	dst := filepath.Join(dir, prefix+strconv.FormatInt(time.Now().UnixNano(), 10))
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return "", err
	}
	return dst, nil
}
