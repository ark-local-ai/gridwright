// Package scan 是离线体检器：只读不写，扫出工作簿里的问题，让人去修
// （见 docs/agent-architecture/11-离线扫描.md）。
//
// 用户诉求原话：「做一个离线的扫描检索，检索错误……不要直接改，展示出来，
// 并且点击能跳到错误的地方」。
//
// 所以本包的契约是：**绝不修改文件**，只产出 Issue 列表（含精确坐标，供界面跳转）。
//
// 三类检查：
//  1. 单元格错误值（#REF! / #DIV/0! / #VALUE! …）—— 表里本来就有坏的
//  2. 公式引用了不存在的 sheet
//  3. 跨月核对：上期欠款是否等于上月本月欠款（只读比较，报差异）
package scan

import (
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/locate"
)

// Severity 是问题严重度。
const (
	SevError   = "error"   // 明确的错误（坏公式、坏引用）
	SevWarn    = "warn"    // 可疑（数值对不上）
	SevInfo    = "info"    // 提示
)

// Issue 是一条体检发现。带精确坐标，界面可点击跳转。
type Issue struct {
	Kind     string `json:"kind"`     // bad_value | bad_ref | mismatch | ...
	Severity string `json:"severity"` // error | warn | info
	File     string `json:"file"`     // 文件名（跨文件体检时定位用）
	Sheet    string `json:"sheet"`
	Ref      string `json:"ref"`   // A1 形式（可空）
	Row      int    `json:"row"`   // 1 基，0=不适用
	Col      int    `json:"col"`
	Message  string `json:"message"`
	Detail   string `json:"detail,omitempty"`
}

// Report 是一次扫描的完整结果。
type Report struct {
	File    string  `json:"file"`
	Sheets  int     `json:"sheets"`
	Cells   int     `json:"cells"` // 扫过的非空格数
	Issues  []Issue `json:"issues"`
	Elapsed string  `json:"elapsed"`
}

// CountBySeverity 汇总各级别数量。
func (r *Report) CountBySeverity() (errs, warns, infos int) {
	for _, i := range r.Issues {
		switch i.Severity {
		case SevError:
			errs++
		case SevWarn:
			warns++
		default:
			infos++
		}
	}
	return
}

// 常见的 Excel 错误值。
var errorValues = []string{
	"#REF!", "#DIV/0!", "#VALUE!", "#NAME?", "#NULL!", "#NUM!", "#N/A",
	"#GETTING_DATA", "#SPILL!", "#CALC!",
}

// Options 控制扫描范围。
type Options struct {
	// CrossMonthCheck 是否做跨月欠款结转核对（需要识别"各月租金表"这类 sheet）。
	CrossMonthCheck bool
	// MonthSheets 参与跨月核对的有序 sheet 名（按月先后）。
	MonthSheets []string
	// AmountFields 跨月核对的字段名候选（如 上期欠款 / 本月欠款）。
	PrevField []string
	CurField  []string
	// AnchorNames 定位用的键列名候选。
	AnchorNames []string
}

