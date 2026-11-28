package mcpserver

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"onessh/internal/cryptox"
	"onessh/internal/hostmanager"
	"onessh/internal/sshkeys"
	"onessh/internal/sshpool"
	"onessh/internal/store"
)

func newKeyTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, err := cryptox.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	pool := sshpool.New(st, box)
	t.Cleanup(pool.Close)
	return &Server{Store: st, Pool: pool, HostManager: hostmanager.New(st, box, pool)}, st
}

func managerContext(st *store.Store, manage bool) context.Context {
	return context.WithValue(context.Background(), principalKey{}, Principal{Token: store.Token{Name: "agent", ManageHosts: manage}, Store: st})
}

func TestKeyImportGenerateAndList(t *testing.T) {
	server, st := newKeyTestServer(t)
	ctx := managerContext(st, true)

	private, public, err := sshkeys.Generate("seed")
	if err != nil {
		t.Fatal(err)
	}
	res, imported, err := server.keyImport(ctx, nil, KeyImportInput{KeyName: "uploaded", PrivateKey: string(private)})
	if err != nil || res != nil {
		t.Fatalf("key_import 失败: res=%v err=%v", res, err)
	}
	if imported.ID == 0 || imported.PublicKey != strings.TrimSpace(public) || !strings.HasPrefix(imported.Fingerprint, "SHA256:") {
		t.Fatalf("key_import 返回 = %+v", imported)
	}

	res, _, _ = server.keyImport(ctx, nil, KeyImportInput{KeyName: "uploaded", PrivateKey: string(private)})
	if res == nil || !res.IsError {
		t.Fatal("重名导入应当失败")
	}
	res, _, _ = server.keyImport(ctx, nil, KeyImportInput{KeyName: "pub", PrivateKey: public})
	if res == nil || !res.IsError {
		t.Fatal("上传公钥应当被拒绝")
	}
	res, _, _ = server.keyImport(ctx, nil, KeyImportInput{KeyName: "both", PrivateKey: string(private), FromHost: "web", FromPath: "~/.ssh/id"})
	if res == nil || !res.IsError {
		t.Fatal("private_key 与 from_host 同时给出应当失败")
	}

	res, generated, err := server.keyGenerate(ctx, nil, KeyGenerateInput{KeyName: "fresh"})
	if err != nil || res != nil || !strings.HasPrefix(generated.PublicKey, "ssh-ed25519 ") {
		t.Fatalf("key_generate 失败: res=%v err=%v out=%+v", res, err, generated)
	}

	keyID := imported.ID
	if _, err = st.CreateHost(context.Background(), store.Host{Name: "web-01", Addr: "10.0.0.1", Port: 22, Username: "root", AuthType: "key", KeyID: sql.NullInt64{Int64: keyID, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	_, listed, err := server.keysList(ctx, nil, Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Keys) != 2 {
		t.Fatalf("keys_list 数量 = %d", len(listed.Keys))
	}
	for _, key := range listed.Keys {
		switch key.Name {
		case "uploaded":
			if len(key.UsedBy) != 1 || key.UsedBy[0] != "web-01" {
				t.Fatalf("uploaded used_by = %v", key.UsedBy)
			}
		case "fresh":
			if key.UsedBy == nil || len(key.UsedBy) != 0 {
				t.Fatalf("fresh used_by = %#v，期望空数组", key.UsedBy)
			}
		}
	}
}

func TestKeyToolsRequireManageHosts(t *testing.T) {
	server, st := newKeyTestServer(t)
	ctx := managerContext(st, false)
	if res, _, _ := server.keysList(ctx, nil, Empty{}); res == nil || !res.IsError {
		t.Fatal("无 manage_hosts 不应能列出密钥")
	}
	if res, _, _ := server.keyGenerate(ctx, nil, KeyGenerateInput{KeyName: "x"}); res == nil || !res.IsError {
		t.Fatal("无 manage_hosts 不应能生成密钥")
	}
	if res, _, _ := server.keyImport(ctx, nil, KeyImportInput{KeyName: "x", PrivateKey: "x"}); res == nil || !res.IsError {
		t.Fatal("无 manage_hosts 不应能导入密钥")
	}
}

// 私钥与口令绝不能以明文进入审计。
func TestRedactHidesPrivateKeyMaterial(t *testing.T) {
	params := redactedJSON(KeyImportInput{KeyName: "k", PrivateKey: "-----BEGIN OPENSSH PRIVATE KEY-----", Passphrase: "hunter2"})
	if strings.Contains(params, "BEGIN") || strings.Contains(params, "hunter2") {
		t.Fatalf("审计参数泄露私钥材料: %s", params)
	}
	if !strings.Contains(params, `"private_key":"<redacted>"`) {
		t.Fatalf("审计参数 = %s", params)
	}
}
