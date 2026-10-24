package tasks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// peakTracker 记录 runner 的并发峰值。测试用阻塞 runner 把「同时」变成可观测事实。
type peakTracker struct {
	mu     sync.Mutex
	active int
	peak   int
}

func (p *peakTracker) enter() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active++
	if p.active > p.peak {
		p.peak = p.active
	}
}

func (p *peakTracker) leave() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active--
}

func (p *peakTracker) max() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.peak
}

// TestParallelDifferentTargets 不同目标表（发票、商铺）必须同时在跑，峰值 2。
func TestParallelDifferentTargets(t *testing.T) {
	pt := &peakTracker{}
	release := make(chan struct{})
	var mu sync.Mutex
	arrived := 0
	q, err := New(t.TempDir(), func(ctx context.Context, task *Task) error {
		pt.enter()
		defer pt.leave()
		mu.Lock()
		arrived++
		mu.Unlock()
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	defer func() {
		releaseOnce.Do(func() { close(release) })
		_ = q.Close()
	}()

	a, err := q.Submit(Task{Title: "改发票", TargetKey: "发票.xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := q.Submit(Task{Title: "改商铺", TargetKey: "商铺.xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "两个任务同时在跑", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return arrived == 2
	})
	if got := pt.max(); got != 2 {
		t.Fatalf("不同目标应峰值 2，实际 %d", got)
	}
	snap := q.Snapshot()
	if len(snap.RunningIDs) != 2 {
		t.Fatalf("应有两个 running，实际 %v", snap.RunningIDs)
	}
	if snap.RunningID == "" || snap.RunningID == a.ID && snap.RunningID == b.ID {
		t.Fatalf("RunningID 应是其中一个 running，实际 %q", snap.RunningID)
	}
	// running 优先且都在 Items 前两位。
	if snap.Items[0].ID != snap.RunningIDs[0] || snap.Items[1].ID != snap.RunningIDs[1] {
		t.Fatalf("running 应排在 Items 前面，实际 %v / %v", snap.Items[0].ID, snap.Items[1].ID)
	}
	releaseOnce.Do(func() { close(release) })
	waitFor(t, "两个任务都结束", func() bool {
		x, _ := q.Get(a.ID)
		y, _ := q.Get(b.ID)
		return x.Status == StatusOK && y.Status == StatusOK
	})
}

// TestParallelSameTargetSerial 同一目标表只能串行：峰值 1，第二个等第一个结束。
func TestParallelSameTargetSerial(t *testing.T) {
	pt := &peakTracker{}
	rec := newRecorder()
	gate := make(chan struct{})
	q, err := New(t.TempDir(), func(ctx context.Context, task *Task) error {
		pt.enter()
		defer pt.leave()
		rec.enter(task.ID)
		defer rec.leave(task.ID)
		select {
		case <-gate:
		case <-ctx.Done():
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var gateOnce sync.Once
	defer func() {
		gateOnce.Do(func() { close(gate) })
		_ = q.Close()
	}()

	a, err := q.Submit(Task{Title: "先改发票", TargetKey: "发票.xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := q.Submit(Task{Title: "再改发票", TargetKey: "发票.xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "第一个在跑", func() bool {
		snap := q.Snapshot()
		return len(snap.RunningIDs) == 1 && snap.RunningIDs[0] == a.ID
	})
	// 给调度器足够机会去「误启动」第二个；同目标必须仍排队。
	time.Sleep(50 * time.Millisecond)
	if got, _ := q.Get(b.ID); got.Status != StatusQueued {
		t.Fatalf("同目标第二个任务应仍在排队，实际 %s", got.Status)
	}
	if snap := q.Snapshot(); len(snap.RunningIDs) != 1 {
		t.Fatalf("同目标不应并发，RunningIDs=%v", snap.RunningIDs)
	}
	gateOnce.Do(func() { close(gate) })
	waitFor(t, "两个任务都结束", func() bool {
		x, _ := q.Get(a.ID)
		y, _ := q.Get(b.ID)
		return x.Status == StatusOK && y.Status == StatusOK
	})
	if got := pt.max(); got != 1 {
		t.Fatalf("同目标峰值应为 1，实际 %d", got)
	}
	_, endA, okA := rec.span(a.ID)
	startB, _, okB := rec.span(b.ID)
	if !okA || !okB {
		t.Fatal("执行记录缺失")
	}
	if startB.Before(endA) {
		t.Fatalf("同目标任务重叠：a 结束于 %v，b 开始于 %v", endA, startB)
	}
}

// TestParallelEmptyTargetKey 空 TargetKey（扫描/只读）不占目标，可并行。
func TestParallelEmptyTargetKey(t *testing.T) {
	pt := &peakTracker{}
	release := make(chan struct{})
	var mu sync.Mutex
	arrived := 0
	q, err := New(t.TempDir(), func(ctx context.Context, task *Task) error {
		pt.enter()
		defer pt.leave()
		mu.Lock()
		arrived++
		mu.Unlock()
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	defer func() {
		releaseOnce.Do(func() { close(release) })
		_ = q.Close()
	}()

	var ids []string
	for i := 0; i < 3; i++ {
		tk, err := q.Submit(Task{Title: "扫描"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, tk.ID)
	}
	waitFor(t, "三个空目标任务同时跑", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return arrived == 3
	})
	if got := pt.max(); got != 3 {
		t.Fatalf("空目标应可并行，峰值 3，实际 %d", got)
	}
	releaseOnce.Do(func() { close(release) })
	waitFor(t, "全部结束", func() bool {
		for _, id := range ids {
			if x, _ := q.Get(id); x.Status != StatusOK {
				return false
			}
		}
		return true
	})
}

// TestCancelOnlyTargetTask Cancel 只停指定任务，别的目标照常跑完。
func TestCancelOnlyTargetTask(t *testing.T) {
	releaseB := make(chan struct{})
	var mu sync.Mutex
	startedA, startedB := false, false
	q, err := New(t.TempDir(), func(ctx context.Context, task *Task) error {
		mu.Lock()
		if task.TargetKey == "发票.xlsx" {
			startedA = true
		} else {
			startedB = true
		}
		mu.Unlock()
		if task.TargetKey == "发票.xlsx" {
			<-ctx.Done()
			return ctx.Err()
		}
		<-releaseB
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var releaseBOnce sync.Once
	defer func() {
		releaseBOnce.Do(func() { close(releaseB) })
		_ = q.Close()
	}()

	a, err := q.Submit(Task{Title: "改发票", TargetKey: "发票.xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := q.Submit(Task{Title: "改商铺", TargetKey: "商铺.xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "两个任务都在跑", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return startedA && startedB
	})
	if err := q.Cancel(a.ID); err != nil {
		t.Fatalf("取消失败：%v", err)
	}
	if got, _ := q.Get(a.ID); got.Status != StatusCanceled {
		t.Fatalf("指定任务应 canceled，实际 %s", got.Status)
	}
	// b 不能被牵连：仍在 running，随后正常结束。
	if got, _ := q.Get(b.ID); got.Status != StatusRunning {
		t.Fatalf("另一个任务不该被取消，实际 %s", got.Status)
	}
	snap := q.Snapshot()
	if len(snap.RunningIDs) != 1 || snap.RunningIDs[0] != b.ID {
		t.Fatalf("取消后只剩 b 在跑，实际 %v", snap.RunningIDs)
	}
	releaseBOnce.Do(func() { close(releaseB) })
	waitFor(t, "b 跑完", func() bool {
		x, _ := q.Get(b.ID)
		return x.Status == StatusOK
	})
}

// TestCloseWaitsAllRunning Close 要等所有 running 结束，不泄漏也不提前返回。
func TestCloseWaitsAllRunning(t *testing.T) {
	var mu sync.Mutex
	exited := 0
	q, err := New(t.TempDir(), func(ctx context.Context, task *Task) error {
		<-ctx.Done()
		mu.Lock()
		exited++
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var ids []string
	for _, key := range []string{"发票.xlsx", "商铺.xlsx", "库存.xlsx"} {
		tk, err := q.Submit(Task{Title: "长任务", TargetKey: key})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, tk.ID)
	}
	waitFor(t, "三个任务都在跑", func() bool {
		return len(q.Snapshot().RunningIDs) == 3
	})

	done := make(chan error, 1)
	go func() { done <- q.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close 失败：%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close 应在所有 running 结束后返回，不能超时")
	}
	mu.Lock()
	n := exited
	mu.Unlock()
	if n != len(ids) {
		t.Fatalf("Close 返回时应有 %d 个 runner 已退出，实际 %d", len(ids), n)
	}
	for _, id := range ids {
		got, ok := q.Get(id)
		if !ok {
			t.Fatalf("任务 %s 丢了", id)
		}
		if got.Status != StatusFailed || got.Error != msgShutdown {
			t.Fatalf("关闭中断的任务应为 failed/「%s」，实际 %s/%s", msgShutdown, got.Status, got.Error)
		}
	}
	// 重复调用安全。
	if err := q.Close(); err != nil {
		t.Fatalf("重复 Close 不该报错：%v", err)
	}
}

// TestPatchesStayOnOwnTask 并发下 Update 的补丁不能串到别的任务上。
func TestPatchesStayOnOwnTask(t *testing.T) {
	gate := make(chan struct{})
	var qRef *Queue
	q, err := New(t.TempDir(), func(ctx context.Context, task *Task) error {
		mark := len(task.TargetKey)
		qRef.Update(task.ID, func(x *Task) {
			x.Counts["mark"] = mark
			x.Summary = task.TargetKey
		})
		<-gate
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	qRef = q
	var gateOnce sync.Once
	defer func() {
		gateOnce.Do(func() { close(gate) })
		_ = q.Close()
	}()

	a, err := q.Submit(Task{Title: "甲", TargetKey: "发票.xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := q.Submit(Task{Title: "乙", TargetKey: "商铺-长名字.xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "两个任务都在跑", func() bool { return len(q.Snapshot().RunningIDs) == 2 })
	gateOnce.Do(func() { close(gate) })
	waitFor(t, "两个任务都结束", func() bool {
		x, _ := q.Get(a.ID)
		y, _ := q.Get(b.ID)
		return x.Status == StatusOK && y.Status == StatusOK
	})
	ga, _ := q.Get(a.ID)
	gb, _ := q.Get(b.ID)
	if ga.Counts["mark"] != len("发票.xlsx") || ga.Summary != "发票.xlsx" {
		t.Fatalf("甲的补丁被串了：%+v", ga)
	}
	if gb.Counts["mark"] != len("商铺-长名字.xlsx") || gb.Summary != "商铺-长名字.xlsx" {
		t.Fatalf("乙的补丁被串了：%+v", gb)
	}
}

// TestLoadLegacyFileWithoutTargetKey 旧 tasks.json（无 targetKey）仍能读入。
func TestLoadLegacyFileWithoutTargetKey(t *testing.T) {
	root := t.TempDir()
	legacy := `{"version":1,"tasks":[` +
		`{"id":"t_legacy01","zoneId":"z1","title":"老任务","status":"ok","steps":[],"counts":{},` +
		`"createdAt":"2020-01-01T00:00:00.000Z","startedAt":"2020-01-01T00:00:00.000Z","endedAt":"2020-01-01T00:00:01.000Z"}]}`
	if err := os.WriteFile(filepath.Join(root, fileName), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	q, err := New(root, func(ctx context.Context, task *Task) error { return nil })
	if err != nil {
		t.Fatalf("旧格式应可读：%v", err)
	}
	defer q.Close()
	got, ok := q.Get("t_legacy01")
	if !ok {
		t.Fatal("旧任务丢了")
	}
	if got.TargetKey != "" {
		t.Fatalf("旧任务没有 targetKey，应为空，实际 %q", got.TargetKey)
	}
	// 新任务写入后仍带 targetKey。
	tk, err := q.Submit(Task{Title: "新任务", TargetKey: "发票.xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "任务结束", func() bool {
		x, _ := q.Get(tk.ID)
		return x.Status == StatusOK
	})
	b, err := os.ReadFile(filepath.Join(root, fileName))
	if err != nil {
		t.Fatal(err)
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("tasks.json 不合法：%v", err)
	}
	found := false
	for _, task := range f.Tasks {
		if task.ID == tk.ID {
			found = task.TargetKey == "发票.xlsx"
		}
	}
	if !found {
		t.Fatal("targetKey 没落盘")
	}
}
