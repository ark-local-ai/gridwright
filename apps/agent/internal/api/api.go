// Package api 提供本地 HTTP 接口（见 docs/agent-architecture/13-接口契约.md）。
//
// 设计原则：只监听 127.0.0.1（单机单人，无鉴权）；JSON 收、JSON 回；
// 只读能力（工作区/联动图/体检/账目）与"改表"严格分离——改表走 /plan → /apply 两步。
//
// 这一层是"通用契约"的落点：Win10/11 的 Tauri 外壳与 Win7 的内嵌页面
// 都通过它访问同一个引擎，所以两端行为天然一致，不需要维护两套逻辑。
package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ark-local-ai/ark/apps/agent/internal/config"
	"github.com/ark-local-ai/ark/apps/agent/internal/graph"
	"github.com/ark-local-ai/ark/apps/agent/internal/ledger"
	"github.com/ark-local-ai/ark/apps/agent/internal/scan"
	"github.com/ark-local-ai/ark/apps/agent/internal/workspace"
)

// Server 持有工作区与账目等运行时依赖。
type Server struct {
	Cfg    *config.Config
	Layout *workspace.Layout
	Ledger *ledger.Ledger
	Addr   string
}

// New 构造 server。Addr 形如 "127.0.0.1:7700"。
func New(cfg *config.Config, layout *workspace.Layout, led *ledger.Ledger, addr string) *Server {
	if addr == "" {
		addr = "127.0.0.1:7700"
	}
	return &Server{Cfg: cfg, Layout: layout, Ledger: led, Addr: addr}
}

// Handler 返回注册好路由的 http.Handler（便于测试直接打）。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspace", s.handleWorkspace)
	mux.HandleFunc("/api/v1/workspace/files", s.handleWorkspaceFiles)
	mux.HandleFunc("/api/v1/graph", s.handleGraph)
	mux.HandleFunc("/api/v1/scan", s.handleScan)
	mux.HandleFunc("/api/v1/scan/run", s.handleScanRun)
	mux.HandleFunc("/api/v1/ledger", s.handleLedger)
	mux.HandleFunc("/api/v1/health", s.handleHealth)
	return withCommon(mux)
}

// ListenAndServe 启动服务（阻塞）。
func (s *Server) ListenAndServe() error {
	log.Printf("Gridwright API 监听 %s（工作区=%s）", s.Addr, s.Layout.Root)
	srv := &http.Server{
		Addr:              s.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.ListenAndServe()
}

// ---------- 通用中间件 ----------

func withCommon(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 简单 CORS：桌面壳 / 网页预览从不同 origin 访问本机服务
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("写响应失败: %v", err)
	}
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// ---------- 工作区 ----------

type workspaceInfo struct {
	Root       string `json:"root"`
	Tables     int    `json:"tables"`
	InboxCount int    `json:"inboxCount"`
	LedgerPath string `json:"ledgerPath"`
}

func (s *Server) handleWorkspace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	tables, err := s.Layout.DataFiles()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	inbox, _ := s.Layout.InboxFiles()
	writeJSON(w, http.StatusOK, workspaceInfo{
		Root:       s.Layout.Root,
		Tables:     len(tables),
		InboxCount: len(inbox),
		LedgerPath: s.Ledger.Path(),
	})
}

type fileInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Time string `json:"time"`
}

func (s *Server) handleWorkspaceFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	tables, _ := s.Layout.DataFiles()
	inbox, _ := s.Layout.InboxFiles()
	writeJSON(w, http.StatusOK, map[string]any{
		"tables":      describeFiles(tables),
		"inbox":       describeFiles(inbox),
		"inboxDone":   describeFiles(mustList(s.Layout.Done)),
		"placeholder": false,
	})
}

