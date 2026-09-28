package api

import (
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"
)

// workspaceInventory 把工作区里的表整理成给模型看的清单。
//
// 为什么要到 sheet 这一层：用户说的是业务名（"填进销售明细表"），
// 而「销售明细表」是 sheet、不是文件。只报文件名的结果是模型找不到它，
// 于是把"打开那个工作簿里的这个 sheet"误解成"工作区里没这张表"，
// 白白问一轮澄清（实测发生过，见 chat.go 里的注释）。
//
// 输出形如：
//
//	御城二期销售报表2026.09.13.xlsx（销售明细表、实收日报表、销售汇总表、诚意金汇总表）
//
// 读不出 sheet 的文件退化成只报文件名（不让一个坏文件拖垮整份清单）。
func workspaceInventory(files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		base := filepath.Base(f)
		sheets, err := sheetNames(f)
		if err != nil || len(sheets) == 0 {
			out = append(out, base)
			continue
		}
		out = append(out, base+"（"+strings.Join(sheets, "、")+"）")
	}
	return out
}

// sheetNames 取一个工作簿里的全部工作表名。
func sheetNames(path string) ([]string, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.GetSheetList(), nil
}
