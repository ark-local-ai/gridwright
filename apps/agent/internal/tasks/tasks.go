// Package tasks 是 gridwright 的任务队列：作业区界面上的「运行」按钮，和对话里
// 让模型「帮我建个任务」，最终都落到这里的一条 Task 上，共用同一套排队、进度、
// 取消与历史。
//
// 为什么一次只跑一个：一本 Excel 台账只有一个写者。两个任务同时改同一批工作簿，
// 轻则互相覆盖、重则留下半写的文件；工作区本来就有 filelock，争锁只会变成
// 「随机一个任务失败」。串行虽然慢一点，但顺序可预期、进度条说得清、失败原因
// 也能归到具体某一步上。界面同时展示最近 6 条，其余进历史，是为了不让「跑了
// 一下午的 200 条」把界面撑爆。
//
// 为什么持久化：任务往往跑几分钟到几十分钟，期间用户会刷新页面、切工作区、
// 甚至重启进程。没有 tasks.json，刷新一次就「任务不见了」，用户会重复点运行。
// 落盘之后，重启还能看到上次跑到哪一步、错在哪。反过来，重启时不能把上次的
// 状态照搬过来执行：遗留的 running 一定已经中断（标 failed「引擎重启中断」），
// 遗留的 queued 一律标 canceled —— 见 restore 里的注释。
//
// 并发模型：一把 sync.Mutex 保护内存状态，所有对外的读都返回副本；runner 拿到
// 的是任务副本，绝不与读者共享内存。写盘也在锁内完成（数据量小、每次几百字节），
// 换来的是「盘上顺序 == 内存顺序」这个简单保证。
package tasks

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// 任务状态。queued → running → ok|failed|canceled，终态不再变化。
const (
	StatusQueued   = "queued"
	StatusRunning  = "running"
	StatusOK       = "ok"
	StatusFailed   = "failed"
	StatusCanceled = "canceled"
)

// 步骤状态。
const (
	StepPending = "pending"
	StepRunning = "running"
	StepOK      = "ok"
	StepSkipped = "skipped"
	StepFailed  = "failed"
)

const (
	fileName     = "tasks.json"
	backupSuffix = ".bak"
	// maxHistory 是保留的任务条数上限（含运行中的）。再多就是垃圾，用户也不会翻。
	maxHistory = 200
	// snapshotSize 是界面一次展示的条数（运行中优先）。
	snapshotSize = 6
	// timeLayout 固定毫秒精度：RFC3339Nano 会省掉尾部零，字符串排序就不再等于时间排序。
	timeLayout = "2006-01-02T15:04:05.000Z07:00"
	// cancelWait 是 Cancel 等待 running 任务真正停下的上限；runner 不响应 ctx 时
	// 宁可报错也不要让调用方（HTTP handler）永久挂住。
	cancelWait = 10 * time.Second

	msgRestart       = "引擎重启中断"
	msgRestartQueued = "引擎重启中断，未执行"
	msgShutdown      = "引擎关闭中断"
	msgCanceled      = "已取消"
)

// 终态错误的兜底文案。
var errClosed = errors.New("任务队列已关闭")

// Step 是任务里的一步。Status 取 pending|running|ok|skipped|failed。
type Step struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// Task 是一条任务。Status 取 queued|running|ok|failed|canceled。
type Task struct {
	ID     string `json:"id"`
	ZoneID string `json:"zoneId"`
	Title  string `json:"title"`
	// Source 标记任务从哪里来：chat（对话采纳）/ manual（接口/界面直跑）。
	// 看板只展示 chat，避免“我没在对话里说过，怎么多了一次运行”。
	Source         string `json:"source,omitempty"`
	ConversationID string `json:"conversationId,omitempty"`
	// IdempotencyKey 非空时，同 key 的第二次 Submit 直接返回已有任务（不新建）。
	// 对话采纳用 conv:<id>:idx:<n>，重试用 retry:<原任务 id>。
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
	// Files 是这条任务要处理的范围（基名）；空 = 按输入目录/收件箱的默认范围。
	Files []string `json:"files,omitempty"`
	// FailedFiles 是本轮处理失败的文件（基名），供「重试失败 N 个」用。
	FailedFiles []string `json:"failedFiles,omitempty"`
	// ProposalIDs 是这条任务产出的待确认清单 id。
	ProposalIDs []string       `json:"proposalIds,omitempty"`
	Status      string         `json:"status"`
	DryRun      bool           `json:"dryRun"`
	Steps       []Step         `json:"steps"`
	Summary     string         `json:"summary"`
	Error       string         `json:"error"`
	Counts      map[string]int `json:"counts"`
	CreatedAt   string         `json:"createdAt"`
	StartedAt   string         `json:"startedAt"`
	EndedAt     string         `json:"endedAt"`
}

