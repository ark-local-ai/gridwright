package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/graph"
	"github.com/ark-local-ai/ark/apps/agent/internal/llm"
	"github.com/ark-local-ai/ark/apps/agent/internal/locate"
	"github.com/ark-local-ai/ark/apps/agent/internal/memory"
	"github.com/ark-local-ai/ark/apps/agent/internal/memory2"
	"github.com/ark-local-ai/ark/apps/agent/internal/propose"
	"github.com/ark-local-ai/ark/apps/agent/internal/terms"
)

// 这是"待改清单"的产出（见 docs/agent-architecture/5-编辑语义.md、19-界面设计.md 阶段 4）。
//
// 设计要点：**LLM 只说业务语义，代码负责定位**。
// 模型返回的编辑指令形如：
//
//	{"sheet":"各月租金表","key":{"物业位置":"B31"},
//	 "month":"2026-08","field":"本月实收","op":"add","value":23540,"reason":"8月租金"}
//
// 我们用 locate 把它翻成具体单元格、算出旧值，交给 propose 待确认。
// 模型**永远不吐坐标**——那样极易错行错列。

// strMap 是「列名 → 值」的键值对，值允许模型给成数字/布尔。
//
// 为什么需要自定义 Unmarshal：模型常把纯数字的业务键写成 JSON 数字
// （"房号":101 而不是 "房号":"101"）。默认解成 map[string]string 会直接报错，
// 整份清单在**解析那一步**就没了——一个格式小毛病把一件能做的事停掉。
// 统一转成字符串，往下（MatchRows 的归一化比较）本来就只按文本比。
type strMap map[string]string

func (m *strMap) UnmarshalJSON(b []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	out := make(strMap, len(raw))
	for k, v := range raw {
		if v == nil {
			out[k] = ""
			continue
		}
		out[k] = strings.TrimSpace(fmt.Sprint(v))
	}
	*m = out
	return nil
}

// PlanOptions 控制一次计划。
type PlanOptions struct {
	File string // 目标文件（空=第一张表）
	// Images 是随指令附的图（截图里的数据）。必须能传进来：
	// "按 D 列名字把 E、F 列填进某表"这类指令的数据**就在图里**，
	// 只给文字的话模型只能反问"数据来自哪张表"，把能做的事问成澄清。
	Images []llm.ImageInput
}

// semanticEdit 是模型返回的一条"业务语义"编辑（**不含坐标**）。
type semanticEdit struct {
	File  string  `json:"file"`
	Sheet string  `json:"sheet"`
	Key   strMap  `json:"key"`   // 定位行的键列（如 物业位置: B31）
	Month string  `json:"month"` // 2026-08（列是月份的汇总表用）
	Field string  `json:"field"` // 字段列名
	Op    string  `json:"op"`    // set | add | upsert
	// Row 是 upsert 时要写入的「列名 → 值」。
	//
	// 为什么 upsert 需要它：新增一行是多列，field+value 只能表达一列。
	// 带 row 的编辑语义是“这个键的行应该长这样”——查得到就覆盖那几列，
	// 查不到就新增一行。
	Row    map[string]any `json:"row"`
	Value  any            `json:"value"`
	Reason string         `json:"reason"`
}

