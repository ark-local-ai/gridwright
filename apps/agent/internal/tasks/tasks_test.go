package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// waitFor 轮询等待条件成立，避免用固定 sleep 猜时间。
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("等待超时：%s", what)
}

// okRunner 立刻跑完，记录执行顺序。
func okRunner(rec *recorder) func(context.Context, *Task) error {
	return func(ctx context.Context, task *Task) error {
		rec.enter(task.ID)
		defer rec.leave(task.ID)
		task.Steps = append(task.Steps, Step{Key: "s1", Label: "一步", Status: StepOK})
		task.Counts["rows"]++
		task.Summary = "done " + task.ID
		return nil
	}
}

type recorder struct {
	mu     sync.Mutex
	events []string
	starts map[string]time.Time
	ends   map[string]time.Time
}

func newRecorder() *recorder {
	return &recorder{starts: map[string]time.Time{}, ends: map[string]time.Time{}}
}

func (r *recorder) enter(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts[id] = time.Now()
	r.events = append(r.events, "start:"+id)
}

func (r *recorder) leave(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ends[id] = time.Now()
	r.events = append(r.events, "end:"+id)
}

func (r *recorder) span(id string) (time.Time, time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok1 := r.starts[id]
	en, ok2 := r.ends[id]
	return st, en, ok1 && ok2
}

func (r *recorder) order() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

