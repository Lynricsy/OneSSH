package store

import (
	"context"
	"database/sql"
	"strings"
)

const memoryColumns = `id,host_id,content,source,importance,veracity,token_id,created_at,updated_at,recall_count,last_recalled,embedding,embedding_model`

type scanner interface {
	Scan(...any) error
}

func scanMemory(row scanner) (Memory, error) {
	var m Memory
	err := row.Scan(
		&m.ID, &m.HostID, &m.Content, &m.Source, &m.Importance, &m.Veracity,
		&m.TokenID, &m.CreatedAt, &m.UpdatedAt, &m.RecallCount, &m.LastRecalled,
		&m.Embedding, &m.EmbeddingModel,
	)
	return m, err
}

func scanMemoryWithRank(row scanner) (MemoryWithRank, error) {
	var m MemoryWithRank
	err := row.Scan(
		&m.ID, &m.HostID, &m.Content, &m.Source, &m.Importance, &m.Veracity,
		&m.TokenID, &m.CreatedAt, &m.UpdatedAt, &m.RecallCount, &m.LastRecalled,
		&m.Embedding, &m.EmbeddingModel, &m.Rank,
	)
	return m, err
}

func (s *Store) AddMemory(ctx context.Context, m Memory) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO memories(host_id,content,source,importance,veracity,token_id,created_at,updated_at,recall_count,last_recalled,embedding,embedding_model)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, m.HostID, m.Content, m.Source, m.Importance, m.Veracity, m.TokenID, m.CreatedAt, m.UpdatedAt, m.RecallCount, m.LastRecalled, m.Embedding, m.EmbeddingModel)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) FindMemoryByContent(ctx context.Context, hostID sql.NullInt64, content string) (Memory, error) {
	return scanMemory(s.DB.QueryRowContext(ctx, `SELECT `+memoryColumns+` FROM memories
		WHERE content=? AND (host_id=? OR (? IS NULL AND host_id IS NULL)) LIMIT 1`, content, hostID, hostID))
}

func (s *Store) GetMemory(ctx context.Context, id int64) (Memory, error) {
	return scanMemory(s.DB.QueryRowContext(ctx, `SELECT `+memoryColumns+` FROM memories WHERE id=?`, id))
}

func (s *Store) UpdateMemory(ctx context.Context, m Memory) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE memories SET content=?,importance=?,veracity=?,updated_at=?,embedding=?,embedding_model=? WHERE id=?`,
		m.Content, m.Importance, m.Veracity, m.UpdatedAt, m.Embedding, m.EmbeddingModel, m.ID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteMemory(ctx context.Context, id int64) error {
	result, err := s.DB.ExecContext(ctx, `DELETE FROM memories WHERE id=?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) ListMemories(ctx context.Context, hostID sql.NullInt64, limit, offset int) ([]Memory, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+memoryColumns+` FROM memories
		WHERE host_id=? OR (? IS NULL AND host_id IS NULL)
		ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?`, hostID, hostID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Memory, 0)
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) ListMemoriesAdmin(ctx context.Context, hostID *int64, q string, limit, offset int) ([]MemoryAdminRow, error) {
	query := `SELECT m.id,m.host_id,h.name,m.content,m.source,m.importance,m.veracity,m.created_at,m.updated_at,m.recall_count,m.last_recalled
		FROM memories m LEFT JOIN hosts h ON h.id=m.host_id WHERE 1=1`
	args := make([]any, 0, 4)
	if hostID != nil {
		if *hostID == 0 {
			query += ` AND m.host_id IS NULL`
		} else {
			query += ` AND m.host_id=?`
			args = append(args, *hostID)
		}
	}
	if q != "" {
		query += ` AND m.content LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(q)+"%")
	}
	query += ` ORDER BY m.created_at DESC,m.id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]MemoryAdminRow, 0)
	for rows.Next() {
		var m MemoryAdminRow
		if err := rows.Scan(&m.ID, &m.HostID, &m.HostName, &m.Content, &m.Source, &m.Importance, &m.Veracity, &m.CreatedAt, &m.UpdatedAt, &m.RecallCount, &m.LastRecalled); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

func memoryScopeSQL(hostID sql.NullInt64, includeGlobal bool) (string, []any) {
	if !hostID.Valid {
		return `m.host_id IS NULL`, nil
	}
	if includeGlobal {
		return `(m.host_id=? OR m.host_id IS NULL)`, []any{hostID.Int64}
	}
	return `m.host_id=?`, []any{hostID.Int64}
}

func (s *Store) SearchMemoriesFTS(ctx context.Context, hostID sql.NullInt64, includeGlobal bool, match string, limit int) ([]MemoryWithRank, error) {
	scope, args := memoryScopeSQL(hostID, includeGlobal)
	query := `SELECT ` + prefixedMemoryColumns("m") + `,bm25(memories_fts)
		FROM memories_fts JOIN memories m ON m.id=memories_fts.rowid
		WHERE memories_fts MATCH ? AND ` + scope + ` ORDER BY bm25(memories_fts) LIMIT ?`
	args = append([]any{match}, args...)
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]MemoryWithRank, 0)
	for rows.Next() {
		m, err := scanMemoryWithRank(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) SearchMemoriesLike(ctx context.Context, hostID sql.NullInt64, includeGlobal bool, q string, limit int) ([]Memory, error) {
	scope, args := memoryScopeSQL(hostID, includeGlobal)
	query := `SELECT ` + prefixedMemoryColumns("m") + ` FROM memories m WHERE ` + scope + ` AND m.content LIKE ? ESCAPE '\'
		ORDER BY m.created_at DESC,m.id DESC LIMIT ?`
	args = append(args, "%"+escapeLike(q)+"%", limit)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Memory, 0)
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func prefixedMemoryColumns(prefix string) string {
	parts := strings.Split(memoryColumns, ",")
	for i := range parts {
		parts[i] = prefix + "." + parts[i]
	}
	return strings.Join(parts, ",")
}

func (s *Store) ListMemoryVectors(ctx context.Context, hostID sql.NullInt64, includeGlobal bool, model string) ([]MemoryVector, error) {
	scope, args := memoryScopeSQL(hostID, includeGlobal)
	query := `SELECT m.id,m.embedding FROM memories m WHERE ` + scope + ` AND m.embedding IS NOT NULL AND m.embedding_model=?`
	args = append(args, model)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]MemoryVector, 0)
	for rows.Next() {
		var v MemoryVector
		if err := rows.Scan(&v.ID, &v.Embedding); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) TouchMemoryRecalls(ctx context.Context, ids []int64, now int64) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	args = append(args, now)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE memories SET recall_count=recall_count+1,last_recalled=? WHERE id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	return err
}

