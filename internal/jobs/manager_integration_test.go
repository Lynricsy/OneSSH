//go:build integration

package jobs

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"onessh/internal/cryptox"
	"onessh/internal/events"
	"onessh/internal/execx"
	"onessh/internal/sshpool"
	"onessh/internal/store"
)

func TestRealJobDefaultCwd(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	st, err := store.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, err := cryptox.New(bytes.Repeat([]byte{3}, 32))
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
	m := New(st, pool, execx.New(data), events.New())
	j, err := m.Start(ctx, h, 1, "sleep 1; echo done", "~", nil)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != "running" {
		t.Fatalf("启动状态 %s", j.Status)
	}
	time.Sleep(2 * time.Second)
	status, err := m.Refresh(ctx, j)
	if err != nil {
		t.Fatal(err)
	}
	if status.Job.Status != "exited" || status.Job.ExitCode == nil || *status.Job.ExitCode != 0 {
		t.Fatalf("结束状态 %+v", status.Job)
	}
	logs, err := m.Logs(ctx, j, 100, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs, "done") {
		t.Fatalf("日志 %q", logs)
	}
}
