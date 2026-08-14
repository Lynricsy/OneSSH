package store

import (
	"context"
	"database/sql"
	"time"
)

func (s *Store) CreateJob(ctx context.Context, j Job) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO jobs(id,host_id,token_id,command,cwd,pid,used_setsid,status,started_at) VALUES(?,?,?,?,?,?,?,?,?)`, j.ID, j.HostID, nullInt(j.TokenID), j.Command, j.Cwd, nullInt(j.PID), boolInt(j.UsedSetsid), j.Status, j.StartedAt)
	return err
}

// CreateJobForCommandRun 保证后台任务和命令记录的 job_id 同时落库，避免只创建出其中一边。
func (s *Store) CreateJobForCommandRun(ctx context.Context, j Job, runID string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO jobs(id,host_id,token_id,command,cwd,pid,used_setsid,status,started_at) VALUES(?,?,?,?,?,?,?,?,?)`, j.ID, j.HostID, nullInt(j.TokenID), j.Command, j.Cwd, nullInt(j.PID), boolInt(j.UsedSetsid), j.Status, j.StartedAt); err != nil {
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
func scanJob(row interface{ Scan(...any) error }) (Job, error) {
	var j Job
	var setsid int
	err := row.Scan(&j.ID, &j.HostID, &j.TokenID, &j.Command, &j.Cwd, &j.PID, &setsid, &j.Status, &j.ExitCode, &j.LogBytes, &j.StartedAt, &j.FinishedAt)
	j.UsedSetsid = setsid != 0
	return j, err
}
func (s *Store) GetJob(ctx context.Context, id string) (Job, error) {
	return scanJob(s.DB.QueryRowContext(ctx, `SELECT id,host_id,token_id,command,cwd,pid,used_setsid,status,exit_code,log_bytes,started_at,finished_at FROM jobs WHERE id=?`, id))
}
func (s *Store) ListJobs(ctx context.Context, tokenID *int64, hostID *int64) ([]Job, error) {
	q := `SELECT id,host_id,token_id,command,cwd,pid,used_setsid,status,exit_code,log_bytes,started_at,finished_at FROM jobs WHERE 1=1`
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
func JobOwnedBy(j Job, tokenID int64) bool { return j.TokenID.Valid && j.TokenID.Int64 == tokenID }

var _ = sql.ErrNoRows
