package locate

import (
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

// 两行表头（分组行 + 列名行）必须合成一行，否则上一行独有的列会找不到。
func TestFindHeaderMergesTwoRowHeader(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "两行表头.xlsx")
	f := excelize.NewFile()
	sh := "Sheet1"
	// 按**真实台账的形状**造：分组行稀疏（只标了几组），列名行密集。
	// 这样 FindHeader 会选中列名那行（第 2 行），再向上合并分组行——
	// 与真实文件里"销售明细表"第 3/4 行的关系一致。
	//
	// 第 1 行：分组名（"按揭银行"只在这一行，下面那列为空）
	_ = f.SetCellValue(sh, "A1", "销售基本情况")
	_ = f.SetCellValue(sh, "C1", "按揭银行")
	// 第 2 行：列名（密集；"按揭银行"这一列为空，靠上行补）
	_ = f.SetCellValue(sh, "A2", "楼栋")
	_ = f.SetCellValue(sh, "B2", "房号")
	_ = f.SetCellValue(sh, "D2", "客户名称")
	_ = f.SetCellValue(sh, "A3", "1栋")
	if err := f.SaveAs(p); err != nil { t.Fatal(err) }
	f.Close()

	f2, _ := excelize.OpenFile(p)
	defer f2.Close()
	s, err := LoadSheet(f2, sh)
	if err != nil { t.Fatal(err) }
	_, hdr := s.FindHeader(10)
	if c := ColByHeader(hdr, "楼栋"); c != 0 {
		t.Errorf("楼栋 应在 col0，得到 %d（表头=%v）", c, hdr)
	}
	if c := ColByHeader(hdr, "房号"); c != 1 {
		t.Errorf("房号 应在 col1，得到 %d", c)
	}
	// ★ 只在上行出现的列也必须能找到
	if c := ColByHeader(hdr, "按揭银行"); c != 2 {
		t.Errorf("按揭银行 只在上行，合并后应能找到（col2），得到 %d；表头=%v", c, hdr)
	}
	// 标题行（只有一格）不该被并进来
	if hdr[0] == "销售基本情况" {
		t.Error("同一列上下都有值时该取下一行（列名本身）")
	}
}
