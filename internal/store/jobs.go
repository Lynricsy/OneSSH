package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// jobInsert 让新任务以启动时刻作为首次确认时间，失联判定从这里起算。
const jobInsert = `INSERT INTO jobs(id,host_id,token_id,command,cwd,pid,used_setsid,status,started_at,last_checked_at) VALUES(?,?,?,?,?,?,?,?,?,?)`

func (s *Store) CreateJob(ctx context.Context, j Job) error {
	_, err := s.DB.ExecContext(ctx, jobInsert, j.ID, j.HostID, nullInt(j.TokenID), j.Command, j.Cwd, nullInt(j.PID), boolInt(j.UsedSetsid), j.Status, j.StartedAt, j.StartedAt)
	return err
}

// CreateJobForCommandRun 保证后台任务和命令记录的 job_id 同时落库，避免只创建出其中一边。
func (s *Store) CreateJobForCommandRun(ctx context.Context, j Job, runID string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, jobInsert, j.ID, j.HostID, nullInt(j.TokenID), j.Command, j.Cwd, nullInt(j.PID), boolInt(j.UsedSetsid), j.Status, j.StartedAt, j.StartedAt); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE command_runs SET job_id=?,output_available=1 WHERE id=? AND status='running'`, j.ID, runID)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

const jobColumns = `id,host_id,token_id,command,cwd,pid,used_setsid,status,exit_code,log_bytes,started_at,finished_at`

func scanJob(row interface{ Scan(...any) error }) (Job, error) {
	var j Job
	var setsid int
	err := row.Scan(&j.ID, &j.HostID, &j.TokenID, &j.Command, &j.Cwd, &j.PID, &setsid, &j.Status, &j.ExitCode, &j.LogBytes, &j.StartedAt, &j.FinishedAt)
	j.UsedSetsid = setsid != 0
	return j, err
}
func (s *Store) GetJob(ctx context.Context, id string) (Job, error) {
	return scanJob(s.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, id))
}
func (s *Store) ListJobs(ctx context.Context, tokenID *int64, hostID *int64) ([]Job, error) {
	q := `SELECT ` + jobColumns + ` FROM jobs WHERE 1=1`
	args := []any{}
	if tokenID != nil {
		q += ` AND token_id=?`
		args = append(args, *tokenID)
	}
	if hostID != nil {
		q += ` AND host_id=?`
		args = append(args, *hostID)
	}
	q += ` ORDER BY started_at DESC`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return scanJobs(rows)
}

// ListRunningJobsOnActiveHosts 列出未归档主机上的运行中任务。归档主机不再登录确认，
// 其任务既不刷新也不应因此被判失联，取消归档后照常恢复跟踪。
func (s *Store) ListRunningJobsOnActiveHosts(ctx context.Context) ([]Job, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE status='running'
		AND host_id IN (SELECT id FROM hosts WHERE archived_at IS NULL) ORDER BY started_at DESC`)
	if err != nil {
		return nil, err
	}
	return scanJobs(rows)
}

func scanJobs(rows *sql.Rows) ([]Job, error) {
	defer rows.Close()
	out := make([]Job, 0)
	for rows.Next() {
		j, e := scanJob(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
func (s *Store) UpdateJobState(ctx context.Context, id, status string, exitCode *int) error {
	var code any
	if exitCode != nil {
		code = *exitCode
	}
	var finished any
	if status != "running" {
		finished = time.Now().Unix()
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE jobs SET status=?,exit_code=?,finished_at=? WHERE id=?`, status, code, finished, id)
	return err
}

func (s *Store) UpdateJobLogBytes(ctx context.Context, id string, bytes int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE jobs SET log_bytes=? WHERE id=?`, bytes, id)
	return err
}

// RecordJobCheck 记下一次成功登录主机确认任务状态：日志大小与确认时间一起更新。
func (s *Store) RecordJobCheck(ctx context.Context, id string, bytes, checkedAt int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE jobs SET log_bytes=?,last_checked_at=? WHERE id=?`, bytes, checkedAt, id)
	return err
}

// ExpireUncheckedJobs 把 ids 中最后一次确认早于 cutoff 的运行中任务标为 lost，返回被标记的任务。
// ids 只应包含本轮确实探测失败的任务；单条 UPDATE 完成筛选与改写，
// 与之并发的成功确认要么先落库从而不被选中，要么晚到但已改不回 running。
func (s *Store) ExpireUncheckedJobs(ctx context.Context, ids []string, cutoff, now int64) ([]Job, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(ids)+2)
	args = append(args, now, cutoff)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.DB.QueryContext(ctx, `UPDATE jobs SET status='lost',finished_at=?
		WHERE status='running' AND COALESCE(last_checked_at,started_at)<?
		AND id IN (?`+strings.Repeat(",?", len(ids)-1)+`)
		RETURNING `+jobColumns, args...)
	if err != nil {
		return nil, err
	}
	return scanJobs(rows)
}

// PruneFinishedJobs 删除结束早于 cutoff 的任务记录；运行中的任务无论多旧都不删。
func (s *Store) PruneFinishedJobs(ctx context.Context, cutoff int64) (int64, error) {
	result, err := s.DB.ExecContext(ctx, `DELETE FROM jobs WHERE status<>'running' AND COALESCE(finished_at,started_at)<?`, cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
func JobOwnedBy(j Job, tokenID int64) bool { return j.TokenID.Valid && j.TokenID.Int64 == tokenID }

var _ = sql.ErrNoRows
