// Package memory 读取工作区里的两个"记忆文件"（spec §5）：
// rules.yaml（长期经验，含 forbid 硬性护栏）与 state.yaml（滚动摘要）。
// 不做 LLM 记忆——记忆外化成文件，人可用记事本编辑。
package memory

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Rule 是一条规则。
//
// 一条规则有两种可能的"半成品状态"：
//   - 只写了 trigger/action（人能读懂，但机器没法执行）→ 发给模型当提醒
//   - 写了 when/then（机器可执行）→ **短路**：直接产出改动，不问模型
//
// 设计上**只有一套 schema**，不给用户两种写法：when/then 就是 trigger/action
// 的机器可读版本。trigger/action 保留为给人看的说明与给模型的提示，
// when/then 缺省时该规则自动降级为"仅提示"。
type Rule struct {
	Name    string   `yaml:"name"`
	Trigger string   `yaml:"trigger"` // 人话：什么情况下适用（同时作为给模型的提示）
	Action  string   `yaml:"action"`  // 人话：该做什么（同时作为给模型的提示）
	Forbid  []string `yaml:"forbid"`  // 硬性护栏列名，LLM 也动不了

	// ---------- 机器可执行部分（有则短路，无则只当提示） ----------
	// When 描述"这条规则管哪些进来的数据"。全部条件都满足才算命中（AND）。
	When *RuleWhen `yaml:"when,omitempty"`
	// Then 描述"命中后怎么改表"。只有 when/then 齐全且合法才短路。
	Then *RuleThen `yaml:"then,omitempty"`
	// Link 声明表间关联：补上"公式够不到、但人知道有关联"的那种关系。
	// 与 when/then 互不影响——一条规则可以只声明关系而不改表。
	Link *RuleLink `yaml:"link,omitempty"`
}

// RuleLink 声明"改了 To 会牵动 From"，与联动图里 formula 边同义，但由人写死。
//
// 为什么需要它：跨表人工搬运（把 A 表的合计抄进 B 表）在公式上看不出来，
// 联动图就会漏掉这条关系，影响面推断也就漏掉下游的表。人写一句即可补上。
// 声明边一律按 high 置信度处理——是人拍板的，比推断可靠。
type RuleLink struct {
	From string `yaml:"from"`           // 被牵动的表（下游）：改了 To 要同步看它
	To   string `yaml:"to"`             // 改动发生的表（上游）
	Note string `yaml:"note,omitempty"` // 为什么有关联（给人看）
}

// RuleWhen 是规则的匹配条件。**全部字段都是可选的，但至少要给一个**，
// 否则这条规则会匹配所有进来的数据（那几乎肯定不是用户想要的）。
type RuleWhen struct {
	// File 命中条件：inbox 文件名（不含扩展名）包含该子串（不区分大小写）。
	File string `yaml:"file,omitempty"`
	// Format 命中条件：inbox 文件格式（csv / xlsx）。
	Format string `yaml:"format,omitempty"`
	// HasColumns 命中条件：inbox 数据必须**同时包含**这些列（按表头名，忽略空格）。
	// 这是最稳的判据——"这批数据里有 铺位 和 实收 两列"比文件名可靠。
	HasColumns []string `yaml:"has_columns,omitempty"`
}

