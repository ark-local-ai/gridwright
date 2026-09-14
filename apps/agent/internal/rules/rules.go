// Package rules 是"规则引擎"的执行层：把 rules.yaml 里 when/then 齐全的规则
// **直接**变成改动（短路），不花 token 问模型。
//
// 它只做三件事，刻意不做第四件：
//  1. 把 inbox 文件读成**带表头的行**（规则要有列名才谈得上匹配）
//  2. 判断一条规则是否命中这批数据
//  3. 命中的规则展开成"要改哪些格、改成什么"（**仍走确认制**）
//
// 不做的第四件：不做算术、不做跨表跳转、不做多字段联动。
// 那是模型擅长的、也是人很难在 yaml 里读懂的；把它们留给 probe→脑 那条路。
//
// 安全边界与模型路径**完全一致**：产出的仍然是一份待确认清单，
// 仍然过 forbid 护栏、仍然记账、仍然要人点确认。短路省的只是"问模型"这一步。
package rules

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/memory"
)

// Row 是 inbox 数据的一行，按表头名取值。
type Row struct {
	// Cells 列名（归一化）→ 值。
	Cells map[string]string
	// Raw 原始值（保留原始列名顺序，便于报错时说清楚是哪一行）。
	Raw []string
	// Line 行号（从数据行算起，1-based；报错用）。
	Line int
}

// Get 按列名取值（归一化匹配，忽略空格与大小写）。
func (r Row) Get(col string) string {
	return r.Cells[norm(col)]
}

// Data 是读进来的一个 inbox 文件。
type Data struct {
	File    string   // 文件基名（含扩展名）
	Base    string   // 文件基名（不含扩展名）
	Format  string   // csv | xlsx
	Header  []string // 原始表头
	Rows    []Row
	missing []string // 空文件等情况的说明
}

// Note 返回读取时值得提醒用户的话（空则无需提醒）。
func (d *Data) Note() string { return strings.Join(d.missing, "；") }

// HasColumn 表头里是否有这一列（归一化匹配）。
func (d *Data) HasColumn(col string) bool {
	want := norm(col)
	for _, h := range d.Header {
		if norm(h) == want {
			return true
		}
	}
	return false
}

// Load 把一个 inbox 文件读成表头 + 行。csv 与 xlsx 都读**第一个表**。
func Load(path string) (*Data, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".csv":
		return loadCSV(path)
	case ".xlsx":
		return loadXLSX(path)
	default:
		return nil, fmt.Errorf("规则只认 csv / xlsx（拿到 %s）", ext)
	}
}

func loadCSV(path string) (*Data, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// BOM 会让第一个表头名比对不上（Excel 导出的 csv 常见），显式去掉。
	recs, err := csv.NewReader(stripBOM(f)).ReadAll()
	if err != nil {
		return nil, err
	}
	return fromRecords(path, "csv", recs), nil
}

func loadXLSX(path string) (*Data, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	recs, err := f.GetRows(f.GetSheetName(0))
	if err != nil {
		return nil, err
	}
	return fromRecords(path, "xlsx", recs), nil
}

func fromRecords(path, format string, recs [][]string) *Data {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	d := &Data{File: filepath.Base(path), Base: base, Format: format}
	if len(recs) == 0 {
		d.missing = append(d.missing, "文件是空的（没有表头）")
		return d
	}
	d.Header = recs[0]
	for i, rec := range recs[1:] {
		// 全空行跳过（Excel 常见尾部空行）
		empty := true
		for _, v := range rec {
			if strings.TrimSpace(v) != "" {
				empty = false
				break
			}
		}
		if empty {
			continue
		}
		cells := make(map[string]string, len(d.Header))
		for ci, name := range d.Header {
			if ci < len(rec) {
				cells[norm(name)] = strings.TrimSpace(rec[ci])
			}
		}
		d.Rows = append(d.Rows, Row{Cells: cells, Raw: rec, Line: i + 1})
	}
	if len(d.Rows) == 0 {
		d.missing = append(d.missing, "只有表头，没有数据行")
	}
	return d
}

// Match 判断一条规则是否命中这批数据（when 内多个条件**全部**满足才算命中）。
func Match(rule memory.Rule, d *Data) bool {
	w := rule.When
	if w == nil {
		return false
	}
	if w.File != "" && !strings.Contains(strings.ToLower(d.Base), strings.ToLower(w.File)) {
		return false
	}
	if w.Format != "" && !strings.EqualFold(d.Format, strings.TrimSpace(w.Format)) {
		return false
	}
	for _, col := range w.HasColumns {
		if !d.HasColumn(col) {
			return false
		}
	}
	return true
}

