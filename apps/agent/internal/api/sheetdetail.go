package api

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/ledger"
	"github.com/ark-local-ai/ark/apps/agent/internal/sheetnotes"
	"github.com/ark-local-ai/ark/apps/agent/internal/weight"
	"github.com/ark-local-ai/ark/apps/agent/internal/xl"
)

// 工作表详情卡（见 gridwright-工作表详述-plan.md）：
//
//	GET  /api/v1/sheets/detail?file=&sheet=   → 描述 + 上次改动 + 改动日期
//	POST /api/v1/sheets/describe              → 立即生成/刷新描述
//
// 描述来自 sheet_notes.json（体检时模型生成并缓存）；上次改动来自账目。
// 两块互不依赖：没配模型 / 没描述时，lastChange 照常有；没有改动时描述照常有。

// maxDescribePerScan 是一次体检里最多为几张表生成描述。
// 目的是别让一次体检触发几百次模型调用（秒级 × 几百 = 分钟级 + 真金白银）。
const maxDescribePerScan = 5

// describeBudget 是一次体检里描述生成的总时间预算。超了就停，剩余的表下次再补。
const describeBudget = 3 * time.Minute

type sheetDetailResp struct {
	File             string `json:"file"`
	Sheet            string `json:"sheet"`
	Description      string `json:"description"`
	DescriptionAt    string `json:"descriptionAt"`
	DescriptionModel string `json:"descriptionModel"`
	LastChange       string `json:"lastChange"`
	LastChangeAt     string `json:"lastChangeAt"`
}

// handleSheetDetail GET /api/v1/sheets/detail?file=&sheet=
func (s *Server) handleSheetDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	q := r.URL.Query()
	sheet := q.Get("sheet")
	if sheet == "" {
		writeErr(w, http.StatusBadRequest, "缺少 sheet 参数")
		return
	}
	target, err := s.pickTable(q.Get("file"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	f, err := excelize.OpenFile(target)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "打开表失败："+err.Error())
		return
	}
	defer f.Close()
	if idx, _ := f.GetSheetIndex(sheet); idx < 0 {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("找不到工作表 %q", sheet))
		return
	}

	base := filepath.Base(target)
	resp := sheetDetailResp{File: base, Sheet: sheet}
	if store := s.notesStore(); store != nil {
		if n, ok := store.Get(nodeKeyForFile(base, sheet)); ok {
			resp.Description, resp.DescriptionAt, resp.DescriptionModel = n.Description, n.At, n.Model
		}
	}
	_, _, led, _ := s.cur()
	entries, _ := led.Entries(500)
	resp.LastChange, resp.LastChangeAt = lastChangeOf(entries, base, sheet)
	writeJSON(w, http.StatusOK, resp)
}

type describeReq struct {
	File  string `json:"file"`
	Sheet string `json:"sheet"`
}

// handleSheetDescribe POST /api/v1/sheets/describe
func (s *Server) handleSheetDescribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req describeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Sheet == "" {
		writeErr(w, http.StatusBadRequest, "缺少 sheet")
		return
	}
	cfg, _, _, _ := s.cur()
	if !cfg.BrainReady() {
		writeErr(w, http.StatusBadRequest, "未配置模型，无法生成描述（看表/体检不受影响）")
		return
	}
	target, err := s.pickTable(req.File)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	note, err := s.describeSheet(r.Context(), target, req.Sheet)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "description": note.Description,
		"at": note.At, "model": note.Model,
	})
}

// describeSheet 生成一张表的描述并落进 sheet_notes.json。
func (s *Server) describeSheet(ctx context.Context, target, sheet string) (sheetnotes.Note, error) {
	st, err := readSheetStructure(target, sheet)
	if err != nil {
		return sheetnotes.Note{}, err
	}
	note, err := s.describeStructure(ctx, target, sheet, st)
	if err != nil {
		return sheetnotes.Note{}, err
	}
	if store := s.notesStore(); store != nil {
		if err := store.Put(nodeKeyForFile(filepath.Base(target), sheet), note); err != nil {
			return note, fmt.Errorf("写入描述缓存失败：%w", err)
		}
	}
	return note, nil
}

