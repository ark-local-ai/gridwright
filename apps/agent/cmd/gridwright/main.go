// gridwright 是跑在办公机（含 Win7）上的"手"：盯住工作区文件夹，
// 新数据进来就调云端"脑"决定怎么改表，执行 edits、记账、发通知（spec §1/§7 M1）。
// 用法：
//
//	gridwright [-config config.yaml]
//
// 工作区 = 一个文件夹；新数据丢进 inbox/，处理后移入 inbox/done/。
package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/ark-local-ai/ark/apps/agent/internal/agent"
	"github.com/ark-local-ai/ark/apps/agent/internal/api"
	"github.com/ark-local-ai/ark/apps/agent/internal/config"
	"github.com/ark-local-ai/ark/apps/agent/internal/ledger"
	"github.com/ark-local-ai/ark/apps/agent/internal/llm"
	"github.com/ark-local-ai/ark/apps/agent/internal/logx"
	"github.com/ark-local-ai/ark/apps/agent/internal/pathsafe"
	"github.com/ark-local-ai/ark/apps/agent/internal/proc"
	"github.com/ark-local-ai/ark/apps/agent/internal/workspace"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "config.yaml 路径")
	apiAddr := flag.String("api", "127.0.0.1:7700", "本地 API 监听地址（空字符串=不启动）")
	parentPID := flag.Int("parent-pid", 0, "父进程 PID（桌面壳传入；父进程退出时本进程随之退出）")
	logLevel := flag.String("log-level", "", "日志级别 debug|info|warn|error（默认 info；也可用 "+logx.EnvLevel+" 环境变量）")
	flag.Parse()

	// 必须最先做：它决定此后所有日志的落点（stdout，而非 stderr）与格式。
	// 放在 flag.Parse 之后、任何 log.Fatalf 之前，配置错误的报错才用得上新格式。
	//
	// 除 stdout 外再按天落一份 <安装目录>/logs/engine-YYYY-MM-DD.log：
	// 桌面壳的 engine.log 是单文件、会被 8MiB×3 轮转掉，翻不了几天历史。
	// 目录建不出来时 SetupWithFile **降级为仅 stdout** 并返回 error：
	// 日志系统自己坏掉不能把引擎搞得起不来，所以这里只警告不退出。
	if err := logx.SetupWithFile(*logLevel, logDir()); err != nil {
		slog.Warn("日志落文件失败，仅 stdout", "err", err)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("配置错误: %v", err)
	}

	layout, err := workspace.LayoutOf(cfg.Workspace)
	if err != nil {
		log.Fatalf("工作区错误: %v", err)
	}
	led, err := ledger.Open(layout.Ledger)
	if err != nil {
		log.Fatalf("账目错误: %v", err)
	}
	brain := llm.New(cfg.LLM)

	// 首次运行落一次配置，让用户能看到/编辑这个本地配置文件
	// （配一次就留下，更新/重装后可复用；见 internal/config/persist.go）
	if cfgPath2 := config.UserConfigPath(); !fileExists(cfgPath2) {
		if err := cfg.Save(cfgPath2); err != nil {
			log.Printf("写入本地配置失败（不影响运行）：%v", err)
		} else {
			log.Printf("已生成本地配置：%s", cfgPath2)
		}
	}
	ag := agent.New(cfg, layout, led, brain)
	// 工作区切换后，inbox 监听与自动处理必须跟着换目录。
	// 以前 ag 与 watcher 都只在启动时构造一次：切换后仍在处理旧工作区，
	// 新工作区的 inbox 永远不跑（界面却显示新目录）。
	var agMu sync.RWMutex
	currentAgent := func() *agent.Agent {
		agMu.RLock()
		defer agMu.RUnlock()
		return ag
	}
	setAgent := func(l *workspace.Layout) {
		led2, err := ledger.Open(l.Ledger)
		if err != nil {
			log.Printf("切换自动处理失败（账目打不开）：%v", err)
			return
		}
		a := agent.New(cfg, l, led2, llm.New(cfg.LLM))
		agMu.Lock()
		ag = a
		agMu.Unlock()
	}
	// 非阻塞转交：切换接口不该等 watcher 主循环。
	switchCh := make(chan string, 1)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 本地 API（界面用的通用契约，见 docs/agent-architecture/13-接口契约.md）
	if *apiAddr != "" {
		srv := api.New(cfg, layout, led, *apiAddr)
		// 工作区注册表放**应用数据目录**，记住用过的工作区供界面切换。
		//
		// 曾经放在 filepath.Dir(layout.Root)（工作区的**上一级**），以为"引擎旁边"
		// 就是应用目录。那是错的：工作区由用户选，可以是任意文件夹——用户选了
		// "...\2026-09\testwork"，注册表就写进他的 "...\2026-09\"；选了 "...\x\a"，
		// 就写进 "...\x\"。实测把 gridwright-workspaces.json 撒进了用户的微信下载
		// 文件夹和上一层目录。**绝不往用户的数据目录里写我们自己的文件。**
		regPath := filepath.Join(config.UserConfigDir(), "gridwright-workspaces.json")
		var reg *workspace.Registry
		if r, rerr := workspace.OpenRegistry(regPath); rerr == nil {
			reg = r
			srv = srv.WithRegistry(reg)
			log.Printf("工作区注册表: %s", regPath)
		} else {
			log.Printf("工作区注册表不可用（切换功能将禁用）: %v", rerr)
		}
		// 路径白名单：自动监听/连接器只能碰到用户显式授权过的根目录。
		// 已有工作区（配置 + 注册表）按 legacy 迁移进来，避免升级后打不开。
		grantsPath := filepath.Join(config.UserConfigDir(), "grants.json")
		if st, gerr := pathsafe.Open(grantsPath); gerr == nil {
			srv.Grants = st
			_, _ = st.Allow(layout.Root, "legacy:workspace")
			_, _ = st.Allow(filepath.Join(config.UserConfigDir(), "workspaces"), "app:workspaces")
			if reg != nil {
				for _, e := range reg.List() {
					_, _ = st.Allow(e.Path, "legacy:registry")
				}
			}
			log.Printf("路径白名单: %s（%d 条）", grantsPath, len(st.List()))
		} else {
			log.Printf("路径白名单不可用（仅 token 保护）：%v", gerr)
		}
		// 切换成功后在 watcher 主循环里重建 agent 并重挂监听目录。
		srv.OnWorkspaceSwitch = func(root string) {
			select {
			case switchCh <- root:
			default:
			}
		}
		// 本机访问令牌：写进应用数据目录，界面通过 GET /api/v1/session 领取。
		// 拿不到令牌就不启动 API——否则等于把一个能读写本机文件的接口
		// 无鉴权地交给同机任意进程。宁可起不来，也不静默降级。
		tokenPath := filepath.Join(config.UserConfigDir(), "engine.token")
		tok, tokSrc, terr := api.LoadOrCreateToken(tokenPath)
		if terr != nil {
			log.Fatalf("生成本机访问令牌失败（API 不启动）：%v", terr)
		}
		srv.Token = tok
		log.Printf("本机访问令牌: %s", tokSrc)
		// 自动化任务调度器：每分钟检查到点任务（见 internal/api/jobs.go）
		// 必须跟在 ctx 之后 —— 引擎退出时调度器一起停。
		srv.StartScheduler(ctx)
		go func() {
			if err := srv.ListenAndServe(); err != nil {
				log.Printf("API 服务退出: %v", err)
			}
		}()
	}

	// 若由桌面壳拉起，监视父进程：壳没了就退出，避免留下占着端口的孤儿进程
	proc.WatchParent(*parentPID, stop)

	// fsnotify 盯 inbox + 工作区根（新表/新数据进来都触发）
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("创建 watcher: %v", err)
	}
	defer watcher.Close()
	watched := []string{}
	watchLayout := func(l *workspace.Layout) {
		for _, d := range watched {
			_ = watcher.Remove(d)
		}
		watched = nil
		for _, d := range []string{l.Inbox, l.Root} {
			if err := watcher.Add(d); err != nil {
				log.Printf("watch %s 失败: %v", d, err)
				continue
			}
			watched = append(watched, d)
		}
	}
	watchLayout(layout)

	// 事件触发即跑一次（留 300ms 让写入落盘，避免文件没写完就处理）
	runOnce := func() {
		if a := currentAgent(); a != nil {
			if err := a.RunInboxFiles(ctx); err != nil {
				log.Printf("运行出错: %v", err)
			}
		}
	}

	// 启动先跑一次（处理上次遗留的 inbox 文件）
	runOnce()

	interval := time.Duration(cfg.PollSeconds) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Printf("Gridwright 已启动：工作区=%s  轮询=%s  模型=%s", layout.Root, interval, brain.Model())

	for {
		select {
		case <-ctx.Done():
			log.Printf("收到退出信号，正在收尾…")
			return
		case <-ticker.C:
			runOnce()
		case root := <-switchCh:
			l, lerr := workspace.LayoutOf(root)
			if lerr != nil {
				log.Printf("切换自动处理失败（目录不可用）：%v", lerr)
				continue
			}
			setAgent(l)
			watchLayout(l)
			log.Printf("自动处理已切到：%s", l.Root)
			// 新工作区里可能已经有待处理文件，立刻跑一次。
			go runOnce()
		case ev, ok := <-watcher.Events:
			if !ok {
				return
			}
			// 只关心 inbox 里的新文件 / 新表
			if isDataEvent(ev) {
				base := filepath.Base(ev.Name)
				if isDataFile(base) {
					log.Printf("检测到 %s %s", ev.Op, base)
					// 留 300ms 让写入落盘再跑
					go func() {
						time.Sleep(300 * time.Millisecond)
						runOnce()
					}()
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("watcher 错误: %v", err)
		}
	}
}

func isDataEvent(ev fsnotify.Event) bool {
	return ev.Op.Has(fsnotify.Create) || ev.Op.Has(fsnotify.Write) || ev.Op.Has(fsnotify.Rename)
}

func isDataFile(name string) bool {
	if len(name) < 2 {
		return false
	}
	if name[0] == '.' || name[0] == '~' {
		return false // 隐藏 / Excel 锁
	}
	ext := filepath.Ext(name)
	return ext == ".csv" || ext == ".xlsx" || ext == ".CSV" || ext == ".XLSX"
}

// logDir 决定按天分文件的日志落在哪个目录。
//
// 顺序：logx.EnvDir 环境变量 → <引擎 exe 所在目录>/logs → ./logs。
//
// 为什么默认放在 exe 旁边：打包后桌面壳把 sidecar（引擎）和它自己放在**同一个安装目录**，
// 而壳也把引擎的 stdout 落成 <安装目录>/logs/engine.log。所以这里算出来的正是同一个 logs/——
// 用户装一次，就能在安装目录一眼找到日志，不必去 %APPDATA% 里翻。
//
// 壳拉起引擎时会显式传 logx.EnvDir（它允许在「设置 → 日志」里改日志目录），所以那个
// 变量优先级最高。开发态用 `go run` 时 exe 在临时目录，请显式设 logx.EnvDir。
func logDir() string {
	if d := strings.TrimSpace(os.Getenv(logx.EnvDir)); d != "" {
		return d
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "logs")
	}
	return "logs"
}

// fileExists 判断文件是否存在。
func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