func (s *Store) MemoryStats(ctx context.Context) ([]MemoryBankStat, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT host_id,count(*),sum(CASE WHEN embedding IS NOT NULL THEN 1 ELSE 0 END),max(created_at)
		FROM memories GROUP BY host_id ORDER BY host_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]MemoryBankStat, 0)
	for rows.Next() {
		var stat MemoryBankStat
		if err := rows.Scan(&stat.HostID, &stat.Count, &stat.Embedded, &stat.LastWritten); err != nil {
			return nil, err
		}
		out = append(out, stat)
	}
	return out, rows.Err()
}

func (s *Store) DedupeMemories(ctx context.Context, hostID sql.NullInt64) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT content FROM memories WHERE host_id=? OR (? IS NULL AND host_id IS NULL)
		GROUP BY content HAVING count(*)>1`, hostID, hostID)
	if err != nil {
		return 0, err
	}
	var contents []string
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			rows.Close()
			return 0, err
		}
		contents = append(contents, content)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	var deleted int64
	for _, content := range contents {
		var keeperID, totalRecalls int64
		err = tx.QueryRowContext(ctx, `SELECT id,(SELECT sum(recall_count) FROM memories WHERE content=? AND (host_id=? OR (? IS NULL AND host_id IS NULL)))
			FROM memories WHERE content=? AND (host_id=? OR (? IS NULL AND host_id IS NULL))
			ORDER BY importance DESC,created_at ASC,id ASC LIMIT 1`, content, hostID, hostID, content, hostID, hostID).Scan(&keeperID, &totalRecalls)
		if err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE memories SET recall_count=? WHERE id=?`, totalRecalls, keeperID); err != nil {
			return 0, err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM memories WHERE content=? AND id<>? AND (host_id=? OR (? IS NULL AND host_id IS NULL))`, content, keeperID, hostID, hostID)
		if err != nil {
			return 0, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		deleted += n
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return deleted, nil
}

func (s *Store) DecayMemories(ctx context.Context, hostID sql.NullInt64, cutoff int64) (int64, error) {
	result, err := s.DB.ExecContext(ctx, `UPDATE memories SET importance=max(0.05,importance*0.9)
		WHERE (host_id=? OR (? IS NULL AND host_id IS NULL)) AND max(created_at,coalesce(last_recalled,0))<? AND importance>0.05`, hostID, hostID, cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) PruneMemories(ctx context.Context, hostID sql.NullInt64, cutoff int64) (int64, error) {
	result, err := s.DB.ExecContext(ctx, `DELETE FROM memories WHERE (host_id=? OR (? IS NULL AND host_id IS NULL))
		AND importance<=0.1 AND recall_count=0 AND created_at<?`, hostID, hostID, cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
