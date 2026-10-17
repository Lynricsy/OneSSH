package jobs

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"onessh/internal/cryptox"
	"onessh/internal/events"
	"onessh/internal/execx"
	"onessh/internal/sshpool"
	"onessh/internal/store"
)

// 所有用例都把预算压到 300ms；判定「受预算约束」时给足调度余量，但远小于 15 秒的建连/握手超时。
const testRefreshBudget = 300 * time.Millisecond

func useRefreshBudget(t *testing.T) {
	saved := listRefreshTimeout
	listRefreshTimeout = testRefreshBudget
	t.Cleanup(func() { listRefreshTimeout = saved })
}

// newRefreshManager 建一台指向 127.0.0.1:port 的密码主机。store/pool 的关闭登记在调用方的
// 服务端清理之前，因此按 LIFO 会在服务端断开、挂住的 worker 退出之后才执行。
func newRefreshManager(t *testing.T, port int) (*Manager, *sshpool.Pool, store.Host) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, err := cryptox.New(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	enc, err := box.Seal([]byte("pass"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := st.CreateHost(context.Background(), store.Host{Name: "stuck", Addr: "127.0.0.1", Port: port, Username: "test", AuthType: "password", PasswordEnc: enc})
	if err != nil {
		t.Fatal(err)
	}
	pool := sshpool.New(st, box)
	t.Cleanup(pool.Close)
	return New(st, pool, execx.New(t.TempDir()), events.New()), pool, h
}

// serve 接受连接并交给 handle；stop 断开全部连接，让挂在这些连接上的客户端调用失败返回。
func serve(t *testing.T, handle func(net.Conn)) (port int, accepted <-chan struct{}, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu    sync.Mutex
		conns []net.Conn
	)
	signal := make(chan struct{}, 16)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
			select {
			case signal <- struct{}{}:
			default:
			}
			go handle(conn)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, signal, func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range conns {
			conn.Close()
		}
	}
}

// 接受 TCP 却永不发送 SSH banner：客户端握手要等满 15 秒。
func serveSilent(t *testing.T) (int, <-chan struct{}, func()) {
	return serve(t, func(net.Conn) {})
}

// 握手与认证都成功，但对 session 通道请求既不接受也不拒绝：NewSession 不响应 context，会一直挂住。
func serveMuteSSH(t *testing.T) (int, func()) {
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
		for range channels {
		}
	})
	return port, stop
}

// waitWorkerExit 等被放弃的 worker 随连接断开而退出，避免它在 store 关闭后还在跑。
func waitWorkerExit(t *testing.T, m *Manager, hostID int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for m.hostStuck(hostID) {
		if time.Now().After(deadline) {
			t.Error("连接断开后被放弃的刷新 worker 仍未退出")
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func refreshTimed(m *Manager, list []store.Job) ([]Status, time.Duration) {
	started := time.Now()
	out := m.RefreshAll(context.Background(), list)
	return out, time.Since(started)
}

// 握手挂起时列表在预算内返回，没能确认的任务保留快照并标出原因，不能从列表里消失。
func TestRefreshAllBoundsUnreachableHost(t *testing.T) {
	useRefreshBudget(t)
	port, _, stop := serveSilent(t)
	m, _, h := newRefreshManager(t, port)
	t.Cleanup(func() { stop(); waitWorkerExit(t, m, h.ID) })

	list := []store.Job{
		{ID: "a", HostID: h.ID, Status: "running", LogBytes: 5},
		{ID: "b", HostID: h.ID, Status: "exited", ExitCode: sql.NullInt64{Int64: 0, Valid: true}},
		{ID: "c", HostID: h.ID, Status: "running"},
	}
	out, elapsed := refreshTimed(m, list)
	if elapsed > 2*time.Second {
		t.Fatalf("列表刷新耗时 %s，没有受预算约束", elapsed)
	}
	if len(out) != len(list) {
		t.Fatalf("返回 %d 条，期望 %d 条", len(out), len(list))
	}
	for i, st := range out {
		if st.Job.ID != list[i].ID || st.Job.Status != list[i].Status {
			t.Fatalf("第 %d 条 = %+v，期望保持 %s/%s", i, st.Job, list[i].ID, list[i].Status)
		}
	}
	if out[0].RefreshError != timeoutReason() || out[2].RefreshError != timeoutReason() || out[0].LogBytes != 5 {
		t.Fatalf("连不上的运行中任务应带快照与超时原因: %+v", out)
	}
	if out[1].RefreshError != "" {
		t.Fatalf("已结束任务不需要登录主机: %+v", out[1])
	}
}

// 已有连接不回应 session 请求时 worker 会永久挂住：列表仍须按时返回，
// 且下一次刷新不再为这台主机叠加新的挂起 worker，而是立即给快照。
func TestRefreshAllReturnsWhenSessionNeverOpens(t *testing.T) {
	useRefreshBudget(t)
	port, stop := serveMuteSSH(t)
	m, _, h := newRefreshManager(t, port)
	t.Cleanup(func() { stop(); waitWorkerExit(t, m, h.ID) })

	list := []store.Job{{ID: "a", HostID: h.ID, Status: "running"}, {ID: "b", HostID: h.ID, Status: "running"}}
	out, elapsed := refreshTimed(m, list)
	if elapsed > 2*time.Second {
		t.Fatalf("session 请求无应答时列表刷新耗时 %s，截止不是硬的", elapsed)
	}
	for _, st := range out {
		if st.RefreshError != timeoutReason() || st.Job.Status != "running" {
			t.Fatalf("挂住的任务应返回快照与超时原因: %+v", st)
		}
	}
	if !m.hostStuck(h.ID) {
		t.Fatal("NewSession 挂住时 worker 应仍在运行并被记为挂起")
	}

	out, elapsed = refreshTimed(m, list)
	if elapsed >= testRefreshBudget {
		t.Fatalf("主机仍挂着上一个 worker 时不应再等预算，耗时 %s", elapsed)
	}
	for _, st := range out {
		if !strings.Contains(st.RefreshError, "仍未返回") {
			t.Fatalf("挂起主机应直接返回快照并说明原因: %+v", st)
		}
	}
}

// 另一个调用正占着连接池的主机锁拨号（握手最长 15 秒）：锁等待不响应 context，列表仍须按时返回。
func TestRefreshAllReturnsWhileDialHoldsHostLock(t *testing.T) {
	useRefreshBudget(t)
	port, accepted, stop := serveSilent(t)
	m, pool, h := newRefreshManager(t, port)
	var dialing sync.WaitGroup
	t.Cleanup(func() { stop(); dialing.Wait(); waitWorkerExit(t, m, h.ID) })

	dialing.Go(func() { _, _ = pool.Get(context.Background(), h.Name) })
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("占锁的拨号没有连上监听器")
	}

	out, elapsed := refreshTimed(m, []store.Job{{ID: "a", HostID: h.ID, Status: "running"}})
	if elapsed > 2*time.Second {
		t.Fatalf("主机锁被占用时列表刷新耗时 %s，截止不是硬的", elapsed)
	}
	if out[0].RefreshError != timeoutReason() {
		t.Fatalf("等锁超时的任务应返回快照与超时原因: %+v", out[0])
	}
}
