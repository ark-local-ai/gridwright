package agent

import (
	"encoding/json"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/locate"
	"github.com/ark-local-ai/ark/apps/agent/internal/propose"
)

// newUpsertFixture 造一张“有表头 + 一行数据”的表：
//
//	A1 楼栋 | B1 客户名称 | C1 实收金额
//	A2 1栋  | B2 张三     | C2 100
func newUpsertFixture(t *testing.T) *excelize.File {
	t.Helper()
	f := excelize.NewFile()
	t.Cleanup(func() { f.Close() })
	_ = f.SetCellValue("Sheet1", "A1", "楼栋")
	_ = f.SetCellValue("Sheet1", "B1", "客户名称")
	_ = f.SetCellValue("Sheet1", "C1", "实收金额")
	_ = f.SetCellValue("Sheet1", "A2", "1栋")
	_ = f.SetCellValue("Sheet1", "B2", "张三")
	_ = f.SetCellValue("Sheet1", "C2", 100)
	return f
}

// TestResolveUpsertFillsOrCreates 是“按 D 列名字填 E/F、查不到就新增”的最小复现：
// 查到 → set；查不到 → append（且新行必须带上业务键）。
//
// 为什么由代码来分 set / append：模型看不到整张表（几百上千行），
// 它只能给“业务键 + 要写的列”；这个键在不在表里只有拿表去查才知道。
func TestResolveUpsertFillsOrCreates(t *testing.T) {
	f := newUpsertFixture(t)
	s, err := locate.LoadSheet(f, "Sheet1")
	if err != nil {
		t.Fatal(err)
	}
	hdrIdx, header := s.FindHeader(10)

	// 已存在 → set
	items, blocked := resolveUpsert(s, f, "t.xlsx", semanticEdit{
		Sheet: "Sheet1", Key: map[string]string{"客户名称": "张三"},
		Row: map[string]any{"实收金额": 200}, Op: "upsert",
	}, hdrIdx+1, header)
	if blocked != nil {
		t.Fatalf("查得到时不该被挡：%+v", blocked)
	}
	if len(items) != 1 || items[0].Op != "set" {
		t.Fatalf("应产出 1 条 set，得到 %+v", items)
	}
	if items[0].Ref != "C2" {
		t.Errorf("应定位到 C2，得到 %s", items[0].Ref)
	}
	if items[0].New != 200 {
		t.Errorf("新值应为 200，得到 %v", items[0].New)
	}
	if items[0].Old != "100" {
		t.Errorf("旧值应读出 100，得到 %v", items[0].Old)
	}

	// 查不到 → append
	items2, blocked2 := resolveUpsert(s, f, "t.xlsx", semanticEdit{
		Sheet: "Sheet1", Key: map[string]string{"客户名称": "李四"},
		Row: map[string]any{"楼栋": "2栋", "实收金额": 300}, Op: "upsert",
	}, hdrIdx+1, header)
	if blocked2 != nil {
		t.Fatalf("查不到时应新增，而不是被挡：%+v", blocked2)
	}
	if len(items2) != 1 || items2[0].Op != "append" {
		t.Fatalf("应产出 1 条 append，得到 %+v", items2)
	}
	if items2[0].HeaderRow != hdrIdx+1 {
		t.Errorf("表头行应为 %d，得到 %d", hdrIdx+1, items2[0].HeaderRow)
	}
	if items2[0].Values["客户名称"] != "李四" {
		t.Errorf("新行必须带上业务键（否则以后再也匹配不到）：%+v", items2[0].Values)
	}
	if items2[0].Values["楼栋"] != "2栋" || items2[0].Values["实收金额"] != 300 {
		t.Errorf("新行没带全要写的列：%+v", items2[0].Values)
	}
}

// TestResolveUpsertRejectsAmbiguity 同一个键匹配到多行时必须挡下，
// 不能替人决定“改哪一行”。
func TestResolveUpsertRejectsAmbiguity(t *testing.T) {
	f := newUpsertFixture(t)
	// 再加一行同名客户（真实台账里客户名重复很常见）
	_ = f.SetCellValue("Sheet1", "A3", "3栋")
	_ = f.SetCellValue("Sheet1", "B3", "张三")
	_ = f.SetCellValue("Sheet1", "C3", 999)

	s, _ := locate.LoadSheet(f, "Sheet1")
	hdrIdx, header := s.FindHeader(10)
	_, blocked := resolveUpsert(s, f, "t.xlsx", semanticEdit{
		Sheet: "Sheet1", Key: map[string]string{"客户名称": "张三"},
		Row: map[string]any{"实收金额": 1}, Op: "upsert",
	}, hdrIdx+1, header)
	if blocked == nil {
		t.Fatal("键匹配到多行时应挡下")
	}
}