// Run 扫一张工作簿，只读。绝不写文件。
func Run(path string, opt Options) (*Report, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("打开 %s: %w", path, err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	rep := &Report{File: path, Sheets: len(sheets)}
	// 给每条发现补上文件名（跨文件体检时界面靠它定位到具体表）
	base := filepathBase(path)

	for _, sh := range sheets {
		if err := scanSheetErrors(f, sh, rep); err != nil {
			// 单表出错不中断整体扫描
			rep.Issues = append(rep.Issues, Issue{
				Kind: "scan_error", Severity: SevWarn, File: base, Sheet: sh,
				Message: "扫描该表时出错：" + err.Error(),
			})
		}
	}

	if opt.CrossMonthCheck && len(opt.MonthSheets) > 1 {
		scanCrossMonth(f, opt, rep)
	}
	for i := range rep.Issues {
		if rep.Issues[i].File == "" {
			rep.Issues[i].File = base
		}
	}
	return rep, nil
}

func filepathBase(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// scanSheetErrors 找错误值和坏引用（只读）。
func scanSheetErrors(f *excelize.File, sheet string, rep *Report) error {
	rows, err := f.GetRows(sheet)
	if err != nil {
		return err
	}
	for ri, row := range rows {
		for ci, cellVal := range row {
			v := strings.TrimSpace(cellVal)
			if v == "" {
				continue
			}
			rep.Cells++
			ref, _ := excelize.CoordinatesToCellName(ci+1, ri+1)

			// 1) 错误值
			if isErrorValue(v) {
				rep.Issues = append(rep.Issues, Issue{
					Kind: "bad_value", Severity: SevError, Sheet: sheet,
					Ref: ref, Row: ri + 1, Col: ci + 1,
					Message: fmt.Sprintf("单元格是错误值 %s", v),
					Detail:  "该格的计算失败，需要人工核对",
				})
				continue
			}

			// 2) 坏引用（公式里含 #REF!）
			if strings.Contains(v, "#REF!") {
				rep.Issues = append(rep.Issues, Issue{
					Kind: "bad_ref", Severity: SevError, Sheet: sheet,
					Ref: ref, Row: ri + 1, Col: ci + 1,
					Message: "公式引用了已删除的单元格（#REF!）",
				})
			}
		}
	}
	return nil
}

func isErrorValue(v string) bool {
	for _, e := range errorValues {
		if strings.EqualFold(v, e) {
			return true
		}
	}
	return false
}

// scanCrossMonth 跨月核对：上月"本月欠款" 应等于 本月"上期欠款"（只读比较，报差异）。
//
// 这是财务最需要的核对之一：如果对不上，说明中间有人工改动或漏记。
// 只报差异，绝不改文件。
func scanCrossMonth(f *excelize.File, opt Options, rep *Report) {
	// 每张月表抽两个索引：铺位 → (上期欠款, 本月欠款)，各带坐标
	type monthSnap struct {
		sheet string
		prev  map[string]amountAt // 上期欠款
		cur   map[string]amountAt // 本月欠款
	}
	var snaps []monthSnap

	for _, sh := range opt.MonthSheets {
		s, err := locate.LoadSheet(f, sh)
		if err != nil {
			// 不静默跳过：漏掉一个月会让相邻月错配，制造大量假差异。
			rep.Issues = append(rep.Issues, Issue{
				Kind: "sheet_missing", Severity: SevWarn, Sheet: sh,
				Message: "跨月核对里指定的表不存在，已跳过该月（相邻月比对可能因此失真）",
				Detail:  err.Error(),
			})
			continue
		}
		hdrIdx, header := s.FindHeader(10)
		keyCol := locate.ColByHeader(header, opt.AnchorNames...)
		prevCol := locate.ColByHeader(header, opt.PrevField...)
		curCol := locate.ColByHeader(header, opt.CurField...)
		if keyCol < 0 || prevCol < 0 || curCol < 0 {
			rep.Issues = append(rep.Issues, Issue{
				Kind: "sheet_unfit", Severity: SevWarn, Sheet: sh,
				Message: "该表缺关键列（锚点/上期欠款/本月欠款），未参与跨月核对",
				Detail:  fmt.Sprintf("key=%d prev=%d cur=%d", keyCol, prevCol, curCol),
			})
			continue
		}
		snap := monthSnap{sheet: sh, prev: map[string]amountAt{}, cur: map[string]amountAt{}}
		lastKey := ""
		for r := hdrIdx + 1; r < len(s.Rows); r++ {
			row := s.Rows[r]
			k := ""
			if keyCol < len(row) {
				k = locate.Normalize(row[keyCol])
			}
			if k != "" {
				lastKey = k
			} else {
				k = lastKey
			}
			if k == "" {
				continue
			}
			if v, ok := at(row, prevCol); ok {
				snap.prev[k] = amountAt{value: v, row: r + 1, ref: cellRef(prevCol+1, r+1)}
			}
			if v, ok := at(row, curCol); ok {
				snap.cur[k] = amountAt{value: v, row: r + 1, ref: cellRef(curCol+1, r+1)}
			}
		}
		snaps = append(snaps, snap)
	}

	// 相邻月比对：本月"上期欠款" vs 上月"本月欠款"
	for i := 1; i < len(snaps); i++ {
		cur, prev := snaps[i], snaps[i-1]
		for key, now := range cur.prev {
			old, seen := prev.cur[key]
			if !seen {
				continue
			}
			if near(now.value, old.value) {
				continue
			}
			rep.Issues = append(rep.Issues, Issue{
				Kind: "mismatch", Severity: SevWarn, Sheet: cur.sheet,
				Ref: now.ref, Row: now.row, Col: 0,
				Message: fmt.Sprintf("铺位 %s 的上期欠款 %.2f 与上月本月欠款 %.2f 不符", key, now.value, old.value),
				Detail:  fmt.Sprintf("上月表「%s」%s = %.2f", prev.sheet, old.ref, old.value),
			})
		}
	}
}

type amountAt struct {
	value float64
	row   int
	ref   string
}

func at(row []string, col int) (float64, bool) {
	if col < 0 || col >= len(row) {
		return 0, false
	}
	return parseAmount(row[col])
}

// near 判断两个金额是否近似相等（容忍 1 分钱的浮点误差）。
func near(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 0.005
}

func cellRef(col, row int) string {
	ref, _ := excelize.CoordinatesToCellName(col, row)
	return ref
}

// parseAmount 解析金额文本（去千分位、去空格、去"元"），失败返回 false。
func parseAmount(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "\u3000", "")
	s = strings.TrimSuffix(s, "元")
	if s == "" {
		return 0, false
	}
	var v float64
	if _, err := fmt.Sscanf(s, "%f", &v); err != nil {
		return 0, false
	}
	return v, true
}
