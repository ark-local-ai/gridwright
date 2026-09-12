// Package graph 扫描工作簿里 sheet 之间的依赖关系（"联动图"，见 docs/agent-architecture/4-联动图.md）。
//
// 三路来源，可信度不同：
//   - 公式扫描（formula / high）：谁在公式里引用了谁。客观硬事实。
//   - 语义推断（semantic / medium）：两张表共享同一"键列"（铺位、租户、月份…）。
//                             只是可能相关，供人参考，不用于自动传播。
//   - 显式声明（declared / high）：rules.yaml 里人写死的链，用于钉住
//                             "每日实收→月租金→汇总→滞纳金"这类跨表搬运（无公式可依）。
//
// 传播（Propagate）只走 high 置信度的边（formula + declared），避免语义误判牵连。
package graph

import (
	"fmt"
	"sort"
	"strings"

	"github.com/xuri/excelize/v2"
)

// Kind 是边的来源类型。
const (
	KindFormula  = "formula"  // 公式引用
	KindSemantic = "semantic" // 共享键列推断
	KindDeclared = "declared" // 人工在 rules.yaml 声明
)

// Confidence 是边的可信度。
const (
	ConfHigh   = "high"
	ConfMedium = "medium"
)

// Edge 是一条依赖边：From 依赖 To（数据从 To 流向 From）。
// 例：各月租金表 引用 每日实收表 → Edge{From:"各月租金表", To:"每日实收表"}。
// 含义："改了 To，牵动 From"。
type Edge struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Kind       string `json:"kind"`
	Count      int    `json:"count"`
	Confidence string `json:"confidence"`
}

// Graph 是一张工作簿的联动图。
type Graph struct {
	File    string              `json:"file"`
	Sheets  []string            `json:"sheets"`
	Edges   []Edge              `json:"edges"`
	Anchors map[string][]string `json:"anchors"` // sheet -> 识别出的键列名
}

// 常见的"锚点列"关键词（用于识别某张表的键列，仅作展示）。
var anchorKeys = []string{
	"物业位置", "铺位", "铺位号", "商铺位", "租户名称", "租户", "客户名称", "客户",
	"店铺名称", "店铺", "店名", "月份", "合同号", "合同编号",
}

// 只有"具体标识"列才驱动语义边。"月份/客户/租户"这类泛列几乎每张表都有，
// 拿它建边会得到 O(n²) 的噪音（实测 24 张表 → 416 条无用边）。
var specificKeys = []string{
	"物业位置", "铺位号", "商铺位", "铺位", "合同号", "合同编号",
}

// Options 控制扫描行为。
type Options struct {
	// IncludeSemantic 是否产出"共享键列"的中置信语义边。
	// 默认关闭：实测同一张业务簿里几乎所有表都有"物业位置/月份"这类列，
	// 两两建边会产生数百条噪音（御龙湾表实测 24 张 → 386 条），
	// 而传播只用高置信边，语义边既不参与也不能直接指导。需要时显式开启。
	IncludeSemantic bool
}

// Scan 扫一张工作簿：公式边（高置信）+ 键列。默认不含语义边（见 Options）。
func Scan(path string) (*Graph, error) {
	return ScanWith(path, Options{})
}

