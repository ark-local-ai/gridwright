package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/generate"
	"github.com/ark-local-ai/ark/apps/agent/internal/locate"
)

// 生成类任务（见 docs/agent-architecture/27-生成类任务.md）。
//
// POST /api/v1/generate —— 给一句需求（或直接给规格）→ 产出新文件到 生成/
// **绝不碰原表**；表格类由代码筛算，文书类由代码取数后交模型组织语言。

type genReq struct {
	// Instruction 自然语言需求（"出一份 9 月欠租清单"）；给了就走模型翻规格
	Instruction string `json:"instruction"`
	// Spec 直接给规格（不配模型也能用；也便于界面/脚本精确控制）
	Spec *generate.Spec `json:"spec"`
	// DryRun 只返回将要生成什么，不写文件
	DryRun bool `json:"dryRun"`
}

// handleGenerate POST /api/v1/generate
func (s *Server) handleGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req genReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	cfg, layout, _, _ := s.cur()

	files, err := layout.DataFiles()
	if err != nil || len(files) == 0 {
		writeErr(w, http.StatusBadRequest, "工作区没有 .xlsx 表")
		return
	}

	// ① 规格：直接给就用；否则让模型翻（需要配模型）
	spec := req.Spec
	if spec == nil {
		if strings.TrimSpace(req.Instruction) == "" {
			writeErr(w, http.StatusBadRequest, "需要 instruction 或 spec")
			return
		}
		if !cfg.BrainReady() {
			writeErr(w, http.StatusBadRequest,
				"还没配置模型（脑）：生成类任务需要它把需求翻成筛选规格。"+
					"也可以直接给 spec（结构化规格），那样不需要模型。")
			return
		}
		got, err := s.specFromInstruction(r, req.Instruction, files)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		spec = got
	}
	if spec.Kind == "" {
		spec.Kind = generate.KindTable
	}

	// ② 定位源表
	target := pickFile(files, spec.Source.File)
	if target == "" {
		target = files[0]
	}

	// ③ 执行（代码）
	f, err := excelize.OpenFile(target)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "打开源表失败："+err.Error())
		return
	}
	cols, rows, sum, err := generate.Extract(f, *spec)
	f.Close()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.DryRun {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "dryRun": true, "spec": spec,
			"columns": cols, "rows": len(rows),
			"message": fmt.Sprintf("将生成一份含 %d 行的表（未写文件）", len(rows)),
		})
		return
	}

	// ④ 落盘
	switch spec.Kind {
	case generate.KindDoc:
		content, err := s.writeDocContent(r, *spec, cols, rows)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		res, err := generate.WriteDoc(layout.Root, titleOf(*spec), content)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		res.Total = len(rows)
		writeJSON(w, http.StatusOK, res)
	default:
		res, err := generate.WriteTable(layout.Root, *spec, cols, rows, sum)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		res.Total = len(rows)
		writeJSON(w, http.StatusOK, res)
	}
}

// specFromInstruction 让模型把一句需求翻成筛选规格（**模型只出规格，不执行**）。
func (s *Server) specFromInstruction(r *http.Request, instruction string, files []string) (*generate.Spec, error) {
	brain := s.brainClient()

	// 给出表结构，模型才知道有哪些列可筛
	var b strings.Builder
	b.WriteString("# 任务\n把用户的需求翻译成一份「筛选规格」，不要自己算数。\n\n# 用户说\n")
	b.WriteString(strings.TrimSpace(instruction))
	b.WriteString("\n\n# 可用的表与列\n")
	for _, fp := range files {
		f, err := excelize.OpenFile(fp)
		if err != nil {
			continue
		}
		base := filepath.Base(fp)
		for _, sh := range f.GetSheetList() {
			hdrs := headerOf(f, sh)
			if len(hdrs) == 0 {
				continue
			}
			fmt.Fprintf(&b, "- 文件「%s」工作表「%s」列：%s\n", base, sh, strings.Join(hdrs, " / "))
		}
		f.Close()
	}
	b.WriteString(`
# 输出要求
只输出一个 JSON 对象，不要解释：
{"kind":"table|doc","source":{"file":"文件名.xlsx","sheet":"工作表名"},
 "filter":[{"column":"列名","op":"gt|lt|ge|le|eq|ne|notEmpty|empty|contains","value":"值"}],
 "columns":["要输出的列"],"title":"标题","instruction":"（doc 时给模型的额外要求）"}
规则：
- **只输出规格，不要输出数据**。筛选由程序执行，数字由程序计算。
- column 必须是上面列出的真实列名。
- 用户说"欠租/未收"通常指 欠款列 > 0；说"已收"指 实收列 > 0。不确定就用 contains 或不要乱加条件。
- 若需求不明确（不知道筛哪张表/哪一列），把 filter 留空并让 title 说明，不要猜。`)

	content, err := brain.ChatJSON(r.Context(), b.String())
	if err != nil {
		return nil, err
	}
	var spec generate.Spec
	if err := json.Unmarshal([]byte(content), &spec); err != nil {
		return nil, fmt.Errorf("解析生成规格失败: %w（原始: %s）", err, truncate(content, 300))
	}
	return &spec, nil
}

// writeDocContent 文书类：**代码把真实数据摆好**，交模型组织语言。
func (s *Server) writeDocContent(r *http.Request, spec generate.Spec, cols []string, rows [][]string) (string, error) {
	brain := s.brainClient()

	var b strings.Builder
	b.WriteString("# 任务\n根据下面的真实数据，写一份")
	b.WriteString(titleOf(spec))
	b.WriteString("。\n\n**只组织语言，不要改动任何数字**。\n\n# 真实数据（由程序从表中取出）\n")
	b.WriteString(strings.Join(cols, " | "))
	b.WriteString("\n")
	for _, row := range rows {
		b.WriteString(strings.Join(row, " | "))
		b.WriteString("\n")
	}
	if spec.Instruction != "" {
		b.WriteString("\n# 额外要求\n" + spec.Instruction + "\n")
	}
	b.WriteString("\n直接输出正文（Markdown），不要额外解释。数字必须与上面完全一致。")

	content, err := brain.ChatText(r.Context(), b.String())
	if err != nil {
		return "", err
	}
	return content, nil
}

// headerOf 取一张表的表头。
func headerOf(f *excelize.File, sheet string) []string {
	s, err := locate.LoadSheet(f, sheet)
	if err != nil {
		return nil
	}
	_, hdr := s.FindHeader(10)
	return hdr
}

func pickFile(files []string, name string) string {
	if name == "" {
		return ""
	}
	for _, p := range files {
		if strings.EqualFold(filepath.Base(p), name) ||
			strings.EqualFold(strings.TrimSuffix(filepath.Base(p), ".xlsx"), strings.TrimSuffix(name, ".xlsx")) {
			return p
		}
	}
	return ""
}

func titleOf(spec generate.Spec) string {
	if spec.Title != "" {
		return spec.Title
	}
	return "生成文书"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