// Plan 组装 prompt → 问脑 → 语义指令翻成具体格 → 产出待确认清单（**不落盘**）。
//
// 先试**规则短路**：rules.yaml 里 when/then 齐全的规则命中这批输入时，
// 直接由规则产出清单，**不问模型**（省 token、可复现、可审计）。
// 没有规则命中才走模型（见 docs/agent-architecture/30-规则引擎.md）。
//
// 需要配好模型；但**规则命中时不需要**——这正是短路的收益：断网也能按规矩办事。
func (a *Agent) Plan(ctx context.Context, instruction string, opts PlanOptions) (*propose.Proposal, error) {
	// 规则短路优先：命中即返回，不调模型。
	if prop, err := a.planFromRules(instruction, opts); err == nil && prop != nil {
		return prop, nil
	}
	if a.Brain == nil || !a.Brain.Ready() {
		return nil, fmt.Errorf("还没配置模型（脑）：请在设置里填 base_url 与 api_key，之后才能让它判断该改哪些格")
	}
	files, err := a.Layout.DataFiles()
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("工作区没有 .xlsx 表")
	}
	target := files[0]
	if opts.File != "" {
		for _, fp := range files {
			if strings.EqualFold(filepath.Base(fp), opts.File) {
				target = fp
				break
			}
		}
	}

	// 素材：表结构 + 联动图
	f, err := excelize.OpenFile(target)
	if err != nil {
		return nil, fmt.Errorf("打开目标表: %w", err)
	}
	structs, err := a.collectStructures(f)
	f.Close()
	if err != nil {
		return nil, err
	}
	g, _ := graph.ScanWorkspace(a.Layout.Root, files, graph.Options{})

	// 相关记忆（见 docs/agent-architecture/21-记忆设计.md）：**只取与本次指令相关的**，
	// 不把全部记忆塞进 prompt——上下文里没有噪音，模型更容易判对。
	memText := ""
	if mem, merr := memory2.Open(a.Layout.Root); merr == nil {
		memText = mem.RetrieveByText(instruction).Describe()
	}
	// 语义映射（见 24/25）：用户嘴里的模糊词先翻译成明确的表/字段/类别。
	// "那笔钱""老李那家"这类说法，第一次问清、以后复用。
	if tm, terr := terms.Open(a.Layout.Root); terr == nil {
		if hits := tm.Resolve(instruction); len(hits) > 0 {
			memText = terms.Describe(hits) + memText
		}
	}

	prompt := assemblePlanPrompt(instruction, structs, g, memText, len(opts.Images) > 0)
	resp, err := a.Brain.PlanSemanticWithImages(ctx, prompt, opts.Images)
	if err != nil {
		return nil, err
	}
	// edits 的原始 JSON 在这里解析成 semanticEdit
	var edits []semanticEdit
	if len(resp.Edits) > 0 {
		if err := json.Unmarshal(resp.Edits, &edits); err != nil {
			return nil, fmt.Errorf("解析编辑指令: %w", err)
		}
	}

	prop := &propose.Proposal{Root: a.Layout.Root, Target: target, Summary: resp.Summary}
	for _, q := range resp.Questions {
		prop.Blocked = append(prop.Blocked, propose.Blocked{Reason: "需要你确认：" + q})
	}
	for _, s := range resp.SkipReasons {
		prop.Blocked = append(prop.Blocked, propose.Blocked{Reason: s})
	}

	// 重新打开用于取值/定位（resolveEdit 会读旧值）
	f2, err := excelize.OpenFile(target)
	if err != nil {
		return nil, err
	}
	defer f2.Close()
	for _, e := range edits {
		items, blocked := resolveEdits(f2, target, e)
		if blocked != nil {
			prop.Blocked = append(prop.Blocked, *blocked)
			continue
		}
		for _, it := range items {
			if g != nil {
				it.Affects = g.Propagate(graph.Node{File: filepath.Base(target), Sheet: it.Sheet})
			}
			prop.Items = append(prop.Items, it)
		}
	}

	// forbid 护栏：模型也不能动"禁止修改列"。
	//
	// 规则短路那条路有 guardEdits（rulesplan → agent.RunInboxFile 的路径），
	// 而模型这条路原本没有——偏偏**模型才是最可能擅自改禁止列的一方**
	// （规则是人写的，模型是猜的）。一视同仁地拦下，并让人看见。
	if rf, _, _, _, lerr := memory.Load(a.Layout.Rules, a.Layout.State); lerr == nil {
		kept, blocked := filterForbidden(prop.Items, rf.ForbidSet())
		prop.Items = kept
		prop.Blocked = append(prop.Blocked, blocked...)
	}

	// 指纹：Apply 前据此确认文件没被换过
	if fp, err := propose.Fingerprint(target); err == nil {
		prop.Finger = fp
	}
	if prop.Summary == "" {
		prop.Summary = fmt.Sprintf("将改动 %d 处", len(prop.Items))
	}
	if len(prop.Items) == 0 && len(prop.Blocked) == 0 {
		prop.Summary = "没有需要改动的地方"
	}
	return prop, nil
}

// resolveEdits 把一条语义编辑翻成清单条目。
//
// 多数 op（set/add）只产出一条；upsert 可能产出多条——查到行时
// “row 里有几列”就产出几条 set（每格一条）。
func resolveEdits(f *excelize.File, target string, e semanticEdit) ([]propose.Item, *propose.Blocked) {
	if strings.ToLower(strings.TrimSpace(e.Op)) != "upsert" {
		item, blocked := resolveEdit(f, target, e)
		if blocked != nil {
			return nil, blocked
		}
		return []propose.Item{*item}, nil
	}

	sheet := e.Sheet
	if sheet == "" {
		sheet = f.GetSheetName(0)
	}
	s, err := locate.LoadSheet(f, sheet)
	if err != nil {
		return nil, &propose.Blocked{Sheet: sheet, Field: e.Field, Reason: "工作表读取失败：" + err.Error()}
	}
	if len(e.Key) == 0 {
		return nil, &propose.Blocked{Sheet: sheet, Reason: "upsert 缺少 key：得说清用哪一列去匹配行"}
	}
	if len(e.Row) == 0 {
		return nil, &propose.Blocked{Sheet: sheet, Reason: "upsert 缺少 row：得给出要写入的列与值"}
	}
	hdrIdx, header := s.FindHeader(10)
	if len(header) == 0 {
		return nil, &propose.Blocked{Sheet: sheet, Reason: "表「" + sheet + "」里找不到表头"}
	}
	return resolveUpsert(s, f, target, e, hdrIdx+1, header)
}

