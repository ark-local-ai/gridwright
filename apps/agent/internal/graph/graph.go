// Package graph 扫描工作区里工作表之间的依赖关系（"联动图"，见 docs/agent-architecture/4-联动图.md 与 17-工作区与跨文件联动.md）。
//
// 节点身份是 文件!工作表（不是光秃秃的 sheet 名）——一个工作区可以有多个 xlsx，
// 两个文件都可能有「月度汇总」，只用 sheet 名会撞车。跨文件的关联是真实存在的：
// 实测真实台账里有 57 个公式带外部工作簿引用，形如 '[2]商铺台账（详）'!I41*6。
//
// 三路来源，可信度不同：
//   - formula  （高）：公式引用。含同文件引用与跨文件引用。
//   - declared （高）：rules.yaml 里人写死的链，用于钉住无公式可依的跨表搬运。
//   - semantic （中）：共享"具体标识列"推断，默认关闭（真实工作簿里噪音极大）。
//
// 传播（Propagate）只走 high 置信度的边，避免语义误判牵连。
package graph

import (
	"fmt"
	"path/filepath"
	"regexp"
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

// Node 是联动图的一个节点：某文件的某张工作表。
// Sheet 非空、File 为空表示"未解析到具体文件"（如跨文件引用但找不到对应文件）。
type Node struct {
	File  string `json:"file"`  // 文件名（不含目录），空=未知
	Sheet string `json:"sheet"` // 工作表名
}

// ID 返回节点的稳定标识，形如 "文件.xlsx!工作表"（无文件时退化为 "工作表"）。
func (n Node) ID() string {
	if n.File == "" {
		return n.Sheet
	}
	return n.File + "!" + n.Sheet
}

// String 便于日志输出。
func (n Node) String() string { return n.ID() }

// Edge 是一条依赖边：From 依赖 To（数据从 To 流向 From）。
// 含义："改了 To（含其所在文件的上下文），牵动 From"。
type Edge struct {
	From        Node   `json:"from"`
	To          Node   `json:"to"`
	Kind        string `json:"kind"`
	Count       int    `json:"count"`
	Confidence  string `json:"confidence"`
	CrossFile   bool   `json:"crossFile"`             // 是否跨文件
	ExternalIdx int    `json:"externalIdx,omitempty"` // 公式里的 [n] 外部索引（>0 表示来自外部链接）
}

// Graph 是一个工作区的联动图（可含多个文件）。
type Graph struct {
	Root    string              `json:"root"`    // 工作区根目录
	Files   []string            `json:"files"`   // 参与扫描的文件名
	Nodes   []Node              `json:"nodes"`   // 全部工作表节点
	Edges   []Edge              `json:"edges"`   //
	Anchors map[string][]string `json:"anchors"` // 节点ID -> 识别出的键列名
}

// SheetsOf 某文件的工作表名（升序）。
func (g *Graph) SheetsOf(file string) []string {
	var out []string
	for _, n := range g.Nodes {
		if n.File == file {
			out = append(out, n.Sheet)
		}
	}
	sort.Strings(out)
	return out
}

// 常见的"锚点列"关键词（用于识别某张表的键列，仅作展示）。
var anchorKeys = []string{
	"物业位置", "铺位", "铺位号", "商铺位", "租户名称", "租户", "客户名称", "客户",
	"店铺名称", "店铺", "店名", "月份", "合同号", "合同编号",
}

// 只有"具体标识"列才驱动语义边。"月份/客户/租户"这类泛列几乎每张表都有，
// 拿它建边会得到 O(n²) 的噪音（实测 24 张表 → 386 条无用边）。
var specificKeys = []string{
	"物业位置", "铺位号", "商铺位", "铺位", "合同号", "合同编号",
}

// Options 控制扫描行为。
type Options struct {
	// IncludeSemantic 是否产出"共享键列"的中置信语义边。默认关闭（噪音大，见上）。
	IncludeSemantic bool
}

// externalRefRe 匹配外部工作簿引用：'[2]Sheet Name'! 或 [1]Sheet!（带或不带引号）。
var externalRefRe = regexp.MustCompile(`'?\[(\d+)\]([^'!]+)'?!`)

// ScanWorkspace 扫一个工作区里的所有 xlsx，产出跨文件联动图。
func ScanWorkspace(root string, files []string, opt Options) (*Graph, error) {
	g := &Graph{Root: root, Anchors: map[string][]string{}}

	type opened struct {
		name   string
		f      *excelize.File
		sheets []string
	}
	var books []opened
	for _, p := range files {
		f, err := excelize.OpenFile(p)
		if err != nil {
			continue // 单个文件打不开不中断整体
		}
		name := filepath.Base(p)
		books = append(books, opened{name: name, f: f, sheets: f.GetSheetList()})
		g.Files = append(g.Files, name)
		defer f.Close()
	}
	sort.Strings(g.Files)

	// 建全量节点表 + sheet 名 → 候选文件（供跨文件引用按名解析）
	sheetToFiles := map[string][]string{}
	for _, b := range books {
		for _, sh := range b.sheets {
			g.Nodes = append(g.Nodes, Node{File: b.name, Sheet: sh})
			if !contains(sheetToFiles[sh], b.name) {
				sheetToFiles[sh] = append(sheetToFiles[sh], b.name)
			}
		}
	}

	edgeCount := map[string]map[string]int{} // edgeKey -> meta
	edgeMeta := map[string]Edge{}
	addEdge := func(from, to Node, kind string, idx int) {
		key := from.ID() + "\x00" + to.ID() + "\x00" + kind
		if edgeCount[key] == nil {
			edgeCount[key] = map[string]int{}
		}
		edgeCount[key]["n"]++
		edgeMeta[key] = Edge{
			From: from, To: to, Kind: kind, Confidence: ConfHigh,
			CrossFile:   from.File != "" && to.File != "" && from.File != to.File,
			ExternalIdx: idx,
		}
	}

	for _, b := range books {
		for _, sh := range b.sheets {
			// 键列
			if ks := detectAnchors(b.f, sh); len(ks) > 0 {
				g.Anchors[Node{File: b.name, Sheet: sh}.ID()] = ks
			}
			// 公式
			rows, err := b.f.GetRows(sh)
			if err != nil {
				continue
			}
			self := Node{File: b.name, Sheet: sh}
			for ri, row := range rows {
				for ci := range row {
					cell, err := excelize.CoordinatesToCellName(ci+1, ri+1)
					if err != nil {
						continue
					}
					formula, err := b.f.GetCellFormula(sh, cell)
					if err != nil || formula == "" {
						continue
					}
					// 同文件引用
					for _, refSheet := range sameFileRefs(formula, b.sheets, sh) {
						addEdge(self, Node{File: b.name, Sheet: refSheet}, KindFormula, 0)
					}
					// 跨文件引用。[n] 按定义指向本工作簿之外，
					// 所以只在"工作区的其他文件"里找；找不到就记为未知外部文件，
					// 绝不解析回本文件（否则重名 sheet 会把跨文件引用吞掉）。
					for _, m := range externalRefRe.FindAllStringSubmatch(formula, -1) {
						idx := atoiSafe(m[1])
						refSheet := strings.TrimSpace(m[2])
						matched := false
						for _, fn := range sheetToFiles[refSheet] {
							if fn == b.name {
								continue // 指向外部，不可能是本文件
							}
							matched = true
							addEdge(self, Node{File: fn, Sheet: refSheet}, KindFormula, idx)
						}
						if !matched {
							// 工作区里没有对应文件：记成"未知外部文件"，让人知道这里是外部依赖
							addEdge(self, Node{File: "", Sheet: refSheet}, KindFormula, idx)
						}
					}
				}
			}
		}
	}

	for key, cnt := range edgeCount {
		e := edgeMeta[key]
		e.Count = cnt["n"]
		g.Edges = append(g.Edges, e)
	}
	if opt.IncludeSemantic {
		g.Edges = append(g.Edges, semanticEdges(g, edgeMeta)...)
	}
	sortEdges(g.Edges)
	return g, nil
}

// Scan 扫单个工作簿（保持向后兼容；节点 File 为该文件名）。
func Scan(path string) (*Graph, error) {
	return ScanWorkspace(filepath.Dir(path), []string{path}, Options{})
}

// ScanWith 单文件 + 选项。
func ScanWith(path string, opt Options) (*Graph, error) {
	return ScanWorkspace(filepath.Dir(path), []string{path}, opt)
}

// sameFileRefs 找公式里引用的同文件其他 sheet。
// 公式里 sheet 名可能带引号（名含空格/括号时必须）：='月度 汇总'!D5
func sameFileRefs(formula string, sheets []string, self string) []string {
	var out []string
	seen := map[string]bool{}
	byLen := append([]string(nil), sheets...)
	sort.Slice(byLen, func(i, j int) bool { return len(byLen[i]) > len(byLen[j]) })
	for _, name := range byLen {
		if name == self || seen[name] {
			continue
		}
		// 只看"表名!"，避免把外部引用里的表名也算成同文件
		if strings.Contains(formula, "'"+name+"'!") || strings.Contains(formula, name+"!") {
			// 若该表名出现在 [n] 外部引用里，跳过（由外部逻辑处理）
			if strings.Contains(formula, "]"+name+"'!") || strings.Contains(formula, "]"+name+"!") {
				continue
			}
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

// semanticEdges 给共享"具体标识列"的 sheet 建语义边（只在同一文件内，跨文件语义援引不可靠）。
func semanticEdges(g *Graph, existing map[string]Edge) []Edge {
	hasHigh := map[string]bool{}
	for _, e := range existing {
		if e.Confidence == ConfHigh {
			hasHigh[e.From.ID()+"\x00"+e.To.ID()] = true
		}
	}
	var out []Edge
	byFile := map[string][]Node{}
	for _, n := range g.Nodes {
		byFile[n.File] = append(byFile[n.File], n)
	}
	for _, nodes := range byFile {
		for i := 0; i < len(nodes); i++ {
			for j := i + 1; j < len(nodes); j++ {
				a, b := nodes[i], nodes[j]
				shared := sharedAnchors(specificOnly(g.Anchors[a.ID()]), specificOnly(g.Anchors[b.ID()]))
				if len(shared) == 0 {
					continue
				}
				if hasHigh[a.ID()+"\x00"+b.ID()] || hasHigh[b.ID()+"\x00"+a.ID()] {
					continue
				}
				out = append(out,
					Edge{From: a, To: b, Kind: KindSemantic, Count: len(shared), Confidence: ConfMedium},
					Edge{From: b, To: a, Kind: KindSemantic, Count: len(shared), Confidence: ConfMedium})
			}
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

// AddDeclared 合并人工声明的高置信边（rules.yaml 里的 link）。
// 节点可用 "文件!sheet" 或裸 "sheet" 指定；裸名会在工作区内解析到唯一匹配的文件。
func (g *Graph) AddDeclared(links []Edge) {
	for _, l := range links {
		l.Kind = KindDeclared
		l.Confidence = ConfHigh
		l.From = g.resolveNode(l.From)
		l.To = g.resolveNode(l.To)
		l.CrossFile = l.From.File != "" && l.To.File != "" && l.From.File != l.To.File
		merged := false
		for i := range g.Edges {
			if g.Edges[i].From.ID() == l.From.ID() && g.Edges[i].To.ID() == l.To.ID() {
				if l.Count == 0 {
					g.Edges[i].Count++
				} else {
					g.Edges[i].Count += l.Count
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

// ResolveNode 把裸 sheet 名解析成工作区里唯一的节点（多个匹配则保持文件为空，不猜）。
func (g *Graph) ResolveNode(n Node) Node { return g.resolveNode(n) }

// resolveNode 把裸 sheet 名解析成工作区里唯一的节点（多个匹配则保持文件为空，不猜）。
func (g *Graph) resolveNode(n Node) Node {
	if n.File != "" || n.Sheet == "" {
		return n
	}
	var matches []Node
	for _, cand := range g.Nodes {
		if cand.Sheet == n.Sheet {
			matches = append(matches, cand)
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return n // 0 或 >1 个匹配：不猜，保留原样
}

// Propagate 返回"改了 node 之后会被牵动"的所有节点 ID（正向、传递闭包，含跨文件）。
// 只走 high 置信度的边；语义边不参与，避免误判牵连。
func (g *Graph) Propagate(node Node) []string {
	adj := map[string][]string{} // to -> []from
	for _, e := range g.Edges {
		if e.Confidence != ConfHigh {
			continue
		}
		adj[e.To.ID()] = append(adj[e.To.ID()], e.From.ID())
	}
	visited := map[string]bool{node.ID(): true}
	queue := []string{node.ID()}
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

// Describe 格式化成发给脑 / 显示给人看的多行文本。
func (g *Graph) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "工作区 %s：%d 个文件、%d 张工作表。\n", filepath.Base(g.Root), len(g.Files), len(g.Nodes))
	high, med := g.splitByConfidence()
	if len(high) == 0 && len(med) == 0 {
		b.WriteString("未发现表间依赖。\n")
		return b.String()
	}
	if len(high) > 0 {
		b.WriteString("依赖关系（高置信）：\n")
		for _, e := range high {
			tag := ""
			if e.CrossFile {
				tag = " [跨文件]"
			}
			fmt.Fprintf(&b, "  「%s」→「%s」(%s ×%d)%s\n", e.To.ID(), e.From.ID(), e.Kind, e.Count, tag)
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
		if es[i].To.ID() != es[j].To.ID() {
			return es[i].To.ID() < es[j].To.ID()
		}
		if es[i].From.ID() != es[j].From.ID() {
			return es[i].From.ID() < es[j].From.ID()
		}
		return es[i].Kind < es[j].Kind
	})
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
