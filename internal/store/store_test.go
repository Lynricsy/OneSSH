package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func TestOpenCreatesSchema(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, table := range []string{"keys", "hosts", "tokens", "sessions", "jobs", "audit", "command_runs", "metrics", "oauth_clients", "oauth_authorization_codes", "oauth_refresh_tokens", "memories", "memories_fts"} {
		var name string
		if err := s.DB.QueryRowContext(context.Background(), `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil {
			t.Fatalf("缺少表 %s: %v", table, err)
		}
	}
	var index string
	if err := s.DB.QueryRowContext(context.Background(), `SELECT name FROM sqlite_master WHERE type='index' AND name='idx_audit_tool'`).Scan(&index); err != nil {
		t.Fatalf("缺少审计工具索引: %v", err)
	}
}

func TestOpenSupportsURICharactersInDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data #% 空格")
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var versions int
	if err = st.DB.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(dir, "onessh.db")
	if _, err = os.Stat(dbPath); err != nil {
		t.Fatalf("数据库未创建在配置目录: %v", err)
	}
	st, err = Open(dir)
	if err != nil {
		t.Fatalf("重新打开特殊路径数据库失败: %v", err)
	}
	defer st.Close()
	var reopenedVersions int
	if err = st.DB.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&reopenedVersions); err != nil {
		t.Fatal(err)
	}
	if reopenedVersions != versions {
		t.Fatalf("重新打开后的迁移版本数 = %d，首次打开为 %d", reopenedVersions, versions)
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
	tx, err := st.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	second, err := st.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

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
	if err = tx.Rollback(); err != nil {
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

// TestBeginTxTakesWriteLockBeforeFirstRead 锁定 issue #18 的触发条件：
// 写事务必须以 BEGIN IMMEDIATE 开启，先取得写锁再读取，
// 否则「先读后写」的事务在 WAL 下遇到并发提交会立即得到 SQLITE_BUSY_SNAPSHOT，
// 且 busy_timeout 对该错误完全无效。
func TestBeginTxTakesWriteLockBeforeFirstRead(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx := context.Background()
	tx, err := st.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// 与所有 store 事务一样：先读后写。
	var metrics int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM metrics`).Scan(&metrics); err != nil {
		t.Fatal(err)
	}

	other, err := st.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	// busy_timeout=0 让竞争立即失败，避免测试依赖计时。
	if _, err = other.ExecContext(ctx, `PRAGMA busy_timeout=0`); err != nil {
		t.Fatal(err)
	}

	// 事务已持有写锁，并发写入必须立刻拿到 SQLITE_BUSY 而不是抢先提交。
	_, err = other.ExecContext(ctx, `INSERT INTO metrics(host_id,ts) VALUES(1,1)`)
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code()&0xff != sqlite3.SQLITE_BUSY {
		t.Fatalf("写事务未在读取前取得写锁，并发写入结果: %v", err)
	}

	// 事务自身的写入与提交不得被过期读快照打断。
	if _, err = tx.ExecContext(ctx, `INSERT INTO metrics(host_id,ts) VALUES(2,2)`); err != nil {
		t.Fatalf("写事务在读取后写入失败: %v", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatalf("写事务提交失败: %v", err)
	}
	if _, err = other.ExecContext(ctx, `INSERT INTO metrics(host_id,ts) VALUES(1,1)`); err != nil {
		t.Fatalf("锁释放后并发写入失败: %v", err)
	}
}

func TestCheckpointWALReportsBusy(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.DB.SetMaxOpenConns(2)

	reader, err := st.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	writer, err := st.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if writer != nil {
			writer.Close()
		}
	}()
	if _, err = writer.ExecContext(ctx, `PRAGMA busy_timeout=0`); err != nil {
		t.Fatal(err)
	}
	if _, err = reader.ExecContext(ctx, `BEGIN`); err != nil {
		t.Fatal(err)
	}
	defer reader.ExecContext(ctx, `ROLLBACK`)
	var count int
	if err = reader.QueryRowContext(ctx, `SELECT count(*) FROM metrics`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.ExecContext(ctx, `INSERT INTO metrics(host_id,ts) VALUES(1,1)`); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	writer = nil

	busy, err := st.CheckpointWAL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !busy {
		t.Fatal("活动读事务阻塞 TRUNCATE 时未返回 busy")
	}
	if _, err = reader.ExecContext(ctx, `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	busy, err = st.CheckpointWAL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if busy {
		t.Fatal("读事务结束后 TRUNCATE 仍返回 busy")
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
	if versions != 12 {
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
	if versions != 12 {
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
	if versions != 12 {
		t.Fatalf("迁移登记数 = %d", versions)
	}
}

func TestOpenRecordsPreexistingJobLogBytesColumn(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "onessh.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(migration0001); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`ALTER TABLE jobs ADD COLUMN log_bytes INTEGER NOT NULL DEFAULT 0`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("已有 log_bytes 列时升级失败: %v", err)
	}
	defer st.Close()
	var versions int
	if err = st.DB.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 12 {
		t.Fatalf("迁移登记数 = %d", versions)
	}
}

func TestOpenAddsMissingCommandRunOutputCleanedColumn(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "onessh.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(migration0001); err != nil {
		t.Fatal(err)
	}
	const legacyCommandRuns = `CREATE TABLE command_runs (
		seq INTEGER PRIMARY KEY AUTOINCREMENT,
		id TEXT NOT NULL UNIQUE,
		token_id INTEGER,
		token_name TEXT,
		tool TEXT NOT NULL,
		host_id INTEGER,
		host TEXT NOT NULL,
		command TEXT NOT NULL,
		cwd TEXT NOT NULL,
		session TEXT,
		job_id TEXT UNIQUE,
		status TEXT NOT NULL CHECK(status IN ('running','succeeded','failed','timeout','cancelled','lost')),
		exit_code INTEGER,
		stdout_preview TEXT NOT NULL DEFAULT '',
		stderr_preview TEXT NOT NULL DEFAULT '',
		stdout_bytes INTEGER NOT NULL DEFAULT 0,
		stderr_bytes INTEGER NOT NULL DEFAULT 0,
		output_available INTEGER NOT NULL DEFAULT 0,
		output_expired INTEGER NOT NULL DEFAULT 0,
		output_error TEXT,
		error_text TEXT,
		started_at INTEGER NOT NULL,
		finished_at INTEGER
	)`
	if _, err = db.Exec(legacyCommandRuns); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("已有命令记录表缺少 output_cleaned 时升级失败: %v", err)
	}
	defer st.Close()
	var versions int
	if err = st.DB.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 12 {
		t.Fatalf("迁移登记数 = %d", versions)
	}
	var cleanedColumns int
	if err = st.DB.QueryRow(`SELECT count(*) FROM pragma_table_info('command_runs') WHERE name='output_cleaned'`).Scan(&cleanedColumns); err != nil {
		t.Fatal(err)
	}
	if cleanedColumns != 1 {
		t.Fatalf("已有命令记录表未补 output_cleaned 列: %d", cleanedColumns)
	}
}

func TestOpenRecordsPreexistingAuditCommandRunIDsColumn(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "onessh.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(migration0001); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`ALTER TABLE audit ADD COLUMN command_run_ids_json TEXT;
		INSERT INTO audit(ts,tool,params_json,command_run_ids_json,ok,duration_ms,bytes_out) VALUES(1,'exec','{}','["run-1"]',1,0,0)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("已有 command_run_ids_json 列时升级失败: %v", err)
	}
	defer st.Close()
	var versions int
	if err = st.DB.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 12 {
		t.Fatalf("迁移登记数 = %d", versions)
	}
	audit, err := st.ListAudit(context.Background(), nil, nil, nil, nil, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) != 1 || len(audit[0].RunIDs) != 1 || audit[0].RunIDs[0] != "run-1" {
		t.Fatalf("已有命令关联未被保留: %#v", audit)
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
	audit, err := st.ListAudit(ctx, nil, nil, nil, nil, 0, 10)
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
	filtered, err := st.ListAudit(ctx, []int64{newID}, nil, nil, nil, 0, 10)
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
	filtered, err = st.ListAudit(ctx, []int64{newID}, nil, nil, nil, 0, 10)
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
	if _, err = st.RotateOAuthRefreshToken(ctx, OAuthRefreshTokenRotation{
		TokenHash: "refresh-hash",
		ClientID:  client.ClientID,
		Resource:  "http://localhost/mcp",
		Now:       time.Now().Unix(),
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("删除主机后受限刷新授权仍存在: %v", err)
	}
}

func TestHostTagsRoundTrip(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	created, err := st.CreateHost(ctx, Host{Name: "tagged", Addr: "127.0.0.1", Port: 22, Username: "user", AuthType: "password", Tags: []string{"prod", "web"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.GetHost(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "prod" || got.Tags[1] != "web" {
		t.Fatalf("读取标签异常: %#v", got.Tags)
	}
	byName, err := st.GetHostByName(ctx, "tagged")
	if err != nil {
		t.Fatal(err)
	}
	if len(byName.Tags) != 2 || byName.Tags[0] != "prod" {
		t.Fatalf("按名读取标签异常: %#v", byName.Tags)
	}

	got.Tags = []string{"staging"}
	if err = st.UpdateHost(ctx, got); err != nil {
		t.Fatal(err)
	}
	updated, err := st.GetHost(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Tags) != 1 || updated.Tags[0] != "staging" {
		t.Fatalf("更新标签异常: %#v", updated.Tags)
	}

	updated.Tags = nil
	if err = st.UpdateHost(ctx, updated); err != nil {
		t.Fatal(err)
	}
	cleared, err := st.GetHost(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Tags == nil || len(cleared.Tags) != 0 {
		t.Fatalf("清空标签异常: %#v", cleared.Tags)
	}
	var raw string
	if err = st.DB.QueryRowContext(ctx, `SELECT tags FROM hosts WHERE id=?`, created.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != "[]" {
		t.Fatalf("空标签落库 = %q", raw)
	}

	plain, err := st.CreateHost(ctx, Host{Name: "plain", Addr: "127.0.0.2", Port: 22, Username: "user", AuthType: "password"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := st.ListHosts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("主机数量 = %d", len(list))
	}
	for _, h := range list {
		if h.Tags == nil {
			t.Fatalf("列表中 %s 的标签为 nil", h.Name)
		}
	}
	gotPlain, err := st.GetHost(ctx, plain.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotPlain.Tags == nil || len(gotPlain.Tags) != 0 {
		t.Fatalf("未传标签读取异常: %#v", gotPlain.Tags)
	}

	// 历史脏数据：非法 JSON 应读出空切片而非报错。
	if _, err = st.DB.ExecContext(ctx, `UPDATE hosts SET tags='not-json' WHERE id=?`, created.ID); err != nil {
		t.Fatal(err)
	}
	dirty, err := st.GetHost(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if dirty.Tags == nil || len(dirty.Tags) != 0 {
		t.Fatalf("脏数据标签容忍异常: %#v", dirty.Tags)
	}

	if _, err = st.DB.ExecContext(ctx, `UPDATE hosts SET tags='null' WHERE id=?`, created.ID); err != nil {
		t.Fatal(err)
	}
	nullTags, err := st.GetHost(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if nullTags.Tags == nil || len(nullTags.Tags) != 0 {
		t.Fatalf("JSON null 标签容忍异常: %#v", nullTags.Tags)
	}
}