func mustList(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

func describeFiles(paths []string) []fileInfo {
	out := make([]fileInfo, 0, len(paths))
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		out = append(out, fileInfo{
			Name: filepath.Base(p),
			Size: fi.Size(),
			Time: fi.ModTime().Format("2006-01-02 15:04"),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RunScan 供外部（如定时体检任务）复用：对工作区第一张表做只读体检。
func (s *Server) RunScan() (*scan.Report, error) { return s.runScan() }

// ---------- 联动图 ----------

func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	// 扫整个工作区（所有 xlsx），产出跨文件联动图
	g, err := s.scanGraph()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	sheets := g.Nodes
	if sheets == nil {
		sheets = []graph.Node{}
	}
	edges := g.Edges
	if edges == nil {
		edges = []graph.Edge{}
	}
	files := g.Files
	if files == nil {
		files = []string{}
	}
	resp := map[string]any{
		"root":   g.Root,
		"files":  files,
		"nodes":  sheets,
		"edges":  edges,
	}
	// ?node=文件!工作表（或裸 sheet 名，若唯一）→ 附上被牵动的闭包
	if q := r.URL.Query().Get("node"); q != "" {
		target := parseNode(q)
		to := g.Propagate(g.ResolveNode(target))
		if to == nil {
			to = []string{} // 保证是数组而非 null，前端不用特判
		}
		resp["propagate"] = map[string]any{
			"from": target.ID(),
			"to":   to,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// scanGraph 扫工作区里全部 xlsx。
func (s *Server) scanGraph() (*graph.Graph, error) {
	tables, err := s.Layout.DataFiles()
	if err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("工作区没有 .xlsx 表（%s）", s.Layout.Root)
	}
	return graph.ScanWorkspace(s.Layout.Root, tables, graph.Options{})
}

// parseNode 解析 "文件!工作表" 或裸 "工作表"。
func parseNode(s string) graph.Node {
	if i := strings.LastIndex(s, "!"); i >= 0 {
		return graph.Node{File: s[:i], Sheet: s[i+1:]}
	}
	return graph.Node{Sheet: s}
}

// ---------- 体检 ----------

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	rep, err := s.runScan()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	errs, warns, infos := rep.CountBySeverity()
	writeJSON(w, http.StatusOK, map[string]any{
		"report": rep,
		"counts": map[string]int{"error": errs, "warn": warns, "info": infos},
	})
}

func (s *Server) handleScanRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	rep, err := s.runScan()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	errs, warns, _ := rep.CountBySeverity()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"errors": errs,
		"warns":  warns,
		"report": rep,
	})
}

// runScan 对工作区**所有**表做只读体检，合并成一份报告（报告按文件分组）。
func (s *Server) runScan() (*scan.Report, error) {
	tables, err := s.Layout.DataFiles()
	if err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("工作区没有 .xlsx 表（%s）", s.Layout.Root)
	}
	start := time.Now()
	merged := &scan.Report{File: s.Layout.Root}
	for _, t := range tables {
		rep, err := scan.Run(t, scan.Options{})
		if err != nil {
			merged.Issues = append(merged.Issues, scan.Issue{
				Kind: "scan_error", Severity: scan.SevWarn,
				Sheet: filepath.Base(t), Message: "扫描失败：" + err.Error(),
			})
			continue
		}
		merged.Sheets += rep.Sheets
		merged.Cells += rep.Cells
		merged.Issues = append(merged.Issues, rep.Issues...)
	}
	merged.Elapsed = time.Since(start).String()
	return merged, nil
}

// ---------- 账目 ----------

func (s *Server) handleLedger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	entries, err := s.Ledger.Entries(limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if entries == nil {
		entries = []ledger.Entry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "limit": limit})
}

// ---------- 健康 ----------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "workspace": s.Layout.Root})
}

// pickTable 选取一张表：给了名字就用它（校验存在），否则用工作区第一张。
func (s *Server) pickTable(name string) (string, error) {
	tables, err := s.Layout.DataFiles()
	if err != nil {
		return "", err
	}
	if len(tables) == 0 {
		return "", fmt.Errorf("工作区没有 .xlsx 表（%s）", s.Layout.Root)
	}
	if name == "" {
		return tables[0], nil
	}
	want := strings.ToLower(strings.TrimSuffix(name, ".xlsx"))
	for _, t := range tables {
		if strings.ToLower(strings.TrimSuffix(filepath.Base(t), ".xlsx")) == want {
			return t, nil
		}
	}
	return "", fmt.Errorf("找不到表 %q", name)
}
