package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

//go:embed migrations/0001_init.sql
var migration0001 string

//go:embed migrations/0002_token_manage_hosts.sql
var migration0002 string

//go:embed migrations/0003_cleanup_orphan_host_refs.sql
var migration0003 string

//go:embed migrations/0004_oauth.sql
var migration0004 string

type Store struct{ DB *sql.DB }

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dataDir, "artifacts"), 0o700); err != nil {
		return nil, fmt.Errorf("创建数据目录: %w", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "onessh.db"))
	if err != nil {
		return nil, fmt.Errorf("打开 sqlite: %w", err)
	}
	for _, pragma := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON"} {
		if _, err = db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("设置数据库参数: %w", err)
		}
	}
	if err = migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("执行数据库迁移: %w", err)
	}
	return &Store{DB: db}, nil
}

type migration struct {
	version int
	sql     string
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return err
	}
	for _, m := range []migration{
		{version: 1, sql: migration0001},
		{version: 2, sql: migration0002},
		{version: 3, sql: migration0003},
		{version: 4, sql: migration0004},
	} {
		if err := applyMigration(db, m); err != nil {
			return fmt.Errorf("版本 %d: %w", m.version, err)
		}
	}
	return nil
}

func applyMigration(db *sql.DB, m migration) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var applied int
	err = tx.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version=?`, m.version).Scan(&applied)
	if err != nil {
		return err
	}
	if applied != 0 {
		return tx.Commit()
	}
	if m.version != 2 {
		if _, err = tx.Exec(m.sql); err != nil {
			return err
		}
	} else {
		hasColumn, err := tableHasColumn(tx, "tokens", "manage_hosts")
		if err != nil {
			return err
		}
		if !hasColumn {
			if _, err = tx.Exec(m.sql); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,strftime('%s','now'))`, m.version); err != nil {
		return err
	}
	return tx.Commit()
}

func tableHasColumn(tx *sql.Tx, table, column string) (bool, error) {
	rows, err := tx.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Store) Close() error                     { return s.DB.Close() }
func (s *Store) Health(ctx context.Context) error { return s.DB.PingContext(ctx) }