// resolveUpsert 实现“查到就改、查不到就新增”。
//
// **为什么必须由代码决定 set 还是 append**：模型看不到整张目标表（几百上千行），
// 它只能给出“业务键 + 要写的列”；**这个键到底在不在表里，只有拿表去查才知道**。
// 让模型自己判断，它就会在“该新增”时说“系统不能新增行”，把能做的事停成拒绝。
//
// headerRow 是表头行（1 基），它同时就是数据起始行（0 基的 hdrIdx+1）——
// 所以既能喂给 MatchRows 当 dataStart，也能存进 Item 给 Apply 的 AppendRow。
func resolveUpsert(s *locate.Sheet, f *excelize.File, target string, e semanticEdit,
	headerRow int, header []string) ([]propose.Item, *propose.Blocked) {
	// 要写的列：row 给的 + key 给的（新增行必须带上业务键，否则以后找不到这行）
	values := map[string]any{}
	for name, v := range e.Row {
		values[name] = v
	}
	// 同名列以 key 为准：key 是“匹配依据”，写别的值会让这行以后再也匹配不上
	for name, v := range e.Key {
		values[name] = v
	}
	// 列名必须在表头里能精确找到，否则直接拒绝（不猜列）
	for name := range values {
		if locate.ColByHeader(header, name) < 0 {
			return nil, &propose.Blocked{Sheet: s.Name, Field: name,
				Reason: "列「" + name + "」不在表头中（表头：" + strings.Join(header, "/") + "）"}
		}
	}
	keyCols := map[int]string{}
	for name, val := range e.Key {
		col := locate.ColByHeader(header, name)
		if col < 0 {
			return nil, &propose.Blocked{Sheet: s.Name, Field: name, Reason: "键列「" + name + "」不在表头中"}
		}
		keyCols[col] = val
	}

	rows := s.MatchRows(headerRow, keyCols)
	switch len(rows) {
	case 0:
		// 查不到 → 新增一行。行号只是**估算**（真正写到第几行由 Apply 时现算，
		// 确认期间表可能已经长了），但足以让人在清单里看出“要加在哪里”。
		newRow := len(s.Rows) + 1
		ref, _ := excelize.CoordinatesToCellName(1, newRow)
		return []propose.Item{{
			File: filepath.Base(target), Sheet: s.Name, Ref: ref, Row: newRow, Col: 1,
			HeaderRow: headerRow,
			Key:       e.Key, Field: fieldLabel(values),
			Op: "append", Values: values, Reason: e.Reason,
		}}, nil
	case 1:
		row := rows[0]
		items := make([]propose.Item, 0, len(values))
		for _, name := range sortedKeys(values) {
			col := locate.ColByHeader(header, name)
			ref, _ := excelize.CoordinatesToCellName(col+1, row+1)
			oldVal, _ := f.GetCellValue(s.Name, ref)
			// 值没变就不入清单。业务键常常跟要写的列一起给
			// （“在客户名称=张三的那行写实收”），把键原样重写一遍
			// 会变成“张三 → 张三”这种纯噪音，人还得逐条辨认——
			// 清单只能装“真的要变的东西”。
			if sameValue(oldVal, values[name]) {
				continue
			}
			items = append(items, propose.Item{
				File: filepath.Base(target), Sheet: s.Name, Ref: ref, Row: row + 1, Col: col + 1,
				Key: e.Key, Field: name,
				Op: "set", Old: oldVal, New: values[name], Reason: e.Reason,
			})
		}
		return items, nil
	default:
		// 歧义：同一个键匹配到多行。取第一行就是改错行——必须让人来定。
		return nil, &propose.Blocked{Sheet: s.Name,
			Reason: fmt.Sprintf("表「%s」：键 %v 匹配到 %d 行，无法确定该改还是该新增；请补一个能区分的键（如房号/客户名称）",
				s.Name, e.Key, len(rows))}
	}
}

