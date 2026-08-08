//go:build integration

package monitor

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	"onessh/internal/cryptox"
	"onessh/internal/execx"
	"onessh/internal/sshpool"
	"onessh/internal/store"
)

func TestRealFreshMetrics(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	st, err := store.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, err := cryptox.New(bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	enc, err := box.Seal([]byte("pass"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := st.CreateHost(ctx, store.Host{Name: "ssh", Addr: "127.0.0.1", Port: 2222, Username: "test", AuthType: "password", PasswordEnc: enc, MonitorEnabled: true, KeyID: sql.NullInt64{}})
	if err != nil {
		t.Fatal(err)
	}
	pool := sshpool.New(st, box)
	defer pool.Close()
	m := New(st, pool, execx.New(data), 0)
	snap, err := m.Fresh(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if snap.CPUPct == nil || snap.MemTotalKB == nil || snap.Load1 == nil || len(snap.Disks) == 0 {
		t.Fatalf("指标不完整 %+v", snap)
	}
}
