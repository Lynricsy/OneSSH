package hostmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"onessh/internal/cryptox"
	"onessh/internal/execx"
	"onessh/internal/sshpool"
	"onessh/internal/store"
)

func newTestManager(t *testing.T) (*Manager, *store.Store, *sshpool.Pool) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	box, err := cryptox.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	pool := sshpool.New(st, box)
	t.Cleanup(pool.Close)
	t.Cleanup(func() { _ = st.Close() })
	return New(st, box, pool), st, pool
}

func TestPasswordCreateUpdateAndSafeOutput(t *testing.T) {
	ctx := context.Background()
	manager, st, _ := newTestManager(t)
	created, err := manager.Create(ctx, Input{
		Name: " ssh ", Addr: " 127.0.0.1 ", Username: " user ", AuthType: "password", Password: new("secret"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "ssh" || created.Addr != "127.0.0.1" || created.Username != "user" || created.Port != 22 || !created.MonitorEnabled {
		t.Fatalf("创建归一化异常: %#v", created)
	}
	stored, err := st.GetHost(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.PasswordEnc) == 0 || bytes.Equal(stored.PasswordEnc, []byte("secret")) {
		t.Fatalf("数据库未保存有效密文: %q", stored.PasswordEnc)
	}
	ciphertext := append([]byte(nil), stored.PasswordEnc...)
	updated, err := manager.Update(ctx, created.ID, Input{
		Name: "ssh-renamed", Addr: "127.0.0.2", Port: 2200, Username: "user2", AuthType: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err = st.GetHost(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored.PasswordEnc, ciphertext) || updated.Name != "ssh-renamed" || !updated.MonitorEnabled {
		t.Fatalf("省略密码更新未保留凭据或配置: %#v", updated)
	}
	for _, value := range []any{created, created.View(), updated, updated.View()} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "password_enc") {
			t.Fatalf("公开 JSON 泄漏凭据: %s", raw)
		}
	}
}

func TestValidationAndErrorKinds(t *testing.T) {
	ctx := context.Background()
	manager, st, _ := newTestManager(t)
	base := Input{Name: "host", Addr: "127.0.0.1", Port: 22, Username: "user", AuthType: "password", Password: new("secret")}
	created, err := manager.Create(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.Create(ctx, base); KindOf(err) != ErrorConflict {
		t.Fatalf("重复名称分类 = %v: %v", KindOf(err), err)
	}
	invalidPort := base
	invalidPort.Name = "invalid-port"
	invalidPort.Port = 65536
	if _, err = manager.Create(ctx, invalidPort); KindOf(err) != ErrorInvalid {
		t.Fatalf("端口错误分类 = %v: %v", KindOf(err), err)
	}
	unknownKey := base
	unknownKey.Name = "unknown-key"
	unknownKey.AuthType = "key"
	unknownKey.Password = nil
	unknownKey.KeyID = new(int64(999))
	if _, err = manager.Create(ctx, unknownKey); KindOf(err) != ErrorInvalid {
		t.Fatalf("未知 key_id 分类 = %v: %v", KindOf(err), err)
	}
	if _, err = manager.Update(ctx, 999, base); KindOf(err) != ErrorNotFound {
		t.Fatalf("未知主机分类 = %v: %v", KindOf(err), err)
	}
	key, err := st.CreateKey(ctx, "key", []byte("encrypted"), "ssh-ed25519 test")
	if err != nil {
		t.Fatal(err)
	}
	keyHost := Input{Name: "key-host", Addr: "127.0.0.1", Port: 22, Username: "user", AuthType: "key", KeyID: &key.ID}
	keyCreated, err := manager.Create(ctx, keyHost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.Update(ctx, keyCreated.ID, Input{Name: "key-host", Addr: "127.0.0.1", Port: 22, Username: "user", AuthType: "password"}); KindOf(err) != ErrorInvalid {
		t.Fatalf("切换密码认证缺少密码分类 = %v: %v", KindOf(err), err)
	}
	if _, err = manager.Update(ctx, created.ID, Input{Name: "host", Addr: "127.0.0.1", Port: 22, Username: "user", AuthType: "key"}); KindOf(err) != ErrorInvalid {
		t.Fatalf("切换密钥认证缺少 key_id 分类 = %v: %v", KindOf(err), err)
	}
}

func TestConnectionFailureKind(t *testing.T) {
	manager, _, _ := newTestManager(t)
	host, err := manager.Create(context.Background(), Input{
		Name: "offline", Addr: "127.0.0.1", Port: 1, Username: "user", AuthType: "password", Password: new("secret"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = manager.Test(ctx, host.ID, execx.New(t.TempDir()))
	if KindOf(err) != ErrorConnection {
		t.Fatalf("连接失败分类 = %v: %v", KindOf(err), err)
	}
}
