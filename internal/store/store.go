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

type Store struct{ DB *sql.DB }

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dataDir, "artifacts"), 0o700); err != nil {
		return nil, fmt.Errorf("创建数据目录: %w", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "onessh.db"))
	if err != nil { return nil, fmt.Errorf("打开 sqlite: %w", err) }
	for _, pragma := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON"} {
		if _, err = db.Exec(pragma); err != nil { db.Close(); return nil, fmt.Errorf("设置数据库参数: %w", err) }
	}
	if _, err = db.Exec(migration0001); err != nil { db.Close(); return nil, fmt.Errorf("执行数据库迁移: %w", err) }
	return &Store{DB: db}, nil
}

func (s *Store) Close() error { return s.DB.Close() }
func (s *Store) Health(ctx context.Context) error { return s.DB.PingContext(ctx) }