// describeStructure 用模型为一张表写描述（不落盘，由调用方决定）。
func (s *Server) describeStructure(ctx context.Context, target, sheet string, st *xl.Structure) (sheetnotes.Note, error) {
	brain := s.brainClient()
	raw, err := brain.ChatText(ctx, buildSheetPrompt(sheet, st))
	if err != nil {
		return sheetnotes.Note{}, err
	}
	desc := cleanDescription(raw)
	if desc == "" {
		return sheetnotes.Note{}, fmt.Errorf("模型没有给出可用的描述（只依据表头与样例说，拿不到信息就留空）")
	}
	return sheetnotes.Note{
		Description: desc,
		At:          time.Now().Format("2006-01-02 15:04"),
		Model:       brain.Model(),
		HeaderHash:  headerHash(st),
	}, nil
}

// buildSheetPrompt 按计划书第 5 节的规格拼 prompt。
//
// 表头走 xl.ReadStructure（WS-7 后它用 locate 找真实表头行，并带列字母），
// 所以这里直接复用，不自己解析。
func buildSheetPrompt(sheet string, st *xl.Structure) string {
	var b strings.Builder
	b.WriteString("你是数据管家。下面给你一张工作表的表头与少量样例数据。\n")
	b.WriteString("用 1–2 句中文说明：这张表是做什么用的、有哪些关键列。\n")
	b.WriteString("只依据给到的内容说；不确定的信息不要写。\n")
	b.WriteString("不要客套、不要 Markdown、不要项目符号，控制在 60 字以内。\n\n")
	b.WriteString("工作表：" + sheet + "\n\n")
	b.WriteString(st.Describe(3))
	return b.String()
}

// cleanDescription 清洗模型输出：去 Markdown/项目符号、压成一行、截断。
// 只留下"能直接显示给人看"的描述；空串表示这次生成不可用（不写库）。
func cleanDescription(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	// 去掉代码块围栏
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	// 压成一行：换行 → 空格，连续空白 → 单个空格
	s = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	// 去掉行首的项目符号/列表序号/"描述："前缀
	s = strings.TrimLeft(s, "-*•· ")
	s = strings.TrimSpace(strings.TrimPrefix(s, "描述："))
	s = strings.TrimSpace(strings.TrimPrefix(s, "描述:"))
	if s == "" {
		return ""
	}
	// 套话/废话判定：宁可留空显示占位，也不要让人不再信任这一栏。
	if isBoilerplate(s) {
		return ""
	}
	// 截断到 200 字（按 rune，避免把汉字截成乱码）
	if utf8.RuneCountInString(s) > 200 {
		r := []rune(s)
		s = strings.TrimSpace(string(r[:200])) + "…"
	}
	return s
}

// isBoilerplate 判断描述是不是"没有信息量的套话"。
// 判据保守，只挡最明显的几种；判错的代价是少一条描述，比多一条废话轻。
func isBoilerplate(s string) bool {
	if utf8.RuneCountInString(s) < 6 {
		return true
	}
	for _, bad := range []string{
		"包含多列数据", "这是一张表格", "这是一张excel表", "无法确定",
		"没有足够信息", "内容为空", "无法描述",
	} {
		if strings.Contains(s, bad) {
			return true
		}
	}
	return false
}

// headerHash 是表头指纹：表头行号 + 各列名。表头变了 → 描述可能过期 → 重算。
func headerHash(st *xl.Structure) string {
	h := fnv.New64a()
	fmt.Fprintf(h, "%d|%s", st.HeaderRow, strings.Join(st.Header, "\x00"))
	return fmt.Sprintf("%x", h.Sum64())
}

// readSheetStructure 打开表读一张工作表的结构（读完即关，结构已拷贝出来）。
func readSheetStructure(target, sheet string) (*xl.Structure, error) {
	f, err := excelize.OpenFile(target)
	if err != nil {
		return nil, fmt.Errorf("打开表失败：%w", err)
	}
	defer f.Close()
	if idx, _ := f.GetSheetIndex(sheet); idx < 0 {
		return nil, fmt.Errorf("找不到工作表 %q", sheet)
	}
	st, err := xl.ReadStructure(f, sheet, 3)
	if err != nil {
		return nil, err
	}
	if len(st.Header) == 0 {
		return nil, fmt.Errorf("表「%s」里读不到表头，无法描述", sheet)
	}
	return st, nil
}

