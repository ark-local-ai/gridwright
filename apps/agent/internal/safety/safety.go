// Package safety 在**写入前**检查工作簿里有没有 excelize 会破坏的东西
// （见 docs/agent-architecture/24-语义映射与安全边界.md 第四节）。
//
// 为什么需要：excelize 会重写整个文件。对普通数据表它保真（已用真实台账实测：
// 24 表/98 部件/共享字符串与工作表字节一致），但表里若含宏、数据透视、图表等，
// 重写可能丢失或失效——**财务表丢了这些是灾难，所以宁可提前拦住**。
//
// 设计：只读检查，产出"风险清单"。有高风险时由上层决定拦下还是要求人显式确认。
// **绝不因为检查而修改文件。**
package safety

import (
	"archive/zip"
	"fmt"
	"io"
	"strings"
)

// Level 是风险等级。
const (
	LevelOK    = "ok"    // 未发现风险，可安全写
	LevelWarn  = "warn"  // 可能受影响，建议先备份再写（我们本来就会备份）
	LevelBlock = "block" // 高风险，excelize 会破坏内容，默认不该写
)

// Risk 是一条风险发现。
type Risk struct {
	Level  string `json:"level"`
	Kind   string `json:"kind"` // macro | pivot | chart | externalLink | ...
	Where  string `json:"where,omitempty"`
	Detail string `json:"detail"`
}

// Report 是对一个工作簿的写入安全性评估。
type Report struct {
	File     string `json:"file"`
	Level    string `json:"level"` // 取所有风险里最高的
	Risks    []Risk `json:"risks"`
	Advice   string `json:"advice"`
	CanWrite bool   `json:"canWrite"` // 是否可以（在备份前提下）安全写入
}

// Check 检查一个 xlsx 是否适合用 excelize 回写。
func Check(path string) (*Report, error) {
	rep := &Report{File: path, Level: LevelOK, Risks: []Risk{}}

	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("打开工作簿: %w", err)
	}
	defer zr.Close()

	has := func(pred func(string) bool) (string, bool) {
		for _, f := range zr.File {
			if pred(f.Name) {
				return f.Name, true
			}
		}
		return "", false
	}

	// ① VBA 宏（.xlsm 或内含 vbaProject）
	if name, ok := has(func(n string) bool { return strings.Contains(n, "vbaProject.bin") }); ok {
		rep.Risks = append(rep.Risks, Risk{
			Level: LevelBlock, Kind: "macro", Where: name,
			Detail: "含 VBA 宏。excelize 不支持宏，回写会丢失——这种表不应由程序改。",
		})
	}
	if strings.EqualFold(ext(path), ".xlsm") {
		rep.Risks = append(rep.Risks, Risk{
			Level: LevelBlock, Kind: "macro", Where: path,
			Detail: "是启用宏的工作簿（.xlsm），程序改完会变成普通 xlsx。",
		})
	}

	// ② 数据透视表
	if name, ok := has(func(n string) bool { return strings.Contains(n, "pivotTable") }); ok {
		rep.Risks = append(rep.Risks, Risk{
			Level: LevelWarn, Kind: "pivot", Where: name,
			Detail: "含数据透视表。改动源数据后透视需在 Excel 里刷新；透视定义通常保留但请核对。",
		})
	}
	if name, ok := has(func(n string) bool { return strings.Contains(n, "pivotCache") }); ok {
		rep.Risks = append(rep.Risks, Risk{
			Level: LevelWarn, Kind: "pivotCache", Where: name,
			Detail: "含透视缓存，回写后可能需刷新。",
		})
	}

	// ③ 图表
	if name, ok := has(func(n string) bool {
		return strings.Contains(n, "charts/chart") || strings.Contains(n, "drawings/chart")
	}); ok {
		rep.Risks = append(rep.Risks, Risk{
			Level: LevelWarn, Kind: "chart", Where: name,
			Detail: "含图表。数据改动后图表需在 Excel 里重算；图表定义一般保留，请核对。",
		})
	}

	// ④ 外部工作簿链接（我们是"识别"而不是"破坏"，但要知道）
	if name, ok := has(func(n string) bool { return strings.Contains(n, "externalLink") }); ok {
		rep.Risks = append(rep.Risks, Risk{
			Level: LevelWarn, Kind: "externalLink", Where: name,
			Detail: "含外部工作簿链接。保留，但改动后外部引用需在 Excel 里更新。",
		})
	}

	// ⑤ 检查是否有条件格式/数据验证（可能部分保留）
	for _, f := range zr.File {
		if !strings.Contains(f.Name, "worksheets/sheet") || !strings.HasSuffix(f.Name, ".xml") {
			continue
		}
		head, err := readHead(f, 64*1024)
		if err != nil {
			continue
		}
		if strings.Contains(head, "<conditionalFormatting") {
			rep.Risks = append(rep.Risks, Risk{
				Level: LevelWarn, Kind: "conditionalFormat", Where: f.Name,
				Detail: "含条件格式，excelize 可能部分保留；改动后请核对格式是否还在。",
			})
			break
		}
	}

	rep.Level = highest(rep.Risks)
	rep.CanWrite = rep.Level != LevelBlock
	rep.Advice = adviceFor(rep.Level, rep.Risks)
	return rep, nil
}

// Describe 给人看的简短结论。
func (r *Report) Describe() string {
	switch r.Level {
	case LevelBlock:
		return "⚠️ 这张表含程序无法安全处理的内容（" + kindsOf(r.Risks) + "），默认不应由程序修改"
	case LevelWarn:
		return "注意：这张表含 " + kindsOf(r.Risks) + "；改动前会自动备份，改完请在 Excel 里核对"
	default:
		return "未发现写入风险（改动前仍会自动备份）"
	}
}

func highest(rs []Risk) string {
	lv := LevelOK
	for _, r := range rs {
		if r.Level == LevelBlock {
			return LevelBlock
		}
		if r.Level == LevelWarn {
			lv = LevelWarn
		}
	}
	return lv
}

func adviceFor(level string, rs []Risk) string {
	switch level {
	case LevelBlock:
		return "建议：不要用程序改这张表。若确实要改，请先另存一份副本，或在 Excel 里手工改。"
	case LevelWarn:
		return "可以改，但改动前会备份；改完请在 Excel 里打开核对图表/透视/条件格式是否正确。"
	default:
		return "可以安全改动（仍会自动备份并逐格记账）。"
	}
}

func kindsOf(rs []Risk) string {
	seen := map[string]bool{}
	var out []string
	label := map[string]string{
		"macro": "宏", "pivot": "数据透视表", "pivotCache": "透视缓存",
		"chart": "图表", "externalLink": "外部链接", "conditionalFormat": "条件格式",
	}
	for _, r := range rs {
		if seen[r.Kind] {
			continue
		}
		seen[r.Kind] = true
		if l, ok := label[r.Kind]; ok {
			out = append(out, l)
		} else {
			out = append(out, r.Kind)
		}
	}
	return strings.Join(out, "、")
}

func readHead(f *zip.File, n int) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	buf := make([]byte, n)
	got, _ := io.ReadFull(rc, buf)
	return string(buf[:got]), nil
}

func ext(p string) string {
	if i := strings.LastIndex(p, "."); i >= 0 {
		return p[i:]
	}
	return ""
}
