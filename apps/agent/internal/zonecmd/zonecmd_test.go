package zonecmd

import (
	"testing"

	"github.com/ark-local-ai/ark/apps/agent/internal/zones"
)

func TestParse(t *testing.T) {
	cases := []struct {
		text   string
		ref    string
		dryRun bool
		ok     bool
	}{
		{"@发票目录 运行", "发票目录", false, true},
		{"@发票目录 试运行", "发票目录", true, true},
		{"@z_abc123 跑一次", "z_abc123", false, true},
		// 中英文空白：全角空格、制表符、换行都算分隔
		{"@发票目录\u3000运行", "发票目录", false, true},
		{"  @发票目录\t试运行\n", "发票目录", true, true},
		// 名字前后多余空格要被剥掉
		{"@  发票目录   运行", "发票目录", false, true},
		// 名称本身含空格时保留内部空格
		{"@发票 目录 试运行", "发票 目录", true, true},
		// 缺 @
		{"发票目录 运行", "", false, false},
		// 缺动作
		{"@发票目录", "", false, false},
		// 只有 @
		{"@", "", false, false},
		{"@   ", "", false, false},
		// 动作在但没区名
		{"@ 运行", "", false, false},
		// 普通对话不该被当成指令
		{"每天下班前体检一次", "", false, false},
		{"帮我看看 @发票目录 的欠款", "", false, false},
		{"", "", false, false},
	}
	for _, c := range cases {
		ref, dry, ok := Parse(c.text)
		if ok != c.ok || ref != c.ref || dry != c.dryRun {
			t.Errorf("Parse(%q) = (%q,%v,%v)，期望 (%q,%v,%v)",
				c.text, ref, dry, ok, c.ref, c.dryRun, c.ok)
		}
	}
}

func TestMatch(t *testing.T) {
	list := []zones.Zone{
		{ID: "z_001", Name: "发票目录"},
		{ID: "z_002", Name: "Rent Ledger"},
		{ID: "z_003", Name: "发票目录(旧)"},
		{ID: "z_004", Name: " 空格区 "},
	}
	cases := []struct {
		ref string
		id  string
		ok  bool
	}{
		{"发票目录", "z_001", true},        // 名称精确
		{"z_002", "z_002", true},       // ID 精确
		{"rent ledger", "z_002", true}, // 名称大小写不敏感
		{"  发票目录  ", "z_001", true},    // 前后空格
		{"空格区", "z_004", true},         // 名称两侧空白
		{"发票", "", false},              // 不做前缀匹配
		{"发票目录(", "", false},           // 不做模糊匹配
		{"z_001 ", "z_001", true},      // ID 带空格
		{"", "", false},
		{"不存在", "", false},
	}
	for _, c := range cases {
		z, ok := Match(list, c.ref)
		if ok != c.ok || z.ID != c.id {
			t.Errorf("Match(%q) = (%q,%v)，期望 (%q,%v)", c.ref, z.ID, ok, c.id, c.ok)
		}
	}
}

func TestMatchEmptyList(t *testing.T) {
	z, ok := Match(nil, "发票目录")
	if ok || z.ID != "" {
		t.Fatalf("空列表不该匹配，得到 %+v", z)
	}
}
