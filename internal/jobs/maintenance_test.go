package jobs

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"net"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"onessh/internal/cryptox"
	"onessh/internal/store"
)

// serveExecSSH 是最小的 exec SSH 服务端：在本机用 sh 执行命令，HOME 指向 home，
// 让后台任务的启动与状态探测脚本走真实路径。
func serveExecSSH(t *testing.T, home string) (int, func()) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) { return nil, nil }}
	config.AddHostKey(signer)
	port, _, stop := serve(t, func(conn net.Conn) {
		_, channels, requests, err := ssh.NewServerConn(conn, config)
		if err != nil {
			return
		}
		go ssh.DiscardRequests(requests)
		for newChannel := range channels {
			if newChannel.ChannelType() != "session" {
				newChannel.Reject(ssh.UnknownChannelType, "仅支持 session")
				continue
			}
			channel, channelRequests, err := newChannel.Accept()
			if err != nil {
				continue
			}
			go runExecSession(channel, channelRequests, home)
		}
	})
	return port, stop
}

func runExecSession(channel ssh.Channel, requests <-chan *ssh.Request, home string) {
	defer channel.Close()
	for req := range requests {
		if req.Type != "exec" {
			if req.WantReply {
				req.Reply(req.Type == "env", nil)
			}
			continue
		}
		var payload struct{ Command string }
		if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
			req.Reply(false, nil)
			return
		}
		req.Reply(true, nil)
		cmd := exec.Command("sh", "-c", payload.Command)
		cmd.Env = append(os.Environ(), "HOME="+home)
		cmd.Stdout = channel
		cmd.Stderr = channel.Stderr()
		code := 0
		if err := cmd.Run(); err != nil {
			code = 255
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				code = exitErr.ExitCode()
			}
		}
		_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
		return
	}
}

