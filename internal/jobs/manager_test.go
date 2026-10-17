package jobs

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"onessh/internal/events"
	"onessh/internal/execx"
	"onessh/internal/store"
)

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
