package api

import (
	"net/http"
	"strings"

	"github.com/ark-local-ai/ark/apps/agent/internal/graph"
	"github.com/ark-local-ai/ark/apps/agent/internal/newtable"
)

// 新增表识别（见 docs/agent-architecture/25-澄清与自检边界.md 第二节）。
//
// 新表刚进来时图上是孤岛，按结构推断什么都找不到——但最容易出问题是这时候
// （链上下一节？新类别？还是和已有表**重复**会算重）。
// 判断"它像谁"是纯代码，不需要模型。

// handleNewTables GET /api/v1/new-tables?format=text
func (s *Server) handleNewTables(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	_, layout, _, _ := s.cur()
	files, err := layout.DataFiles()
	if err != nil || len(files) == 0 {
		writeErr(w, http.StatusBadRequest, "工作区没有 .xlsx 表")
		return
	}
	g, err := graph.ScanWorkspace(layout.Root, files, graph.Options{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 有新数据流经的表不算孤岛——人声明的关联也要算进来。
	s.mergeDeclared(g)
	got := newtable.Detect(newtable.Input{Graph: g, Files: files})

	if wantsText(r) {
		var b strings.Builder
		if len(got) == 0 {
			b.WriteString("没有孤立表：工作区里的表都已建立关联\n")
		} else {
			b.WriteString(line("未建立关联的表：%d 张", len(got)))
			b.WriteString("\n")
			for _, a := range got {
				b.WriteString(line("%-28s  %s", a.Node.Sheet, kindLabel(a.Kind)))
				b.WriteString(line("    %s", a.Message))
				for _, sim := range a.Similar {
					b.WriteString(line("    像：%s（表名%.0f%% 表头%.0f%%）",
						sim.Node.Sheet, sim.SheetSim*100, sim.HeaderSim*100))
				}
			}
		}
		writeText(w, b.String())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": got})
}

func kindLabel(k string) string {
	switch k {
	case newtable.KindDuplicate:
		return "⚠ 可能与已有表重复"
	case newtable.KindNextInChain:
		return "像是链上下一节"
	case newtable.KindNewCategory:
		return "可能是新类别"
	default:
		return "暂无关联"
	}
}
