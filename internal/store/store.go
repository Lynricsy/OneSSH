package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"net/url"
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

//go:embed migrations/0005_oauth_code_tombstone.sql
var migration0005 string

//go:embed migrations/0006_memories.sql
var migration0006 string

//go:embed migrations/0007_jump_host.sql
var migration0007 string

//go:embed migrations/0008_audit_token_name.sql
var migration0008 string

//go:embed migrations/0009_audit_tool_index.sql
var migration0009 string

//go:embed migrations/0010_host_tags.sql
var migration0010 string

type Store struct{ DB *sql.DB }

func sqliteDSN(dbPath string) (string, error) {
	absolutePath, err := filepath.Abs(dbPath)
	if err != nil {
		return "", err
	}
	uriPath := filepath.ToSlash(absolutePath)
	if uriPath[0] != '/' {
		uriPath = "/" + uriPath
	}
	query := url.Values{}
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "foreign_keys(ON)")
	return (&url.URL{Scheme: "file", Path: uriPath, RawQuery: query.Encode()}).String(), nil
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dataDir, "artifacts"), 0o700); err != nil {
		return nil, fmt.Errorf("创建数据目录: %w", err)
	}
	dsn, err := sqliteDSN(filepath.Join(dataDir, "onessh.db"))
	if err != nil {
		return nil, fmt.Errorf("解析 sqlite 路径: %w", err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 sqlite: %w", err)
	}
	if _, err = db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("设置数据库参数: %w", err)
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
		{version: 5, sql: migration0005},
		{version: 6, sql: migration0006},
		{version: 7, sql: migration0007},
		{version: 8, sql: migration0008},
		{version: 9, sql: migration0009},
		{version: 10, sql: migration0010},
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
	switch m.version {
	case 2:
		hasColumn, err := tableHasColumn(tx, "tokens", "manage_hosts")
		if err != nil {
			return err
		}
		if !hasColumn {
			if _, err = tx.Exec(m.sql); err != nil {
				return err
			}
		}
	case 8:
		hasColumn, err := tableHasColumn(tx, "audit", "token_name")
		if err != nil {
			return err
		}
		if !hasColumn {
			if _, err = tx.Exec(`ALTER TABLE audit ADD COLUMN token_name TEXT`); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(m.sql); err != nil {
			return err
		}
	default:
		if _, err = tx.Exec(m.sql); err != nil {
			return err
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
