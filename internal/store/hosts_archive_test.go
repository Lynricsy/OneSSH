package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func archiveTestHosts(t *testing.T) (*Store, Host, Host, Host) {
	t.Helper()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	create := func(h Host) Host {
		h.Addr, h.Port, h.Username, h.AuthType, h.MonitorEnabled = "127.0.0.1", 22, "root", "password", true
		created, err := st.CreateHost(ctx, h)
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	jump := create(Host{Name: "jump"})
	web := create(Host{Name: "web", JumpHostID: sql.NullInt64{Int64: jump.ID, Valid: true}})
	solo := create(Host{Name: "solo"})
	return st, jump, web, solo
}

func hostNames(hosts []Host) map[string]bool {
	names := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		names[h.Name] = true
	}
	return names
}

// 归档的主机对任何令牌都不可见（含 all_hosts 令牌），也不再被监控；取消归档后恢复，令牌绑定不丢。
func TestArchivedHostHiddenFromTokensAndMonitoring(t *testing.T) {
	st, _, _, solo := archiveTestHosts(t)
	ctx := context.Background()
	if _, err := st.CreateToken(ctx, TokenCreate{Name: "all", Hash: "all-hash", AllHosts: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateToken(ctx, TokenCreate{Name: "bound", Hash: "bound-hash", HostIDs: []int64{solo.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := st.ArchiveHost(ctx, solo.ID, 100); err != nil {
		t.Fatal(err)
	}
	for _, hash := range []string{"all-hash", "bound-hash"} {
		_, hosts, err := st.FindToken(ctx, hash)
		if err != nil {
			t.Fatal(err)
		}
		if hostNames(hosts)["solo"] {
			t.Fatalf("令牌 %s 仍能看到已归档主机", hash)
		}
	}
	monitored, err := st.MonitoredHosts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if hostNames(monitored)["solo"] {
		t.Fatal("已归档主机仍在监控列表中")
	}
	archived, err := st.GetHost(ctx, solo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view := archived.View(); !view.Archived || view.ArchivedAt == nil || *view.ArchivedAt != 100 {
		t.Fatalf("归档视图 = %+v", view)
	}

	if err = st.UnarchiveHost(ctx, solo.ID, 200); err != nil {
		t.Fatal(err)
	}
	_, hosts, err := st.FindToken(ctx, "bound-hash")
	if err != nil {
		t.Fatal(err)
	}
	if !hostNames(hosts)["solo"] {
		t.Fatal("取消归档后令牌绑定应恢复可见")
	}
}

// 未归档主机不能经由已归档跳板连接：仍有活跃依赖者时拒绝归档跳板，跳板未恢复时拒绝取消归档依赖者。
func TestArchiveKeepsJumpChainActive(t *testing.T) {
	st, jump, web, _ := archiveTestHosts(t)
	ctx := context.Background()
	if err := st.ArchiveHost(ctx, jump.ID, 100); !errors.Is(err, ErrHostIsJumpHost) {
		t.Fatalf("归档仍被活跃主机依赖的跳板 err = %v", err)
	}
	if err := st.ArchiveHost(ctx, web.ID, 100); err != nil {
		t.Fatal(err)
	}
	if err := st.ArchiveHost(ctx, jump.ID, 100); err != nil {
		t.Fatalf("依赖者都已归档时应允许归档跳板: %v", err)
	}
	if err := st.UnarchiveHost(ctx, web.ID, 200); !errors.Is(err, ErrJumpHostArchived) {
		t.Fatalf("跳板仍归档时取消归档依赖者 err = %v", err)
	}
	if err := st.ArchiveHost(ctx, 9999, 100); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("归档不存在的主机 err = %v", err)
	}
}

// 归档主机上的运行中任务不参与维护；取消归档时重新起算失联宽限期。
func TestArchivedHostJobsLeaveMaintenance(t *testing.T) {
	st, _, _, solo := archiveTestHosts(t)
	ctx := context.Background()
	if err := st.CreateJob(ctx, Job{ID: "job", HostID: solo.ID, Command: "sleep", Cwd: "~", Status: "running", StartedAt: 10}); err != nil {
		t.Fatal(err)
	}
	if err := st.ArchiveHost(ctx, solo.ID, 100); err != nil {
		t.Fatal(err)
	}
	running, err := st.ListRunningJobsOnActiveHosts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(running) != 0 {
		t.Fatalf("归档主机上的任务不应进入维护: %+v", running)
	}
	if err = st.UnarchiveHost(ctx, solo.ID, 500); err != nil {
		t.Fatal(err)
	}
	var checked int64
	if err = st.DB.QueryRow(`SELECT last_checked_at FROM jobs WHERE id='job'`).Scan(&checked); err != nil {
		t.Fatal(err)
	}
	if checked != 500 {
		t.Fatalf("取消归档后确认时间 = %d，期望从取消归档时刻起算", checked)
	}
	if running, err = st.ListRunningJobsOnActiveHosts(ctx); err != nil || len(running) != 1 {
		t.Fatalf("取消归档后任务应恢复维护: %+v err=%v", running, err)
	}
}
