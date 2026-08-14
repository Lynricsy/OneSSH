//go:build integration

package jobs

import (
	"bytes"
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"

	"onessh/internal/cryptox"
	"onessh/internal/events"
	"onessh/internal/execx"
	"onessh/internal/sshpool"
	"onessh/internal/store"
)

func TestRealJobDefaultCwd(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	st, err := store.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, err := cryptox.New(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	enc, err := box.Seal([]byte("pass"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := st.CreateHost(ctx, store.Host{Name: "ssh", Addr: "127.0.0.1", Port: 2222, Username: "test", AuthType: "password", PasswordEnc: enc, MonitorEnabled: true, KeyID: sql.NullInt64{}})
	if err != nil {
		t.Fatal(err)
	}
	pool := sshpool.New(st, box)
	defer pool.Close()
	m := New(st, pool, execx.New(data), events.New())
	j, err := m.Start(ctx, h, 1, "sleep 1; echo done", "~", nil)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != "running" {
		t.Fatalf("启动状态 %s", j.Status)
	}
	time.Sleep(2 * time.Second)
	status, err := m.Refresh(ctx, j)
	if err != nil {
		t.Fatal(err)
	}
	if status.Job.Status != "exited" || status.Job.ExitCode == nil || *status.Job.ExitCode != 0 {
		t.Fatalf("结束状态 %+v", status.Job)
	}
	logs, err := m.Logs(ctx, j, 100, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs, "done") {
		t.Fatalf("日志 %q", logs)
	}

	client, err := pool.Get(ctx, h.Name)
	if err != nil {
		t.Fatal(err)
	}
	waitForExitMarker := func(id string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			result, runErr := m.Exec.Run(ctx, client, `test -f "$HOME/.onessh/jobs/`+id+`/exit"`, "~", nil, execx.Options{Timeout: 2 * time.Second, MaxLines: 2})
			if runErr == nil && result.ExitCode == 0 {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("任务 %s 未写入退出标记", id)
	}

	alreadyDone, err := m.Start(ctx, h, 1, "printf already", "~", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitForExitMarker(alreadyDone.ID)
	if err = m.Kill(ctx, alreadyDone, "TERM"); err != nil {
		t.Fatal(err)
	}
	storedDone, err := st.GetJob(ctx, alreadyDone.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedDone.Status != "exited" || !storedDone.ExitCode.Valid || storedDone.ExitCode.Int64 != 0 {
		t.Fatalf("远端已结束任务不应被改成 killed: %#v", storedDone)
	}

	graceful, err := m.Start(ctx, h, 1, `trap 'sleep 0.2; printf final; exit 0' TERM; printf start; while :; do sleep 1; done`, "~", nil)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if err = m.Kill(ctx, graceful, "TERM"); err != nil {
		t.Fatal(err)
	}
	storedGraceful, err := st.GetJob(ctx, graceful.ID)
	if err != nil {
		t.Fatal(err)
	}
	sizeResult, err := m.Exec.Run(ctx, client, `wc -c < "$HOME/.onessh/jobs/`+graceful.ID+`/out.log"`, "~", nil, execx.Options{Timeout: 2 * time.Second, MaxLines: 2})
	if err != nil {
		t.Fatal(err)
	}
	finalSize, err := strconv.ParseInt(strings.TrimSpace(sizeResult.Output), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if storedGraceful.Status != "killed" || storedGraceful.LogBytes != finalSize || finalSize <= int64(len("start")) {
		t.Fatalf("终止状态或最终日志大小异常: job=%#v remote_size=%d", storedGraceful, finalSize)
	}

	stubborn, err := m.Start(ctx, h, 1, `trap '' TERM; printf stubborn; while :; do sleep 1; done`, "~", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !stubborn.UsedSetsid {
		t.Fatal("集成夹具缺少 setsid，无法验证进程组二次强杀")
	}
	defer func() {
		_, _ = m.Exec.Run(context.Background(), client, `kill -KILL -- -`+strconv.FormatInt(stubborn.PID.Int64, 10), "~", nil, execx.Options{Timeout: 2 * time.Second, MaxLines: 2})
	}()
	time.Sleep(200 * time.Millisecond)
	if err = m.Kill(ctx, stubborn, "TERM"); err == nil {
		t.Fatal("忽略 TERM 的任务不应被提前标记为结束")
	}
	stillRunning, err := st.GetJob(ctx, stubborn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stillRunning.Status != "running" {
		t.Fatalf("TERM 超时后任务状态不应提前变化: %#v", stillRunning)
	}
	if err = m.Kill(ctx, stillRunning, "KILL"); err != nil {
		t.Fatal(err)
	}
	killed, err := st.GetJob(ctx, stubborn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if killed.Status != "killed" {
		t.Fatalf("leader 退出后的残留进程组未被 KILL: %#v", killed)
	}

	foreignResult, err := m.Exec.Run(ctx, client, `nohup sleep 30 >/dev/null 2>&1 & echo "$!"`, "~", nil, execx.Options{Timeout: 2 * time.Second, MaxLines: 2})
	if err != nil {
		t.Fatal(err)
	}
	foreignPID, err := strconv.ParseInt(strings.TrimSpace(foreignResult.Output), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = m.Exec.Run(context.Background(), client, `kill -KILL -- `+strconv.FormatInt(foreignPID, 10), "~", nil, execx.Options{Timeout: 2 * time.Second, MaxLines: 2})
	}()
	stale := store.Job{
		ID: "stale-reused-pid", HostID: h.ID, Command: "old", Cwd: "~",
		PID: sql.NullInt64{Int64: foreignPID, Valid: true}, Status: "running", StartedAt: time.Now().Unix(),
	}
	if err = st.CreateJob(ctx, stale); err != nil {
		t.Fatal(err)
	}
	if err = m.Kill(ctx, stale, "TERM"); err != nil {
		t.Fatal(err)
	}
	probe, err := m.Exec.Run(ctx, client, `kill -0 -- `+strconv.FormatInt(foreignPID, 10), "~", nil, execx.Options{Timeout: 2 * time.Second, MaxLines: 2})
	if err != nil || probe.ExitCode != 0 {
		t.Fatalf("PID 归属不匹配时误杀了无关进程: result=%#v err=%v", probe, err)
	}
	storedStale, err := st.GetJob(ctx, stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedStale.Status != "lost" {
		t.Fatalf("PID 归属不匹配应标记 lost: %#v", storedStale)
	}
}
