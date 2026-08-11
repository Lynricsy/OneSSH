package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenCreatesSchema(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, table := range []string{"keys", "hosts", "tokens", "sessions", "jobs", "audit", "metrics", "oauth_clients", "oauth_authorization_codes", "oauth_refresh_tokens", "memories", "memories_fts"} {
		var name string
		if err := s.DB.QueryRowContext(context.Background(), `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil {
			t.Fatalf("缺少表 %s: %v", table, err)
		}
	}
}

func TestOpenConfiguresEveryConnection(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx := context.Background()
	first, err := st.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := st.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	for i, conn := range []*sql.Conn{first, second} {
		var busyTimeout, foreignKeys int
		if err = conn.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
			t.Fatal(err)
		}
		if err = conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
			t.Fatal(err)
		}
		if busyTimeout != 5000 || foreignKeys != 1 {
			t.Fatalf("连接 %d 的数据库参数异常: busy_timeout=%d foreign_keys=%d", i+1, busyTimeout, foreignKeys)
		}
	}
}

func TestOpenWaitsForConcurrentWriter(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx := context.Background()
	first, err := st.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := st.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	if _, err = first.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	defer first.ExecContext(ctx, `ROLLBACK`)

	result := make(chan error, 1)
	go func() {
		_, execErr := second.ExecContext(ctx, `INSERT INTO metrics(host_id,ts) VALUES(1,1)`)
		result <- execErr
	}()

	select {
	case err = <-result:
		t.Fatalf("并发写入未等待锁释放: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err = first.ExecContext(ctx, `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-result:
		if err != nil {
			t.Fatalf("锁释放后并发写入失败: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("锁释放后并发写入仍未完成")
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
		INSERT INTO audit(ts,token_id,tool,params_json,ok) VALUES(2000,1,'exec','{}',1);
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
	var auditTokenName sql.NullString
	if err = st.DB.QueryRowContext(ctx, `SELECT token_name FROM audit WHERE token_id=1`).Scan(&auditTokenName); err != nil {
		t.Fatal(err)
	}
	if auditTokenName.Valid {
		t.Fatalf("无快照旧审计被错误回填为 %q", auditTokenName.String)
	}
	if err = st.DB.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version IN (1,2,3,4,5,6,7,8)`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 8 {
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
	if versions != 8 {
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
	if versions != 8 {
		t.Fatalf("迁移登记数 = %d", versions)
	}
}

func TestOpenRecordsPreexistingAuditTokenNameColumn(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "onessh.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(migration0001); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`ALTER TABLE audit ADD COLUMN token_name TEXT;
		INSERT INTO tokens(id,name,token_hash,all_hosts,created_at) VALUES(1,'legacy','legacy-hash',1,1);
		INSERT INTO audit(id,ts,token_id,tool,params_json,ok) VALUES(1,2000,1,'exec','{}',1);
		INSERT INTO audit(id,ts,token_id,token_name,tool,params_json,ok) VALUES(2,2001,1,'snapshot','exec','{}',1);`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(dir)
	if err != nil {
		t.Fatalf("已有 token_name 列时升级失败: %v", err)
	}
	defer st.Close()
	var tokenName sql.NullString
	if err = st.DB.QueryRow(`SELECT token_name FROM audit WHERE id=1`).Scan(&tokenName); err != nil {
		t.Fatal(err)
	}
	if tokenName.Valid {
		t.Fatalf("无快照旧审计被错误回填为 %q", tokenName.String)
	}
	if err = st.DB.QueryRow(`SELECT token_name FROM audit WHERE id=2`).Scan(&tokenName); err != nil {
		t.Fatal(err)
	}
	if !tokenName.Valid || tokenName.String != "snapshot" {
		t.Fatalf("已有审计令牌快照被覆盖 = %#v", tokenName)
	}
	var versions int
	if err = st.DB.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 8 {
		t.Fatalf("迁移登记数 = %d", versions)
	}
}

func TestOpenDoesNotAttributeReusedTokenAudit(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "onessh.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(migration0001); err != nil {
		t.Fatal(err)
	}
	first, err := db.Exec(`INSERT INTO tokens(name,token_hash,all_hosts,created_at) VALUES('old-token','old-hash',1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	oldID, err := first.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM tokens WHERE id=?`, oldID); err != nil {
		t.Fatal(err)
	}
	second, err := db.Exec(`INSERT INTO tokens(name,token_hash,all_hosts,created_at) VALUES('new-token','new-hash',1,2)`)
	if err != nil {
		t.Fatal(err)
	}
	newID, err := second.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if newID != oldID {
		t.Fatalf("测试前提不成立：SQLite 未复用令牌 ID，old=%d new=%d", oldID, newID)
	}
	if _, err = db.Exec(`INSERT INTO audit(ts,token_id,tool,params_json,ok,duration_ms,bytes_out) VALUES(3000,?,'exec','{}',1,0,0)`, newID); err != nil {
		t.Fatal(err)
	}
	// 模拟旧令牌的长任务在删除并复用 ID 后才结束；时间下界无法证明它属于新令牌。
	if _, err = db.Exec(`INSERT INTO audit(ts,token_id,tool,params_json,ok,duration_ms,bytes_out) VALUES(4000,?,'exec','{}',1,0,0)`, oldID); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	audit, err := st.ListAudit(ctx, nil, "", "", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) != 2 {
		t.Fatalf("审计数量 = %d", len(audit))
	}
	for _, row := range audit {
		if row.TokenName.Valid {
			t.Fatalf("无快照旧审计被错误归属为 %q: %#v", row.TokenName.String, row)
		}
	}
	filtered, err := st.ListAudit(ctx, &newID, "", "", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 0 {
		t.Fatalf("新令牌过滤混入无快照旧主体: %#v", filtered)
	}
	if err = st.AddAudit(ctx, Audit{
		Ts:         5000,
		TokenID:    sql.NullInt64{Int64: newID, Valid: true},
		TokenName:  sql.NullString{String: "new-token", Valid: true},
		Tool:       "exec",
		ParamsJSON: "{}",
		OK:         true,
	}); err != nil {
		t.Fatal(err)
	}
	filtered, err = st.ListAudit(ctx, &newID, "", "", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].Ts != 5000 {
		t.Fatalf("新令牌过滤结果异常: %#v", filtered)
	}
	if err = st.DeleteToken(ctx, newID); err != nil {
		t.Fatal(err)
	}
	replacement, err := st.CreateToken(ctx, TokenCreate{Name: "replacement", Hash: "replacement-hash", AllHosts: true})
	if err != nil {
		t.Fatal(err)
	}
	if replacement.ID <= newID {
		t.Fatalf("迁移后令牌 ID 被复用: old=%d replacement=%d", newID, replacement.ID)
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
	if _, err = st.AddMemory(ctx, Memory{
		HostID: sql.NullInt64{Int64: host.ID, Valid: true}, Content: "主机删除时一并清理",
		Source: "test", Importance: 0.5, Veracity: "stated", CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err = st.DeleteHost(ctx, host.ID); err != ErrHostHasRunningJobs {
		t.Fatalf("运行任务未阻止删除: %v", err)
	}
	for _, table := range []string{"hosts", "token_hosts", "sessions", "jobs", "metrics", "memories"} {
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
	for _, table := range []string{"hosts", "token_hosts", "sessions", "jobs", "metrics", "memories"} {
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

func TestMemoryCRUDAndFTS(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	host, err := st.CreateHost(ctx, Host{Name: "memory-host", Addr: "127.0.0.1", Port: 22, Username: "user", AuthType: "password"})
	if err != nil {
		t.Fatal(err)
	}
	hostID := sql.NullInt64{Int64: host.ID, Valid: true}
	id, err := st.AddMemory(ctx, Memory{
		HostID: hostID, Content: "部署目录在 /opt/app", Source: "test", Importance: 0.6, Veracity: "stated",
		CreatedAt: 10, UpdatedAt: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.GetMemory(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "部署目录在 /opt/app" || got.HostID != hostID {
		t.Fatalf("读取记忆异常: %#v", got)
	}
	found, err := st.FindMemoryByContent(ctx, hostID, got.Content)
	if err != nil || found.ID != id {
		t.Fatalf("按内容查找失败: memory=%#v err=%v", found, err)
	}
	matches, err := st.SearchMemoriesFTS(ctx, hostID, false, `"部署目录"`, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].ID != id {
		t.Fatalf("中文 FTS 未命中: %#v", matches)
	}
	got.Content = "服务目录在 /srv/app"
	got.Importance = 0.9
	got.UpdatedAt = 20
	got.Embedding = []byte{1, 2, 3, 4}
	got.EmbeddingModel = sql.NullString{String: "test-model", Valid: true}
	if err = st.UpdateMemory(ctx, got); err != nil {
		t.Fatal(err)
	}
	listed, err := st.ListMemories(ctx, hostID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Content != got.Content || listed[0].Importance != 0.9 {
		t.Fatalf("更新或列表异常: %#v", listed)
	}
	vectors, err := st.ListMemoryVectors(ctx, hostID, false, "test-model")
	if err != nil || len(vectors) != 1 || vectors[0].ID != id {
		t.Fatalf("向量列表异常: vectors=%#v err=%v", vectors, err)
	}
	if err = st.TouchMemoryRecalls(ctx, []int64{id}, 30); err != nil {
		t.Fatal(err)
	}
	got, err = st.GetMemory(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.RecallCount != 1 || !got.LastRecalled.Valid || got.LastRecalled.Int64 != 30 {
		t.Fatalf("召回计数异常: %#v", got)
	}
	if err = st.DeleteMemory(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err = st.GetMemory(ctx, id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("删除后仍能读取: %v", err)
	}
}

func TestDeleteHostRevokesRestrictedOAuthRefreshToken(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	host, err := st.CreateHost(ctx, Host{Name: "ephemeral", Addr: "127.0.0.1", Port: 22, Username: "user", AuthType: "password"})
	if err != nil {
		t.Fatal(err)
	}
	client, err := st.CreateOAuthClient(ctx, OAuthClient{ClientID: "client", ClientName: "client", RedirectURIs: []string{"http://localhost/callback"}})
	if err != nil {
		t.Fatal(err)
	}
	accessToken, err := st.CreateToken(ctx, TokenCreate{Name: "oauth", Hash: "access-hash", HostIDs: []int64{host.ID}, Source: "oauth", Resource: "http://localhost/mcp", ClientID: client.ClientID})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.CreateOAuthRefreshToken(ctx, OAuthRefreshToken{TokenHash: "refresh-hash", GrantID: "grant", AccessTokenID: accessToken.ID, ClientID: client.ClientID, Resource: "http://localhost/mcp", Scope: "mcp", HostIDs: []int64{host.ID}, ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	if err = st.DeleteHost(ctx, host.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = st.UseOAuthRefreshToken(ctx, "refresh-hash", client.ClientID, "http://localhost/mcp", time.Now().Unix()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("删除主机后受限刷新授权仍存在: %v", err)
	}
}
