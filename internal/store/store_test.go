package store

import (
	"context"
	"testing"
)

func TestOpenCreatesSchema(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, table := range []string{"keys", "hosts", "tokens", "sessions", "jobs", "audit", "metrics"} {
		var name string
		if err := s.DB.QueryRowContext(context.Background(), `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil {
			t.Fatalf("缺少表 %s: %v", table, err)
		}
	}
}
