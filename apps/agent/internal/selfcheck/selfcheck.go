// Package selfcheck 是**执行后自检**（见 docs/agent-architecture/26-自检流程.md）。
//
// 与 scan（体检）的区别：
//
//	scan     ：定时/手动触发，扫**全工作区**，发现**既有**问题
//	selfcheck：**每次改动后自动**，只查**本次相关表**，确认**本次改动完整**
//
// 只查相关表的原因：改动后的问题一定在相关表附近；聚焦才快、才不淹没重点。
// 全量交给定时体检。
//
// **只用代码，不调模型**：判断"该同步动的表动了吗"是确定的；
// "动的数对不对"是语义判断，归下一层（模型），不做在这里。
package selfcheck

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/graph"
	"github.com/ark-local-ai/ark/apps/agent/internal/impact"
	"github.com/ark-local-ai/ark/apps/agent/internal/locate"
	"github.com/ark-local-ai/ark/apps/agent/internal/memory2"
)

// Level 是自检结果等级。
const (
	LevelOK     = "ok"     // 本次改动已同步，无遗留
	LevelNotice = "notice" // 有需要人看一眼的（如"按惯例该影响它"）
	LevelIssue  = "issue"  // 确定性问题（错误值/不一致）
)

// Finding 是一条自检发现。
type Finding struct {
	Node    graph.Node `json:"node"`
	Level   string     `json:"level"`
	Kind    string     `json:"kind"` // badValue | mismatch | notTouched | ...
	Where   string     `json:"where,omitempty"`
	Message string     `json:"message"`
	// 需要人判断的（走澄清，不可跳过）
	NeedClarify bool `json:"needClarify"`
	// 相关度（为什么查了这张表）
	Score   float64  `json:"score"`
	Reasons []string `json:"reasons"`
}

// Report 是一次自检结果。
type Report struct {
	Level    string    `json:"level"`   // 取所有发现里最高的
	Checked  int       `json:"checked"` // 查了几张表
	Findings []Finding `json:"findings"`
	Summary  string    `json:"summary"`
	Elapsed  string    `json:"elapsed"`
	// 建议补正入口（用户可指出漏了哪些）
	Correctable bool `json:"correctable"`
}

// Input 是自检所需原料。
type Input struct {
	Root string
	// Changed 是本次改动点（"输入表"，不给它算相关性）
	Changed graph.Node
	// Kind 语义类别（收租/售房…），用于记忆候选
	Kind string
	// Touched 本次实际改过的节点 ID（这些算"已同步"）
	Touched []string
	// Files 工作区所有 xlsx（用于打开相关表）
	Files []string
	// Graph / Memory
	Graph  *graph.Graph
	Memory *memory2.Store
	// MaxCheck 最多细查几张（默认 8；相关性高的优先）
	MaxCheck int
}

// Run 执行自检。
func Run(in Input) *Report {
	start := time.Now()
	rep := &Report{Level: LevelOK, Findings: []Finding{}, Correctable: true}

	// ① 相关表（复用影响面推断）
	res := impact.Infer(impact.Input{
		Graph: in.Graph, From: in.Changed, Kind: in.Kind, Memory: in.Memory,
	})
	cands := res.Candidates
	maxN := in.MaxCheck
	if maxN <= 0 {
		maxN = 8
	}
	if len(cands) > maxN {
		cands = cands[:maxN] // 已按相关性降序
	}
	rep.Checked = len(cands)

	touched := map[string]bool{}
	for _, t := range in.Touched {
		touched[t] = true
	}

	// ② 逐张查
	for _, c := range cands {
		f := inspect(in, c, touched)
		rep.Findings = append(rep.Findings, f...)
	}

	SortFindings(rep.Findings)

	// ③ 汇总
	if len(rep.Findings) == 0 {
		rep.Summary = fmt.Sprintf("本次改动已同步，检查了 %d 张相关表，无遗留", rep.Checked)
	} else {
		issues, notices := 0, 0
		for _, f := range rep.Findings {
			if f.Level == LevelIssue {
				issues++
			} else {
				notices++
			}
		}
		rep.Level = LevelNotice
		if issues > 0 {
			rep.Level = LevelIssue
		}
		rep.Summary = fmt.Sprintf("检查了 %d 张相关表：%d 处需处理、%d 处请核对", rep.Checked, issues, notices)
	}
	rep.Elapsed = time.Since(start).String()
	return rep
}

