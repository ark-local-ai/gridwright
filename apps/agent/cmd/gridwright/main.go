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
	"github.com/ark-local-ai/ark/apps/agent/internal/config"
	"github.com/ark-local-ai/ark/apps/agent/internal/ledger"
	"github.com/ark-local-ai/ark/apps/agent/internal/llm"
	"github.com/ark-local-ai/ark/apps/agent/internal/workspace"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "config.yaml 路径")
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
	ag := agent.New(cfg, layout, led, brain)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