// Snapshot 是给界面的一帧快照。Items 最多 6 条，运行中的排第一，其余按创建时间倒序。
type Snapshot struct {
	RunningID string `json:"runningId"`
	Items     []Task `json:"items"`
	Queued    int    `json:"queued"`
}

// file 是 tasks.json 的形状。
type file struct {
	Version int    `json:"version"`
	Tasks   []Task `json:"tasks"`
}

// fileVersion 用于将来改格式时有据可依。
const fileVersion = 1

// taskEntry 是内存里的任务：任务本体 + 创建序号（同一毫秒内也能稳定排序）+ 完成信号。
type taskEntry struct {
	task        Task
	seq         int64
	done        chan struct{}
	doneOnce    sync.Once
	cancel      context.CancelFunc // 仅在 running 期间非 nil
	cancelAsked bool               // 用户显式 Cancel 过（区别于 Close 的取消）
}

// Queue 是串行任务队列。零值不可用，请用 New。
type Queue struct {
	path   string
	runner func(context.Context, *Task) error

	mu        sync.Mutex
	tasks     map[string]*taskEntry
	fifo      []string // 待跑任务的 id，FIFO
	seq       int64
	runningID string
	patches   []func(*Task) // 运行期间 Update 打的补丁，任务结束时补到 runner 的副本上
	closed    bool

	ctx       context.Context
	cancelAll context.CancelFunc
	wake      chan struct{}
	done      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}

// New 打开（或创建）工作区根目录下的任务队列，并启动唯一的 worker。
// runner 负责真正干活：它拿到的 *Task 是私有副本，可以随意改 Steps/Counts/Summary，
// 返回后由队列决定最终 Status、EndedAt 并落盘。
func New(root string, runner func(context.Context, *Task) error) (*Queue, error) {
	if root == "" {
		return nil, errors.New("任务队列需要工作区根目录")
	}
	if runner == nil {
		return nil, errors.New("任务队列需要 runner")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("创建任务目录 %s: %w", root, err)
	}
	q := &Queue{
		path:   filepath.Join(root, fileName),
		runner: runner,
		tasks:  make(map[string]*taskEntry),
		wake:   make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	q.ctx, q.cancelAll = context.WithCancel(context.Background())

	if err := q.load(); err != nil {
		return nil, err
	}
	q.trimLocked()
	// 载入时就修正过状态（running→failed、queued→canceled），必须立刻写回去，
	// 否则下次重启会看到同一批「僵尸 running」。
	if err := q.persistLocked(); err != nil {
		return nil, err
	}

	q.wg.Add(1)
	go q.loop()
	return q, nil
}

// Path 返回持久化文件路径，便于日志与排查。
func (q *Queue) Path() string { return q.path }

// Submit 入队一条任务。ID 为空时生成 t_+8 位十六进制；状态一律重置为 queued，
// 时间戳与上一次的错误也清空（同一条 Task 结构体被复用时不至于串味）。
func (q *Queue) Submit(t Task) (Task, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return Task{}, errClosed
	}
	// 幂等：同一个 key 已经有一条任务（无论什么状态）就直接返回它。
	// 用户在对话里连点两次采纳、或网络重放，都不该变成两次改表。
	if t.IdempotencyKey != "" {
		if existing, ok := q.findByKeyLocked(t.IdempotencyKey); ok {
			return cloneTask(existing), nil
		}
	}
	if t.ID == "" {
		id, err := newID()
		if err != nil {
			return Task{}, err
		}
		t.ID = id
	}
	if _, ok := q.tasks[t.ID]; ok {
		return Task{}, fmt.Errorf("任务 %s 已存在", t.ID)
	}
	t.Status = StatusQueued
	if t.CreatedAt == "" {
		t.CreatedAt = now()
	}
	t.StartedAt, t.EndedAt, t.Error = "", "", ""
	if t.Steps == nil {
		t.Steps = []Step{}
	}
	if t.Counts == nil {
		t.Counts = map[string]int{}
	}
	q.seq++
	e := &taskEntry{task: t, seq: q.seq, done: make(chan struct{})}
	q.tasks[t.ID] = e
	q.fifo = append(q.fifo, t.ID)
	q.trimLocked()
	if err := q.persistLocked(); err != nil {
		// 任务仍在队列里（内存里已经排上了），但调用方必须知道盘上没落成。
		return Task{}, err
	}
	q.signal()
	return cloneTask(t), nil
}

