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
type Status struct {
	Job      store.Job `json:"job"`
	LogBytes int64     `json:"log_bytes"`
}

func New(st *store.Store, p *sshpool.Pool, e *execx.Runner, b *events.Bus) *Manager {
	return &Manager{Store: st, Pool: p, Exec: e, Events: b}
}
func (m *Manager) Start(ctx context.Context, h store.Host, tokenID int64, command, cwd string, env map[string]string) (store.Job, error) {
	id := uuid.NewString()
	cwdShell := execx.SHQ(cwd)
	if cwd == "~" {
		cwdShell = `"$HOME"`
	}
	inner := command + `; __ec=$?; echo "$__ec" > "$d/exit"`
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
	if err = m.Store.CreateJob(ctx, j); err != nil {
		return store.Job{}, err
	}
	m.Events.Publish("job_status", map[string]any{"job_id": id, "status": "running"})
	return j, nil
}
func (m *Manager) Refresh(ctx context.Context, j store.Job) (Status, error) {
	if j.Status != "running" {
		return Status{Job: j}, nil
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
	if status != j.Status {
		_ = m.Store.UpdateJobState(context.Background(), j.ID, status, code)
		j.Status = status
		if code != nil {
			j.ExitCode = sql.NullInt64{Int64: int64(*code), Valid: true}
		}
		m.Events.Publish("job_status", map[string]any{"job_id": j.ID, "status": status})
	}
	return Status{Job: j, LogBytes: size}, nil
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
	_ = m.Store.UpdateJobState(ctx, j.ID, "killed", nil)
	m.Events.Publish("job_status", map[string]any{"job_id": j.ID, "status": "killed"})
	return nil
}
