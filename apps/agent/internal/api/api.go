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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/ark-local-ai/ark/apps/agent/internal/config"
	"github.com/ark-local-ai/ark/apps/agent/internal/graph"
	"github.com/ark-local-ai/ark/apps/agent/internal/ledger"
	"github.com/ark-local-ai/ark/apps/agent/internal/llm"
	"github.com/ark-local-ai/ark/apps/agent/internal/propose"
	"github.com/ark-local-ai/ark/apps/agent/internal/scan"
	"github.com/ark-local-ai/ark/apps/agent/internal/webui"
	"github.com/ark-local-ai/ark/apps/agent/internal/workspace"
)

// Server 持有工作区与账目等运行时依赖。
// 工作区可在运行时切换（见 docs/agent-architecture/19-界面设计.md 阶段 2），
// 因此 Layout/Cfg/Ledger 都在锁内读写。
type Server struct {
	mu     sync.RWMutex
	Cfg    *config.Config
	Layout *workspace.Layout
	Ledger *ledger.Ledger
	Reg    *workspace.Registry
	Addr   string
	props  *propose.Store
	brain  *llm.Client
}

// New 构造 server。Addr 形如 "127.0.0.1:7700"。
func New(cfg *config.Config, layout *workspace.Layout, led *ledger.Ledger, addr string) *Server {
	if addr == "" {
		addr = "127.0.0.1:7700"
	}
	return &Server{
		Cfg: cfg, Layout: layout, Ledger: led, Addr: addr,
		props: propose.NewStore(30 * time.Minute),
		brain: llm.New(cfg.LLM),
	}
}

// proposals 返回待确认清单暂存区。
func (s *Server) proposals() *propose.Store { return s.props }

// brainClient 返回当前"脑"客户端（设置改动后需重建）。
func (s *Server) brainClient() *llm.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.brain = llm.New(s.Cfg.LLM)
	return s.brain
}

// WithRegistry 挂上工作区注册表（可为 nil，则不支持切换）。
//
// 只在用户**显式配过**工作区时把它登记进去。
// 首次运行时（没配过），工作区是个临时默认目录——绝不能把它当成"用户选过的"，
// 否则界面会以为已经选好工作区，直接进空看板，用户永远看不到"选工作区"的引导。
func (s *Server) WithRegistry(reg *workspace.Registry) *Server {
	s.mu.Lock()
	s.Reg = reg
	layout := s.Layout
	cfg := s.Cfg
	s.mu.Unlock()
	if reg != nil && layout != nil && cfg != nil && cfg.HasWorkspace() {
		tables, _ := layout.DataFiles()
		_, _ = reg.Touch(layout.Root, len(tables))
	}
	return s
}

// cur 取当前工作区相关引用（读锁）。
func (s *Server) cur() (*config.Config, *workspace.Layout, *ledger.Ledger, *workspace.Registry) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Cfg, s.Layout, s.Ledger, s.Reg
}

// SwitchWorkspace 切换当前工作区：建目录结构、开新账目、登记。
// 供 handleWorkspaceOpen / handleWorkspaceCreate 调用。
func (s *Server) SwitchWorkspace(dir string) error {
	layout, err := workspace.LayoutOf(dir)
	if err != nil {
		return err
	}
	led, err := ledger.Open(layout.Ledger)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.Layout = layout
	s.Ledger = led
	s.Cfg.Workspace = layout.Root
	s.Cfg.MarkWorkspaceChosen() // 用户显式选了 → 不再是"首次运行"
	cfg := s.Cfg
	reg := s.Reg
	s.mu.Unlock()

	// 持久化：选了就记住，下次启动直接用（否则每次打开都要重选）
	if err := cfg.Save(config.UserConfigPath()); err != nil {
		// 保存失败不阻断本次使用，但要让调用方知道
		if reg != nil {
			tables, _ := layout.DataFiles()
			_, _ = reg.Touch(layout.Root, len(tables))
		}
		return fmt.Errorf("已切换工作区，但写入配置失败（下次打开需重选）：%w", err)
	}

	if reg != nil {
		tables, _ := layout.DataFiles()
		if _, err := reg.Touch(layout.Root, len(tables)); err != nil {
			return err
		}
	}
	return nil
}