// FindByKey 按幂等键找任务。同一个 key 理论上只有一条；万一有历史重复，
// 返回最新提交的那条（seq 最大），保证调用方与「刚刚发生的事」对应。
func (q *Queue) FindByKey(key string) (Task, bool) {
	if key == "" {
		return Task{}, false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.findByKeyLocked(key)
}

// findByKeyLocked 是 FindByKey 的锁内实现。调用方必须持有 q.mu。
func (q *Queue) findByKeyLocked(key string) (Task, bool) {
	var best *taskEntry
	for _, e := range q.tasks {
		if e.task.IdempotencyKey != key {
			continue
		}
		if best == nil || e.seq > best.seq {
			best = e
		}
	}
	if best == nil {
		return Task{}, false
	}
	return cloneTask(best.task), true
}

// Cancel 取消一条任务：queued 直接置 canceled；running 则 cancel 掉 ctx，
// 并等到 runner 真正返回（最多 cancelWait），保证返回时状态已是终态。
func (q *Queue) Cancel(id string) error {
	q.mu.Lock()
	e, ok := q.tasks[id]
	if !ok {
		q.mu.Unlock()
		return fmt.Errorf("任务 %s 不存在", id)
	}
	switch e.task.Status {
	case StatusQueued:
		e.task.Status = StatusCanceled
		e.task.Error = msgCanceled
		e.task.EndedAt = now()
		q.removeFIFOLocked(id)
		q.finishLocked(e)
		err := q.persistLocked()
		q.mu.Unlock()
		return err
	case StatusRunning:
		e.cancelAsked = true
		cancel := e.cancel
		done := e.done
		q.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		select {
		case <-done:
			return nil
		case <-time.After(cancelWait):
			return fmt.Errorf("取消任务 %s 超时：runner 未响应 ctx", id)
		}
	default:
		st := e.task.Status
		q.mu.Unlock()
		return fmt.Errorf("任务 %s 已结束（%s），无法取消", id, st)
	}
}

// Get 取一条任务的副本。
func (q *Queue) Get(id string) (Task, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	e, ok := q.tasks[id]
	if !ok {
		return Task{}, false
	}
	return cloneTask(e.task), true
}

// Snapshot 返回界面要的一帧：运行中优先，其余按创建时间倒序，最多 6 条。
// Queued 是当前排队数（不含运行中）。
func (q *Queue) Snapshot() Snapshot {
	q.mu.Lock()
	defer q.mu.Unlock()
	s := Snapshot{RunningID: q.runningID, Items: make([]Task, 0, snapshotSize)}
	desc := q.sortedDescLocked()
	for _, e := range desc {
		if e.task.Status == StatusQueued {
			s.Queued++
		}
	}
	if e, ok := q.tasks[q.runningID]; ok && q.runningID != "" {
		s.Items = append(s.Items, cloneTask(e.task))
	}
	for _, e := range desc {
		if len(s.Items) >= snapshotSize {
			break
		}
		if e.task.ID == q.runningID {
			continue
		}
		s.Items = append(s.Items, cloneTask(e.task))
	}
	return s
}

// History 返回全部任务，创建时间倒序（同时间按入队先后倒序）。limit<=0 表示不限。
func (q *Queue) History(limit int) []Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	desc := q.sortedDescLocked()
	if limit > 0 && len(desc) > limit {
		desc = desc[:limit]
	}
	out := make([]Task, 0, len(desc))
	for _, e := range desc {
		out = append(out, cloneTask(e.task))
	}
	return out
}

// Update 在锁内改任务（通常由 runner 用来推进步骤/计数），并立刻落盘。
// runner 里也可以直接改自己那份 *Task；两种写法都能到盘上。
func (q *Queue) Update(id string, fn func(*Task)) {
	if fn == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	e, ok := q.tasks[id]
	if !ok {
		return
	}
	fn(&e.task)
	if id == q.runningID {
		// runner 手里的是副本，这里改的是盘上那份；打补丁保证两边一致。
		q.patches = append(q.patches, fn)
	}
	_ = q.persistLocked()
}

// Close 停止 worker 并等当前任务结束：先 cancel 所有任务的 ctx（runner 必须响应），
// 再关 done 通知 worker 退出，最后 wg.Wait。重复调用安全。
func (q *Queue) Close() error {
	q.closeOnce.Do(func() {
		q.mu.Lock()
		q.closed = true
		q.mu.Unlock()

		q.cancelAll()
		close(q.done)
		q.wg.Wait()

		q.mu.Lock()
		q.closeErr = q.persistLocked()
		q.mu.Unlock()
	})
	return q.closeErr
}

