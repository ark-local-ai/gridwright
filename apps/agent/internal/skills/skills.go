// Package skills 装配 Agent Skills（agentskills.io 的 SKILL.md 目录格式）。
//
// 为什么是“装配”而不是自己实现一套：技能只是**指令包**，格式已经被
// anthropics/skills、TerminalSkills/skills 等仓库统一。这里只做三件事：
// 找到目录、读 frontmatter、把正文拼进提示词。不执行技能里的脚本——
// 引擎没有任意代码执行沙箱，装了带脚本的技能也只会用到它的说明部分。
//
// 查找顺序（同名以先找到的为准）：
//  1. <workspace>/skills/<name>/SKILL.md   —— 跟工作区走的装配
//  2. <exe>/skills/<name>/SKILL.md         —— 安装包随包的技能
//  3. <cwd>/skills/<name>/SKILL.md         —— 开发时从仓库根跑
package skills

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Skill 是一条 SKILL.md。
type Skill struct {
	Name        string
	Description string
	Body        string
	Dir         string
}

// Load 扫描装配目录。目录不存在不算错误（用户可能一个技能都没装）。
func Load(workspaceRoot string) []Skill {
	var dirs []string
	if workspaceRoot != "" {
		dirs = append(dirs, filepath.Join(workspaceRoot, "skills"))
	}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Join(filepath.Dir(exe), "skills"))
	}
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs, filepath.Join(wd, "skills"))
	}

	seen := map[string]bool{}
	var out []Skill
	for _, dir := range dirs {
		abs, err := filepath.Abs(dir)
		if err != nil || seen[abs] {
			continue
		}
		seen[abs] = true
		out = append(out, loadDir(abs)...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func loadDir(dir string) []Skill {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Skill
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name(), "SKILL.md")
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		s := parse(string(b))
		if s.Name == "" {
			s.Name = e.Name()
		}
		s.Dir = filepath.Join(dir, e.Name())
		if strings.TrimSpace(s.Body) == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// parse 读最小 YAML frontmatter：只认 name / description，其余键忽略。
// 不引入 YAML 依赖——技能元数据就这两个字段有用。
func parse(raw string) Skill {
	var s Skill
	raw = strings.TrimPrefix(raw, "\ufeff")
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		s.Body = strings.TrimSpace(raw)
		return s
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		s.Body = strings.TrimSpace(raw)
		return s
	}
	for _, ln := range lines[1:end] {
		k, v, ok := strings.Cut(ln, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "name":
			s.Name = v
		case "description":
			s.Description = v
		}
	}
	s.Body = strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
	return s
}

// Block 把技能拼成提示词的一段。有上限：技能正文可能很长，全塞进去会
// 把上下文吃光；单条与总量都截断，并在末尾说清楚“截断了”。
func Block(list []Skill, maxRunes int) string {
	if len(list) == 0 {
		return ""
	}
	const perSkill = 4000
	var b strings.Builder
	total := 0
	b.WriteString("## 已装配的技能（按需遵循）\n")
	b.WriteString("下面是本机装配的 SKILL.md。与当前任务相关的部分照做；无关的忽略，不要向用户复述技能本身。\n")
	for _, s := range list {
		head := "\n### " + s.Name + "\n"
		if s.Description != "" {
			head += s.Description + "\n"
		}
		body := s.Body
		if rs := []rune(body); len(rs) > perSkill {
			body = string(rs[:perSkill]) + "\n（技能过长，已截断）"
		}
		chunk := head + body + "\n"
		if total+len([]rune(chunk)) > maxRunes {
			b.WriteString("\n（更多技能因上下文上限未展开）\n")
			break
		}
		total += len([]rune(chunk))
		b.WriteString(chunk)
	}
	return b.String()
}