// RuleThen 是规则的执行动作。**取值方式只有一种：从 inbox 行里取列**——
// 不做算术、不做跨表跳转，那类需求交给模型。
//
// 为什么刻意做窄：规则的价值是"确定、可复现、不花 token"，
// 一旦允许规则里写表达式，人就失去"看一眼就知道它干什么"的能力，
// 而安全边界（确认制 + 账目 + forbid）反而更难保证。
type RuleThen struct {
	// Sheet 写哪张工作表（空=第一张）。
	Sheet string `yaml:"sheet,omitempty"`
	// Key 定位用：表里的键列名 → inbox 里取值的列名。
	// 例：{"铺位": "铺位号"} 表示"用 inbox 的 铺位号 列，去表里匹配 铺位 列那一行"。
	Key map[string]string `yaml:"key,omitempty"`
	// Field 定位用：要写的字段列名 → inbox 里取值的列名。
	// 例：{"本月实收": "实收金额"} 表示"把 inbox 的 实收金额 写进 本月实收 列"。
	Field map[string]string `yaml:"field,omitempty"`
	// MonthFrom 该表列是"按月的日期序列号"时用：从 inbox 哪一列取月份（如 月份）。
	// 给了它就走"月列定位"（LocateMonthCell），否则走"键行 + 字段列"定位。
	MonthFrom string `yaml:"month_from,omitempty"`
	// Op set（覆盖，默认）| add（累加）。add 用于"这笔是新增一笔"的场景。
	Op string `yaml:"op,omitempty"`
	// TargetFile 目标文件名（不含扩展名）。给了它就不必再让模型分诊"该进哪张表"。
	TargetFile string `yaml:"target_file,omitempty"`
}

// RulesFile 是 rules.yaml 的顶层结构。
type RulesFile struct {
	Rules []Rule `yaml:"rules"`
}

// State 是 state.yaml（滚动摘要）。
type State struct {
	Updated         string            `yaml:"updated"`
	Tables          map[string]string `yaml:"tables"`
	OpenIssues      []string          `yaml:"open_issues"`
	RecentDecisions []string          `yaml:"recent_decisions"`
}

// Load 读取工作区记忆文件（缺失时给合理空值，不报错）。
func Load(rulesPath, statePath string) (rules RulesFile, state State, rulesRaw, stateRaw string, err error) {
	rb, _ := os.ReadFile(rulesPath)
	sb, _ := os.ReadFile(statePath)
	rulesRaw, stateRaw = string(rb), string(sb)
	if len(rb) > 0 {
		if err := yaml.Unmarshal(rb, &rules); err != nil {
			return rules, state, rulesRaw, stateRaw, fmt.Errorf("解析 rules.yaml: %w", err)
		}
	}
	if len(sb) > 0 {
		if err := yaml.Unmarshal(sb, &state); err != nil {
			// state.yaml 只是速读版，解析失败不致命
			state = State{}
		}
	}
	return rules, state, rulesRaw, stateRaw, nil
}

// ForbidSet 汇总所有规则的禁止列（归一化小写、去空格）。
func (r RulesFile) ForbidSet() map[string]bool {
	set := make(map[string]bool)
	for _, rule := range r.Rules {
		for _, col := range rule.Forbid {
			set[normCol(col)] = true
		}
	}
	return set
}

// Kind 返回这条规则属于哪一类（决定它"做完了没有"该怎么判）。
//
// 为什么要分类：一条只声明 link（表间关联）的规则是**完整**的——它的全部用途
// 就是把人的判断喂给联动图，不需要 when/then。若按"能短路改表"去判，
// 它会被误报成"跑不起来"，用户就会以为自己写漏了东西。
const (
	// KindEdge 改表：when 命中 → then 直接产出一处改动（可短路）。
	KindEdge = "edge"
	// KindLink 只声明表间关联，不改表。
	KindLink = "link"
	// KindForbid 只设护栏（不许动哪些列），不改表。
	KindForbid = "forbid"
	// KindHint 只写了人话（trigger/action），交给模型当提示。
	KindHint = "hint"
)

// Kind 判定这条规则的类别。优先级：能改表的 > 声明关系 > 护栏 > 纯提示。
func (r Rule) Kind() string {
	if r.Then != nil || r.When != nil {
		return KindEdge
	}
	if r.Link != nil {
		return KindLink
	}
	if len(r.Forbid) > 0 {
		return KindForbid
	}
	return KindHint
}