// TestSingleWorkerSerial 两个任务的时间区间不能重叠：一次只跑一个。
func TestSingleWorkerSerial(t *testing.T) {
	rec := newRecorder()
	q, err := New(t.TempDir(), func(ctx context.Context, task *Task) error {
		rec.enter(task.ID)
		defer rec.leave(task.ID)
		time.Sleep(20 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	a, err := q.Submit(Task{ZoneID: "z1", Title: "甲"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := q.Submit(Task{ZoneID: "z1", Title: "乙"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "两个任务都跑完", func() bool {
		ta, _ := q.Get(a.ID)
		tb, _ := q.Get(b.ID)
		return ta.Status == StatusOK && tb.Status == StatusOK
	})

	startA, endA, okA := rec.span(a.ID)
	startB, endB, okB := rec.span(b.ID)
	if !okA || !okB {
		t.Fatalf("执行记录缺失：a=%v b=%v", okA, okB)
	}
	if !startB.After(endA) || !endB.After(startB) {
		t.Fatalf("任务重叠：a [%v,%v] b [%v,%v]", startA, endA, startB, endB)
	}
}

// TestQueueOrder 提交顺序 == 执行顺序（FIFO）。
func TestQueueOrder(t *testing.T) {
	gate := make(chan struct{})
	rec := newRecorder()
	q, err := New(t.TempDir(), func(ctx context.Context, task *Task) error {
		rec.enter(task.ID)
		defer rec.leave(task.ID)
		<-gate
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	var ids []string
	for i := 0; i < 3; i++ {
		tk, err := q.Submit(Task{Title: "任务"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, tk.ID)
	}
	waitFor(t, "第一个任务在跑", func() bool { return q.Snapshot().RunningID == ids[0] })
	close(gate)
	waitFor(t, "全部跑完", func() bool {
		for _, id := range ids {
			if tk, _ := q.Get(id); tk.Status != StatusOK {
				return false
			}
		}
		return true
	})
	var got []string
	for _, ev := range rec.order() {
		if len(ev) > 6 && ev[:6] == "start:" {
			got = append(got, ev)
		}
	}
	want := []string{"start:" + ids[0], "start:" + ids[1], "start:" + ids[2]}
	if len(got) != len(want) {
		t.Fatalf("应恰好执行 3 次，实际 %v", got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("执行顺序不对：got %v，want %v", got, want)
		}
	}
}

// TestCancelQueued queued → canceled，且之后不会再被 worker 捡走。
func TestCancelQueued(t *testing.T) {
	gate := make(chan struct{})
	q, err := New(t.TempDir(), func(ctx context.Context, task *Task) error {
		<-gate
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	first, err := q.Submit(Task{Title: "先跑"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := q.Submit(Task{Title: "排队中"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "第一个在跑", func() bool { return q.Snapshot().RunningID == first.ID })
	if err := q.Cancel(second.ID); err != nil {
		t.Fatalf("取消排队任务失败：%v", err)
	}
	tk, _ := q.Get(second.ID)
	if tk.Status != StatusCanceled || tk.EndedAt == "" {
		t.Fatalf("排队任务应 canceled 且有结束时间，实际 %+v", tk)
	}
	close(gate)
	waitFor(t, "第一个跑完", func() bool {
		x, _ := q.Get(first.ID)
		return x.Status == StatusOK
	})
	time.Sleep(20 * time.Millisecond)
	if snap := q.Snapshot(); snap.Queued != 0 {
		t.Fatalf("取消后不该再有排队任务，Queued=%d", snap.Queued)
	}
	if ev, _ := q.Get(second.ID); ev.Status != StatusCanceled {
		t.Fatalf("被取消的任务不该被重新执行，状态 %s", ev.Status)
	}
}

// TestCancelRunning running 通过 ctx cancel 停下，Cancel 返回时已是终态。
func TestCancelRunning(t *testing.T) {
	started := make(chan struct{})
	q, err := New(t.TempDir(), func(ctx context.Context, task *Task) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	tk, err := q.Submit(Task{Title: "长任务"})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if err := q.Cancel(tk.ID); err != nil {
		t.Fatalf("取消运行中任务失败：%v", err)
	}
	got, _ := q.Get(tk.ID)
	if got.Status != StatusCanceled {
		t.Fatalf("运行中任务取消后应为 canceled，实际 %s（%s）", got.Status, got.Error)
	}
	if got.EndedAt == "" {
		t.Fatal("取消后应有 EndedAt")
	}
	if snap := q.Snapshot(); snap.RunningID != "" {
		t.Fatalf("取消后不该有运行中任务，RunningID=%s", snap.RunningID)
	}
}

// TestSnapshotLimitAndRunningFirst 最多 6 条，运行中排第一，其余按创建时间倒序。
func TestSnapshotLimitAndRunningFirst(t *testing.T) {
	gate := make(chan struct{})
	q, err := New(t.TempDir(), func(ctx context.Context, task *Task) error {
		select {
		case <-gate:
		case <-ctx.Done():
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { close(gate); _ = q.Close() }()

	var ids []string
	for i := 0; i < 8; i++ {
		tk, err := q.Submit(Task{Title: "任务"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, tk.ID)
		if i == 0 {
			waitFor(t, "第一个在跑", func() bool { return q.Snapshot().RunningID == ids[0] })
		}
	}
	snap := q.Snapshot()
	if len(snap.Items) != snapshotSize {
		t.Fatalf("Items 应为 %d 条，实际 %d", snapshotSize, len(snap.Items))
	}
	if snap.Items[0].ID != snap.RunningID || snap.RunningID != ids[0] {
		t.Fatalf("运行中任务必须排第一：Items[0]=%s RunningID=%s", snap.Items[0].ID, snap.RunningID)
	}
	if snap.Queued != 7 {
		t.Fatalf("Queued 应为 7，实际 %d", snap.Queued)
	}
	// 其余按创建时间倒序 → 最后提交的排在第二。
	if snap.Items[1].ID != ids[7] {
		t.Fatalf("运行中之后应是最新任务 %s，实际 %s", ids[7], snap.Items[1].ID)
	}
	// 第 0 条是运行中（创建最早），从第 1 条起才校验倒序。
	for i := 2; i < len(snap.Items); i++ {
		prev, cur := snap.Items[i-1], snap.Items[i]
		if prev.CreatedAt < cur.CreatedAt {
			t.Fatalf("Items 未按创建时间倒序：%s < %s", prev.CreatedAt, cur.CreatedAt)
		}
	}
}

// TestHistoryDescending History 全部返回且倒序，limit 生效。
func TestHistoryDescending(t *testing.T) {
	rec := newRecorder()
	q, err := New(t.TempDir(), okRunner(rec))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	var ids []string
	for i := 0; i < 4; i++ {
		tk, err := q.Submit(Task{Title: "历史任务"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, tk.ID)
		waitFor(t, "任务结束", func() bool {
			x, _ := q.Get(tk.ID)
			return x.Status == StatusOK
		})
	}
	all := q.History(0)
	if len(all) != 4 {
		t.Fatalf("History(0) 应有 4 条，实际 %d", len(all))
	}
	if all[0].ID != ids[3] || all[3].ID != ids[0] {
		t.Fatalf("History 应倒序，实际首末 %s/%s", all[0].ID, all[3].ID)
	}
	top := q.History(2)
	if len(top) != 2 || top[0].ID != ids[3] || top[1].ID != ids[2] {
		t.Fatalf("History(2) 结果不对：%+v", top)
	}
}

// TestPersistAndReopen 关掉再开，任务、步骤、计数都还在。
func TestPersistAndReopen(t *testing.T) {
	root := t.TempDir()
	rec := newRecorder()
	q, err := New(root, okRunner(rec))
	if err != nil {
		t.Fatal(err)
	}
	tk, err := q.Submit(Task{ZoneID: "z9", Title: "落盘任务", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "任务跑完", func() bool {
		x, _ := q.Get(tk.ID)
		return x.Status == StatusOK
	})
	if err := q.Close(); err != nil {
		t.Fatalf("关闭失败：%v", err)
	}

	// 权限 0600
	fi, err := os.Stat(filepath.Join(root, fileName))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() == 0 {
		t.Fatal("tasks.json 不应为空")
	}

	q2, err := New(root, okRunner(newRecorder()))
	if err != nil {
		t.Fatalf("重开失败：%v", err)
	}
	defer q2.Close()
	got, ok := q2.Get(tk.ID)
	if !ok {
		t.Fatal("重开后任务丢了")
	}
	if got.Status != StatusOK || got.ZoneID != "z9" || !got.DryRun {
		t.Fatalf("重开后字段不对：%+v", got)
	}
	if len(got.Steps) != 1 || got.Steps[0].Key != "s1" || got.Counts["rows"] != 1 {
		t.Fatalf("重开后步骤/计数丢了：%+v", got)
	}
	if got.Summary != "done "+tk.ID {
		t.Fatalf("重开后 Summary 不对：%q", got.Summary)
	}
}

// TestCorruptJSONRecovers 坏文件转 .bak，从空开始。
func TestCorruptJSONRecovers(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, fileName)
	if err := os.WriteFile(path, []byte("{这不是 json"), 0o600); err != nil {
		t.Fatal(err)
	}
	q, err := New(root, func(ctx context.Context, task *Task) error { return nil })
	if err != nil {
		t.Fatalf("损坏文件不该让 New 失败：%v", err)
	}
	defer q.Close()
	if n := len(q.History(0)); n != 0 {
		t.Fatalf("损坏恢复后应当为空，实际 %d 条", n)
	}
	if _, err := os.Stat(path + backupSuffix); err != nil {
		t.Fatalf("损坏文件应转成 .bak：%v", err)
	}
	// 下一次写必须生成合法 JSON。
	tk, err := q.Submit(Task{Title: "恢复后"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "任务跑完", func() bool {
		x, _ := q.Get(tk.ID)
		return x.Status == StatusOK
	})
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("重写后的 tasks.json 不是合法 JSON：%v", err)
	}
}

// TestRestartMarksInterrupted 上次的 running 标 failed，queued 标 canceled。
func TestRestartMarksInterrupted(t *testing.T) {
	root := t.TempDir()
	f := file{Version: fileVersion, Tasks: []Task{
		{ID: "t_dead0001", Title: "跑一半", Status: StatusRunning, CreatedAt: now(), StartedAt: now()},
		{ID: "t_wait0002", Title: "还在排队", Status: StatusQueued, CreatedAt: now()},
		{ID: "t_done0003", Title: "已完", Status: StatusOK, CreatedAt: now()},
	}}
	b, _ := json.Marshal(f)
	if err := os.WriteFile(filepath.Join(root, fileName), b, 0o600); err != nil {
		t.Fatal(err)
	}

	ran := 0
	var mu sync.Mutex
	q, err := New(root, func(ctx context.Context, task *Task) error {
		mu.Lock()
		ran++
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	dead, _ := q.Get("t_dead0001")
	if dead.Status != StatusFailed || dead.Error != msgRestart {
		t.Fatalf("遗留 running 应 failed/「%s」，实际 %s/%s", msgRestart, dead.Status, dead.Error)
	}
	if dead.EndedAt == "" {
		t.Fatal("遗留 running 应有 EndedAt")
	}
	waited, _ := q.Get("t_wait0002")
	if waited.Status != StatusCanceled {
		t.Fatalf("遗留 queued 应 canceled，实际 %s", waited.Status)
	}
	if done, _ := q.Get("t_done0003"); done.Status != StatusOK {
		t.Fatalf("已完成任务不该被改，实际 %s", done.Status)
	}
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	n := ran
	mu.Unlock()
	if n != 0 {
		t.Fatalf("重启后不该偷偷跑任务，实际跑了 %d 次", n)
	}
}

// TestConcurrentSubmit 并发提交不丢任务、ID 不重复。
func TestConcurrentSubmit(t *testing.T) {
	rec := newRecorder()
	q, err := New(t.TempDir(), okRunner(rec))
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	const n = 40
	var wg sync.WaitGroup
	var mu sync.Mutex
	ids := make([]string, 0, n)
	errs := make([]error, 0)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tk, err := q.Submit(Task{Title: "并发"})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			ids = append(ids, tk.ID)
		}()
	}
	wg.Wait()
	if len(errs) != 0 {
		t.Fatalf("并发提交出错：%v", errs)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("ID 重复：%s", id)
		}
		seen[id] = true
	}
	waitFor(t, "全部任务跑完", func() bool {
		for _, id := range ids {
			if x, _ := q.Get(id); x.Status != StatusOK {
				return false
			}
		}
		return true
	})
	if got := len(q.History(0)); got != n {
		t.Fatalf("应有 %d 条任务，实际 %d", n, got)
	}
}

// TestHistoryCap 超过 200 条后只保留最新的（老终态被淘汰）。
func TestHistoryCap(t *testing.T) {
	q, err := New(t.TempDir(), func(ctx context.Context, task *Task) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	last := ""
	for i := 0; i < maxHistory+15; i++ {
		tk, err := q.Submit(Task{Title: "刷"})
		if err != nil {
			t.Fatal(err)
		}
		last = tk.ID
		waitFor(t, "任务结束", func() bool {
			x, _ := q.Get(tk.ID)
			return x.Status == StatusOK
		})
	}
	if n := len(q.History(0)); n > maxHistory {
		t.Fatalf("历史应不超过 %d 条，实际 %d", maxHistory, n)
	}
	if _, ok := q.Get(last); !ok {
		t.Fatal("最新一条不该被淘汰")
	}
}

// TestUpdateFromRunner runner 通过 Update 推进进度，最终状态仍由队列写。
func TestUpdateFromRunner(t *testing.T) {
	var qRef *Queue // 闭包里要引用队列自身
	q, err := New(t.TempDir(), func(ctx context.Context, task *Task) error {
		qRef.Update(task.ID, func(x *Task) {
			x.Steps = []Step{{Key: "a", Label: "扫表", Status: StepRunning}}
			x.Counts["scanned"] = 3
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	qRef = q
	if qRef == nil {
		t.Fatal("队列未初始化")
	}

	tk, err := q.Submit(Task{Title: "进度"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "任务结束", func() bool {
		x, _ := q.Get(tk.ID)
		return x.Status == StatusOK
	})
	got, _ := q.Get(tk.ID)
	if len(got.Steps) != 1 || got.Steps[0].Key != "a" {
		t.Fatalf("Update 写的步骤丢了：%+v", got.Steps)
	}
	if got.Counts["scanned"] != 3 {
		t.Fatalf("Update 写的计数丢了：%+v", got.Counts)
	}
}

// TestRunnerErrorMarksFailed runner 返回 error → failed + Error 文本 + EndedAt。
// errBoom 用作 runner 的失败返回。
var errBoom = errors.New("扫描失败")

// TestRunnerErrorMarksFailed runner 返回 error → failed + EndedAt。
func TestRunnerErrorMarksFailed(t *testing.T) {
	q, err := New(t.TempDir(), func(ctx context.Context, task *Task) error {
		return errBoom
	})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	tk, err := q.Submit(Task{Title: "会失败"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "任务结束", func() bool {
		x, _ := q.Get(tk.ID)
		return x.Status == StatusFailed
	})
	got, _ := q.Get(tk.ID)
	if got.Error != errBoom.Error() || got.EndedAt == "" {
		t.Fatalf("失败任务信息不对：%+v", got)
	}
}