// TestResolveUpsertRejectsUnknownColumn 列名不在表头 → 直接拒绝，不猜列。
func TestResolveUpsertRejectsUnknownColumn(t *testing.T) {
	f := newUpsertFixture(t)
	s, _ := locate.LoadSheet(f, "Sheet1")
	hdrIdx, header := s.FindHeader(10)
	_, blocked := resolveUpsert(s, f, "t.xlsx", semanticEdit{
		Sheet: "Sheet1", Key: map[string]string{"客户名称": "王五"},
		Row: map[string]any{"没有这一列": 1}, Op: "upsert",
	}, hdrIdx+1, header)
	if blocked == nil {
		t.Fatal("列名不在表头时应拒绝")
	}
}

// TestResolveEditsDispatchesUpsert upsert 走新的多条目通道，set/add 仍走老的单条通道。
func TestResolveEditsDispatchesUpsert(t *testing.T) {
	f := newUpsertFixture(t)

	// set 单条
	items, blocked := resolveEdits(f, "t.xlsx", semanticEdit{
		Sheet: "Sheet1", Key: map[string]string{"客户名称": "张三"},
		Field: "实收金额", Op: "set", Value: 7,
	})
	if blocked != nil || len(items) != 1 || items[0].Op != "set" {
		t.Fatalf("set 应产出 1 条，得到 %+v / %+v", items, blocked)
	}

	// upsert 多列时查到行 → 每列一条 set
	items2, blocked2 := resolveEdits(f, "t.xlsx", semanticEdit{
		Sheet: "Sheet1", Key: map[string]string{"客户名称": "张三"},
		Row: map[string]any{"楼栋": "9栋", "实收金额": 8}, Op: "upsert",
	})
	if blocked2 != nil {
		t.Fatalf("不该被挡：%+v", blocked2)
	}
	if len(items2) != 2 {
		t.Fatalf("两列应产出 2 条 set，得到 %d 条：%+v", len(items2), items2)
	}
}

// TestSemanticEditNumericKey 模型把纯数字的业务键写成 JSON 数字时也要能解析。
//
// 不这么做的后果：`"key":{"房号":101}` 直接让 json.Unmarshal 报错，
// 整份清单在**解析那一步**就没了——一个格式小毛病把一件能做的事停掉。
func TestSemanticEditNumericKey(t *testing.T) {
	var edits []semanticEdit
	raw := `[{"sheet":"销售明细表","key":{"房号":101},"row":{"实收金额":12000},"op":"upsert"}]`
	if err := json.Unmarshal([]byte(raw), &edits); err != nil {
		t.Fatalf("数字键不该让解析失败：%v", err)
	}
	if len(edits) != 1 {
		t.Fatalf("应解析出 1 条，得到 %d", len(edits))
	}
	if got := edits[0].Key["房号"]; got != "101" {
		t.Errorf("键值应归一成字符串 101，得到 %q", got)
	}
}

// TestFilterForbiddenBlocksModelEdits forbid 护栏对模型路径同样生效：
// 规则是人写的可以信，模型是猜的，更需要拦。
func TestFilterForbiddenBlocksModelEdits(t *testing.T) {
	forbid := map[string]bool{"本月实收": true}
	items := []propose.Item{
		{Op: "set", Field: "本月实收", Sheet: "S", Ref: "E2"},
		{Op: "set", Field: "本月欠款", Sheet: "S", Ref: "F2"},
		{Op: "append", Values: map[string]any{"本月实收": 1, "楼栋": "1栋"}, Sheet: "S"},
	}
	kept, blocked := filterForbidden(items, forbid)
	if len(kept) != 1 || kept[0].Field != "本月欠款" {
		t.Fatalf("只该留下“本月欠款”一条，得到 %+v", kept)
	}
	if len(blocked) != 2 {
		t.Fatalf("应挡下 2 条（含 append 里带禁止列的），得到 %+v", blocked)
	}
	// forbid 为空时原样放行（别把正常路径一并误杀）
	if k2, b2 := filterForbidden(items, nil); len(k2) != 3 || len(b2) != 0 {
		t.Fatalf("无 forbid 时应全部放行，得到 %d/%d", len(k2), len(b2))
	}
}
