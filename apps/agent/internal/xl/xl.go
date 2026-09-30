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

	"github.com/ark-local-ai/ark/apps/agent/internal/locate"
	"github.com/ark-local-ai/ark/apps/agent/internal/plan"
)

// headerProbeRows 是表头探测的窗口：在前 10 行里找最像表头的一行。
// 真实台账的标题/日期行一般不超过 3 行，10 行足够且不会把数据行误当表头。
const headerProbeRows = 10

// Structure 是发给脑的表结构（表头 + 其下前 N 行样例，spec §4 组装 prompt ①）。
type Structure struct {
	Sheet     string   `json:"sheet"`
	HeaderRow int      `json:"header_row"` // 表头所在行（1 基）
	Header    []string `json:"header"`
	Sample    [][]any  `json:"sample"`
}

// ReadStructure 读一个工作表的结构：真实表头行 + 其下 n 行样例。
//
// **不假定表头在第 1 行**。真实台账的第一行几乎总是标题
// （"御城二期销售明细表"），表头在第 2–4 行，有的还是两行合并。
// 写死 rows[0] 会把标题字符串当成列头，模型拿不到任何真实列名，
// 只能回"无法确定源数据表和目标字段"（WS-7 实测，清单全空）。
//
// 表头行交给 locate.FindHeader 定位（真实表识别表头的既有能力，不必新写），
// 并用它的合并结果：两行表头里只在上级出现过的列（"出证日期""按揭银行"）
// 也得以保留，叶子列名仍然优先。
func ReadStructure(f *excelize.File, sheet string, n int) (*Structure, error) {
	sh, err := locate.LoadSheet(f, sheet)
	if err != nil {
		return nil, err
	}
	s := &Structure{Sheet: sheet}
	if len(sh.Rows) == 0 {
		return s, nil
	}
	idx, header := sh.FindHeader(headerProbeRows)
	if idx < 0 || idx >= len(sh.Rows) {
		idx, header = 0, sh.Rows[0]
	}
	s.HeaderRow = idx + 1
	s.Header = header
	if n > 0 {
		// 表头行以下是数据，不是表头
		for i := idx + 1; i < len(sh.Rows) && len(s.Sample) < n; i++ {
			s.Sample = append(s.Sample, toAny(sh.Rows[i]))
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
//
// 列头带**列字母**：用户嘴里的位置常常是“按 D 列的名字填 E、F 列”，
// 而提示词禁止模型输出坐标、只允许业务列名。没有列字母，“D 列”就无法
// 被翻译成“房号”，模型只能回“目标列不明确”（实测发生过）。
func (s *Structure) Describe(sampleRows int) string {
	var b strings.Builder
	if s.HeaderRow > 0 {
		fmt.Fprintf(&b, "工作表「%s」表头在第 %d 行（字母=列名）: %s\n", s.Sheet, s.HeaderRow, strings.Join(s.headerWithLetters(), " | "))
	} else {
		fmt.Fprintf(&b, "工作表「%s」列头（字母=列名）: %s\n", s.Sheet, strings.Join(s.headerWithLetters(), " | "))
	}
	limit := sampleRows
	if limit <= 0 || limit > len(s.Sample) {
		limit = len(s.Sample)
	}
	for i := 0; i < limit; i++ {
		b.WriteString("  " + strings.Join(vals(s.Sample[i]), " | ") + "\n")
	}
	return b.String()
}

// headerWithLetters 渲染为 "A=序号 | B=楼栋 | …"。
//
// 假定 Header[i] 对应第 i+1 列（即从 A 列起）。这一假定成立的前提是
// GetRows 不会吞掉行首的空单元格——它只裁掉行尾的空值。销售明细表的表头行
// 首格恰好为空（上方还有一行合并的上级表头），正好是这个假定最容易翻车的地方，
// 所以 describe_test.go 里专门用它做了回归。
func (s *Structure) headerWithLetters() []string {
	out := make([]string, len(s.Header))
	for i, h := range s.Header {
		letter, err := excelize.ColumnNumberToName(i + 1)
		if err != nil {
			letter = "?"
		}
		name := strings.TrimSpace(h)
		if name == "" {
			name = "（空）"
		}
		out[i] = letter + "=" + name
	}
	return out
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
// 表头默认在第 1 行——inbox 那条老路径的数据表都是干净的。
func applyAppend(f *excelize.File, e plan.Edit) plan.Result {
	if _, err := AppendRow(f, e.Sheet, 1, e.Row); err != nil {
		return plan.Result{Status: "rejected", Note: err.Error()}
	}
	return plan.Result{Status: "ok"}
}

// AppendRow 按表头列名把 row 写入下一空行，返回写入的行号（1 基）。
//
// headerRow 由调用方给，不能写死：真实台账的表头常常不在第 1 行
// （前面有 1–3 行标题/日期），写死第 1 行会把表头认成标题行，于是
// “列不在表头中”把每一条都拒掉。
//
// 任何列不在表头中 → 直接报错，**不猜列**（宁可拒绝，也不要把日期写进房号列）。
func AppendRow(f *excelize.File, sheet string, headerRow int, row map[string]any) (int, error) {
	name, ok := sheetOK(f, sheet)
	if !ok {
		return 0, fmt.Errorf("工作表 %s 不存在", sheet)
	}
	if headerRow < 1 {
		headerRow = 1
	}
	rows, err := f.GetRows(name)
	if err != nil {
		return 0, fmt.Errorf("读取失败: %w", err)
	}
	var header []string
	if headerRow-1 < len(rows) {
		header = rows[headerRow-1]
	}
	// 先校验所有列都在表头里，再写（避免写一半）
	colIdx := make(map[string]int, len(row))
	for colName := range row {
		idx := -1
		for i, h := range header {
			if strings.TrimSpace(h) == strings.TrimSpace(colName) {
				idx = i
				break
			}
		}
		if idx < 0 {
			return 0, fmt.Errorf("列「%s」不在表头中", colName)
		}
		colIdx[colName] = idx
	}
	newRow := len(rows) + 1
	for colName, v := range row {
		cellName, _ := excelize.CoordinatesToCellName(colIdx[colName]+1, newRow)
		if err := f.SetCellValue(name, cellName, coerce(v)); err != nil {
			return 0, fmt.Errorf("写入 %s 失败: %w", colName, err)
		}
	}
	return newRow, nil
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
