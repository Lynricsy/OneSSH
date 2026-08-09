package store

import (
	"context"
	"database/sql"
	"path/filepath"
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

func TestOpenUpgradesLegacyDatabase(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "onessh.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(migration0001); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO tokens(id,name,token_hash,all_hosts,created_at) VALUES(1,'legacy','legacy-hash',0,1);
		INSERT INTO token_hosts(token_id,host_id) VALUES(1,99);
		INSERT INTO sessions(token_id,host_id,label,cwd,env_json,updated_at) VALUES(1,99,'default','~','{}',1);
		INSERT INTO jobs(id,host_id,token_id,command,cwd,status,started_at) VALUES('legacy-job',99,1,'true','~','exited',1);
		INSERT INTO metrics(host_id,ts) VALUES(99,1);`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	token, hosts, err := st.FindToken(ctx, "legacy-hash")
	if err != nil {
		t.Fatal(err)
	}
	if token.ManageHosts {
		t.Fatal("旧令牌不应获得主机管理权限")
	}
	if len(hosts) != 0 {
		t.Fatalf("孤儿授权未清理: %#v", hosts)
	}
	for _, table := range []string{"token_hosts", "sessions", "jobs", "metrics"} {
		var count int
		if err = st.DB.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s 仍有 %d 条孤儿记录", table, count)
		}
	}
	var versions int
	if err = st.DB.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version IN (1,2,3)`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 3 {
		t.Fatalf("迁移版本数 = %d", versions)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(dir)
	if err != nil {
		t.Fatalf("二次打开失败: %v", err)
	}
	defer st.Close()
	if err = st.DB.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 3 {
		t.Fatalf("二次迁移版本数 = %d", versions)
	}
}

func TestOpenRecordsPreexistingManageHostsColumn(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "onessh.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(migration0001); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`ALTER TABLE tokens ADD COLUMN manage_hosts INTEGER NOT NULL DEFAULT 0`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("已有 manage_hosts 列时升级失败: %v", err)
	}
	defer st.Close()
	var versions int
	if err = st.DB.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 3 {
		t.Fatalf("迁移登记数 = %d", versions)
	}
}

func TestTokenManageHostsRoundTripWithoutExecutionExpansion(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	allowed, err := st.CreateHost(ctx, Host{Name: "allowed", Addr: "127.0.0.1", Port: 22, Username: "user", AuthType: "password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.CreateHost(ctx, Host{Name: "denied", Addr: "127.0.0.2", Port: 22, Username: "user", AuthType: "password"}); err != nil {
		t.Fatal(err)
	}
	created, err := st.CreateToken(ctx, TokenCreate{Name: "manager", Hash: "manager-hash", ManageHosts: true, HostIDs: []int64{allowed.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if !created.ManageHosts || created.AllHosts {
		t.Fatalf("创建结果权限异常: %#v", created)
	}
	found, hosts, err := st.FindToken(ctx, "manager-hash")
	if err != nil {
		t.Fatal(err)
	}
	if !found.ManageHosts || len(hosts) != 1 || hosts[0].Name != "allowed" {
		t.Fatalf("查找结果越权或丢失权限: token=%#v hosts=%#v", found, hosts)
	}
	list, err := st.ListTokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !list[0].ManageHosts {
		t.Fatalf("列表未保留管理权限: %#v", list)
	}
}

func TestDeleteHostProtectsRunningJobsAndCleansReferences(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	host, err := st.CreateHost(ctx, Host{Name: "old", Addr: "127.0.0.1", Port: 22, Username: "user", AuthType: "password"})
	if err != nil {
		t.Fatal(err)
	}
	token, err := st.CreateToken(ctx, TokenCreate{Name: "restricted", Hash: "restricted-hash", HostIDs: []int64{host.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SaveSession(ctx, Session{TokenID: token.ID, HostID: host.ID, Label: "default", Cwd: "~", Env: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if err = st.CreateJob(ctx, Job{ID: "running-job", HostID: host.ID, TokenID: sql.NullInt64{Int64: token.ID, Valid: true}, Command: "sleep 60", Cwd: "~", Status: "running", StartedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB.ExecContext(ctx, `INSERT INTO metrics(host_id,ts) VALUES(?,?)`, host.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err = st.DeleteHost(ctx, host.ID); err != ErrHostHasRunningJobs {
		t.Fatalf("运行任务未阻止删除: %v", err)
	}
	for _, table := range []string{"hosts", "token_hosts", "sessions", "jobs", "metrics"} {
		var count int
		if err = st.DB.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("删除受阻后 %s 记录数 = %d", table, count)
		}
	}
	exitCode := 0
	if err = st.UpdateJobState(ctx, "running-job", "exited", &exitCode); err != nil {
		t.Fatal(err)
	}
	if err = st.DeleteHost(ctx, host.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"hosts", "token_hosts", "sessions", "jobs", "metrics"} {
		var count int
		if err = st.DB.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("成功删除后 %s 记录数 = %d", table, count)
		}
	}
	replacement, err := st.CreateHost(ctx, Host{Name: "replacement", Addr: "127.0.0.2", Port: 22, Username: "user", AuthType: "password"})
	if err != nil {
		t.Fatal(err)
	}
	if replacement.ID != host.ID {
		t.Fatalf("测试前提不成立，主机 ID 未复用: old=%d new=%d", host.ID, replacement.ID)
	}
	_, hosts, err := st.FindToken(ctx, "restricted-hash")
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 0 {
		t.Fatalf("旧令牌授权附着到替代主机: %#v", hosts)
	}
}
