package hostmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
func TestJumpHostValidation(t *testing.T) {
	ctx := context.Background()
	manager, st, _ := newTestManager(t)
	passwordInput := func(name, jumpHost string) Input {
		return Input{
			Name: name, Addr: "127.0.0.1", Port: 22, Username: "user",
			AuthType: "password", Password: new("secret"), JumpHost: jumpHost,
		}
	}

	a, err := manager.Create(ctx, passwordInput("A", ""))
	if err != nil {
		t.Fatal(err)
	}
	b, err := manager.Create(ctx, passwordInput("B", "A"))
	if err != nil {
		t.Fatal(err)
	}
	storedB, err := st.GetHost(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !storedB.JumpHostID.Valid || storedB.JumpHostID.Int64 != a.ID {
		t.Fatalf("B 的跳板引用异常: %#v", storedB.JumpHostID)
	}

	if _, err = manager.Create(ctx, passwordInput("C", "不存在")); KindOf(err) != ErrorInvalid {
		t.Fatalf("不存在的跳板分类 = %v: %v", KindOf(err), err)
	}
	cycle := passwordInput("A", "B")
	cycle.Password = nil
	if _, err = manager.Update(ctx, a.ID, cycle); KindOf(err) != ErrorInvalid {
		t.Fatalf("成环更新分类 = %v: %v", KindOf(err), err)
	}
	self := passwordInput("A", "A")
	self.Password = nil
	if _, err = manager.Update(ctx, a.ID, self); KindOf(err) != ErrorInvalid {
		t.Fatalf("自引用更新分类 = %v: %v", KindOf(err), err)
	}
	if err = manager.Delete(ctx, a.ID); KindOf(err) != ErrorConflict {
		t.Fatalf("删除被依赖跳板分类 = %v: %v", KindOf(err), err)
	}
	directB := passwordInput("B", "")
	directB.Password = nil
	if _, err = manager.Update(ctx, b.ID, directB); err != nil {
		t.Fatal(err)
	}
	if err = manager.Delete(ctx, a.ID); err != nil {
		t.Fatalf("解除依赖后删除跳板失败: %v", err)
	}

	for i := 1; i <= 6; i++ {
		jumpHost := ""
		if i > 1 {
			jumpHost = fmt.Sprintf("h%d", i-1)
		}
		if _, err = manager.Create(ctx, passwordInput(fmt.Sprintf("h%d", i), jumpHost)); err != nil {
			t.Fatalf("创建第 %d 级链失败: %v", i, err)
		}
	}
	if _, err = manager.Create(ctx, passwordInput("h7", "h6")); KindOf(err) != ErrorInvalid {
		t.Fatalf("超长跳板链分类 = %v: %v", KindOf(err), err)
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

func TestTagsNormalization(t *testing.T) {
	ctx := context.Background()
	manager, st, _ := newTestManager(t)
	base := Input{Name: "tagged", Addr: "127.0.0.1", Port: 22, Username: "user", AuthType: "password", Password: new("secret")}

	withTags := base
	withTags.Tags = []string{" prod ", "web", "Web", "web", "", "  ", "prod", "数据库"}
	created, err := manager.Create(ctx, withTags)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"prod", "web", "数据库"}
	if len(created.Tags) != len(want) {
		t.Fatalf("标签归一化异常: %#v", created.Tags)
	}
	for i, tag := range want {
		if created.Tags[i] != tag {
			t.Fatalf("标签归一化异常: got=%#v want=%#v", created.Tags, want)
		}
	}
	stored, err := st.GetHost(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Tags) != len(want) || stored.Tags[2] != "数据库" {
		t.Fatalf("标签未持久化: %#v", stored.Tags)
	}
	raw, err := json.Marshal(created.View())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"tags":["prod","web","数据库"]`) {
		t.Fatalf("视图 JSON 标签异常: %s", raw)
	}

	// 整体替换语义：省略 tags 即清空。
	cleared := base
	cleared.Password = nil
	updated, err := manager.Update(ctx, created.ID, cleared)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Tags == nil || len(updated.Tags) != 0 {
		t.Fatalf("省略 tags 未清空: %#v", updated.Tags)
	}
	raw, err = json.Marshal(updated.View())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"tags":[]`) {
		t.Fatalf("空标签 JSON 应为 []: %s", raw)
	}

	tooLong := base
	tooLong.Tags = []string{strings.Repeat("长", maxTagLength+1)}
	if _, err = manager.Update(ctx, created.ID, tooLong); KindOf(err) != ErrorInvalid {
		t.Fatalf("超长标签分类 = %v: %v", KindOf(err), err)
	}
	tooMany := base
	tooMany.Tags = make([]string, maxTagsCount+1)
	for i := range tooMany.Tags {
		tooMany.Tags[i] = fmt.Sprintf("tag-%d", i)
	}
	if _, err = manager.Update(ctx, created.ID, tooMany); KindOf(err) != ErrorInvalid {
		t.Fatalf("超量标签分类 = %v: %v", KindOf(err), err)
	}
	// 去重后不超量则应通过。
	deduped := base
	deduped.Tags = make([]string, maxTagsCount+5)
	for i := range deduped.Tags {
		deduped.Tags[i] = fmt.Sprintf("tag-%d", i%maxTagsCount)
	}
	if _, err = manager.Update(ctx, created.ID, deduped); err != nil {
		t.Fatalf("去重后不超量应通过: %v", err)
	}
}
