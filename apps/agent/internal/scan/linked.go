package scan

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/xuri/excelize/v2"
)

// 跨表引用核对（linked value reconciliation）。
//
// 用户要的是这个（2026-09-21 原话）：
//
//	「展示就展示改动之后数据对不上那种，比如日收表今天明明说入账 5w，
//	 但是汇总表今天的日期入账却写少了个 0 变 5000 这种错误」
//
// 这正是财务改完表最容易留下的伤：**明细改了，汇总没跟着改**。
//
// 怎么查才不误报（这一点是关键）：
// 一个"汇总 5000 vs 明细 50000"的差异，只有在**能证明它们本该相等**时才敢报。
// 光看"一个数字和另一个数字不一样"是不行的——表里到处是本来就不相等的数
// （面积 vs 金额、上期 vs 本期），那样报出来的全是噪音，在财务工具里比不报更糟。
//
// 所以这里只认一种**可证明**的情形：汇总表的某格是一个**指向明细表某格的公式**
// （如 =销售明细表!R179）。这种情况下两者由定义就该相等；而 Excel 保存时会
// 缓存上一计算结果，若缓存值 ≠ 被引用格现在的值，就是"源改了、这格没重算/没跟着改"。
// 这在交叉表里是硬证据，不是猜测。
//
// 范围限制（宁少报不误报）：
//   - 只核对**同工作簿内**的引用（外部 [n] 引用的目标表不在手上，无从比）
//   - 只核对公式体是**单个引用**的情形；含运算（=A1+B1）的不碰——
//     那需要求值器才能判定，猜不得
//   - 只比数字；文本/空值跳过

// 形如 销售明细表!R179 或 '我的 表'!A1 的单个单元格引用，整条公式就是它。
var singleRefRe = regexp.MustCompile(`^'?([^'!]+)'?!\$?([A-Z]{1,3})\$?(\d+)$`)

// 表名可能带引号且内含 ! 之外字符；这里不做贪婪匹配，Excel 表名不含 '!'。
func scanLinkedValues(f *excelize.File, rep *Report) {
	sheets := f.GetSheetList()
	known := make(map[string]bool, len(sheets))
	for _, s := range sheets {
		known[s] = true
	}

	for _, sh := range sheets {
		rows, err := f.GetRows(sh)
		if err != nil {
			continue
		}
		for ri, row := range rows {
			for ci := range row {
				ref, err := excelize.CoordinatesToCellName(ci+1, ri+1)
				if err != nil {
					continue
				}
				formula, err := f.GetCellFormula(sh, ref)
				if err != nil || formula == "" {
					continue
				}
				m := singleRefRe.FindStringSubmatch(strings.TrimSpace(formula))
				if m == nil {
					continue // 不是"单个单元格引用"，不碰
				}
				srcSheet, srcCol, srcRow := m[1], m[2], m[3]
				// 外部引用（[n]表名）与不存在的表：跳过
				if strings.Contains(srcSheet, "[") || !known[srcSheet] {
					continue
				}
				srcRef := fmt.Sprintf("%s%s", srcCol, srcRow)

				// 两边都取"缓存的计算结果值"。excelize 不求值，读到的就是
				// Excel 上次保存时缓存的值——正好是我们要比的东西。
				gotV, err1 := f.GetCellValue(sh, ref)
				srcV, err2 := f.GetCellValue(srcSheet, srcRef)
				if err1 != nil || err2 != nil {
					continue
				}
				a, ok1 := parseAmount(gotV)
				b, ok2 := parseAmount(srcV)
				if !ok1 || !ok2 {
					continue // 有文本/空值，不判
				}
				if near(a, b) {
					continue
				}

				rep.Issues = append(rep.Issues, Issue{
					Kind:     "mismatch",
					Severity: SevWarn,
					Sheet:    sh,
					Ref:      ref,
					Row:      ri + 1,
					Col:      ci + 1,
					Message: fmt.Sprintf("本格 %s 与所引用的 %s!%s 对不上：这里是 %s，源格是 %s",
						ref, srcSheet, srcRef, trimNum(gotV), trimNum(srcV)),
					Detail: fmt.Sprintf("本格公式 =%s，按定义应与 %s!%s 相等；"+
						"源表改过之后这一格没有跟着变（或在公式上盖了死数）。改动前请核对。",
						formula, srcSheet, srcRef),
				})
			}
		}
	}
}

// trimNum 去掉展示用字符串两端空白（金额原样，不做四舍五入处理）。
func trimNum(s string) string { return strings.TrimSpace(s) }
