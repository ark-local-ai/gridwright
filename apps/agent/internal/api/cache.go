package api

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ark-local-ai/ark/apps/agent/internal/graph"
	"github.com/ark-local-ai/ark/apps/agent/internal/scan"
)

// 工作区只读结果的短生命周期缓存。
//
// 背景：一次界面刷新会并发请求 /workspace、/workspace/files、/graph、/scan/run，
// 而 /graph 与 /scan/run 各自把整个工作区的 xlsx 全量扫一遍；体检结束后的
// 描述回填还会再扫一次图。三份结果来自同一批磁盘文件，却各算各的。
//
// 做法：以「工作区根 + 每张表的 size/mtime + rules.yaml」为签名。
// 签名没变就复用上一份结果——文件一改签名立刻变，不依赖手动失效，
// 也不会把 apply/import 之后的旧结果继续端给界面。
//
// 缓存不落盘、不跨工作区：切工作区时根路径变了，签名自然不同。

// workspaceSig 计算工作区当前状态的签名。stat 失败的文件用零值占位，
// 这样"文件消失"也会改变签名。
func (s *Server) workspaceSig() string {
	_, layout, _, _ := s.cur()
	if layout == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(layout.Root)
	tables, _ := layout.DataFiles()
	for _, t := range tables {
		fi, err := os.Stat(t)
		if err != nil {
			fmt.Fprintf(&b, "|%s:gone", t)
			continue
		}
		fmt.Fprintf(&b, "|%s:%d:%d", t, fi.Size(), fi.ModTime().UnixNano())
	}
	// 人工声明的 link 会改变联动图。
	if fi, err := os.Stat(layout.Rules); err == nil {
		fmt.Fprintf(&b, "|rules:%d:%d", fi.Size(), fi.ModTime().UnixNano())
	}
	return b.String()
}

func (s *Server) cachedGraph(sig string) (*graph.Graph, bool) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.gCache != nil && s.gSig == sig {
		return s.gCache, true
	}
	return nil, false
}

func (s *Server) storeGraph(sig string, g *graph.Graph) {
	s.cacheMu.Lock()
	s.gSig, s.gCache = sig, g
	s.cacheMu.Unlock()
}

func (s *Server) cachedScan(sig string) (*scan.Report, bool) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.scanCache != nil && s.scanSig == sig {
		return s.scanCache, true
	}
	return nil, false
}

func (s *Server) storeScan(sig string, rep *scan.Report) {
	s.cacheMu.Lock()
	s.scanSig, s.scanCache = sig, rep
	s.cacheMu.Unlock()
}

// ensureGraph 复用缓存；没有就扫一次并写入。
func (s *Server) ensureGraph(sig string) (*graph.Graph, error) {
	if g, ok := s.cachedGraph(sig); ok {
		return g, nil
	}
	g, err := s.scanGraph()
	if err != nil {
		return nil, err
	}
	s.storeGraph(sig, g)
	return g, nil
}

// startBackfill 在后台补齐缺失的工作表描述。
//
// 以前它在 runScan 里同步执行：刷新请求要等最多 3 分钟（含最多 5 次模型调用）
// 才返回，而这本该是"扫描完了先给结果，描述慢慢补"。
// 现在单飞 + 5 分钟节流：同一批文件反复刷新不会反复烧 token。
func (s *Server) startBackfill(tables []string, g *graph.Graph) {
	s.cacheMu.Lock()
	if s.describeBusy || time.Since(s.describeAt) < 5*time.Minute {
		s.cacheMu.Unlock()
		return
	}
	s.describeBusy = true
	s.describeAt = time.Now()
	s.cacheMu.Unlock()

	go func() {
		defer func() {
			s.cacheMu.Lock()
			s.describeBusy = false
			s.cacheMu.Unlock()
		}()
		// 请求的 ctx 在响应写出后就会取消；回填必须用自己的预算。
		ctx, cancel := context.WithTimeout(context.Background(), describeBudget+30*time.Second)
		defer cancel()
		s.backfillSheetNotes(ctx, tables, g)
	}()
}