// fieldLabel 把一个 upsert 的“要写的列”拼成一句话（清单里显示用）。
func fieldLabel(values map[string]any) string {
	keys := sortedKeys(values)
	if len(keys) == 0 {
		return ""
	}
	return strings.Join(keys, "、")
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sameValue 判断“要写的值”与单元格里已经有的值是不是同一个（按 locate.Normalize）。
// 用 Normalize 是为了不让“A03 ”与“A03”、“张三”与“张三 ”这种差异
// 变成一条“看起来很要紧、实际啥也没改”的清单项。
func sameValue(old string, newVal any) bool {
	return locate.Normalize(old) == locate.Normalize(fmt.Sprint(newVal))
}

// filterForbidden 把动到 forbid 列的条目拦下来（模型路径）。
//
// 与 guardEdits 同一套判据（append：任一列名命中；其余：field 命中），
// 只是共享数据结构不同（propose.Item vs plan.Edit）。
func filterForbidden(items []propose.Item, forbid map[string]bool) (kept []propose.Item, blocked []propose.Blocked) {
	if len(forbid) == 0 {
		return items, nil
	}
	for _, it := range items {
		hit := ""
		if it.Op == "append" {
			for name := range it.Values {
				if forbid[locate.Normalize(name)] {
					hit = name
					break
				}
			}
		} else if forbid[locate.Normalize(it.Field)] {
			hit = it.Field
		}
		if hit == "" {
			kept = append(kept, it)
			continue
		}
		blocked = append(blocked, propose.Blocked{
			Sheet: it.Sheet, Ref: it.Ref, Field: hit,
			Reason: fmt.Sprintf("列「%s」是禁止修改列，已拦下", hit),
		})
	}
	return kept, blocked
}

// resolveEdit 把一条语义编辑翻成具体单元格（含旧值）。
// 定位失败 → 返回 blocked（让人看见，不静默丢）。
func resolveEdit(f *excelize.File, target string, e semanticEdit) (*propose.Item, *propose.Blocked) {
	sheet := e.Sheet
	if sheet == "" {
		sheet = f.GetSheetName(0)
	}
	s, err := locate.LoadSheet(f, sheet)
	if err != nil {
		return nil, &propose.Blocked{Sheet: sheet, Field: e.Field, Reason: "工作表读取失败：" + err.Error()}
	}
	if e.Field == "" {
		return nil, &propose.Blocked{Sheet: sheet, Reason: "缺少字段名（field）"}
	}

	var cell *locate.Cell
	if e.Month != "" {
		y, m, ok := parseYearMonth(e.Month)
		if !ok {
			return nil, &propose.Blocked{Sheet: sheet, Field: e.Field,
				Reason: "月份格式无法识别（应如 2026-08）：" + e.Month}
		}
		cell, err = s.LocateMonthCell(e.Key, y, m)
	} else {
		cell, err = s.LocateRowField(nil, []string{e.Field}, e.Key)
	}
	if err != nil {
		return nil, &propose.Blocked{Sheet: sheet, Field: e.Field, Reason: "定位失败：" + err.Error()}
	}

	oldVal, _ := f.GetCellValue(sheet, cell.Ref)
	newVal := e.Value
	op := strings.ToLower(strings.TrimSpace(e.Op))
	if op == "" {
		op = "set"
	}
	if op == "add" {
		oldNum, _ := parseNum(oldVal)
		addNum, _ := parseNum(fmt.Sprint(e.Value))
		newVal = oldNum + addNum
	}

	return &propose.Item{
		File: filepath.Base(target), Sheet: sheet, Ref: cell.Ref, Row: cell.Row, Col: cell.Col,
		Key: e.Key, Month: e.Month, Field: e.Field,
		Op: op, Old: oldVal, New: newVal, Reason: e.Reason,
	}, nil
}

// parseYearMonth 解析 "2026-08" / "2026/8" / "2026年8月"。
func parseYearMonth(s string) (int, int, bool) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "年", "-")
	s = strings.ReplaceAll(s, "月", "")
	s = strings.ReplaceAll(s, "/", "-")
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	var y, m int
	if _, err := fmt.Sscanf(strings.TrimSpace(parts[0]), "%d", &y); err != nil {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &m); err != nil {
		return 0, 0, false
	}
	if y < 1900 || y > 2200 || m < 1 || m > 12 {
		return 0, 0, false
	}
	return y, m, true
}

// parseNum 宽松解析金额（去千分位/空格），失败返回 false。
func parseNum(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "\u3000", "")
	if s == "" {
		return 0, false
	}
	var v float64
	if _, err := fmt.Sscanf(s, "%f", &v); err != nil {
		return 0, false
	}
	return v, true
}
