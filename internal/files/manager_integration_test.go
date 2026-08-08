//go:build integration

package files

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"onessh/internal/cryptox"
	"onessh/internal/execx"
	"onessh/internal/sshpool"
	"onessh/internal/store"
)

func TestRealSFTPWriteEditReadTransfer(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	st, err := store.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, err := cryptox.New(bytes.Repeat([]byte{4}, 32))
	if err != nil {
		t.Fatal(err)
	}
	enc, err := box.Seal([]byte("pass"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.CreateHost(ctx, store.Host{Name: "ssh", Addr: "127.0.0.1", Port: 2222, Username: "test", AuthType: "password", PasswordEnc: enc, MonitorEnabled: true, KeyID: sql.NullInt64{}})
	if err != nil {
		t.Fatal(err)
	}
	pool := sshpool.New(st, box)
	defer pool.Close()
	m := New(pool, execx.New(data))
	defer m.Clients.Close()
	wr, err := m.Write(ctx, "ssh", "~/onessh-file-test/a.txt", []byte("one\ntwo\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	edited, err := m.Edit(ctx, "ssh", "~/onessh-file-test/a.txt", []Edit{{OldText: "two", NewText: "second"}}, wr.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(edited.Diff, "+second") {
		t.Fatalf("diff %q", edited.Diff)
	}
	got, err := m.Read(ctx, "ssh", "~/onessh-file-test/a.txt", 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Content, "2:second") {
		t.Fatalf("内容 %q", got.Content)
	}
	if _, err = m.Edit(ctx, "ssh", "~/onessh-file-test/a.txt", []Edit{{OldText: "one", NewText: "1"}}, "bad"); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("未检测冲突: %v", err)
	}
	tr, err := m.Transfer(ctx, "ssh", "~/onessh-file-test/a.txt", "ssh", "~/onessh-file-test/b.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Verified || tr.SourceSHA256 != tr.DestinationSHA256 {
		t.Fatalf("传输校验 %+v", tr)
	}
}
