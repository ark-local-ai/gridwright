package ledger

import (
	"encoding/csv"
	"io"
	"strings"
	"time"
)

// 账目筛选与导出（见 internal/api/ledger_export.go）。
//
// 为什么筛选放这里而不是 API：它是纯函数、没有 IO，最适合单测；
// API 只负责解析 query 和写响应。账目量在本产品的场景里是"一台机器一本台账"，
// 全量读入再过滤足够，不引入索引/数据库。

// Filter 是账目筛选条件。零值表示不过滤。
type Filter struct {
	Table  string
	Status string
	Source string
	// Op 精确匹配 set/append/rollback。
	Op   string
	Zone string
	// Since/Until 按账目里的本地时间戳比较（不是字符串比较——
	// 旧账目有 "15:04" 与 "15:04:05" 两种格式）。
	Since time.Time
	Until time.Time
}

// Match 判断一条账目是否满足条件。
func (f Filter) Match(e Entry) bool {
	if f.Table != "" && !strings.EqualFold(e.Table, f.Table) &&
		!strings.EqualFold(strings.TrimSuffix(e.Table, ".xlsx"), strings.TrimSuffix(f.Table, ".xlsx")) {
		return false
	}
	if f.Status != "" && !strings.EqualFold(e.Status, f.Status) {
		return false
	}
	if f.Source != "" && !strings.Contains(e.Source, f.Source) {
		return false
	}
	if f.Op != "" && !strings.EqualFold(e.Op, f.Op) {
		return false
	}
	if f.Zone != "" && !strings.EqualFold(e.Zone, f.Zone) {
		return false
	}
	if !f.Since.IsZero() || !f.Until.IsZero() {
		t, ok := parseTs(e.Ts)
		if !ok {
			return false
		}
		if !f.Since.IsZero() && t.Before(f.Since) {
			return false
		}
		if !f.Until.IsZero() && t.After(f.Until) {
			return false
		}
	}
	return true
}

// FilterEntries 返回满足条件的条目（保持原顺序）。
func FilterEntries(entries []Entry, f Filter) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if f.Match(e) {
			out = append(out, e)
		}
	}
	return out
}

// WriteCSV 把账目写成 CSV（含 UTF-8 BOM，Excel/WPS 打开中文不乱码）。
// 列顺序与 ledger.csv 一致（含 zone），便于财务把导出文件与原件逐列对齐。
func WriteCSV(w io.Writer, entries []Entry) error {
	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(headerCols); err != nil {
		return err
	}
	for _, e := range entries {
		rec := []string{
			e.Ts, e.Table, e.Sheet, e.Cell, e.Op,
			e.Old, e.New, e.Reason, e.Source, e.Rule, e.Model, e.Status, e.Zone,
		}
		for i := range rec {
			rec[i] = sanitizeCell(rec[i])
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// sanitizeCell 防止 CSV 公式注入。
//
// 账目里的 reason/source/old/new 可能来自模型或文件名；财务用 Excel 打开导出文件时，
// 以 = + @ 开头的单元格会被当成公式执行（DDE/HYPERLINK 是真实攻击面）。
// 处理：
//   - = + @、制表符、回车开头 → 前置单引号
//   - - 开头只在**不是负数**时前置（负数必须保持数值，否则对账会失真）
//
// 单引号在 Excel 里是“按文本显示”的转义，不进入单元格内容。
func sanitizeCell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '@', '\t', '\r':
		return "'" + s
	case '-':
		if len(s) == 1 {
			return "'" + s
		}
		// -123 / -1.5 / -.5 都是数值，保持原样。
		c := s[1]
		if (c >= '0' && c <= '9') || c == '.' {
			return s
		}
		return "'" + s
	}
	return s
}