// Handler 返回注册好路由的 http.Handler（便于测试直接打）。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/workspace", s.handleWorkspace)
	mux.HandleFunc("/api/v1/workspace/files", s.handleWorkspaceFiles)
	mux.HandleFunc("/api/v1/workspace/open", s.handleWorkspaceOpen)
	mux.HandleFunc("/api/v1/workspace/create", s.handleWorkspaceCreate)
	mux.HandleFunc("/api/v1/workspace/forget", s.handleWorkspaceForget)
	mux.HandleFunc("/api/v1/workspaces", s.handleWorkspaces)
	mux.HandleFunc("/api/v1/settings", s.handleSettings)
	mux.HandleFunc("/api/v1/plan", s.handlePlanOrGet)
	mux.HandleFunc("/api/v1/apply", s.handleApply)
	mux.HandleFunc("/api/v1/chat", s.handleChat)
	mux.HandleFunc("/api/v1/conversations", s.handleConversations)
	mux.HandleFunc("/api/v1/conversation", s.handleConversation)
	mux.HandleFunc("/api/v1/memory", s.handleMemoryOrPut)
	mux.HandleFunc("/api/v1/memory/stale", s.handleMemoryStale)
	mux.HandleFunc("/api/v1/weights", s.handleWeights)
	mux.HandleFunc("/api/v1/impact", s.handleImpact)
	mux.HandleFunc("/api/v1/impact/learn", s.handleImpactLearn)
	mux.HandleFunc("/api/v1/relations", s.handleRelations)
	mux.HandleFunc("/api/v1/safety", s.handleSafety)
	mux.HandleFunc("/api/v1/terms", s.handleTermsOrPut)
	mux.HandleFunc("/api/v1/terms/delete", s.handleTermsDelete)
	mux.HandleFunc("/api/v1/selfcheck", s.handleSelfCheck)
	mux.HandleFunc("/api/v1/new-tables", s.handleNewTables)
	mux.HandleFunc("/api/v1/generate", s.handleGenerate)
	mux.HandleFunc("/api/v1/rollback", s.handleRollbackOrList)
	mux.HandleFunc("/api/v1/fs/list", s.handleFSList)
	mux.HandleFunc("/api/v1/graph", s.handleGraph)
	mux.HandleFunc("/api/v1/scan", s.handleScan)
	mux.HandleFunc("/api/v1/scan/run", s.handleScanRun)
	mux.HandleFunc("/api/v1/ledger", s.handleLedger)
	mux.HandleFunc("/api/v1/sheets", s.handleListSheets)
	mux.HandleFunc("/api/v1/sheets/preview", s.handleSheetPreview)
	mux.HandleFunc("/api/v1/health", s.handleHealth)

	// 界面：把前端产物嵌进二进制（Win7/8 单文件版靠它自带界面，无需 WebView2）。
	// 未嵌入时返回 nil → 只提供 API，不影响运行。
	if ui := webui.Handler(); ui != nil {
		mux.Handle("/", ui)
	}
	return withCommon(mux)
}

// ListenAndServe 启动服务（阻塞）。
func (s *Server) ListenAndServe() error {
	_, layout, _, _ := s.cur()
	log.Printf("Gridwright API 监听 %s（工作区=%s）", s.Addr, layout.Root)
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
	// 脑是否配好（没配也能看表/体检，只是不能让它判断改表）
	BrainReady bool `json:"brainReady"`
	// 环境是否"离线可用"的说明位（前端据此弱化联网功能）
	Offline bool `json:"offline"`
	// 用户是否**显式**选过工作区。false = 首次运行，界面应引导去选一个，
	// 而不是把临时默认目录当成用户的工作区。
	WorkspaceChosen bool `json:"workspaceChosen"`
}