// closedPort 返回一个刚释放的本地端口：连接会被立即拒绝，模拟离线主机且不拖慢测试。
func closedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func seedJob(t *testing.T, st *store.Store, j store.Job, lastChecked, finishedAt int64) {
	t.Helper()
	if err := st.CreateJob(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	var finished any
	if finishedAt > 0 {
		finished = finishedAt
	}
	if _, err := st.DB.Exec(`UPDATE jobs SET last_checked_at=?,finished_at=? WHERE id=?`, lastChecked, finished, j.ID); err != nil {
		t.Fatal(err)
	}
}

func jobStatus(t *testing.T, st *store.Store, id string) string {
	t.Helper()
	j, err := st.GetJob(context.Background(), id)
	if errors.Is(err, sql.ErrNoRows) {
		return "deleted"
	}
	if err != nil {
		t.Fatal(err)
	}
	return j.Status
}

func TestMaintainExpiresUnconfirmedAndPrunesFinished(t *testing.T) {
	useRefreshBudget(t)
	m, _, h := newRefreshManager(t, closedPort(t))
	st := m.Store
	ctx := context.Background()
	now := time.Now()
	day := int64(24 * time.Hour / time.Second)
	at := func(daysAgo int64) int64 { return now.Unix() - daysAgo*day }

	run := store.CommandRun{ID: "run-stale", Tool: "job_start", Host: h.Name, Command: "sleep", Cwd: "~", StartedAt: at(10) * 1000}
	if err := st.CreateCommandRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	stale := store.Job{ID: "stale", HostID: h.ID, Command: "sleep", Cwd: "~", Status: "running", StartedAt: at(10)}
	if err := st.CreateJobForCommandRun(ctx, stale, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`UPDATE jobs SET last_checked_at=? WHERE id='stale'`, at(8)); err != nil {
		t.Fatal(err)
	}
	// 启动很久，但最近一次确认还在 7 天内：只能算暂时连不上
	seedJob(t, st, store.Job{ID: "recent", HostID: h.ID, Command: "sleep", Cwd: "~", Status: "running", StartedAt: at(40)}, at(6), 0)
	seedJob(t, st, store.Job{ID: "old-exited", HostID: h.ID, Command: "true", Cwd: "~", Status: "exited", StartedAt: at(40)}, at(40), at(31))
	seedJob(t, st, store.Job{ID: "young-exited", HostID: h.ID, Command: "true", Cwd: "~", Status: "exited", StartedAt: at(40)}, at(40), at(29))
	// 结束时间缺失时按启动时间计算保留期
	seedJob(t, st, store.Job{ID: "old-killed", HostID: h.ID, Command: "true", Cwd: "~", Status: "killed", StartedAt: at(40)}, at(40), 0)

	report, err := m.Maintain(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if report != (MaintenanceReport{Expired: 1, Pruned: 2}) {
		t.Fatalf("维护统计 = %+v", report)
	}
	for id, want := range map[string]string{
		"stale": "lost", "recent": "running", "old-exited": "deleted", "young-exited": "exited", "old-killed": "deleted",
	} {
		if got := jobStatus(t, st, id); got != want {
			t.Errorf("任务 %s 状态 = %s，期望 %s", id, got, want)
		}
	}
	stored, err := st.GetCommandRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "lost" {
		t.Fatalf("失联任务的命令记录状态 = %s，期望 lost", stored.Status)
	}
}

// 维护被取消时刷新没有跑完，不能拿过期的确认时间断定任务失联。
func TestMaintainSkipsExpiryWhenRefreshDidNotComplete(t *testing.T) {
	useRefreshBudget(t)
	m, _, h := newRefreshManager(t, closedPort(t))
	now := time.Now()
	seedJob(t, m.Store, store.Job{ID: "stale", HostID: h.ID, Command: "sleep", Cwd: "~", Status: "running", StartedAt: now.Unix() - 864000}, now.Unix()-864000, 0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, _ := m.Maintain(ctx, now)
	if report.Expired != 0 || jobStatus(t, m.Store, "stale") != "running" {
		t.Fatalf("刷新未完成时不应标记失联: report=%+v status=%s", report, jobStatus(t, m.Store, "stale"))
	}
}

// 没人打开任务页时确认时间也必须由维护轮次推进：健康的长任务即使上次确认已过去 10 天，
// 本轮刷新成功后也不能被判为失联。
func TestMaintainKeepsReachableRunningJob(t *testing.T) {
	home := t.TempDir()
	port, stop := serveExecSSH(t, home)
	m, _, h := newRefreshManager(t, port)
	t.Cleanup(stop)
	ctx := context.Background()
	j, err := m.Start(ctx, h, 0, "sleep 30", "~", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		target := int(j.PID.Int64)
		if j.UsedSetsid {
			target = -target
		}
		_ = syscall.Kill(target, syscall.SIGKILL)
	})
	tenDaysAgo := time.Now().Add(-10 * 24 * time.Hour).Unix()
	if _, err = m.Store.DB.Exec(`UPDATE jobs SET last_checked_at=? WHERE id=?`, tenDaysAgo, j.ID); err != nil {
		t.Fatal(err)
	}

	before := time.Now().Unix()
	report, err := m.Maintain(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if report.Expired != 0 || jobStatus(t, m.Store, j.ID) != "running" {
		t.Fatalf("可达主机上的运行中任务被误判: report=%+v status=%s", report, jobStatus(t, m.Store, j.ID))
	}
	var checked int64
	if err = m.Store.DB.QueryRow(`SELECT last_checked_at FROM jobs WHERE id=?`, j.ID).Scan(&checked); err != nil {
		t.Fatal(err)
	}
	if checked < before {
		t.Fatalf("刷新成功后确认时间未推进: %d < %d", checked, before)
	}
}

// addHost 在 m 的库里再建一台密码主机；密钥与 newRefreshManager 相同，连接池能解开密码。
func addHost(t *testing.T, m *Manager, name string, port int) store.Host {
	t.Helper()
	box, err := cryptox.New(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	enc, err := box.Seal([]byte("pass"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := m.Store.CreateHost(context.Background(), store.Host{Name: name, Addr: "127.0.0.1", Port: port, Username: "test", AuthType: "password", PasswordEnc: enc})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// 慢主机占满并发槽时，排队的健康主机也必须轮到探测：网关停机超过 7 天恢复后，
// 所有确认时间都已过期，若按整批预算截止再全库判失联，没轮到的健康任务会被误杀。
func TestMaintainProbesHostsQueuedBehindSlowOnes(t *testing.T) {
	savedHosts, savedBudget := refreshHosts, maintenanceHostBudget
	refreshHosts, maintenanceHostBudget = 1, 300*time.Millisecond
	t.Cleanup(func() { refreshHosts, maintenanceHostBudget = savedHosts, savedBudget })
	useRefreshBudget(t)

	home := t.TempDir()
	healthyPort, stopHealthy := serveExecSSH(t, home)
	m, _, healthy := newRefreshManager(t, healthyPort)
	t.Cleanup(stopHealthy)
	ctx := context.Background()
	j, err := m.Start(ctx, healthy, 0, "sleep 30", "~", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		target := int(j.PID.Int64)
		if j.UsedSetsid {
			target = -target
		}
		_ = syscall.Kill(target, syscall.SIGKILL)
	})

	now := time.Now()
	tenDaysAgo := now.Add(-10 * 24 * time.Hour).Unix()
	slowIDs := []string{"slow-0", "slow-1", "slow-2"}
	for i, id := range slowIDs {
		port, _, stop := serveSilent(t)
		h := addHost(t, m, id, port)
		t.Cleanup(func() { stop(); waitWorkerExit(t, m, h.ID) })
		seedJob(t, m.Store, store.Job{ID: id, HostID: h.ID, Command: "sleep", Cwd: "~", Status: "running", StartedAt: tenDaysAgo - int64(i)}, tenDaysAgo, 0)
	}
	if _, err = m.Store.DB.Exec(`UPDATE jobs SET last_checked_at=? WHERE id=?`, tenDaysAgo, j.ID); err != nil {
		t.Fatal(err)
	}

	report, err := m.Maintain(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := jobStatus(t, m.Store, j.ID); got != "running" {
		t.Fatalf("排在慢主机后面的健康任务状态 = %s，期望 running（report=%+v）", got, report)
	}
	for _, id := range slowIDs {
		if got := jobStatus(t, m.Store, id); got != "lost" {
			t.Errorf("探测超时的任务 %s 状态 = %s，期望 lost", id, got)
		}
	}
	if report.Expired != len(slowIDs) {
		t.Fatalf("维护统计 = %+v", report)
	}
}
