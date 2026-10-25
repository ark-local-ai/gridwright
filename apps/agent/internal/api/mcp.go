package api

import (
	"net/http"

	"github.com/ark-local-ai/ark/apps/agent/internal/logx"
	"github.com/ark-local-ai/ark/apps/agent/internal/mcp"
)

// MCP 工具接入（见 docs/agent-architecture/15-对话与提示词.md）。
//
// 为什么要「惰性 + 跟工作区走」：MCP server 是子进程，拉起一次要握手、列工具，
// 不是免费的。但配置又在 <工作区>/mcp.json 里，换工作区就换了工具集。
// 所以缓存的是 (root, registry) 这一对：root 没变就复用，变了就关旧的再开。
//
// 为什么全程只 err 到日志、不 err 到接口：没配 mcp.json 是**常态**，
// 不是故障。对话本身不依赖工具，为一个可选能力让 /chat 500 是错的。

// toolRunner 返回当前工作区装配好的工具注册表；没有可用工具时返回 nil。
//
// 返回的是 mcp.Runner 而不是 *mcp.Registry：agent 只需要「列工具 / 调一个」，
// 单元测试也能塞假实现。
func (s *Server) toolRunner() mcp.Runner {
	_, layout, _, _ := s.cur()
	if layout == nil {
		return nil
	}
	root := layout.Root

	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	if s.mcpReg != nil && s.mcpRoot == root {
		// 缓存的是「这个 root 已经开过了」，即使结果是空注册表也要复用——
		// 否则每次对话都会重读 mcp.json 并试图拉起子进程。
		if len(s.mcpReg.Catalog()) == 0 {
			return nil
		}
		return s.mcpReg
	}
	// 根变了：先关掉旧的一批子进程，否则换几次工作区就攒一堆孤儿进程。
	if s.mcpReg != nil {
		if err := s.mcpReg.Close(); err != nil {
			logx.Step("mcp", "close", "root", s.mcpRoot, "err", err.Error())
		}
		s.mcpReg = nil
		s.mcpRoot = ""
	}
	reg := mcp.Open(root)
	if reg == nil || len(reg.Catalog()) == 0 {
		// 空注册表也留着：否则每次对话都会重新读一遍 mcp.json 并试图拉进程。
		s.mcpReg = reg
		s.mcpRoot = root
		return nil
	}
	s.mcpReg = reg
	s.mcpRoot = root
	logx.Step("mcp", "open", "root", root, "tools", len(reg.Catalog()))
	return reg
}

// handleMCPTools GET /api/v1/tools/mcp —— 列出已装配的 MCP 工具
//
// 只读、无副作用：界面用它显示「这次能用哪些工具」，排障时用它确认
// 某个 server 到底有没有起来。没有工具时返回空数组（不是 null），
// 前端不必为两种空值写两个分支。
func (s *Server) handleMCPTools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	items := []map[string]any{}
	var runner mcp.Runner = s.toolRunner()
	if runner != nil {
		for _, t := range runner.Catalog() {
			schema := t.Schema
			if len(schema) == 0 {
				schema = []byte("{}")
			}
			items = append(items, map[string]any{
				"server":      t.Server,
				"name":        t.Name,
				"description": t.Description,
				"schema":      schema,
			})
		}
	}
	logx.Step("api.tools", "mcp", "items", len(items))
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
