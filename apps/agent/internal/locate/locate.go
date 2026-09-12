// Package locate 把"业务语义坐标"翻译成 Excel 单元格坐标（见 docs/agent-architecture/5-编辑语义.md）。
//
// 为什么需要它：真实台账里编辑单位不是 (sheet, 单元格)，
// 而是 (铺位, 租户, 月份, 字段)。让 LLM 直接吐坐标极易错行错列
// （汇总表的列还是日期序列号），所以由本包负责定位，LLM 只说业务语义。
//
// 处理真实表的"不干净"：
//   - 表头不在第 1 行（前面有标题行）→ FindHeader 在前若干行里挑最像表头的
//   - 一个铺位占多行 + 合并单元格（值只在首行）→ 取值时向下填充
//   - 汇总表列头混用"日期序列号"与"2026年9月"文字 → 两种都认
package locate

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

// 表头识别用的关键词：一行里命中越多，越可能是表头行。
var headerHints = []string{
	"序号", "物业位置", "铺位", "铺位号", "商铺位", "租户名称", "租户", "客户名称",
	"客户", "店铺名称", "店铺", "合同", "月份", "金额", "租金", "备注", "合计", "实收",
}

// Sheet 是某张工作表的内容视图。
type Sheet struct {
	Name string
	Rows [][]string
}

// LoadSheet 读出一张工作表。
func LoadSheet(f *excelize.File, name string) (*Sheet, error) {
	rows, err := f.GetRows(name)
	if err != nil {
		return nil, fmt.Errorf("读取工作表 %s: %w", name, err)
	}
	return &Sheet{Name: name, Rows: rows}, nil
}

