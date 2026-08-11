package store

import (
	"context"
	"database/sql"
	"time"
)

func (s *Store) AddAudit(ctx context.Context, a Audit) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO audit(ts,token_id,token_name,tool,host,params_json,ok,exit_code,duration_ms,bytes_out) VALUES(?,?,?,?,?,?,?,?,?,?)`, a.Ts, nullInt(a.TokenID), nullString(a.TokenName), a.Tool, nullString(a.Host), a.ParamsJSON, boolInt(a.OK), nullInt(a.ExitCode), a.DurationMS, a.BytesOut)
	return err
}
func (s *Store) ListAudit(ctx context.Context, tokenID *int64, host, tool string, before int64, limit int) ([]Audit, error) {
	q := `SELECT id,ts,token_id,token_name,tool,host,params_json,ok,exit_code,duration_ms,bytes_out FROM audit WHERE 1=1`
	args := []any{}
	if tokenID != nil {
		// 迁移前的 ID 可能已被复用；当前令牌存在时，只接受写入时已快照其名称的记录。
		q += ` AND audit.token_id=? AND (
			NOT EXISTS (SELECT 1 FROM tokens WHERE tokens.id=?)
			OR EXISTS (
				SELECT 1 FROM tokens
				WHERE tokens.id=? AND audit.token_name=tokens.name
			)
		)`
		args = append(args, *tokenID, *tokenID, *tokenID)
	}
	if host != "" {
		q += ` AND host=?`
		args = append(args, host)
	}
	if tool != "" {
		q += ` AND tool=?`
		args = append(args, tool)
	}
	if before > 0 {
		q += ` AND id<?`
		args = append(args, before)
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Audit, 0)
	for rows.Next() {
		var a Audit
		var ok int
		if err := rows.Scan(&a.ID, &a.Ts, &a.TokenID, &a.TokenName, &a.Tool, &a.Host, &a.ParamsJSON, &ok, &a.ExitCode, &a.DurationMS, &a.BytesOut); err != nil {
			return nil, err
		}
		a.OK = ok != 0
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) AddMetric(ctx context.Context, m Metric) error {
	_, err := s.DB.ExecContext(ctx, `INSERT OR REPLACE INTO metrics(host_id,ts,cpu_pct,mem_used_kb,mem_total_kb,load1,disks_json) VALUES(?,?,?,?,?,?,?)`, m.HostID, m.Ts, nullFloat(m.CPUPct), nullInt(m.MemUsedKB), nullInt(m.MemTotalKB), nullFloat(m.Load1), m.DisksJSON)
	return err
}
func (s *Store) LatestMetric(ctx context.Context, hostID int64) (Metric, error) {
	var m Metric
	err := s.DB.QueryRowContext(ctx, `SELECT host_id,ts,cpu_pct,mem_used_kb,mem_total_kb,load1,disks_json FROM metrics WHERE host_id=? ORDER BY ts DESC LIMIT 1`, hostID).Scan(&m.HostID, &m.Ts, &m.CPUPct, &m.MemUsedKB, &m.MemTotalKB, &m.Load1, &m.DisksJSON)
	return m, err
}
func (s *Store) MetricsSince(ctx context.Context, hostID, since int64) ([]Metric, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT host_id,ts,cpu_pct,mem_used_kb,mem_total_kb,load1,disks_json FROM metrics WHERE host_id=? AND ts>=? ORDER BY ts`, hostID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Metric, 0)
	for rows.Next() {
		var m Metric
		if err := rows.Scan(&m.HostID, &m.Ts, &m.CPUPct, &m.MemUsedKB, &m.MemTotalKB, &m.Load1, &m.DisksJSON); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *Store) CleanupMetrics(ctx context.Context, cutoff int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM metrics WHERE ts<?`, cutoff)
	return err
}
func nullString(v sql.NullString) any {
	if v.Valid {
		return v.String
	}
	return nil
}
func nullFloat(v sql.NullFloat64) any {
	if v.Valid {
		return v.Float64
	}
	return nil
}
func NowAudit() int64 { return time.Now().UnixMilli() }