// ---------------------------------------------------------------------------
// worker

func (q *Queue) loop() {
	defer q.wg.Done()
	for {
		select {
		case <-q.done:
			return
		case <-q.wake:
		}
		// 醒来后把能跑的跑完；一次一个，天然串行。
		for {
			e, ok := q.takeNext()
			if !ok {
				break
			}
			q.runOne(e)
			select {
			case <-q.done:
				return
			default:
			}
		}
	}
}

// takeNext 取出队首 queued 任务（不置 running，置状态在 runOne 里做，
// 保证 running 与 runningID 同一次加锁完成）。
func (q *Queue) takeNext() (*taskEntry, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil, false
	}
	return q.popLocked()
}

func (q *Queue) popLocked() (*taskEntry, bool) {
	for len(q.fifo) > 0 {
		id := q.fifo[0]
		q.fifo = q.fifo[1:]
		e, ok := q.tasks[id]
		if !ok {
			continue
		}
		if e.task.Status != StatusQueued {
			continue
		}
		if len(q.fifo) == 0 {
			q.fifo = nil
		}
		return e, true
	}
	q.fifo = nil
	return nil, false
}

func (q *Queue) runOne(e *taskEntry) {
	q.mu.Lock()
	if q.closed || e.task.Status != StatusQueued {
		q.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(q.ctx)
	e.cancel = cancel
	e.cancelAsked = false
	e.task.Status = StatusRunning
	e.task.StartedAt = now()
	e.task.EndedAt = ""
	e.task.Error = ""
	q.runningID = e.task.ID
	q.patches = nil
	work := cloneTask(e.task) // runner 只碰副本：读者永远拿锁内的快照，-race 干净
	id := e.task.ID
	_ = q.persistLocked()
	q.mu.Unlock()

	err := q.runner(ctx, &work)

	q.mu.Lock()
	// 运行期间通过 Update 打的补丁补到副本上（runner 的直接修改优先于早先的补丁）。
	for _, p := range q.patches {
		p(&work)
	}
	q.patches = nil
	cancel()
	e.cancel = nil

	cur, ok := q.tasks[id]
	if !ok {
		// 理论上不会发生（running 不会被淘汰），真发生了也不能 panic。
		q.runningID = ""
		q.mu.Unlock()
		q.finishLocked(e)
		return
	}
	stored := cur.task
	stored.Steps = work.Steps
	stored.Counts = work.Counts
	stored.Summary = work.Summary
	// 这两个列表只有 runner 知道（谁失败、出了哪几份清单）：不写回就丢了。
	stored.ProposalIDs = work.ProposalIDs
	stored.FailedFiles = work.FailedFiles
	if work.Error != "" && err == nil {
		stored.Error = work.Error
	}
	switch {
	case e.cancelAsked:
		stored.Status = StatusCanceled
		stored.Error = msgCanceled
	case q.closed:
		stored.Status = StatusFailed
		stored.Error = msgShutdown
	case err != nil:
		stored.Status = StatusFailed
		stored.Error = err.Error()
	default:
		stored.Status = StatusOK
	}
	stored.EndedAt = now()
	if stored.StartedAt == "" {
		stored.StartedAt = stored.EndedAt
	}
	cur.task = stored
	q.runningID = ""
	q.finishLocked(cur)
	q.trimLocked()
	_ = q.persistLocked()
	q.mu.Unlock()
}

// ---------------------------------------------------------------------------
// 内部工具

// restore 处理重启遗留状态。选择：
//   - running → failed「引擎重启中断」：进程没了，任务必然断了，谎称还在跑最糟。
//   - queued → canceled 而不是保留排队：重启后没有内存里的 FIFO，也判断不出用户
//     当时是不是还在等；把几小时前的任务偷偷跑起来，比让用户重按一次更糟。
func (q *Queue) load() error {
	b, err := os.ReadFile(q.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读取 %s: %w", q.path, err)
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		// 坏文件留证据（.bak），当前进程从空开始；下一次写会生成干净文件。
		_ = os.Remove(q.path + backupSuffix)
		if rerr := os.Rename(q.path, q.path+backupSuffix); rerr != nil {
			return fmt.Errorf("备份损坏的任务文件 %s: %w", q.path, rerr)
		}
		return nil
	}
	ts := now()
	for _, t := range f.Tasks {
		if t.ID == "" {
			continue
		}
		if _, dup := q.tasks[t.ID]; dup {
			continue
		}
		t = normalizeTask(t)
		switch t.Status {
		case StatusRunning:
			t.Status = StatusFailed
			t.Error = msgRestart
			if t.EndedAt == "" {
				t.EndedAt = ts
			}
		case StatusQueued:
			t.Status = StatusCanceled
			t.Error = msgRestartQueued
			if t.EndedAt == "" {
				t.EndedAt = ts
			}
		}
		q.seq++
		e := &taskEntry{task: t, seq: q.seq, done: make(chan struct{})}
		q.finishLocked(e) // 载入的都是终态，done 直接关闭
		q.tasks[t.ID] = e
	}
	return nil
}

// persistLocked 原子写 tasks.json（临时文件 + fsync + chmod 0600 + rename）。
// 调用方必须持有 q.mu。
func (q *Queue) persistLocked() error {
	f := file{Version: fileVersion, Tasks: make([]Task, 0, len(q.tasks))}
	for _, e := range q.sortedAscLocked() {
		f.Tasks = append(f.Tasks, cloneTask(e.task))
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化任务: %w", err)
	}
	b = append(b, '\n')

	dir := filepath.Dir(q.path)
	tmp, err := os.CreateTemp(dir, "tasks-*.json.tmp")
	if err != nil {
		return fmt.Errorf("创建临时文件: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("写入临时文件: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("同步临时文件: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("关闭临时文件: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		cleanup()
		return fmt.Errorf("设置权限: %w", err)
	}
	if err := os.Rename(tmpName, q.path); err != nil {
		cleanup()
		return fmt.Errorf("替换 %s: %w", q.path, err)
	}
	return nil
}

func (q *Queue) sortedAscLocked() []*taskEntry {
	out := make([]*taskEntry, 0, len(q.tasks))
	for _, e := range q.tasks {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].seq < out[j].seq })
	return out
}

// sortedDescLocked 按创建时间倒序，同一时刻（毫秒内）按入队先后倒序。
func (q *Queue) sortedDescLocked() []*taskEntry {
	out := q.sortedAscLocked()
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].task.CreatedAt != out[j].task.CreatedAt {
			return out[i].task.CreatedAt > out[j].task.CreatedAt
		}
		return out[i].seq > out[j].seq
	})
	return out
}

