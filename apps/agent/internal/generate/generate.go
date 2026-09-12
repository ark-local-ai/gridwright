// Package generate 做"生成类任务"（见 docs/agent-architecture/27-生成类任务.md）。
//
// 与"改台账"的根本区别：**产物是新文件，绝不碰原表**。
// 生成错了，删掉那个文件就行——所以可以比改表宽松，但仍要看得清数据来源。
//
// 两类分工（关键）：
//
//	表格类：**代码**筛行/汇总（数字必须精确）
//	文书类：**代码**先取出真实数字 → **模型**据此写正文（模型只组织语言，不算数）
//
// 落点固定在工作区的 生成/ 目录，文件名带时间戳，**同名不覆盖**。
package generate

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/locate"
)

// DirName 是生成物的固定目录名。
const DirName = "生成"

// Kind 生成类型。
const (
	KindTable = "table"
	KindDoc   = "doc"
)

// Cond 是一个筛选条件。
type Cond struct {
	Column string `json:"column"`
	Op     string `json:"op"` // gt lt ge le eq ne notEmpty empty contains
	Value  string `json:"value"`
}

// Source 指要读哪张表。
type Source struct {
	File  string `json:"file"`
	Sheet string `json:"sheet"`
}

// Spec 是一份生成规格（模型产出，代码执行）。
type Spec struct {
	Kind        string   `json:"kind"` // table | doc
	Source      Source   `json:"source"`
	Filter      []Cond   `json:"filter"`
	Columns     []string `json:"columns"` // 空=全部列
	Title       string   `json:"title"`
	Template    string   `json:"template"`    // 文书模板名（kind=doc）
	Instruction string   `json:"instruction"` // 给模型的额外要求（kind=doc）
}

// Result 是生成结果。
type Result struct {
	OK      bool               `json:"ok"`
	Kind    string             `json:"kind"`
	Title   string             `json:"title"`
	Path    string             `json:"path"`              // 生成物的完整路径
	File    string             `json:"file"`              // 文件名（给人看）
	Rows    int                `json:"rows"`              // 命中/写入多少行（table）
	Total   int                `json:"total"`             // 源表总行数
	Columns []string           `json:"columns"`           // 实际用到的列
	Preview [][]string         `json:"preview"`           // 前若干行（供界面预览）
	Sum     map[string]float64 `json:"sum,omitempty"`     // 数值列的合计
	Content string             `json:"content,omitempty"` // 文书正文（doc）
	Notes   []string           `json:"notes"`             // 诚实说明（如"草稿，请审阅"）
}

// Extract 读源表、按条件筛行，返回表头 + 选中的行（**纯代码，可单测**）。
func Extract(f *excelize.File, spec Spec) (header []string, rows [][]string, sum map[string]float64, err error) {
	sheet := spec.Source.Sheet
	if sheet == "" {
		sheet = f.GetSheetName(0)
	}
	s, err := locate.LoadSheet(f, sheet)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("读取工作表「%s」: %w", sheet, err)
	}
	hdrIdx, hdr := s.FindHeader(10)
	if len(hdr) == 0 {
		return nil, nil, nil, fmt.Errorf("工作表「%s」没找到表头", sheet)
	}

	// 选定列（不指定则全部）
	colIdx := map[string]int{}
	var cols []string
	if len(spec.Columns) == 0 {
		for i, h := range hdr {
			name := strings.TrimSpace(h)
			if name == "" {
				continue
			}
			colIdx[name] = i
			cols = append(cols, name)
		}
	} else {
		for _, want := range spec.Columns {
			i := locate.ColByHeader(hdr, want)
			if i < 0 {
				return nil, nil, nil, fmt.Errorf("找不到列「%s」", want)
			}
			real := strings.TrimSpace(hdr[i])
			colIdx[real] = i
			cols = append(cols, real)
		}
	}

	// 条件里涉及的列也要能定位
	condIdx := map[string]int{}
	for _, c := range spec.Filter {
		i := locate.ColByHeader(hdr, c.Column)
		if i < 0 {
			return nil, nil, nil, fmt.Errorf("筛选条件里的列「%s」不存在", c.Column)
		}
		condIdx[c.Column] = i
	}

	sum = map[string]float64{}
	for r := hdrIdx + 1; r < len(s.Rows); r++ {
		row := s.Rows[r]
		if isEmptyRow(row) {
			continue
		}
		if !matchAll(row, spec.Filter, condIdx) {
			continue
		}
		out := make([]string, len(cols))
		for i, cn := range cols {
			ci := colIdx[cn]
			if ci < len(row) {
				out[i] = strings.TrimSpace(row[ci])
			}
			// 数值列顺带合计（便于人核对）
			if v, ok := parseNum(out[i]); ok && looksNumeric(hdr[colIdx[cn]]) {
				sum[cn] += v
			}
		}
		rows = append(rows, out)
	}
	return cols, rows, sum, nil
}

// matchAll 判断一行是否满足全部条件。
func matchAll(row []string, conds []Cond, idx map[string]int) bool {
	for _, c := range conds {
		ci, ok := idx[c.Column]
		if !ok {
			return false
		}
		got := ""
		if ci < len(row) {
			got = strings.TrimSpace(row[ci])
		}
		if !matchOne(got, c) {
			return false
		}
	}
	return true
}

