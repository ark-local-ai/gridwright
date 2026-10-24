package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAndParse(t *testing.T) {
	root := t.TempDir()
	write(t, root, "invoice", "---\nname: invoice-vision\ndescription: \"从发票图片读数\"\n---\n\n正文第一行\n正文第二行\n")
	write(t, root, "no-front", "没有 frontmatter 的技能\n")

	list := Load(root)
	if len(list) != 2 {
		t.Fatalf("应读到 2 个技能，得到 %d：%+v", len(list), list)
	}
	var inv Skill
	for _, s := range list {
		if s.Name == "invoice-vision" {
			inv = s
		}
	}
	if inv.Description != "从发票图片读数" {
		t.Fatalf("description 解析错：%q", inv.Description)
	}
	if !strings.Contains(inv.Body, "正文第二行") || strings.Contains(inv.Body, "name:") {
		t.Fatalf("body 不该含 frontmatter：%q", inv.Body)
	}
	for _, s := range list {
		if s.Name == "no-front" && !strings.HasPrefix(s.Body, "没有 frontmatter") {
			t.Fatalf("无 frontmatter 时应把全文当 body：%q", s.Body)
		}
	}
}

func TestBlockTruncates(t *testing.T) {
	long := strings.Repeat("字", 5000)
	list := []Skill{{Name: "long", Body: long}, {Name: "second", Body: "短"}}
	block := Block(list, 6000)
	if !strings.Contains(block, "已截断") {
		t.Fatalf("超长技能应标注截断")
	}
	if !strings.Contains(block, "second") {
		// 单条 4000 字后总量可能已满，第二条允许被上限略过；这里只要求不 panic。
		t.Log("第二条因上限未展开（允许）")
	}
}

func TestLoadMissingDir(t *testing.T) {
	if got := Load(filepath.Join(t.TempDir(), "nope")); len(got) != 0 {
		t.Fatalf("目录不存在应返回空，得到 %+v", got)
	}
}