// inspect 查一张相关表，产出发现。
func inspect(in Input, c impact.Candidate, touched map[string]bool) []Finding {
	var out []Finding
	base := Finding{Node: c.Node, Score: c.Score, Reasons: c.Reasons}

	// 找该表对应的文件
	path := fileForNode(in, c.Node)
	if path == "" {
		// 外部文件节点（工作区里没有对应文件）：只提示，不展开
		f := base
		f.Level = LevelNotice
		f.Kind = "external"
		f.Message = fmt.Sprintf("「%s」不在工作区内（可能是外部引用的文件），无法自动核对", c.Node.Sheet)
		return append(out, f)
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		f2 := base
		f2.Level = LevelNotice
		f2.Kind = "unreadable"
		f2.Message = "打不开该表：" + err.Error()
		return append(out, f2)
	}
	defer f.Close()

	sheet := c.Node.Sheet
	if _, err := locate.LoadSheet(f, sheet); err != nil {
		f2 := base
		f2.Level = LevelNotice
		f2.Kind = "missing"
		f2.Message = fmt.Sprintf("工作表「%s」不存在（结构已变？）", sheet)
		return append(out, f2)
	}

	// 检查 1：错误值（确定性）
	for _, bv := range findErrorCells(f, sheet) {
		bv.Level = LevelIssue
		bv.Kind = "badValue"
		bv.Score = c.Score
		bv.Reasons = c.Reasons
		out = append(out, bv)
	}

	// 检查 2：该同步改却没动（确定性：它在相关表里，但本次没碰它）
	if !touched[c.Node.ID()] && c.Score > 0 {
		nt := base
		nt.Level = LevelNotice
		nt.Kind = "notTouched"
		nt.NeedClarify = true
		why := "图上与本次改动相关"
		if c.ByMemory {
			why = "以往这类改动都会涉及它"
		}
		nt.Message = fmt.Sprintf("「%s」本次没改动（%s）。请核对该不该同步更新", c.Node.Sheet, why)
		out = append(out, nt)
	}
	return out
}

// findErrorCells 扫一张表的错误值（确定性检查，复用与 scan 相同的判定）。
func findErrorCells(f *excelize.File, sheet string) []Finding {
	errVals := map[string]bool{
		"#REF!": true, "#DIV/0!": true, "#VALUE!": true, "#NAME?": true,
		"#NULL!": true, "#NUM!": true, "#N/A": true, "#SPILL!": true, "#CALC!": true,
	}
	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil
	}
	var out []Finding
	for ri, row := range rows {
		for ci, v := range row {
			s := strings.TrimSpace(v)
			if !errVals[s] {
				continue
			}
			ref, _ := excelize.CoordinatesToCellName(ci+1, ri+1)
			f := Finding{
				Node: graph.Node{Sheet: sheet}, Level: LevelIssue, Kind: "badValue",
				Where: ref, Message: fmt.Sprintf("「%s」!%s 是错误值 %s", sheet, ref, s),
			}
			out = append(out, f)
		}
	}
	return out
}

// fileForNode 找节点对应的文件路径。
func fileForNode(in Input, n graph.Node) string {
	base := strings.TrimSuffix(n.File, ".xlsx")
	for _, p := range in.Files {
		bn := p
		if i := strings.LastIndexAny(bn, `/\`); i >= 0 {
			bn = bn[i+1:]
		}
		if strings.EqualFold(strings.TrimSuffix(bn, ".xlsx"), base) {
			return p
		}
	}
	return ""
}

// SortFindings 按等级 + 相关性排序（问题在前，高相关在前）。
func SortFindings(fs []Finding) {
	rank := func(l string) int {
		switch l {
		case LevelIssue:
			return 0
		case LevelNotice:
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(fs, func(i, j int) bool {
		if rank(fs[i].Level) != rank(fs[j].Level) {
			return rank(fs[i].Level) < rank(fs[j].Level)
		}
		return fs[i].Score > fs[j].Score
	})
}
