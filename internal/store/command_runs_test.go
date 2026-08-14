package store

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestCommandRunLifecycleAndFilters(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	token, err := st.CreateToken(ctx, TokenCreate{Name: "deploy-agent", Hash: "deploy-agent-hash", AllHosts: true})
	if err != nil {
		t.Fatal(err)
	}
	run := CommandRun{
		ID: "run-1", TokenID: sql.NullInt64{Int64: token.ID, Valid: true},
		TokenName: sql.NullString{String: token.Name, Valid: true}, Tool: "exec",
		Host: "web-01", Command: "printf hello", Cwd: "/srv", StartedAt: 1000,
	}
	if err = st.CreateCommandRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	created, err := st.GetCommandRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != "running" || created.Command != run.Command || created.Seq == 0 {
		t.Fatalf("创建后的命令记录异常: %#v", created)
	}
	if err = st.FinishCommandRun(ctx, run.ID, CommandRunFinish{
		Status: "failed", ExitCode: sql.NullInt64{Int64: 7, Valid: true},
		StdoutPreview: "hello", StderrPreview: "bad", StdoutBytes: 5, StderrBytes: 3,
		OutputAvailable: true, FinishedAt: 1300,
	}); err != nil {
		t.Fatal(err)
	}
	finished, err := st.GetCommandRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != "failed" || finished.ExitCode.Int64 != 7 || finished.StdoutPreview != "hello" || !finished.OutputAvailable {
		t.Fatalf("完成后的命令记录异常: %#v", finished)
	}
	rows, err := st.ListCommandRuns(ctx, CommandRunFilter{
		TokenIDs: []int64{token.ID}, Hosts: []string{"web-01"}, Tools: []string{"exec"}, Statuses: []string{"failed"}, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != run.ID {
		t.Fatalf("筛选结果异常: %#v", rows)
	}
	rows, err = st.ListCommandRuns(ctx, CommandRunFilter{Query: "PRINTF HELLO", Limit: 10})
	if err != nil || len(rows) != 1 || rows[0].ID != run.ID {
		t.Fatalf("跨字段搜索结果异常: %#v err=%v", rows, err)
	}
	rows, err = st.ListCommandRuns(ctx, CommandRunFilter{Query: "%", Limit: 10})
	if err != nil || len(rows) != 0 {
		t.Fatalf("搜索词不应被当成 SQL 通配符: %#v err=%v", rows, err)
	}
	if err = st.DeleteToken(ctx, token.ID); err != nil {
		t.Fatal(err)
	}
	rows, err = st.ListCommandRuns(ctx, CommandRunFilter{TokenIDs: []int64{token.ID}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].TokenName.Valid || rows[0].TokenName.String != token.Name {
		t.Fatalf("删除令牌后的身份快照丢失: %#v", rows)
	}
}

func TestCommandRunRecoveryExpiryAndJobLink(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, run := range []CommandRun{
		{ID: "sync-run", Tool: "exec", Host: "web", Command: "sleep 10", Cwd: "~", StartedAt: 1},
		{ID: "job-run", Tool: "job_start", Host: "web", Command: "sleep 10", Cwd: "~", StartedAt: 2},
		{ID: "expired-run", Tool: "exec", Host: "web", Command: "old", Cwd: "~", StartedAt: 3},
		{ID: "unavailable-run", Tool: "exec", Host: "web", Command: "broken capture", Cwd: "~", StartedAt: 4},
		{ID: "fresh-run", Tool: "exec", Host: "web", Command: "fresh", Cwd: "~", StartedAt: 5},
	} {
		if err = st.CreateCommandRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	for id, finish := range map[string]CommandRunFinish{
		"expired-run":     {Status: "succeeded", OutputAvailable: true, FinishedAt: 1000},
		"unavailable-run": {Status: "failed", OutputAvailable: false, OutputError: sql.NullString{String: "磁盘写入失败", Valid: true}, FinishedAt: 1000},
		"fresh-run":       {Status: "succeeded", OutputAvailable: true, FinishedAt: 8000},
	} {
		if err = st.FinishCommandRun(ctx, id, finish); err != nil {
			t.Fatal(err)
		}
	}
	job := Job{ID: "job-1", HostID: 1, Command: "sleep 10", Cwd: "~", Status: "running", StartedAt: 1}
	if err = st.CreateJobForCommandRun(ctx, job, "job-run"); err != nil {
		t.Fatal(err)
	}
	linked, err := st.GetCommandRunByJob(ctx, job.ID)
	if err != nil || linked.ID != "job-run" || !linked.OutputAvailable {
		t.Fatalf("任务关联异常: %#v err=%v", linked, err)
	}
	if err = st.RecoverInterruptedCommandRuns(ctx, 5000); err != nil {
		t.Fatal(err)
	}
	syncRun, _ := st.GetCommandRun(ctx, "sync-run")
	linked, _ = st.GetCommandRun(ctx, "job-run")
	if syncRun.Status != "lost" || !syncRun.ErrorText.Valid || linked.Status != "running" {
		t.Fatalf("恢复状态异常: sync=%#v job=%#v", syncRun, linked)
	}
	code := 0
	if err = st.UpdateCommandRunJobBytes(ctx, job.ID, 42); err != nil {
		t.Fatal(err)
	}
	changed, err := st.FinishCommandRunByJob(ctx, job.ID, "succeeded", &code, 6000)
	if err != nil || !changed {
		t.Fatalf("首次完成后台命令 changed=%v err=%v", changed, err)
	}
	changed, err = st.FinishCommandRunByJob(ctx, job.ID, "succeeded", &code, 6000)
	if err != nil || changed {
		t.Fatalf("重复完成后台命令 changed=%v err=%v", changed, err)
	}
	linked, _ = st.GetCommandRun(ctx, "job-run")
	if linked.Status != "succeeded" || linked.StdoutBytes != 42 || linked.ExitCode.Int64 != 0 {
		t.Fatalf("任务完成状态异常: %#v", linked)
	}
	if err = st.ExpireCommandRunOutputs(ctx, 7000); err != nil {
		t.Fatal(err)
	}
	expired, _ := st.GetCommandRun(ctx, "expired-run")
	unavailable, _ := st.GetCommandRun(ctx, "unavailable-run")
	fresh, _ := st.GetCommandRun(ctx, "fresh-run")
	if !expired.OutputExpired || unavailable.OutputExpired || fresh.OutputExpired || syncRun.OutputExpired || linked.OutputExpired {
		t.Fatalf("输出过期范围异常: expired=%#v unavailable=%#v fresh=%#v sync=%#v job=%#v", expired, unavailable, fresh, syncRun, linked)
	}
	deletable, err := st.DeletableCommandRunOutputIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"expired-run", "unavailable-run", "sync-run"} {
		if _, ok := deletable[id]; !ok {
			t.Fatalf("待删除集合缺少 %s: %#v", id, deletable)
		}
	}
	if len(deletable) != 3 {
		t.Fatalf("待删除集合包含仍应保留的记录: %#v", deletable)
	}
	if err = st.MarkCommandRunOutputsCleaned(ctx, deletable); err != nil {
		t.Fatal(err)
	}
	deletable, err = st.DeletableCommandRunOutputIDs(ctx)
	if err != nil || len(deletable) != 0 {
		t.Fatalf("清理状态未收敛: %#v err=%v", deletable, err)
	}
}

func TestListCommandRunsRejectsOversizedFilter(t *testing.T) {
	st := &Store{}
	values := make([]string, MaxAuditFilterValues+1)
	for i := range values {
		values[i] = strings.Repeat("x", i%3+1)
	}
	if _, err := st.ListCommandRuns(context.Background(), CommandRunFilter{Statuses: values}); err == nil {
		t.Fatal("超出上限的命令状态筛选未被拒绝")
	}
}