// FindHeader 在前 probe 行里挑最像表头的一行，返回其行号（0 基）。
// 一行里命中的关键词越多越可能是表头；完全没命中则退回第 0 行。
func (s *Sheet) FindHeader(probe int) (int, []string) {
	limit := len(s.Rows)
	if probe > 0 && probe < limit {
		limit = probe
	}
	best, bestScore := 0, -1
	for i := 0; i < limit; i++ {
		score := 0
		for _, cell := range s.Rows[i] {
			n := Normalize(cell)
			if n == "" {
				continue
			}
			for _, h := range headerHints {
				if strings.Contains(n, Normalize(h)) {
					score++
					break
				}
			}
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	if best >= len(s.Rows) {
		return 0, nil
	}
	return best, s.Rows[best]
}

// ColByHeader 在表头里找列号（0 基），按归一化后的精确 → 包含 依次匹配。
// names 可给多个候选名（如"本月实收"、"实收"）。找不到返回 -1。
func ColByHeader(header []string, names ...string) int {
	for _, want := range names {
		w := Normalize(want)
		for i, h := range header {
			if Normalize(h) == w {
				return i
			}
		}
	}
	for _, want := range names {
		w := Normalize(want)
		if w == "" {
			continue
		}
		for i, h := range header {
			hn := Normalize(h)
			if hn != "" && (strings.Contains(hn, w) || strings.Contains(w, hn)) {
				return i
			}
		}
	}
	return -1
}

// RowByKeys 从 dataStart 行起找匹配的铺位/租户行，返回行号（0 基），找不到 -1。
//
// 处理合并单元格：某铺位占多行时值只在首行，故对每个键列做"向下填充"，
// 空单元格沿用上一次见到的非空值。要求给出的所有键都命中（AND）。
// 键的值用归一化比较（去空格、忽略大小写）。
func (s *Sheet) RowByKeys(dataStart int, keyCols map[int]string) int {
	if len(keyCols) == 0 || dataStart >= len(s.Rows) {
		return -1
	}
	last := map[int]string{} // 列 → 最近一次非空值（向下填充）
	for r := dataStart; r < len(s.Rows); r++ {
		row := s.Rows[r]
		for col := range keyCols {
			if col < len(row) && Normalize(row[col]) != "" {
				last[col] = Normalize(row[col])
			}
		}
		ok := true
		for col, want := range keyCols {
			got := last[col]
			if col < len(row) && Normalize(row[col]) != "" {
				got = Normalize(row[col])
			}
			if got != Normalize(want) {
				ok = false
				break
			}
		}
		if ok {
			return r
		}
	}
	return -1
}

// ColByMonth 在表头里找某年某月对应的列号（0 基），找不到 -1。
// 认两种写法：文字"2026年9月"、日期序列号（如 45292 = 2024-01-01）。
func ColByMonth(header []string, year, month int) int {
	for i, h := range header {
		t, ok := parseHeaderMonth(strings.TrimSpace(h))
		if ok && t.Year() == year && int(t.Month()) == month {
			return i
		}
	}
	return -1
}

// parseHeaderMonth 解析一个表头单元格为年月。
func parseHeaderMonth(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	// 文字：2026年9月 / 2026年09月
	if strings.Contains(s, "年") && strings.Contains(s, "月") {
		s2 := strings.ReplaceAll(s, "年", "-")
		s2 = strings.ReplaceAll(s2, "月", "")
		s2 = strings.TrimSuffix(s2, "-")
		parts := strings.SplitN(s2, "-", 2)
		if len(parts) == 2 {
			y, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
			m, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err1 == nil && err2 == nil && m >= 1 && m <= 12 {
				return time.Date(y, time.Month(m), 1, 0, 0, 0, 0, time.UTC), true
			}
		}
		return time.Time{}, false
	}
	// 日期序列号：纯数字且落在合理区间（1970-01-01 之后 ~ 2100 之前）
	if n, err := strconv.Atoi(s); err == nil && n > 25569 && n < 73415 {
		return fromExcelSerial(n), true
	}
	return time.Time{}, false
}

// fromExcelSerial 把 Excel 日期序列号转成时间（基准 1899-12-30）。
func fromExcelSerial(serial int) time.Time {
	base := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	return base.AddDate(0, 0, serial)
}

// Cell 是定位结果。
type Cell struct {
	Sheet string `json:"sheet"`
	Ref   string `json:"ref"` // A1 形式
	Row   int    `json:"row"` // 1 基
	Col   int    `json:"col"` // 1 基
	Note  string `json:"note,omitempty"`
}

// LocateRowField 定位"某铺位某行的某字段"（用于各月租金表这类：行=铺位，列=字段）。
// anchorNames 给铺位列的候选名；keys 是要匹配的 列名→值（如 物业位置=A03）。
func (s *Sheet) LocateRowField(anchorNames, fieldNames []string, keys map[string]string) (*Cell, error) {
	hdrIdx, header := s.FindHeader(10)
	// 把 keys 的列名解析成列号
	keyCols := map[int]string{}
	for name, val := range keys {
		col := ColByHeader(header, name)
		if col < 0 {
			return nil, fmt.Errorf("表「%s」找不到键列「%s」", s.Name, name)
		}
		keyCols[col] = val
	}
	_ = anchorNames
	row := s.RowByKeys(hdrIdx+1, keyCols)
	if row < 0 {
		return nil, fmt.Errorf("表「%s」找不到匹配行 %v", s.Name, keys)
	}
	fieldCol := ColByHeader(header, fieldNames...)
	if fieldCol < 0 {
		return nil, fmt.Errorf("表「%s」找不到字段列 %v", s.Name, fieldNames)
	}
	ref, _ := excelize.CoordinatesToCellName(fieldCol+1, row+1)
	return &Cell{Sheet: s.Name, Ref: ref, Row: row + 1, Col: fieldCol + 1}, nil
}

// LocateMonthCell 定位"某铺位在某年某月的格"（用于汇总表这类：行=铺位，列=月份）。
func (s *Sheet) LocateMonthCell(keys map[string]string, year, month int) (*Cell, error) {
	hdrIdx, header := s.FindHeader(10)
	keyCols := map[int]string{}
	for name, val := range keys {
		col := ColByHeader(header, name)
		if col < 0 {
			return nil, fmt.Errorf("表「%s」找不到键列「%s」", s.Name, name)
		}
		keyCols[col] = val
	}
	row := s.RowByKeys(hdrIdx+1, keyCols)
	if row < 0 {
		return nil, fmt.Errorf("表「%s」找不到匹配行 %v", s.Name, keys)
	}
	col := ColByMonth(header, year, month)
	if col < 0 {
		return nil, fmt.Errorf("表「%s」找不到 %d-%02d 的列", s.Name, year, month)
	}
	ref, _ := excelize.CoordinatesToCellName(col+1, row+1)
	return &Cell{Sheet: s.Name, Ref: ref, Row: row + 1, Col: col + 1}, nil
}

// Normalize 归一化：去半角/全角空格、去首尾空白、转小写。
func Normalize(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "\u3000", "") // 全角空格
	s = strings.ReplaceAll(s, "\t", "")
	return strings.ToLower(s)
}