// Intent 是一条规则命中后展开出的"要改什么"。
// **不含坐标**——和模型那条路一样，只说业务语义，坐标交给 locate 算。
type Intent struct {
	Rule   string            `json:"rule"`   // 规则名（要记账，回滚时能看出这条是谁干的）
	Sheet  string            `json:"sheet"`  // 目标工作表
	Key    map[string]string `json:"key"`    // 定位行的键（表列名 → 值）
	Field  string            `json:"field"`  // 要写的字段列名
	Month  string            `json:"month"`  // 按月定位时的 "2026-09"（否则空）
	Op     string            `json:"op"`     // set | add
	Value  string            `json:"value"`  // 新值（原文，数值由下游解析）
	Line   int               `json:"line"`   // 来自 inbox 第几行（报错/追溯用）
	Reason string            `json:"reason"` // 人话说明
}

// Skip 是一条命中但没能展开的情况（缺列、空值……）——**要让人看见**，
// 不能静默：用户以为规则处理了，其实漏了几行，是更坏的结果。
type Skip struct {
	Rule   string `json:"rule"`
	Line   int    `json:"line,omitempty"`
	Reason string `json:"reason"`
}

// Expand 把一条命中的规则展开成逐行的 Intent。
// 任何一行缺列/空值都单独记 Skip 并跳过该行（不中断其余行）。
func Expand(rule memory.Rule, d *Data) (intents []Intent, skips []Skip) {
	if rule.Then == nil {
		return nil, []Skip{{Rule: rule.Name, Reason: "规则没有 then，无法执行"}}
	}
	t := rule.Then
	// field 恰好一个（Executable 已保证；这里再挡一次，防止绕过校验直接调用）。
	if len(t.Field) != 1 {
		return nil, []Skip{{Rule: rule.Name, Reason: "then.field 必须恰好一个字段映射"}}
	}
	var fieldCol, fieldSrc string
	for k, v := range t.Field {
		fieldCol, fieldSrc = k, v
	}
	op := strings.ToLower(strings.TrimSpace(t.Op))
	if op == "" {
		op = "set"
	}

	for _, row := range d.Rows {
		// 1) 取定位键（表列名 → 值）
		key := make(map[string]string, len(t.Key))
		bad := ""
		for tblCol, srcCol := range t.Key {
			v := row.Get(srcCol)
			if v == "" {
				bad = fmt.Sprintf("第 %d 行「%s」列是空的，定不到行", row.Line, srcCol)
				break
			}
			key[tblCol] = v
		}
		if bad != "" {
			skips = append(skips, Skip{Rule: rule.Name, Line: row.Line, Reason: bad})
			continue
		}
		// 2) 取值
		val := row.Get(fieldSrc)
		if val == "" {
			skips = append(skips, Skip{Rule: rule.Name, Line: row.Line,
				Reason: fmt.Sprintf("第 %d 行「%s」列是空的，没值可写", row.Line, fieldSrc)})
			continue
		}
		// 3) 月份（按月定位的汇总表用）
		month := ""
		if t.MonthFrom != "" {
			m := row.Get(t.MonthFrom)
			if m == "" {
				skips = append(skips, Skip{Rule: rule.Name, Line: row.Line,
					Reason: fmt.Sprintf("第 %d 行「%s」列是空的，认不出月份", row.Line, t.MonthFrom)})
				continue
			}
			month = m
		}
		intents = append(intents, Intent{
			Rule: rule.Name, Sheet: t.Sheet, Key: key, Field: fieldCol,
			Month: month, Op: op, Value: val, Line: row.Line,
			Reason: fmt.Sprintf("规则「%s」：%s", rule.Name, describe(rule, d)),
		})
	}
	return intents, skips
}

// describe 给出一条简短的人话说明（进账目的 reason 列）。
func describe(rule memory.Rule, d *Data) string {
	if rule.Action != "" {
		return rule.Action
	}
	return fmt.Sprintf("按规则处理 %s", d.File)
}

// Target 返回规则声明的目标文件名（空=没声明，需要分诊）。
func Target(rule memory.Rule) string {
	if rule.Then == nil {
		return ""
	}
	return strings.TrimSpace(rule.Then.TargetFile)
}

// norm 归一化列名：去空格 + 全角空格 + 小写。
// 「本月实收」「本月实收 」和「本月 实收」在用户眼里是同一列，代码也得这么认。
func norm(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "\u3000", "")
	s = strings.ReplaceAll(s, "\ufeff", "")
	return strings.ToLower(s)
}

func stripBOM(f *os.File) *os.File {
	var bom [3]byte
	n, _ := f.Read(bom[:])
	if n == 3 && bom[0] == 0xEF && bom[1] == 0xBB && bom[2] == 0xBF {
		return f // 已跳过 3 字节
	}
	if n > 0 {
		_, _ = f.Seek(0, 0)
	}
	return f
}