// lastChangeOf 从账目里找这张表最近一次成功的改动，拼成"坐标 旧 → 新"。
// 从后往前找（账目是 append-only，最后一条最新）。
func lastChangeOf(entries []ledger.Entry, fileBase, sheet string) (string, string) {
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if !strings.EqualFold(strings.TrimSpace(e.Status), "ok") {
			continue
		}
		if !sameTable(e.Table, fileBase) {
			continue
		}
		// Sheet 精确匹配：空 sheet 不匹配"有 sheet 的表"，反之亦然
		if strings.TrimSpace(e.Sheet) != strings.TrimSpace(sheet) {
			continue
		}
		return changeText(e), e.Ts
	}
	return "", ""
}

// changeText 拼「E12 100 → 23540」；坐标空时退化为「100 → 23540」，值空用（空）。
func changeText(e ledger.Entry) string {
	old, new := strings.TrimSpace(e.Old), strings.TrimSpace(e.New)
	if old == "" {
		old = "（空）"
	}
	if new == "" {
		new = "（空）"
	}
	prefix := ""
	if c := strings.TrimSpace(e.Cell); c != "" {
		prefix = c + " "
	}
	return prefix + old + " → " + new
}

// sameTable 宽容比较表名：去目录、去扩展名、忽略大小写。
func sameTable(a, b string) bool {
	return tableBase(a) == tableBase(b)
}

func tableBase(s string) string {
	b := filepath.Base(strings.TrimSpace(s))
	if i := strings.LastIndex(b, "."); i >= 0 {
		b = b[:i]
	}
	return strings.ToLower(b)
}

// nodeKeyForFile 拼 sheet_notes.json 的键：文件名（含扩展名）+ "!" + sheet。
// 与 graph.Node.ID() 一致，前端拿到的 file 也是带扩展名的。
func nodeKeyForFile(fileBase, sheet string) string {
	return fileBase + "!" + sheet
}

// backfillSheetNotes 在体检后补齐缺失/过期的描述（计划书第 4.3 节）。
//
// 只在配了模型时做；单张失败只记日志、绝不连累体检；每次最多 maxDescribePerScan 张，
// 按权重（注意力）从高到低取——最重要的表先有描述。
func (s *Server) backfillSheetNotes(ctx context.Context, tables []string) {
	cfg, _, led, _ := s.cur()
	if !cfg.BrainReady() {
		return // 没配模型：体检照常，描述留空（离线可用是产品底线）
	}
	store := s.notesStore()
	if store == nil {
		return
	}
	g, err := s.scanGraph()
	if err != nil || g == nil {
		return
	}
	activity, _ := led.ActivityByNode(0)
	scores := weight.Compute(weight.Input{Graph: g, Activity: activity})
	deadline := time.Now().Add(describeBudget)
	done := 0
	for _, sc := range scores {
		if done >= maxDescribePerScan {
			break
		}
		if time.Now().After(deadline) {
			log.Printf("[sheetnotes] 描述生成超预算（%s），本次先补 %d 张", describeBudget, done)
			break
		}
		node := sc.Node
		if node.File == "" || node.Sheet == "" {
			continue
		}
		target := findTable(tables, node.File)
		if target == "" {
			continue
		}
		key := node.ID()
		cur, has := store.Get(key)
		st, need := pendingDescription(target, node.Sheet, cur, has)
		if !need {
			continue
		}
		note, err := s.describeStructure(ctx, target, node.Sheet, st)
		if err != nil {
			log.Printf("[sheetnotes] 生成「%s」描述失败：%v", key, err)
			continue
		}
		if err := store.Put(key, note); err != nil {
			log.Printf("[sheetnotes] 写入「%s」描述失败：%v", key, err)
			continue
		}
		done++
	}
}

// pendingDescription 判断某表是否需要（重新）生成描述，并回传其结构。
// 已有描述且表头指纹没变 → 不需要。读不到表头 → 不需要（也没法描述）。
func pendingDescription(target, sheet string, cur sheetnotes.Note, has bool) (*xl.Structure, bool) {
	st, err := readSheetStructure(target, sheet)
	if err != nil {
		return nil, false
	}
	if has && strings.TrimSpace(cur.Description) != "" && cur.HeaderHash == headerHash(st) {
		return st, false
	}
	return st, true
}

// findTable 把图节点的文件名（含扩展名）映射回工作区里的真实路径。
func findTable(tables []string, file string) string {
	want := tableBase(file)
	for _, t := range tables {
		if tableBase(t) == want {
			return t
		}
	}
	return ""
}
