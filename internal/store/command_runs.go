package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const commandRunColumns = `seq,id,token_id,token_name,tool,host_id,host,command,cwd,session,job_id,status,exit_code,
	stdout_preview,stderr_preview,stdout_bytes,stderr_bytes,output_available,output_expired,output_error,error_text,started_at,finished_at`

func (s *Store) CreateCommandRun(ctx context.Context, run CommandRun) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO command_runs(
		id,token_id,token_name,tool,host_id,host,command,cwd,session,status,started_at
	) VALUES(?,?,?,?,?,?,?,?,?,'running',?)`, run.ID, nullInt(run.TokenID), nullString(run.TokenName), run.Tool,
		nullInt(run.HostID), run.Host, run.Command, run.Cwd, nullString(run.Session), run.StartedAt)
	return err
}

func (s *Store) FinishCommandRun(ctx context.Context, id string, finish CommandRunFinish) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE command_runs SET
		status=?,exit_code=?,stdout_preview=?,stderr_preview=?,stdout_bytes=?,stderr_bytes=?,
		output_available=?,output_error=?,error_text=?,finished_at=?
		WHERE id=? AND status='running'`, finish.Status, nullInt(finish.ExitCode), finish.StdoutPreview,
		finish.StderrPreview, finish.StdoutBytes, finish.StderrBytes, boolInt(finish.OutputAvailable),
		nullString(finish.OutputError), nullString(finish.ErrorText), finish.FinishedAt, id)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 0 {
		return fmt.Errorf("命令执行记录不存在或已经结束: %s", id)
	}
	return nil
}

// FinishCommandRunByJob 同步后台任务的最终状态，并返回本次是否完成了仍在运行的记录。
// 调用方只在 changed=true 时发布完成事件，避免并发刷新产生重复事件。
func (s *Store) FinishCommandRunByJob(ctx context.Context, jobID, status string, exitCode *int, finishedAt int64) (bool, error) {
	var code any
	if exitCode != nil {
		code = *exitCode
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE command_runs SET status=?,exit_code=?,finished_at=?
		WHERE job_id=? AND status='running'`, status, code, finishedAt, jobID)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	return updated > 0, err
}

func (s *Store) UpdateCommandRunJobBytes(ctx context.Context, jobID string, bytes int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE command_runs SET stdout_bytes=? WHERE job_id=?`, bytes, jobID)
	return err
}

func (s *Store) GetCommandRun(ctx context.Context, id string) (CommandRun, error) {
	return scanCommandRun(s.DB.QueryRowContext(ctx, `SELECT `+commandRunColumns+` FROM command_runs WHERE id=?`, id))
}

func (s *Store) GetCommandRunByJob(ctx context.Context, jobID string) (CommandRun, error) {
	return scanCommandRun(s.DB.QueryRowContext(ctx, `SELECT `+commandRunColumns+` FROM command_runs WHERE job_id=?`, jobID))
}

