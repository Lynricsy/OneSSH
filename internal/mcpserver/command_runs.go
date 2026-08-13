package mcpserver

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"onessh/internal/execx"
	"onessh/internal/store"
)

const commandPreviewLimit = 64 << 10

func (s *Server) startCommandRun(ctx context.Context, tool string, host store.Host, command, cwd, session string) (store.CommandRun, error) {
	principal, _ := FromContext(ctx)
	run := store.CommandRun{
		ID:        uuid.NewString(),
		Tool:      tool,
		HostID:    sql.NullInt64{Int64: host.ID, Valid: host.ID != 0},
		Host:      host.Name,
		Command:   command,
		Cwd:       cwd,
		Session:   sql.NullString{String: session, Valid: session != ""},
		Status:    "running",
		StartedAt: time.Now().UnixMilli(),
	}
	if principal.Token.ID != 0 {
		run.TokenID = sql.NullInt64{Int64: principal.Token.ID, Valid: true}
		run.TokenName = sql.NullString{String: principal.Token.Name, Valid: true}
	}
	if err := s.Store.CreateCommandRun(ctx, run); err != nil {
		return store.CommandRun{}, err
	}
	s.Events.Publish("command_started", map[string]any{
		"run_id": run.ID, "tool": tool, "host": host.Name, "command": command,
		"cwd": cwd, "status": "running", "started_at": run.StartedAt,
	})
	return run, nil
}

func commandRunStatus(ctx context.Context, result execx.Result, runErr error) string {
	if result.Timeout {
		if ctx.Err() != nil {
			return "cancelled"
		}
		return "timeout"
	}
	if runErr != nil || result.ExitCode != 0 {
		return "failed"
	}
	return "succeeded"
}

func (s *Server) finishCommandRun(ctx context.Context, run store.CommandRun, result execx.Result, runErr error) error {
	status := commandRunStatus(ctx, result, runErr)
	finish := store.CommandRunFinish{
		Status:          status,
		ExitCode:        sql.NullInt64{Int64: int64(result.ExitCode), Valid: runErr == nil || result.ExitCode != 0},
		StdoutPreview:   commandPreview(result.Stdout),
		StderrPreview:   commandPreview(result.Stderr),
		StdoutBytes:     int64(result.StdoutBytes),
		StderrBytes:     int64(result.StderrBytes),
		OutputAvailable: result.OutputRecorded,
		FinishedAt:      time.Now().UnixMilli(),
	}
	if result.OutputCaptureError != "" {
		finish.OutputError = sql.NullString{String: result.OutputCaptureError, Valid: true}
	}
	if runErr != nil {
		finish.ErrorText = sql.NullString{String: runErr.Error(), Valid: true}
	}
	// 请求被取消时仍要可靠落最终状态，不能继续使用已经取消的 request context。
	if err := s.Store.FinishCommandRun(context.Background(), run.ID, finish); err != nil {
		return err
	}
	var exitCode any
	if finish.ExitCode.Valid {
		exitCode = finish.ExitCode.Int64
	}
	s.Events.Publish("command_finished", map[string]any{
		"run_id": run.ID, "tool": run.Tool, "host": run.Host, "status": status,
		"exit_code": exitCode, "stdout_bytes": finish.StdoutBytes, "stderr_bytes": finish.StderrBytes,
		"output_available": finish.OutputAvailable, "finished_at": finish.FinishedAt,
	})
	return nil
}

func commandPreview(value string) string {
	value = strings.ToValidUTF8(value, "�")
	if len(value) <= commandPreviewLimit {
		return value
	}
	head := commandPreviewLimit / 2
	tail := commandPreviewLimit - head
	for head > 0 && !utf8.RuneStart(value[head]) {
		head--
	}
	tailStart := len(value) - tail
	for tailStart < len(value) && !utf8.RuneStart(value[tailStart]) {
		tailStart++
	}
	return value[:head] + "\n\n… 中间输出已省略，完整内容请在命令详情中读取 …\n\n" + value[tailStart:]
}

type commandOutputPublisher struct {
	server  *Server
	run     store.CommandRun
	mu      sync.Mutex
	offsets map[string]int64
	pending map[string][]byte
}

func newCommandOutputPublisher(server *Server, run store.CommandRun) *commandOutputPublisher {
	return &commandOutputPublisher{
		server: server, run: run,
		offsets: map[string]int64{"stdout": 0, "stderr": 0},
		pending: map[string][]byte{"stdout": nil, "stderr": nil},
	}
}

func (p *commandOutputPublisher) Publish(stream string, chunk []byte) {
	p.publish(stream, chunk, false)
}

// Finish 在 command_finished 之前冲洗可能被 SSH 数据块切开的最后一个 UTF-8 字符。
func (p *commandOutputPublisher) Finish() {
	p.publish("stdout", nil, true)
	p.publish("stderr", nil, true)
}

func (p *commandOutputPublisher) publish(stream string, chunk []byte, flush bool) {
	p.mu.Lock()
	data := append(p.pending[stream], chunk...)
	emit := data
	if !flush {
		emit = execx.CompleteUTF8Prefix(data, true)
	}
	p.pending[stream] = append([]byte(nil), data[len(emit):]...)
	offset := p.offsets[stream]
	p.offsets[stream] += int64(len(emit))
	p.mu.Unlock()
	if len(emit) == 0 {
		return
	}
	p.server.Events.Publish("command_output", map[string]any{
		"run_id": p.run.ID, "tool": p.run.Tool, "host": p.run.Host, "stream": stream,
		"offset_bytes": offset, "data": strings.ToValidUTF8(string(emit), "�"),
	})
}
