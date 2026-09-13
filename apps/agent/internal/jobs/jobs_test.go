package jobs

import (
	"testing"
	"time"
)

// TestParseCronBasics 常见写法要能解析。
func TestParseCronBasics(t *testing.T) {
	ok := []string{"30 17 * * *", "*/5 * * * *", "0 9 * * 1-5", "0 0 1 * *", "0,30 * * * *"}
	for _, e := range ok {
		if _, err := ParseCron(e); err != nil {
			t.Errorf("%q 应该能解析：%v", e, err)
		}
	}
	bad := []string{"", "* * *", "60 * * * *", "0 24 * * *", "*/0 * * * *", "@daily", "* * 32 * *"}
	for _, e := range bad {
		if _, err := ParseCron(e); err == nil {
			t.Errorf("%q 应该报错", e)
		}
	}
}

// TestMatchDaily1730 用户指定的"每天 17:30"要精确命中。
func TestMatchDaily1730(t *testing.T) {
	m, _ := ParseCron("30 17 * * *")
	hit := time.Date(2026, 9, 13, 17, 30, 0, 0, time.Local)
	miss := time.Date(2026, 9, 13, 17, 31, 0, 0, time.Local)
	if !m.Match(hit) {
		t.Error("17:30 应命中")
	}
	if m.Match(miss) {
		t.Error("17:31 不该命中")
	}
}

// TestMatchWeekdays 工作日限定（1-5 = 周一到周五）。
func TestMatchWeekdays(t *testing.T) {
	m, _ := ParseCron("0 9 * * 1-5")
	monday := time.Date(2026, 9, 14, 9, 0, 0, 0, time.Local) // 周一
	sunday := time.Date(2026, 9, 13, 9, 0, 0, 0, time.Local) // 周日
	if !m.Match(monday) {
		t.Error("周一 9:00 应命中")
	}
	if m.Match(sunday) {
		t.Error("周日不该命中")
	}
}

// TestStoreAddValidatesCron 存进一条永远不跑的任务比报错更糟 —— 必须校验。
func TestStoreAddValidatesCron(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(Job{Name: "坏任务", Schedule: "不是cron"}); err == nil {
		t.Fatal("非法时间应报错")
	}
	j, err := s.Add(Job{Name: "每日体检"})
	if err != nil {
		t.Fatal(err)
	}
	// 默认应是 17:30（用户指定）
	if j.Schedule != "30 17 * * *" {
		t.Errorf("默认时间应为 30 17 * * *，得到 %q", j.Schedule)
	}
	if j.NextRun == "" {
		t.Error("应算出下次运行时间")
	}
	if j.Kind != KindScan {
		t.Errorf("默认类型应为只读体检，得到 %q", j.Kind)
	}
}

// TestDueFiresOnce 到点触发且同一分钟不重复触发。
func TestDueFiresOnce(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	j, _ := s.Add(Job{Name: "t", Schedule: "30 17 * * *"})
	j.Enabled = true
	_, _ = s.Update(j.ID, func(x *Job) { x.Enabled = true })

	at := time.Date(2026, 9, 13, 17, 30, 10, 0, time.Local)
	due := s.Due(at)
	if len(due) != 1 {
		t.Fatalf("17:30 应触发 1 个，得到 %d", len(due))
	}
	// 记一次运行后，同一分钟不再触发
	_ = s.RecordRun(Run{JobID: due[0].ID, Name: due[0].Name,
		Started: at.Format("2006-01-02 15:04"), OK: true})
	if again := s.Due(at); len(again) != 0 {
		t.Fatalf("同一分钟不该重复触发，得到 %d", len(again))
	}
}

// TestDisabledNeverDue 关掉的任务不该跑（用户要有开关）。
func TestDisabledNeverDue(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	j, _ := s.Add(Job{Name: "t", Schedule: "30 17 * * *"}) // Add 默认 Enabled=false
	at := time.Date(2026, 9, 13, 17, 30, 10, 0, time.Local)
	if due := s.Due(at); len(due) != 0 {
		t.Fatalf("新建任务默认不该自动跑（要用户显式开启），得到 %d", len(due))
	}
	_, _ = s.Update(j.ID, func(x *Job) { x.Enabled = true })
	if due := s.Due(at); len(due) != 1 {
		t.Fatalf("开启后应触发，得到 %d", len(due))
	}
}

// TestRunHistoryKept 运行历史要留（可回查"上次什么时候跑的"）。
func TestRunHistoryKept(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	j, _ := s.Add(Job{Name: "t"})
	_ = s.RecordRun(Run{JobID: j.ID, Name: "t", Started: "2026-09-13 17:30", OK: true, Summary: "无问题"})
	rs := s.RecentRuns(10)
	if len(rs) != 1 || rs[0].Summary != "无问题" {
		t.Fatalf("应留运行历史，得到 %+v", rs)
	}
	// 任务上也要回填状态
	list := s.List()
	if list[0].LastRun == "" || !list[0].LastOK {
		t.Errorf("任务应回填上次运行状态：%+v", list[0])
	}
}

// TestDescribe 人话描述（界面显示"每天 17:30"）。
func TestDescribe(t *testing.T) {
	if got := Describe("30 17 * * *"); got != "每天 17:30" {
		t.Errorf("得到 %q", got)
	}
	if got := Describe("0 9 * * *"); got != "每天 09:00" {
		t.Errorf("得到 %q", got)
	}
	if got := Describe("坏"); got != "时间不合法" {
		t.Errorf("得到 %q", got)
	}
}

// TestPersistence 落盘（重启后任务还在）。
func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	_, _ = s.Add(Job{Name: "每日体检", Schedule: "30 17 * * *"})
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	list := s2.List()
	if len(list) != 1 || list[0].Name != "每日体检" {
		t.Fatalf("应读回任务，得到 %+v", list)
	}
}
