package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"onessh/internal/events"
	"onessh/internal/execx"
	"onessh/internal/sshpool"
	"onessh/internal/store"
)

type Manager struct {
	Store  *store.Store
	Pool   *sshpool.Pool
	Exec   *execx.Runner
	Events *events.Bus
}
type JobView struct {
	ID         string `json:"id"`
	HostID     int64  `json:"host_id"`
	Command    string `json:"command"`
	Cwd        string `json:"cwd"`
	PID        *int64 `json:"pid"`
	UsedSetsid bool   `json:"used_setsid"`
	Status     string `json:"status"`
	ExitCode   *int64 `json:"exit_code"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt *int64 `json:"finished_at"`
}
type Status struct {
	Job      JobView `json:"job"`
	LogBytes int64   `json:"log_bytes"`
}

type LogChunk struct {
	Content    string `json:"content"`
	Offset     int64  `json:"offset_bytes"`
	NextOffset int64  `json:"next_offset_bytes"`
	TotalBytes int64  `json:"total_bytes"`
	Complete   bool   `json:"complete"`
}

func view(j store.Job) JobView {
	out := JobView{ID: j.ID, HostID: j.HostID, Command: j.Command, Cwd: j.Cwd, UsedSetsid: j.UsedSetsid, Status: j.Status, StartedAt: j.StartedAt}
	if j.PID.Valid {
		out.PID = &j.PID.Int64
	}
	if j.ExitCode.Valid {
		out.ExitCode = &j.ExitCode.Int64
	}
	if j.FinishedAt.Valid {
		out.FinishedAt = &j.FinishedAt.Int64
	}
	return out
}

func New(st *store.Store, p *sshpool.Pool, e *execx.Runner, b *events.Bus) *Manager {
	return &Manager{Store: st, Pool: p, Exec: e, Events: b}
}
func (m *Manager) Start(ctx context.Context, h store.Host, tokenID int64, command, cwd string, env map[string]string) (store.Job, error) {
	return m.start(ctx, h, tokenID, command, cwd, env, "")
}

func (m *Manager) StartTracked(ctx context.Context, h store.Host, tokenID int64, command, cwd string, env map[string]string, runID string) (store.Job, error) {
	return m.start(ctx, h, tokenID, command, cwd, env, runID)
}

func (m *Manager) start(ctx context.Context, h store.Host, tokenID int64, command, cwd string, env map[string]string, runID string) (store.Job, error) {
	id := uuid.NewString()
	cwdShell := execx.SHQ(cwd)
	if cwd == "~" {
		cwdShell = `"$HOME"`
	}
	inner := trackedJobCommand(command)
	script := `d="$HOME/.onessh/jobs/` + id + `"; export d; mkdir -p "$d"; cd ` + cwdShell + ` || exit 97; if command -v setsid >/dev/null 2>&1; then S=setsid; else S=; fi; $S nohup sh -c ` + execx.SHQ(inner) + ` >"$d/out.log" 2>&1 </dev/null & echo "$!:$S"`
	client, err := m.Pool.Get(ctx, h.Name)
	if err != nil {
		return store.Job{}, err
	}
	res, err := m.Exec.Run(ctx, client, script, "~", env, execx.Options{Timeout: 15 * time.Second, MaxLines: 20, Tail: true})
	if err != nil || res.ExitCode != 0 {
		if err == nil {
			err = fmt.Errorf("启动任务失败: %s", res.Output)
		}
		return store.Job{}, err
	}
	line := strings.TrimSpace(res.Output)
	parts := strings.Split(line, ":")
	if len(parts) < 1 {
		return store.Job{}, fmt.Errorf("无法解析任务 PID: %q", line)
	}
	pid, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil {
		return store.Job{}, fmt.Errorf("无法解析任务 PID: %w", err)
	}
	j := store.Job{ID: id, HostID: h.ID, TokenID: sql.NullInt64{Int64: tokenID, Valid: tokenID > 0}, Command: command, Cwd: cwd, PID: sql.NullInt64{Int64: pid, Valid: true}, UsedSetsid: len(parts) > 1 && strings.TrimSpace(parts[1]) == "setsid", Status: "running", StartedAt: time.Now().Unix()}
	if runID != "" {
		err = m.Store.CreateJobForCommandRun(ctx, j, runID)
	} else {
		err = m.Store.CreateJob(ctx, j)
	}
	if err != nil {
		// 远端进程已经启动，但本地事务没有落成时不能把它变成无人可见、无法终止的孤儿。
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		target := strconv.FormatInt(pid, 10)
		if j.UsedSetsid {
			target = "-" + target
		}
		_, _ = m.Exec.Run(cleanupCtx, client, `kill -TERM -- `+target, "~", nil, execx.Options{Timeout: 5 * time.Second, MaxLines: 5})
		return store.Job{}, err
	}
	m.Events.Publish("job_status", map[string]any{"job_id": id, "status": "running"})
	if runID != "" {
		m.Events.Publish("command_job_linked", map[string]any{"run_id": runID, "job_id": id})
	}
	return j, nil
}

func trackedJobCommand(command string) string {
	// 在子 shell 中执行用户命令，确保 command 自己调用 exit 或启用 set -e 时，
	// 外层仍能写入真实退出码。
	return `( ` + command + `
); __ec=$?; echo "$__ec" > "$d/exit"`
}
func (m *Manager) Refresh(ctx context.Context, j store.Job) (Status, error) {
	if j.Status != "running" {
		m.syncCommandRun(j, nil)
		return Status{Job: view(j), LogBytes: j.LogBytes}, nil
	}
	h, err := m.Store.GetHost(ctx, j.HostID)
	if err != nil {
		return Status{}, err
	}
	client, err := m.Pool.Get(ctx, h.Name)
	if err != nil {
		return Status{}, err
	}
	script := `d="$HOME/.onessh/jobs/` + j.ID + `"; if [ -f "$d/exit" ]; then printf 'exited:'; tr -d '\n' < "$d/exit"; printf ':'; wc -c < "$d/out.log"; elif kill -0 ` + strconv.FormatInt(j.PID.Int64, 10) + ` 2>/dev/null; then printf 'running::'; wc -c < "$d/out.log"; else printf 'lost::'; wc -c < "$d/out.log" 2>/dev/null || echo 0; fi`
	res, err := m.Exec.Run(ctx, client, script, "~", nil, execx.Options{Timeout: 15 * time.Second, MaxLines: 5})
	if err != nil {
		return Status{}, err
	}
	parts := strings.Split(strings.TrimSpace(res.Output), ":")
	if len(parts) < 3 {
		return Status{}, fmt.Errorf("无法解析任务状态: %q", res.Output)
	}
	status := parts[0]
	var code *int
	if status == "exited" {
		x, e := strconv.Atoi(strings.TrimSpace(parts[1]))
		if e == nil {
			code = &x
		}
	}
	size, _ := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
	j.LogBytes = size
	_ = m.Store.UpdateJobLogBytes(context.Background(), j.ID, size)
	_ = m.Store.UpdateCommandRunJobBytes(context.Background(), j.ID, size)
	if status != j.Status {
		_ = m.Store.UpdateJobState(context.Background(), j.ID, status, code)
		j.Status = status
		if code != nil {
			j.ExitCode = sql.NullInt64{Int64: int64(*code), Valid: true}
		}
		if status != "running" {
			j.FinishedAt = sql.NullInt64{Int64: time.Now().Unix(), Valid: true}
		}
		m.Events.Publish("job_status", map[string]any{"job_id": j.ID, "status": status})
		m.syncCommandRun(j, code)
	}
	return Status{Job: view(j), LogBytes: size}, nil
}
func (m *Manager) Logs(ctx context.Context, j store.Job, tailLines int, pattern string, offset int64) (string, error) {
	h, err := m.Store.GetHost(ctx, j.HostID)
	if err != nil {
		return "", err
	}
	client, err := m.Pool.Get(ctx, h.Name)
	if err != nil {
		return "", err
	}
	if tailLines <= 0 {
		tailLines = 100
	}
	if tailLines > 5000 {
		tailLines = 5000
	}
	prefix := `d="$HOME/.onessh/jobs/` + j.ID + `"; `
	var cmd string
	if offset > 0 {
		cmd = prefix + `tail -c +` + strconv.FormatInt(offset, 10) + ` "$d/out.log"`
	} else if pattern != "" {
		cmd = prefix + `grep -E ` + execx.SHQ(pattern) + ` "$d/out.log" | tail -n ` + strconv.Itoa(tailLines)
	} else {
		cmd = prefix + `tail -n ` + strconv.Itoa(tailLines) + ` "$d/out.log"`
	}
	res, err := m.Exec.Run(ctx, client, cmd, "~", nil, execx.Options{Timeout: 30 * time.Second, MaxLines: tailLines, Tail: true})
	if err != nil {
		return "", err
	}
	return res.Output, nil
}

// LogChunk 用字节游标增量读取后台任务的合并日志。单次上限 128 KiB，既低于 Runner
// 的捕获上限，也避免一个详情请求占用过多内存。
func (m *Manager) LogChunk(ctx context.Context, j store.Job, offset int64, limit int) (LogChunk, error) {
	host, err := m.Store.GetHost(ctx, j.HostID)
	if err != nil {
		return LogChunk{}, err
	}
	client, err := m.Pool.Get(ctx, host.Name)
	if err != nil {
		return LogChunk{}, err
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 128 << 10
	}
	if limit > 128<<10 {
		limit = 128 << 10
	}
	start := offset + 1
	readLimit := limit + 3
	command := `d="$HOME/.onessh/jobs/` + j.ID + `"; f="$d/out.log"; test -f "$f" || exit 44; size=$(wc -c < "$f") || exit; printf '%s\n' "$size"; if [ "$size" -gt ` + strconv.FormatInt(offset, 10) + ` ]; then count=$((size-` + strconv.FormatInt(offset, 10) + `)); if [ "$count" -gt ` + strconv.Itoa(readLimit) + ` ]; then count=` + strconv.Itoa(readLimit) + `; fi; tail -c +` + strconv.FormatInt(start, 10) + ` "$f" | head -c "$count"; fi`
	result, err := m.Exec.Run(ctx, client, command, "~", nil, execx.Options{Timeout: 30 * time.Second, MaxLines: 10000})
	if err != nil {
		return LogChunk{}, err
	}
	if result.ExitCode != 0 {
		return LogChunk{}, fmt.Errorf("读取任务日志失败: %s", strings.TrimSpace(result.Output))
	}
	lineEnd := strings.IndexByte(result.Stdout, '\n')
	if lineEnd < 0 {
		return LogChunk{}, fmt.Errorf("无法解析任务日志大小")
	}
	total, err := strconv.ParseInt(strings.TrimSpace(result.Stdout[:lineEnd]), 10, 64)
	if err != nil {
		return LogChunk{}, fmt.Errorf("无法解析任务日志大小: %w", err)
	}
	content := result.Stdout[lineEnd+1:]
	content = string(execx.UTF8Page([]byte(content), limit, offset+int64(len(content)) < total))
	next := offset + int64(len(content))
	_ = m.Store.UpdateJobLogBytes(context.Background(), j.ID, total)
	_ = m.Store.UpdateCommandRunJobBytes(context.Background(), j.ID, total)
	return LogChunk{Content: content, Offset: offset, NextOffset: next, TotalBytes: total, Complete: next >= total}, nil
}
func (m *Manager) Kill(ctx context.Context, j store.Job, signal string) error {
	if signal != "TERM" && signal != "KILL" {
		return fmt.Errorf("signal 仅支持 TERM 或 KILL")
	}
	h, err := m.Store.GetHost(ctx, j.HostID)
	if err != nil {
		return err
	}
	client, err := m.Pool.Get(ctx, h.Name)
	if err != nil {
		return err
	}
	target := strconv.FormatInt(j.PID.Int64, 10)
	if j.UsedSetsid {
		target = "-" + target
	}
	res, err := m.Exec.Run(ctx, client, `kill -`+signal+` -- `+target, "~", nil, execx.Options{Timeout: 15 * time.Second, MaxLines: 20})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("kill 失败: %s", res.Output)
	}
	logResult, logErr := m.Exec.Run(ctx, client, `d="$HOME/.onessh/jobs/`+j.ID+`"; wc -c < "$d/out.log"`, "~", nil, execx.Options{Timeout: 15 * time.Second, MaxLines: 5})
	if logErr == nil && logResult.ExitCode == 0 {
		if size, parseErr := strconv.ParseInt(strings.TrimSpace(logResult.Output), 10, 64); parseErr == nil {
			j.LogBytes = size
			_ = m.Store.UpdateJobLogBytes(context.Background(), j.ID, size)
			_ = m.Store.UpdateCommandRunJobBytes(context.Background(), j.ID, size)
		}
	}
	_ = m.Store.UpdateJobState(ctx, j.ID, "killed", nil)
	j.Status = "killed"
	j.FinishedAt = sql.NullInt64{Int64: time.Now().Unix(), Valid: true}
	m.syncCommandRun(j, nil)
	m.Events.Publish("job_status", map[string]any{"job_id": j.ID, "status": "killed"})
	return nil
}

func (m *Manager) syncCommandRun(j store.Job, exitCode *int) {
	run, err := m.Store.GetCommandRunByJob(context.Background(), j.ID)
	if err != nil || run.Status != "running" {
		return
	}
	status := "lost"
	switch j.Status {
	case "exited":
		status = "failed"
		code := exitCode
		if code == nil && j.ExitCode.Valid {
			value := int(j.ExitCode.Int64)
			code = &value
		}
		if code != nil && *code == 0 {
			status = "succeeded"
		}
		exitCode = code
	case "killed":
		status = "cancelled"
	case "lost":
		status = "lost"
	default:
		return
	}
	finishedAt := time.Now().UnixMilli()
	if j.FinishedAt.Valid {
		finishedAt = j.FinishedAt.Int64 * 1000
	}
	changed, err := m.Store.FinishCommandRunByJob(context.Background(), j.ID, status, exitCode, finishedAt)
	if err != nil || !changed {
		return
	}
	var code any
	if exitCode != nil {
		code = *exitCode
	}
	m.Events.Publish("command_finished", map[string]any{
		"run_id": run.ID, "tool": run.Tool, "host": run.Host, "status": status,
		"exit_code": code, "stdout_bytes": j.LogBytes, "stderr_bytes": 0,
		"output_available": true, "finished_at": finishedAt,
	})
}