func matchOne(got string, c Cond) bool {
	op := strings.ToLower(strings.TrimSpace(c.Op))
	switch op {
	case "notempty":
		return got != ""
	case "empty":
		return got == ""
	case "contains":
		return strings.Contains(got, c.Value)
	case "eq":
		if n1, ok1 := parseNum(got); ok1 {
			if n2, ok2 := parseNum(c.Value); ok2 {
				return n1 == n2
			}
		}
		return got == c.Value
	case "ne":
		if n1, ok1 := parseNum(got); ok1 {
			if n2, ok2 := parseNum(c.Value); ok2 {
				return n1 != n2
			}
		}
		return got != c.Value
	}
	// 数值比较
	n1, ok1 := parseNum(got)
	n2, ok2 := parseNum(c.Value)
	if !ok1 || !ok2 {
		return false
	}
	switch op {
	case "gt":
		return n1 > n2
	case "lt":
		return n1 < n2
	case "ge":
		return n1 >= n2
	case "le":
		return n1 <= n2
	}
	return false // 未知 op：不匹配（宁可少选，不静默放宽）
}

// WriteTable 把筛选结果写成新的 xlsx（落在 生成/）。
func WriteTable(root string, spec Spec, cols []string, rows [][]string, sum map[string]float64) (*Result, error) {
	dir, err := ensureDir(root)
	if err != nil {
		return nil, err
	}
	title := spec.Title
	if title == "" {
		title = "生成表"
	}
	name := safeName(title) + "-" + time.Now().Format("20060102-1504") + ".xlsx"
	path := filepath.Join(dir, name)
	// 防重名覆盖（同分钟生成两次）
	path = uniquePath(path)

	f := excelize.NewFile()
	defer f.Close()
	sheet := "结果"
	_ = f.SetSheetName("Sheet1", sheet)

	// 标题行 + 抬头
	_ = f.SetCellValue(sheet, "A1", title)
	for i, c := range cols {
		cell, _ := excelize.CoordinatesToCellName(i+1, 3)
		_ = f.SetCellValue(sheet, cell, c)
	}
	for ri, row := range rows {
		for ci, v := range row {
			cell, _ := excelize.CoordinatesToCellName(ci+1, ri+4)
			_ = f.SetCellValue(sheet, cell, coerce(v))
		}
	}
	// 末尾附合计（数值列）
	if len(sum) > 0 {
		sumRow := len(rows) + 5
		_ = f.SetCellValue(sheet, "A"+strconv.Itoa(sumRow), "合计")
		for i, c := range cols {
			if v, ok := sum[c]; ok {
				cell, _ := excelize.CoordinatesToCellName(i+1, sumRow)
				_ = f.SetCellValue(sheet, cell, round2(v))
			}
		}
	}
	if err := f.SaveAs(path); err != nil {
		return nil, fmt.Errorf("写出生成文件: %w", err)
	}

	preview := rows
	if len(preview) > 10 {
		preview = preview[:10]
	}
	return &Result{
		OK: true, Kind: KindTable, Title: title, Path: path, File: name,
		Rows: len(rows), Columns: cols, Preview: preview, Sum: sum,
		Notes: []string{
			"这是草稿，请打开核对后再使用",
			"生成不改动原表",
		},
	}, nil
}

// WriteDoc 把文书正文写成 .md（落在 生成/）。
func WriteDoc(root, title, content string) (*Result, error) {
	dir, err := ensureDir(root)
	if err != nil {
		return nil, err
	}
	if title == "" {
		title = "生成文书"
	}
	name := safeName(title) + "-" + time.Now().Format("20060102-1504") + ".md"
	path := uniquePath(filepath.Join(dir, name))

	// 抬头写明"草稿"，避免被当成定稿直接用
	body := "# " + title + "\n\n" +
		"> 草稿，由 gridwright 生成于 " + time.Now().Format("2006-01-02 15:04") + "，请审阅后再使用。\n" +
		"> 数据取自工作区原表，本文件不改动原表。\n\n" + content + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return nil, fmt.Errorf("写出生成文件: %w", err)
	}
	return &Result{
		OK: true, Kind: KindDoc, Title: title, Path: path, File: name, Content: content,
		Notes: []string{"这是草稿，请审阅后再使用", "生成不改动原表"},
	}, nil
}

// ensureDir 确保 生成/ 目录存在。
func ensureDir(root string) (string, error) {
	dir := filepath.Join(root, DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建生成目录: %w", err)
	}
	return dir, nil
}

// uniquePath 若已存在则加序号（**绝不覆盖**）。
func uniquePath(p string) string {
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return p
	}
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for i := 1; ; i++ {
		cand := fmt.Sprintf("%s-%d%s", base, i, ext)
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
	}
}

// safeName 去掉文件名非法字符。
func safeName(s string) string {
	s = strings.TrimSpace(s)
	r := strings.NewReplacer("\\", "", "/", "", ":", "", "*", "", "?", "",
		"\"", "", "<", "", ">", "", "|", "", "\n", "", "\t", " ")
	out := r.Replace(s)
	if out == "" {
		out = "生成"
	}
	if len([]rune(out)) > 40 {
		out = string([]rune(out)[:40])
	}
	return out
}

func isEmptyRow(row []string) bool {
	for _, v := range row {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

func parseNum(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "\u3000", "")
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// looksNumeric 表头像数值列（用于决定是否合计）。
func looksNumeric(h string) bool {
	h = strings.ToLower(h)
	for _, k := range []string{"欠款", "金额", "租金", "实收", "应收", "小计", "合计", "单价", "数量", "保证金", "定金"} {
		if strings.Contains(h, k) {
			return true
		}
	}
	return false
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

func coerce(v string) any {
	if n, ok := parseNum(v); ok {
		return n
	}
	return v
}
