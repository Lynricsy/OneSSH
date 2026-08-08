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
func scanJob(row interface{ Scan(...any) error }) (Job, error) {
	var j Job
	var setsid int
	err := row.Scan(&j.ID, &j.HostID, &j.TokenID, &j.Command, &j.Cwd, &j.PID, &setsid, &j.Status, &j.ExitCode, &j.StartedAt, &j.FinishedAt)
	j.UsedSetsid = setsid != 0
	return j, err
}
func (s *Store) GetJob(ctx context.Context, id string) (Job, error) {
	return scanJob(s.DB.QueryRowContext(ctx, `SELECT id,host_id,token_id,command,cwd,pid,used_setsid,status,exit_code,started_at,finished_at FROM jobs WHERE id=?`, id))
}
func (s *Store) ListJobs(ctx context.Context, tokenID *int64, hostID *int64) ([]Job, error) {
	q := `SELECT id,host_id,token_id,command,cwd,pid,used_setsid,status,exit_code,started_at,finished_at FROM jobs WHERE 1=1`
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
	var out []Job
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
func JobOwnedBy(j Job, tokenID int64) bool { return j.TokenID.Valid && j.TokenID.Int64 == tokenID }

var _ = sql.ErrNoRows
