// Package prompts 是"快捷任务模板"（见 docs 的复核反馈 → 沉淀设计）。
//
// 一次成功的改表对话（尤其是经过几轮复核纠正后确认的那种），值得固化成
// 可复用的模板：下次同类事情一句话触发，仍走 /chat → /plan → 确认 的同一条路。
//
// 它和隔壁几个存储的分工：
//   - rules.yaml   ：命中即短路、不调模型，确定但表达力刻意做窄
//   - term-map.yaml：把模糊词映射到明确的表/字段
//   - memory.json  ：沉淀"事实/决策"，影响模型判断
//   - prompts.yaml ：沉淀"怎么说这件事"，作为一个快捷入口，**仍然要模型+确认**
//
// 存成 YAML 放**工作区里**（快捷任务跟着业务走），人手可编辑。
package prompts

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Prompt 是一个快捷任务模板。
type Prompt struct {
	// ID 稳定标识：账目/引用靠它认人。缺省由 Name 派生。
	ID string `yaml:"id" json:"id"`
	// Name 快捷任务名（给列表看，短）。
	Name string `yaml:"name" json:"name"`
	// When 什么时候用（一句话，给人判断该点哪个）。
	When string `yaml:"when,omitempty" json:"when,omitempty"`
	// Template 指令模板，含 {槽位} 占位符，本身就是一句完整指令。
	Template string `yaml:"template" json:"template"`
	// Slots 触发时要填的槽位。由 Template 派生，落盘只为可读。
	Slots []string `yaml:"slots,omitempty" json:"slots,omitempty"`
	// Note 触发后要注意什么（可选，给人看）。
	Note string `yaml:"note,omitempty" json:"note,omitempty"`
	// Source 出处：对话:<id> | manual。
	Source string `yaml:"source,omitempty" json:"source,omitempty"`
	// Created / Hits 用于排序与淘汰。
	Created string `yaml:"created,omitempty" json:"created,omitempty"`
	Hits    int    `yaml:"hits,omitempty" json:"hits,omitempty"`
}

// File 是 prompts.yaml 的形状。
type File struct {
	Prompts []Prompt `yaml:"prompts"`
}

// Store 是某工作区的快捷任务表。
type Store struct {
	mu   sync.Mutex
	path string
	f    File
}

// Open 打开（或创建）工作区的 prompts.yaml。
func Open(workspaceRoot string) (*Store, error) {
	p := filepath.Join(workspaceRoot, "prompts.yaml")
	s := &Store{path: p}
	b, err := os.ReadFile(p)
	if err == nil {
		_ = yaml.Unmarshal(b, &s.f) // 损坏当空表，不阻塞
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("读取 prompts.yaml: %w", err)
	}
	return s, nil
}

// Path 返回文件路径。
func (s *Store) Path() string { return s.path }

// All 返回全部模板（按命中次数、再按名字排序：常用的靠前）。
func (s *Store) All() []Prompt {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Prompt, len(s.f.Prompts))
	copy(out, s.f.Prompts)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Hits != out[j].Hits {
			return out[i].Hits > out[j].Hits
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Get 按 id 找一条。
func (s *Store) Get(id string) (Prompt, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.f.Prompts {
		if p.ID == id {
			return p, true
		}
	}
	return Prompt{}, false
}

// Save 写入一条模板（同 id 更新；无 id 时按 name 去重）。
//
// 槽位一律**由 template 重算**，不信任调用方传的 Slots——模板改过而槽位没跟着改，
// 界面上就会缺输入框或给出多余的。以模板为准是唯一可靠的做法。
func (s *Store) Save(p Prompt) (Prompt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p.Name = strings.TrimSpace(p.Name)
	p.Template = strings.TrimSpace(p.Template)
	if p.Name == "" {
		return Prompt{}, fmt.Errorf("缺少 name")
	}
	if p.Template == "" {
		return Prompt{}, fmt.Errorf("缺少 template")
	}
	if p.ID == "" {
		p.ID = slugID(p.Name)
	}
	p.Slots = ExtractSlots(p.Template)
	if p.Source == "" {
		p.Source = "manual"
	}
	if p.Created == "" {
		p.Created = time.Now().Format("2006-01-02 15:04")
	}
	for i := range s.f.Prompts {
		if s.f.Prompts[i].ID == p.ID {
			// 命中次数是使用统计，不该被这次编辑清零
			p.Hits = s.f.Prompts[i].Hits
			if p.Created == "" {
				p.Created = s.f.Prompts[i].Created
			}
			s.f.Prompts[i] = p
			return p, s.saveLocked()
		}
	}
	s.f.Prompts = append(s.f.Prompts, p)
	return p, s.saveLocked()
}

// Delete 删掉一条。
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.f.Prompts[:0]
	for _, p := range s.f.Prompts {
		if p.ID != id {
			out = append(out, p)
		}
	}
	s.f.Prompts = out
	return s.saveLocked()
}

