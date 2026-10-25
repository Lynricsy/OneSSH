package jobs

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"testing"
	"time"

	"onessh/internal/store"
)

// closedPort 返回一个刚释放的本地端口：连接会被立即拒绝，模拟离线主机且不拖慢测试。
func closedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func seedJob(t *testing.T, st *store.Store, j store.Job, lastChecked, finishedAt int64) {
	t.Helper()
	if err := st.CreateJob(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	var finished any
	if finishedAt > 0 {
		finished = finishedAt
	}
	if _, err := st.DB.Exec(`UPDATE jobs SET last_checked_at=?,finished_at=? WHERE id=?`, lastChecked, finished, j.ID); err != nil {
		t.Fatal(err)
	}
}

func jobStatus(t *testing.T, st *store.Store, id string) string {
	t.Helper()
	j, err := st.GetJob(context.Background(), id)
	if errors.Is(err, sql.ErrNoRows) {
		return "deleted"
	}
	if err != nil {
		t.Fatal(err)
	}
	return j.Status
}

func TestMaintainExpiresUnconfirmedAndPrunesFinished(t *testing.T) {
	useRefreshBudget(t)
	m, _, h := newRefreshManager(t, closedPort(t))
	st := m.Store
	ctx := context.Background()
	now := time.Now()
	day := int64(24 * time.Hour / time.Second)
	at := func(daysAgo int64) int64 { return now.Unix() - daysAgo*day }

	run := store.CommandRun{ID: "run-stale", Tool: "job_start", Host: h.Name, Command: "sleep", Cwd: "~", StartedAt: at(10) * 1000}
	if err := st.CreateCommandRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	stale := store.Job{ID: "stale", HostID: h.ID, Command: "sleep", Cwd: "~", Status: "running", StartedAt: at(10)}
	if err := st.CreateJobForCommandRun(ctx, stale, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`UPDATE jobs SET last_checked_at=? WHERE id='stale'`, at(8)); err != nil {
		t.Fatal(err)
	}
	// 启动很久，但最近一次确认还在 7 天内：只能算暂时连不上
	seedJob(t, st, store.Job{ID: "recent", HostID: h.ID, Command: "sleep", Cwd: "~", Status: "running", StartedAt: at(40)}, at(6), 0)
	seedJob(t, st, store.Job{ID: "old-exited", HostID: h.ID, Command: "true", Cwd: "~", Status: "exited", StartedAt: at(40)}, at(40), at(31))
	seedJob(t, st, store.Job{ID: "young-exited", HostID: h.ID, Command: "true", Cwd: "~", Status: "exited", StartedAt: at(40)}, at(40), at(29))
	// 结束时间缺失时按启动时间计算保留期
	seedJob(t, st, store.Job{ID: "old-killed", HostID: h.ID, Command: "true", Cwd: "~", Status: "killed", StartedAt: at(40)}, at(40), 0)

	report, err := m.Maintain(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if report != (MaintenanceReport{Expired: 1, Pruned: 2}) {
		t.Fatalf("维护统计 = %+v", report)
	}
	for id, want := range map[string]string{
		"stale": "lost", "recent": "running", "old-exited": "deleted", "young-exited": "exited", "old-killed": "deleted",
	} {
		if got := jobStatus(t, st, id); got != want {
			t.Errorf("任务 %s 状态 = %s，期望 %s", id, got, want)
		}
	}
	stored, err := st.GetCommandRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "lost" {
		t.Fatalf("失联任务的命令记录状态 = %s，期望 lost", stored.Status)
	}
}

// 维护被取消时刷新没有跑完，不能拿过期的确认时间断定任务失联。
func TestMaintainSkipsExpiryWhenRefreshDidNotComplete(t *testing.T) {
	useRefreshBudget(t)
	m, _, h := newRefreshManager(t, closedPort(t))
	now := time.Now()
	seedJob(t, m.Store, store.Job{ID: "stale", HostID: h.ID, Command: "sleep", Cwd: "~", Status: "running", StartedAt: now.Unix() - 864000}, now.Unix()-864000, 0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, _ := m.Maintain(ctx, now)
	if report.Expired != 0 || jobStatus(t, m.Store, "stale") != "running" {
		t.Fatalf("刷新未完成时不应标记失联: report=%+v status=%s", report, jobStatus(t, m.Store, "stale"))
	}
}
