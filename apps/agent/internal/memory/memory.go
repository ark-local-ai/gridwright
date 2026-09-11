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
type Rule struct {
	Name    string   `yaml:"name"`
	Trigger string   `yaml:"trigger"`
	Action  string   `yaml:"action"`
	Forbid  []string `yaml:"forbid"` // 硬性护栏列名，LLM 也动不了
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