// Executable 判断这条规则是否能被机器直接执行（短路）。
// 判据是**结构完整性**，不是"字段非空"——因为半条规则执行起来比不执行更危险：
// 少一个 key 就可能定位到错的行走错的行，用户还以为规则替他办了。
//
// 只声明 link 或只设 forbid 的规则**不需要** when/then，它们天然"完整"，
// 所以不算"跑不起来"（那是另一类用途，不是写漏了）。
func (r Rule) Executable() error {
	switch r.Kind() {
	case KindLink:
		if strings.TrimSpace(r.Link.From) == "" || strings.TrimSpace(r.Link.To) == "" {
			return fmt.Errorf("link 要同时给 from（下游表）与 to（上游表），少一半等于连错")
		}
		return nil
	case KindForbid:
		return nil // 护栏只要列名非空即可，ForbidSet 会取
	case KindHint:
		return fmt.Errorf("只写了 trigger/action（人话），引擎不会执行它；" +
			"要自动办就补 when 与 then，要声明表间关联就补 link")
	}
	if r.When == nil {
		return fmt.Errorf("缺少 when（这条规则管哪些进来的数据）")
	}
	if r.When.File == "" && r.When.Format == "" && len(r.When.HasColumns) == 0 {
		return fmt.Errorf("when 至少要给一个条件（file / format / has_columns），否则它会命中所有数据")
	}
	if r.Then == nil {
		return fmt.Errorf("缺少 then（命中后怎么改表）")
	}
	if len(r.Then.Key) == 0 && r.Then.MonthFrom == "" {
		return fmt.Errorf("then 缺少定位方式：要么给 key（按行匹配），要么给 month_from（按月定位）")
	}
	if len(r.Then.Field) != 1 {
		return fmt.Errorf("then.field 必须**恰好一个**字段映射（拿到 %d 个）：一条规则一次改一个字段，"+
			"要改多个字段就写多条规则", len(r.Then.Field))
	}
	if r.Then.MonthFrom != "" && len(r.Then.Key) == 0 {
		return fmt.Errorf("then.month_from 需要配合 key 使用（先按 key 找到行，再按月份找到列）")
	}
	if op := strings.ToLower(strings.TrimSpace(r.Then.Op)); op != "" && op != "set" && op != "add" {
		return fmt.Errorf("then.op 只能是 set 或 add（拿到 %q）", r.Then.Op)
	}
	return nil
}

// Declared 该规则是否声明了目标文件（有则跳过分诊，省一次模型调用）。
func (r Rule) Declared() bool {
	return r.Then != nil && strings.TrimSpace(r.Then.TargetFile) != ""
}

// ExecutableRules 挑出全部"结构完整"的规则（能做它那一类该做的事）；
// 同时返回**不完整的原因**（要让人看见，不能悄悄降级——用户以为规则在跑，
// 其实一直在问模型，是更坏的情况）。
//
// 注意：这里包含 link/forbid 类规则（它们结构上就是完整的）。
// 要拿"能改表的规则"去短路，用 PlanRules——两类混用会把 link 规则也当计划展开。
func (r RulesFile) ExecutableRules() (ok []Rule, notOK []RuleProblem) {
	for _, rule := range r.Rules {
		if err := rule.Executable(); err != nil {
			notOK = append(notOK, RuleProblem{Name: rule.Name, Reason: err.Error()})
			continue
		}
		ok = append(ok, rule)
	}
	return ok, notOK
}

// PlanRules 只挑出**能改表**的规则（KindEdge 且结构完整）——短路路径专用。
// link / forbid / hint 规则不会产出改动，交到这里只会白跑一趟。
func (r RulesFile) PlanRules() []Rule {
	var out []Rule
	for _, rule := range r.Rules {
		if rule.Kind() != KindEdge {
			continue
		}
		if err := rule.Executable(); err != nil {
			continue
		}
		out = append(out, rule)
	}
	return out
}

