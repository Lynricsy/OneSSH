//go:build integration

package mcpserver

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"onessh/internal/cryptox"
	"onessh/internal/execx"
	"onessh/internal/sshpool"
	"onessh/internal/store"
)

func TestRealExecManyRunsConcurrently(t *testing.T) {
	base := context.Background()
	data := t.TempDir()
	st, err := store.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, err := cryptox.New(bytes.Repeat([]byte{6}, 32))
	if err != nil {
		t.Fatal(err)
	}
	enc, err := box.Seal([]byte("pass"))
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]store.Host{}
	for _, name := range []string{"ssh1", "ssh2"} {
		h, err := st.CreateHost(base, store.Host{Name: name, Addr: "127.0.0.1", Port: 2222, Username: "test", AuthType: "password", PasswordEnc: enc, MonitorEnabled: true, KeyID: sql.NullInt64{}})
		if err != nil {
			t.Fatal(err)
		}
		allowed[name] = h
	}
	pool := sshpool.New(st, box)
	defer pool.Close()
	s := &Server{Pool: pool, Exec: execx.New(data)}
	ctx := context.WithValue(base, principalKey{}, Principal{Token: store.Token{ID: 1}, Hosts: allowed})
	started := time.Now()
	out := s.execMany(ctx, ExecManyInput{Hosts: []string{"ssh1", "ssh2"}, Command: "sleep 1; echo ok", TimeoutS: 10})
	if time.Since(started) > 1800*time.Millisecond {
		t.Fatalf("未并发执行，耗时 %v", time.Since(started))
	}
	for _, item := range out.Results {
		if item.ExitCode != 0 || strings.TrimSpace(item.Output) != "ok" {
			t.Fatalf("结果 %+v", item)
		}
	}
}