func (s *Store) ListCommandRuns(ctx context.Context, filter CommandRunFilter) ([]CommandRun, error) {
	if err := validateAuditFilterCount("令牌", len(filter.TokenIDs)); err != nil {
		return nil, err
	}
	if err := validateAuditFilterCount("主机", len(filter.Hosts)); err != nil {
		return nil, err
	}
	if err := validateAuditFilterCount("工具", len(filter.Tools)); err != nil {
		return nil, err
	}
	if err := validateAuditFilterCount("状态", len(filter.Statuses)); err != nil {
		return nil, err
	}
	query := `SELECT ` + commandRunColumns + ` FROM command_runs WHERE 1=1`
	args := make([]any, 0)
	if len(filter.TokenIDs) > 0 {
		cond := `command_runs.token_id=? AND (
			NOT EXISTS (SELECT 1 FROM tokens WHERE tokens.id=?)
			OR EXISTS (SELECT 1 FROM tokens WHERE tokens.id=? AND command_runs.token_name=tokens.name)
		)`
		conditions := make([]string, len(filter.TokenIDs))
		for i, id := range filter.TokenIDs {
			conditions[i] = `(` + cond + `)`
			args = append(args, id, id, id)
		}
		query += ` AND (` + strings.Join(conditions, ` OR `) + `)`
	}
	query, args = appendStringFilter(query, args, "host", filter.Hosts)
	query, args = appendStringFilter(query, args, "tool", filter.Tools)
	query, args = appendStringFilter(query, args, "status", filter.Statuses)
	if needle := strings.TrimSpace(filter.Query); needle != "" {
		// instr 按普通子串搜索，不会把用户输入的 % / _ 当成 LIKE 通配符。
		query += ` AND instr(lower(command || ' ' || host || ' ' || tool || ' ' || id || ' ' || coalesce(token_name,'')), lower(?)) > 0`
		args = append(args, needle)
	}
	if filter.Before > 0 {
		query += ` AND seq<?`
		args = append(args, filter.Before)
	}
	if filter.Limit <= 0 || filter.Limit > 500 {
		filter.Limit = 100
	}
	query += ` ORDER BY seq DESC LIMIT ?`
	args = append(args, filter.Limit)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CommandRun, 0)
	for rows.Next() {
		run, err := scanCommandRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func appendStringFilter(query string, args []any, column string, values []string) (string, []any) {
	if len(values) == 0 {
		return query, args
	}
	query += ` AND ` + column + ` IN (` + placeholders(len(values)) + `)`
	for _, value := range values {
		args = append(args, value)
	}
	return query, args
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanCommandRun(row rowScanner) (CommandRun, error) {
	var run CommandRun
	var outputAvailable, outputExpired int
	err := row.Scan(&run.Seq, &run.ID, &run.TokenID, &run.TokenName, &run.Tool, &run.HostID, &run.Host,
		&run.Command, &run.Cwd, &run.Session, &run.JobID, &run.Status, &run.ExitCode,
		&run.StdoutPreview, &run.StderrPreview, &run.StdoutBytes, &run.StderrBytes,
		&outputAvailable, &outputExpired, &run.OutputError, &run.ErrorText, &run.StartedAt, &run.FinishedAt)
	run.OutputAvailable = outputAvailable != 0
	run.OutputExpired = outputExpired != 0
	return run, err
}

// RecoverInterruptedCommandRuns 把网关重启时遗留的同步命令标为失联。关联后台任务的记录
// 仍由 jobs.Manager 刷新，因为远端进程可能在网关重启期间继续运行。
func (s *Store) RecoverInterruptedCommandRuns(ctx context.Context, now int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE command_runs SET status='lost',finished_at=?,
		error_text='网关在命令完成前重启，无法确认最终状态'
		WHERE status='running' AND job_id IS NULL`, now)
	return err
}

func (s *Store) ExpireCommandRunOutputs(ctx context.Context, cutoff int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE command_runs SET stdout_preview='',stderr_preview='',
		output_available=0,output_expired=1
		WHERE job_id IS NULL AND status<>'running' AND finished_at<? AND output_available=1 AND output_expired=0`, cutoff)
	return err
}

// DeletableCommandRunOutputIDs 返回数据库明确判定不再需要本地文件的同步命令。
// 除已过期记录外，也包含捕获失败或重启失联后从未标记为可用的残留文件。
func (s *Store) DeletableCommandRunOutputIDs(ctx context.Context) (map[string]struct{}, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM command_runs
		WHERE job_id IS NULL AND status<>'running' AND output_cleaned=0 AND (output_expired=1 OR output_available=0)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = struct{}{}
	}
	return ids, rows.Err()
}

// MarkCommandRunOutputsCleaned 在文件删除成功或确认文件不存在后收敛清理状态，避免
// 每小时把全部历史过期记录重新装入内存。
func (s *Store) MarkCommandRunOutputsCleaned(ctx context.Context, ids map[string]struct{}) error {
	if len(ids) == 0 {
		return nil
	}
	values := make([]string, 0, len(ids))
	for id := range ids {
		values = append(values, id)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	const batchSize = 500
	for start := 0; start < len(values); start += batchSize {
		end := min(start+batchSize, len(values))
		args := make([]any, end-start)
		for i, id := range values[start:end] {
			args[i] = id
		}
		if _, err = tx.ExecContext(ctx, `UPDATE command_runs SET output_cleaned=1 WHERE id IN (`+placeholders(len(args))+`)`, args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Compile-time check: *sql.Rows and *sql.Row both satisfy rowScanner.
var _ rowScanner = (*sql.Rows)(nil)
var _ rowScanner = (*sql.Row)(nil)