// trimLocked 只淘汰最老的终态任务，running/queued 永不淘汰。
func (q *Queue) trimLocked() {
	if len(q.tasks) <= maxHistory {
		return
	}
	for _, e := range q.sortedAscLocked() {
		if len(q.tasks) <= maxHistory {
			return
		}
		if e.task.Status == StatusRunning || e.task.Status == StatusQueued {
			continue
		}
		select {
		case <-e.done:
		default:
			continue // 还在收尾，先留着
		}
		delete(q.tasks, e.task.ID)
		q.removeFIFOLocked(e.task.ID)
	}
}

func (q *Queue) removeFIFOLocked(id string) {
	for i, v := range q.fifo {
		if v == id {
			q.fifo = append(q.fifo[:i], q.fifo[i+1:]...)
			if len(q.fifo) == 0 {
				q.fifo = nil
			}
			return
		}
	}
}

func (q *Queue) finishLocked(e *taskEntry) {
	e.doneOnce.Do(func() { close(e.done) })
}

func (q *Queue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func normalizeTask(t Task) Task {
	if t.Steps == nil {
		t.Steps = []Step{}
	}
	if t.Counts == nil {
		t.Counts = map[string]int{}
	}
	return t
}

func cloneTask(t Task) Task {
	if t.Steps != nil {
		t.Steps = append([]Step(nil), t.Steps...)
	}
	if t.Counts != nil {
		m := make(map[string]int, len(t.Counts))
		for k, v := range t.Counts {
			m[k] = v
		}
		t.Counts = m
	}
	if t.Files != nil {
		t.Files = append([]string(nil), t.Files...)
	}
	if t.FailedFiles != nil {
		t.FailedFiles = append([]string(nil), t.FailedFiles...)
	}
	if t.ProposalIDs != nil {
		t.ProposalIDs = append([]string(nil), t.ProposalIDs...)
	}
	return t
}

func now() string { return time.Now().Format(timeLayout) }

// newID 生成 t_+8 位十六进制。用 crypto/rand 是为了 ID 不撞车（并发 Submit 时
// 时间戳可能完全一样）。
func newID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("生成任务 ID: %w", err)
	}
	return "t_" + hex.EncodeToString(b[:]), nil
}