// Bump 记一次使用（用于排序）。
func (s *Store) Bump(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.f.Prompts {
		if s.f.Prompts[i].ID == id {
			s.f.Prompts[i].Hits++
		}
	}
	_ = s.saveLocked()
}

// slotRe 匹配 {槽位}。槽位名不含花括号，允许中文/数字/字母。
var slotRe = regexp.MustCompile(`\{([^{}\n]{1,20})\}`)

// ExtractSlots 从模板里抽出 `{槽位}` 占位符，按出现顺序去重。
//
// 为什么放在这里而不是让模型给：模型会漏、会多给、会和模板对不上。
// 模板是唯一事实来源，槽位必须从它算出来。
func ExtractSlots(template string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range slotRe.FindAllStringSubmatch(template, -1) {
		name := strings.TrimSpace(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// Render 用槽位取值拼出最终指令。未提供或为空的槽位保留原占位符——
// 让人在对话框里能看见还差什么，而不是被替换成空字符串、发出去少一个值。
func Render(template string, values map[string]string) string {
	return slotRe.ReplaceAllStringFunc(template, func(m string) string {
		name := strings.TrimSpace(m[1 : len(m)-1])
		if v, ok := values[name]; ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		return m
	})
}

// Builtins 返回随包内置的通用模板骨架（离线、可审计）。
//
// 它们不是"已保存的快捷任务"，只是给用户一个起点：选中后填槽位/改名再存。
// 为什么不从网上下载：本应用本地优先、且模板会被喂给模型去动财务表——
// 下载来的模板既要求联网，又让用户难以审阅，风险面明显变大。
func Builtins() []Prompt {
	raw := []Prompt{
		{
			Name:     "收租录账",
			When:     "收到一笔租金实收，要记进当月表时",
			Template: "记一笔：{铺位} 收到 {月份} 租金 {金额}，累加进本月实收",
			Note:     "按物业位置匹配铺位；收款用累加（add），不要覆盖。",
		},
		{
			Name:     "补录实收",
			When:     "漏记或更正某铺位某月的实收时",
			Template: "把 {铺位} 的 {月份} 本月实收 改成 {金额}",
			Note:     "更正用覆盖（set），会记旧值可回滚。",
		},
		{
			Name:     "按列匹配补录",
			When:     "手上有名单/明细，要按某列匹配写进台账时",
			Template: "按「{匹配列}」把我给的名单里的「{值列}」写进 {目标表}；查不到就新增一行",
			Note:     "用 upsert：找到行就覆盖几列，找不到就表尾新增。",
		},
		{
			Name:     "月末汇总核对",
			When:     "月底想核对应收/实收/欠款对不对时",
			Template: "核对 {月份} 各铺位的应收合计、本月实收、本月欠款是否对得上，对不上的列出来",
			Note:     "只列不符项，不改表。",
		},
		{
			Name:     "滞纳金检查",
			When:     "想知道哪些铺位欠租、该收多少滞纳金时",
			Template: "检查 {月份} 有哪些铺位欠款，按 {天数} 天计滞纳金，算出金额",
			Note:     "先把口径说清楚，避免模型猜公式。",
		},
		{
			Name:     "跨表同步",
			When:     "改了主数据后，要把引用它的表一起更新时",
			Template: "我改了 {源表} 的 {字段}，把相关的月份表里对应铺位的 {字段} 一起更新",
			Note:     "会先列出牵动的表，确认后再写。",
		},
	}
	for i := range raw {
		raw[i].Source = "builtin"
		raw[i].Slots = ExtractSlots(raw[i].Template)
	}
	return raw
}

// slugID 由名字派生一个稳定 id（中文也可用，去掉空白与危险字符）。
func slugID(name string) string {
	id := strings.TrimSpace(name)
	repl := strings.NewReplacer(" ", "-", "\t", "-", "/", "-", "\\", "-", ":", "-", ".", "-")
	id = repl.Replace(id)
	if id == "" {
		id = fmt.Sprintf("p%d", time.Now().UnixNano())
	}
	return id
}

func (s *Store) saveLocked() error {
	b, err := yaml.Marshal(&s.f)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o644)
}