// ScanWith 带选项扫描。
func ScanWith(path string, opt Options) (*Graph, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("打开 %s: %w", path, err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	g := &Graph{File: path, Sheets: sheets, Anchors: map[string][]string{}}

	formulaEdges := map[string]map[string]int{} // to -> from -> count
	for _, sh := range sheets {
		rows, err := f.GetRows(sh)
		if err != nil {
			continue
		}
		for ri, row := range rows {
			for ci := range row {
				cell, err := excelize.CoordinatesToCellName(ci+1, ri+1)
				if err != nil {
					continue
				}
				formula, err := f.GetCellFormula(sh, cell)
				if err != nil || formula == "" {
					continue
				}
				for _, ref := range referencedSheets(formula, sheets, sh) {
					if formulaEdges[ref] == nil {
						formulaEdges[ref] = map[string]int{}
					}
					formulaEdges[ref][sh]++
				}
			}
		}
	}
	// 键列（扫前若干行，标题行可能占了好几行）
	for _, sh := range sheets {
		if ks := detectAnchors(f, sh); len(ks) > 0 {
			g.Anchors[sh] = ks
		}
	}

	// 汇总公式边
	for to, froms := range formulaEdges {
		for from, c := range froms {
			g.Edges = append(g.Edges, Edge{From: from, To: to, Kind: KindFormula, Count: c, Confidence: ConfHigh})
		}
	}
	// 语义边：共享键列的两张表（双向，仅供人看）。默认关闭，见 Options。
	if opt.IncludeSemantic {
		g.Edges = append(g.Edges, semanticEdges(sheets, g.Anchors, g.Edges)...)
	}

	sortEdges(g.Edges)
	return g, nil
}

// referencedSheets 从公式文本里找出被引用的 sheet（排除自身）。
// 公式里 sheet 名可能带引号（名含空格/括号时必须）：='2026年9月租金 （日） '!D5
func referencedSheets(formula string, sheets []string, self string) []string {
	var out []string
	seen := map[string]bool{}
	// 按名字长度降序，先匹配长的，避免短名误吞长名
	byLen := append([]string(nil), sheets...)
	sort.Slice(byLen, func(i, j int) bool { return len(byLen[i]) > len(byLen[j]) })
	for _, name := range byLen {
		if name == self || seen[name] {
			continue
		}
		if strings.Contains(formula, "'"+name+"'!") || strings.Contains(formula, name+"!") {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// detectAnchors 在前 8 行里找锚点列（表头可能被标题行推后）。
func detectAnchors(f *excelize.File, sheet string) []string {
	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil
	}
	limit := len(rows)
	if limit > 8 {
		limit = 8
	}
	var keys []string
	seen := map[string]bool{}
	for i := 0; i < limit; i++ {
		for _, cell := range rows[i] {
			c := strings.TrimSpace(cell)
			if c == "" {
				continue
			}
			for _, k := range anchorKeys {
				if strings.Contains(c, k) && !seen[k] {
					seen[k] = true
					keys = append(keys, k)
				}
			}
		}
	}
	sort.Strings(keys)
	return keys
}

// semanticEdges 给共享"具体标识列"的 sheet 两两建语义边（双向）。
// 只用 specificKeys，避免泛列造成 O(n²) 噪音。
// 另外：若两表之间已有公式/声明的高置信边，则不再建语义边（高置信已说明关系）。
func semanticEdges(sheets []string, anchors map[string][]string, existing []Edge) []Edge {
	var out []Edge
	hasHigh := map[string]bool{}
	for _, e := range existing {
		if e.Confidence == ConfHigh {
			hasHigh[e.From+"\x00"+e.To] = true
		}
	}
	for i := 0; i < len(sheets); i++ {
		for j := i + 1; j < len(sheets); j++ {
			a, b := sheets[i], sheets[j]
			shared := sharedAnchors(specificOnly(anchors[a]), specificOnly(anchors[b]))
			if len(shared) == 0 {
				continue
			}
			if hasHigh[a+"\x00"+b] || hasHigh[b+"\x00"+a] {
				continue
			}
			out = append(out, Edge{From: a, To: b, Kind: KindSemantic, Count: len(shared), Confidence: ConfMedium})
			out = append(out, Edge{From: b, To: a, Kind: KindSemantic, Count: len(shared), Confidence: ConfMedium})
		}
	}
	return out
}

func specificOnly(keys []string) []string {
	set := make(map[string]bool, len(specificKeys))
	for _, k := range specificKeys {
		set[k] = true
	}
	var out []string
	for _, k := range keys {
		if set[k] {
			out = append(out, k)
		}
	}
	return out
}

func sharedAnchors(a, b []string) []string {
	set := make(map[string]bool, len(a))
	for _, x := range a {
		set[x] = true
	}
	var out []string
	for _, y := range b {
		if set[y] {
			out = append(out, y)
		}
	}
	return out
}

// AddDeclared 合并人工声明的高置信边（rules.yaml 里的 link），覆盖同向同类的旧边。
func (g *Graph) AddDeclared(links []Edge) {
	for _, l := range links {
		l.Kind = KindDeclared
		l.Confidence = ConfHigh
		// 去重：同 from/to 已存在则计数累加
		merged := false
		for i := range g.Edges {
			if g.Edges[i].From == l.From && g.Edges[i].To == l.To {
				g.Edges[i].Count += l.Count
				if l.Count == 0 {
					g.Edges[i].Count++
				}
				g.Edges[i].Confidence = ConfHigh
				if g.Edges[i].Kind == KindSemantic {
					g.Edges[i].Kind = KindDeclared
				}
				merged = true
				break
			}
		}
		if !merged {
			if l.Count == 0 {
				l.Count = 1
			}
			g.Edges = append(g.Edges, l)
		}
	}
	sortEdges(g.Edges)
}

// Propagate 返回"改了 sheet 之后，会被牵动"的所有 sheet（正向、传递闭包）。
// 只走 high 置信度的边（formula / declared），语义边不参与，避免误判牵连。
func (g *Graph) Propagate(sheet string) []string {
	adj := map[string][]string{} // to -> []from（改了 to 牵动 from）
	for _, e := range g.Edges {
		if e.Confidence != ConfHigh {
			continue
		}
		adj[e.To] = append(adj[e.To], e.From)
	}
	visited := map[string]bool{sheet: true}
	queue := []string{sheet}
	var out []string
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range adj[cur] {
			if visited[next] {
				continue
			}
			visited[next] = true
			out = append(out, next)
			queue = append(queue, next)
		}
	}
	sort.Strings(out)
	return out
}

// Describe 把联动图格式化成发给脑 / 显示给人看的多行文本。
func (g *Graph) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "工作簿 %s：%d 张表。\n", baseName(g.File), len(g.Sheets))
	if len(g.Edges) == 0 {
		b.WriteString("未发现表间依赖。\n")
		return b.String()
	}
	high, med := g.splitByConfidence()
	if len(high) > 0 {
		b.WriteString("依赖关系（高置信）：\n")
		for _, e := range high {
			fmt.Fprintf(&b, "  「%s」→「%s」(%s ×%d)\n", e.To, e.From, e.Kind, e.Count)
		}
	}
	if len(med) > 0 {
		fmt.Fprintf(&b, "可能的关联（中置信，共享键列）：%d 条\n", len(med))
	}
	return b.String()
}

func (g *Graph) splitByConfidence() (high, med []Edge) {
	for _, e := range g.Edges {
		if e.Confidence == ConfHigh {
			high = append(high, e)
		} else {
			med = append(med, e)
		}
	}
	return
}

func sortEdges(es []Edge) {
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].To != es[j].To {
			return es[i].To < es[j].To
		}
		if es[i].From != es[j].From {
			return es[i].From < es[j].From
		}
		return es[i].Kind < es[j].Kind
	})
}

func baseName(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
