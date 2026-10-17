package jobs

import (
	"bytes"
	"context"
	"database/sql"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"onessh/internal/cryptox"
	"onessh/internal/events"
	"onessh/internal/execx"
	"onessh/internal/sshpool"
	"onessh/internal/store"
)

// 主机接受 TCP 却永不发送 SSH banner：单个任务的握手要等满 15 秒。列表刷新必须在预算内返回，
// 且没能确认的任务保留快照并标出原因，不能从列表里消失。
func TestRefreshAllBoundsUnreachableHost(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { conn.Close() })
		}
	}()
	box, err := cryptox.New(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	enc, err := box.Seal([]byte("pass"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := st.CreateHost(ctx, store.Host{Name: "silent", Addr: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, Username: "test", AuthType: "password", PasswordEnc: enc})
	if err != nil {
		t.Fatal(err)
	}
	pool := sshpool.New(st, box)
	defer pool.Close()
	manager := New(st, pool, execx.New(t.TempDir()), events.New())

	saved := listRefreshTimeout
	listRefreshTimeout = 300 * time.Millisecond
	defer func() { listRefreshTimeout = saved }()

	list := []store.Job{
		{ID: "a", HostID: h.ID, Status: "running", LogBytes: 5},
		{ID: "b", HostID: h.ID, Status: "exited", ExitCode: sql.NullInt64{Int64: 0, Valid: true}},
		{ID: "c", HostID: h.ID, Status: "running"},
	}
	started := time.Now()
	out := manager.RefreshAll(ctx, list)
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("列表刷新耗时 %s，没有受预算约束", elapsed)
	}
	if len(out) != len(list) {
		t.Fatalf("返回 %d 条，期望 %d 条", len(out), len(list))
	}
	for i, st := range out {
		if st.Job.ID != list[i].ID || st.Job.Status != list[i].Status {
			t.Fatalf("第 %d 条 = %+v，期望保持 %s/%s", i, st.Job, list[i].ID, list[i].Status)
		}
	}
	if out[0].RefreshError == "" || out[2].RefreshError == "" || out[0].LogBytes != 5 {
		t.Fatalf("连不上的运行中任务应带快照与 refresh_error: %+v", out)
	}
	if out[1].RefreshError != "" {
		t.Fatalf("已结束任务不需要登录主机: %+v", out[1])
	}
}

func TestSyncCommandRunFromFinishedJob(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	run := store.CommandRun{ID: "run", Tool: "job_start", Host: "web", Command: "true", Cwd: "~", StartedAt: 1000}
	if err = st.CreateCommandRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	job := store.Job{ID: "job", HostID: 1, Command: "true", Cwd: "~", Status: "running", StartedAt: 1}
	if err = st.CreateJobForCommandRun(ctx, job, run.ID); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{Store: st, Events: events.New()}
	code := 0
	job.Status = "exited"
	job.ExitCode = sql.NullInt64{Int64: 0, Valid: true}
	job.FinishedAt = sql.NullInt64{Int64: 2, Valid: true}
	job.LogBytes = 12
	manager.syncCommandRun(job, &code)
	stored, err := st.GetCommandRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "succeeded" || stored.ExitCode.Int64 != 0 || stored.FinishedAt.Int64 != 2000 {
		t.Fatalf("后台任务没有同步到命令记录: %#v", stored)
	}
}

func TestTrackedJobCommandRecordsExplicitExit(t *testing.T) {
	dir := t.TempDir()
	script := `d=` + execx.SHQ(dir) + `; export d; ` + trackedJobCommand("exit 7")
	result := exec.Command("sh", "-c", script)
	if output, err := result.CombinedOutput(); err != nil {
		t.Fatalf("外层 shell 不应随用户 exit 退出: %v output=%s", err, output)
	}
	code, err := os.ReadFile(filepath.Join(dir, "exit"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(code)) != "7" {
		t.Fatalf("记录的退出码=%q，期望 7", code)
	}
}

func TestKillFinishedJobIsNoOp(t *testing.T) {
	manager := &Manager{}
	for _, status := range []string{"exited", "killed", "lost"} {
		if err := manager.Kill(context.Background(), store.Job{Status: status}, "INVALID"); err != nil {
			t.Fatalf("已结束任务 %s 再次终止应直接成功: %v", status, err)
		}
	}
}