// DeclaredLinks 汇总所有规则里声明的关系（供联动图合并）。
// 节点用 "文件!sheet" 或裸 "sheet" 都可以——由 graph.AddDeclared 去解析。
func (r RulesFile) DeclaredLinks() []DeclaredLink {
	var out []DeclaredLink
	for _, rule := range r.Rules {
		if rule.Link == nil {
			continue
		}
		from, to := strings.TrimSpace(rule.Link.From), strings.TrimSpace(rule.Link.To)
		if from == "" || to == "" {
			continue // 半条关系不生效（少一半等于连错，比不连更坏）
		}
		out = append(out, DeclaredLink{From: from, To: to, Rule: rule.Name, Note: rule.Link.Note})
	}
	return out
}

// DeclaredLink 是一条人工声明的表间关系（已去掉半条无效的）。
type DeclaredLink struct {
	From string `json:"from"`
	To   string `json:"to"`
	Rule string `json:"rule,omitempty"`
	Note string `json:"note,omitempty"`
}

// RuleProblem 是一条"写了但跑不起来"的规则。
type RuleProblem struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// Save 把规则写回 rules.yaml。
//
// 写入前**整体校验**：一条规则跑不起来，仍然让它存（用户可能在写草稿），
// 但会把问题一并返回给调用方去提示。硬失败（如文件不可写）才返回 error。
//
// 用"写临时文件再改名"保证原子性：写到一半断电不会留下半截 rules.yaml——
// 这个文件直接决定改表行为，读到一个坏文件比读不到更危险。
func (r RulesFile) Save(path string) error {
	b, err := yaml.Marshal(r)
	if err != nil {
		return fmt.Errorf("序列化规则: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("写规则临时文件: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("替换规则文件: %w", err)
	}
	return nil
}

// Parse 从一段 YAML 文本解析规则（供界面导入/校验用，不落盘）。
func Parse(text string) (RulesFile, error) {
	var rf RulesFile
	if strings.TrimSpace(text) == "" {
		return rf, nil
	}
	if err := yaml.Unmarshal([]byte(text), &rf); err != nil {
		return rf, fmt.Errorf("解析 rules.yaml: %w", err)
	}
	return rf, nil
}

// Describe 给出一条规则的人话说明（界面/账目共用）。
func (r Rule) Describe() string {
	var kind []string
	if err := r.Executable(); err == nil {
		kind = append(kind, "可执行（命中即办，不问模型）")
	} else if r.When != nil || r.Then != nil {
		kind = append(kind, "不可执行："+err.Error())
	} else {
		kind = append(kind, "仅提示（写全 when/then 才能自动执行）")
	}
	if len(r.Forbid) > 0 {
		kind = append(kind, "护栏列："+strings.Join(r.Forbid, "、"))
	}
	return strings.Join(kind, "；")
}

// Describe 把 state 格式化成发给脑的多行文本。
func (s State) Describe() string {
	if s.Updated == "" && len(s.Tables) == 0 && len(s.OpenIssues) == 0 {
		return "（无滚动摘要）\n"
	}
	var b []string
	if s.Updated != "" {
		b = append(b, "摘要更新时间: "+s.Updated)
	}
	if len(s.Tables) > 0 {
		names := make([]string, 0, len(s.Tables))
		for k := range s.Tables {
			names = append(names, k)
		}
		sort.Strings(names)
		b = append(b, "各表近期情况:")
		for _, k := range names {
			b = append(b, "  "+k+": "+s.Tables[k])
		}
	}
	if len(s.OpenIssues) > 0 {
		b = append(b, "未决问题:")
		for _, i := range s.OpenIssues {
			b = append(b, "  - "+i)
		}
	}
	if len(s.RecentDecisions) > 0 {
		b = append(b, "近期决定:")
		for _, d := range s.RecentDecisions {
			b = append(b, "  - "+d)
		}
	}
	return joinLines(b)
}

func normCol(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "")
	return strings.ToLower(s)
}

func joinLines(b []string) string {
	out := ""
	for _, l := range b {
		out += l + "\n"
	}
	return out
}
