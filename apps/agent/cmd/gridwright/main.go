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
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/ark-local-ai/ark/apps/agent/internal/agent"
	"github.com/ark-local-ai/ark/apps/agent/internal/api"
	"github.com/ark-local-ai/ark/apps/agent/internal/config"
	"github.com/ark-local-ai/ark/apps/agent/internal/ledger"
	"github.com/ark-local-ai/ark/apps/agent/internal/llm"
	"github.com/ark-local-ai/ark/apps/agent/internal/proc"
	"github.com/ark-local-ai/ark/apps/agent/internal/workspace"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "config.yaml 路径")
	apiAddr := flag.String("api", "127.0.0.1:7700", "本地 API 监听地址（空字符串=不启动）")
	parentPID := flag.Int("parent-pid", 0, "父进程 PID（桌面壳传入；父进程退出时本进程随之退出）")
	flag.Parse()

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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 本地 API（界面用的通用契约，见 docs/agent-architecture/13-接口契约.md）
	if *apiAddr != "" {
		srv := api.New(cfg, layout, led, *apiAddr)
		// 工作区注册表放在引擎同目录（桌面壳会把它指向应用数据目录），
		// 记住用过的工作区，供界面切换。
		regPath := filepath.Join(filepath.Dir(layout.Root), "gridwright-workspaces.json")
		if reg, rerr := workspace.OpenRegistry(regPath); rerr == nil {
			srv = srv.WithRegistry(reg)
			log.Printf("工作区注册表: %s", regPath)
		} else {
			log.Printf("工作区注册表不可用（切换功能将禁用）: %v", rerr)
		}
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
	for _, dir := range []string{layout.Inbox, layout.Root} {
		if err := watcher.Add(dir); err != nil {
			log.Printf("watch %s 失败: %v", dir, err)
		}
	}

	// 事件触发即跑一次（留 300ms 让写入落盘，避免文件没写完就处理）
	runOnce := func() {
		if err := ag.RunInboxFiles(ctx); err != nil {
			log.Printf("运行出错: %v", err)
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

// fileExists 判断文件是否存在。
func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