func (s *Server) handleWorkspace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET")
		return
	}
	cfg, layout, led, _ := s.cur()
	tables, err := layout.DataFiles()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	inbox, _ := layout.InboxFiles()
	if wantsText(r) {
		var b strings.Builder
		b.WriteString(line("工作区：%s", layout.Root))
		b.WriteString(line("表：%d 张", len(tables)))
		b.WriteString(line("inbox 待处理：%d", len(inbox)))
		if cfg.BrainReady() {
			b.WriteString(line("模型：已配置（可改表/对话）"))
		} else {
			b.WriteString(line("模型：未配置（看表/体检/联动图/账目可用，改表需先配模型）"))
		}
		writeText(w, b.String())
		return
	}
	writeJSON(w, http.StatusOK, workspaceInfo{
		Root:            layout.Root,
		Tables:          len(tables),
		InboxCount:      len(inbox),
		LedgerPath:      led.Path(),
		BrainReady:      cfg.BrainReady(),
		Offline:         !cfg.BrainReady(),
		WorkspaceChosen: cfg.HasWorkspace(),
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
	_, layout, _, _ := s.cur()
	tables, _ := layout.DataFiles()
	inbox, _ := layout.InboxFiles()
	writeJSON(w, http.StatusOK, map[string]any{
		"tables":    describeFiles(tables),
		"inbox":     describeFiles(inbox),
		"inboxDone": describeFiles(mustList(layout.Done)),
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
		"root":  g.Root,
		"files": files,
		"nodes": sheets,
		"edges": edges,
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
	_, layout, _, _ := s.cur()
	tables, err := layout.DataFiles()
	if err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("工作区没有 .xlsx 表（%s）", layout.Root)
	}
	return graph.ScanWorkspace(layout.Root, tables, graph.Options{})
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
	if wantsText(r) {
		var b strings.Builder
		b.WriteString(line("体检：%d 张表 / %d 格 / 耗时 %s", rep.Sheets, rep.Cells, rep.Elapsed))
		b.WriteString(line("发现：错误 %d、存疑 %d", errs, warns))
		b.WriteString("\n")
		shown := 0
		for _, it := range rep.Issues {
			if shown >= 20 {
				b.WriteString(line("…（还有 %d 条，用界面查看全部）", len(rep.Issues)-shown))
				break
			}
			b.WriteString(line("[%s] %s!%s  %s", it.Severity, it.Sheet, it.Ref, it.Message))
			shown++
		}
		if len(rep.Issues) == 0 {
			b.WriteString("未发现问题\n")
		}
		writeText(w, b.String())
		return
	}
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
	_, layout, _, _ := s.cur()
	tables, err := layout.DataFiles()
	if err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("工作区没有 .xlsx 表（%s）", layout.Root)
	}
	start := time.Now()
	merged := &scan.Report{File: layout.Root}
	for _, t := range tables {
		// 打开跨月核对：**账对不上比 #REF! 更值得人看**，但需要知道哪些 sheet 是
		// 按月的、以及"上期欠款/本月欠款"这类列名。这里按名字自动探测；
		// 探测不到就自动跳过那项（不会因此报错）。
		opt := scan.Options{
			CrossMonthCheck: true,
			MonthSheets:     detectMonthSheets(t),
			PrevField:       []string{"上期欠款"},
			CurField:        []string{"本月欠款"},
			AnchorNames:     []string{"物业位置", "铺位", "商铺位", "铺位号"},
		}
		rep, err := scan.Run(t, opt)
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
	_, _, led, _ := s.cur()
	entries, err := led.Entries(limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if entries == nil {
		entries = []ledger.Entry{}
	}
	if wantsText(r) {
		var b strings.Builder
		if len(entries) == 0 {
			b.WriteString("账目：还没有改动记录\n")
		} else {
			b.WriteString(line("账目（最近 %d 条）：", len(entries)))
			for _, e := range entries {
				b.WriteString(line("%s  %s %s!%s  %s -> %s  [%s]",
					e.Ts, e.Table, e.Sheet, e.Cell, e.Old, e.New, e.Status))
			}
		}
		writeText(w, b.String())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "limit": limit})
}

// ---------- 健康 ----------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	_, layout, _, _ := s.cur()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "workspace": layout.Root})
}

// pickTable 选取一张表：给了文件名就用它（校验存在），否则用工作区第一张。
func (s *Server) pickTable(name string) (string, error) {
	_, layout, _, _ := s.cur()
	tables, err := layout.DataFiles()
	if err != nil {
		return "", err
	}
	if len(tables) == 0 {
		return "", fmt.Errorf("工作区没有 .xlsx 表（%s）", layout.Root)
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

// detectMonthSheets 找出形如「2026年9月租金（日）」的月度表，**按月份先后排序**。
// 跨月核对依赖这个顺序（相邻月才可比），所以不能返回 map 或乱序。
func detectMonthSheets(path string) []string {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	type ms struct {
		name string
		y, m int
	}
	var found []ms
	re := regexp.MustCompile(`(\d{4})\s*年\s*(\d{1,2})\s*月`)
	for _, sh := range f.GetSheetList() {
		g := re.FindStringSubmatch(sh)
		if g == nil {
			continue
		}
		y, _ := strconv.Atoi(g[1])
		m, _ := strconv.Atoi(g[2])
		if m < 1 || m > 12 {
			continue
		}
		found = append(found, ms{sh, y, m})
	}
	// 按年月排序（跨月核对要求相邻）
	sort.Slice(found, func(i, j int) bool {
		if found[i].y != found[j].y {
			return found[i].y < found[j].y
		}
		return found[i].m < found[j].m
	})
	out := make([]string, 0, len(found))
	for _, x := range found {
		out = append(out, x.name)
	}
	return out
}
