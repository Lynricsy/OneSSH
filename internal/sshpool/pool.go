package sshpool

import (
	"context"
	"fmt"
	"log"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"onessh/internal/cryptox"
	"onessh/internal/store"
)

const (
	// maxJumpChain 须与 hostmanager 中的同名常量保持一致。
	maxJumpChain      = 5
	sshConnectTimeout = 15 * time.Second
)

type entry struct {
	mu     sync.Mutex
	client *ssh.Client
	online bool
}
type Pool struct {
	store   *store.Store
	box     *cryptox.Box
	mu      sync.Mutex
	entries map[string]*entry
	closed  chan struct{}
}

func New(st *store.Store, box *cryptox.Box) *Pool {
	return &Pool{store: st, box: box, entries: make(map[string]*entry), closed: make(chan struct{})}
}
func (p *Pool) getEntry(name string) *entry {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entries[name]
	if e == nil {
		e = &entry{}
		p.entries[name] = e
	}
	return e
}
func (p *Pool) Get(ctx context.Context, name string) (*ssh.Client, error) {
	return p.get(ctx, name, nil)
}

func (p *Pool) get(ctx context.Context, name string, via []string) (*ssh.Client, error) {
	if slices.Contains(via, name) {
		return nil, fmt.Errorf("跳板链存在循环: %s", strings.Join(append(via, name), " -> "))
	}
	if len(via) > maxJumpChain {
		return nil, fmt.Errorf("跳板链超过 %d 级", maxJumpChain)
	}
	e := p.getEntry(name)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.client != nil && e.online {
		return e.client, nil
	}
	h, err := p.store.GetHostByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("unknown host: %s", name)
	}
	client, err := p.dial(ctx, h, via)
	if err != nil {
		e.online = false
		return nil, err
	}
	e.client = client
	e.online = true
	go p.keepalive(name, e, client)
	return client, nil
}
func (p *Pool) dial(ctx context.Context, h store.Host, via []string) (*ssh.Client, error) {
	var (
		auths        []ssh.AuthMethod
		authCallback ssh.ClientAuthCallback
	)
	switch h.AuthType {
	case "password":
		plain, err := p.box.Open(h.PasswordEnc)
		if err != nil {
			return nil, err
		}
		auths, authCallback = passwordAuthentication(string(plain))
		for i := range plain {
			plain[i] = 0
		}
	case "key":
		if !h.KeyID.Valid {
			return nil, fmt.Errorf("主机缺少密钥")
		}
		k, err := p.store.GetKey(ctx, h.KeyID.Int64)
		if err != nil {
			return nil, err
		}
		plain, err := p.box.Open(k.PrivateKeyEnc)
		if err != nil {
			return nil, err
		}
		signer, err := ssh.ParsePrivateKey(plain)
		for i := range plain {
			plain[i] = 0
		}
		if err != nil {
			return nil, fmt.Errorf("解析私钥: %w", err)
		}
		auths = append(auths, ssh.PublicKeys(signer))
	default:
		return nil, fmt.Errorf("不支持的认证类型 %q", h.AuthType)
	}
	callback := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		fp := ssh.FingerprintSHA256(key)
		if !h.HostKeyFP.Valid || h.HostKeyFP.String == "" {
			if err := p.store.UpdateHostFingerprint(ctx, h.ID, &fp); err != nil {
				return fmt.Errorf("记录主机指纹: %w", err)
			}
			return nil
		}
		if h.HostKeyFP.String != fp {
			return fmt.Errorf("SSH 主机指纹变更，需管理员重置（期望 %s，收到 %s）", h.HostKeyFP.String, fp)
		}
		return nil
	}
	config := &ssh.ClientConfig{
		User:            h.Username,
		Auth:            auths,
		AuthCallback:    authCallback,
		HostKeyCallback: callback,
		Timeout:         sshConnectTimeout,
	}
	addr := net.JoinHostPort(h.Addr, strconv.Itoa(h.Port))
	var conn net.Conn
	var err error
	if h.JumpHostID.Valid {
		jump, err := p.store.GetHost(ctx, h.JumpHostID.Int64)
		if err != nil {
			return nil, fmt.Errorf("查找 %s 的跳板主机: %w", nameAddr(h), err)
		}
		jc, err := p.get(ctx, jump.Name, append(via, h.Name))
		if err != nil {
			return nil, fmt.Errorf("连接跳板 %s: %w", nameAddr(jump), err)
		}
		// 跳板也是连接池中的普通 entry，拥有独立 keepalive，并可被多个目标复用。
		// 跳板断开后，目标连接会在下次使用或 30 秒内的 keepalive 中失效并重建。
		conn, err = jc.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("经跳板 %s 连接 %s: %w", jump.Name, nameAddr(h), err)
		}
	} else {
		d := net.Dialer{Timeout: sshConnectTimeout}
		conn, err = d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("连接 %s: %w", nameAddr(h), err)
		}
	}
	client, err := newSSHClient(ctx, conn, addr, config)
	if err != nil {
		return nil, fmt.Errorf("SSH 握手 %s: %w", nameAddr(h), err)
	}
	return client, nil
}

// ClientConfig.Timeout 只限制 TCP 建连；NewClientConn 需要独立的握手期限与取消处理。
func newSSHClient(ctx context.Context, conn net.Conn, addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
	if err := ctx.Err(); err != nil {
		conn.Close()
		return nil, err
	}
	handshakeCtx, cancel := context.WithTimeout(ctx, sshConnectTimeout)
	defer cancel()
	stopCancel := context.AfterFunc(handshakeCtx, func() { conn.Close() })
	cc, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	cancelStopped := stopCancel()
	if err != nil {
		conn.Close()
		if ctxErr := handshakeCtx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	if !cancelStopped {
		cc.Close()
		if ctxErr := handshakeCtx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, context.Canceled
	}
	if ctxErr := handshakeCtx.Err(); ctxErr != nil {
		cc.Close()
		return nil, ctxErr
	}
	return ssh.NewClient(cc, chans, reqs), nil
}

func passwordAuthentication(password string) ([]ssh.AuthMethod, ssh.ClientAuthCallback) {
	auths := []ssh.AuthMethod{
		ssh.Password(password),
		ssh.KeyboardInteractive(passwordChallenge(password)),
	}
	// 保留 x/crypto/ssh 的原生方法选择与回退，只在服务端已接受一个因素后中止。
	// 这样既兼容仅支持 keyboard-interactive 的设备，也不会把保存的密码用于 OTP。
	callback := func(ctx *ssh.ClientAuthContext) (ssh.AuthMethod, error) {
		if len(ctx.PartialSuccessMethods) > 0 {
			return nil, fmt.Errorf("不支持 SSH 多因素认证（已完成 %s）", strings.Join(ctx.PartialSuccessMethods, ", "))
		}
		return nil, nil
	}
	return auths, callback
}

func passwordChallenge(password string) ssh.KeyboardInteractiveChallenge {
	var answered bool
	return func(name, instruction string, questions []string, echos []bool) ([]string, error) {
		if answered {
			return nil, fmt.Errorf("不支持多轮 keyboard-interactive 认证")
		}
		if len(questions) != 1 || len(echos) != 1 {
			return nil, fmt.Errorf("keyboard-interactive 提示数量异常")
		}
		if echos[0] {
			return nil, fmt.Errorf("拒绝向可回显的 keyboard-interactive 字段发送密码")
		}
		// echo=false 是协议提供的秘密字段边界；提示文本可能被设备本地化或自定义。
		answered = true
		return []string{password}, nil
	}
}
func nameAddr(h store.Host) string { return h.Name + "(" + h.Addr + ")" }
func (p *Pool) keepalive(name string, e *entry, c *ssh.Client) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.closed:
			return
		case <-ticker.C:
			_, _, err := c.SendRequest("keepalive@onessh", true, nil)
			if err != nil {
				e.mu.Lock()
				if e.client == c {
					e.online = false
					e.client = nil
				}
				e.mu.Unlock()
				c.Close()
				log.Printf("SSH %s keepalive 失败: %v", name, err)
				return
			}
		}
	}
}
func (p *Pool) IsOnline(name string) bool {
	e := p.getEntry(name)
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.online
}
func (p *Pool) Invalidate(name string) {
	e := p.getEntry(name)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.client != nil {
		e.client.Close()
	}
	e.client = nil
	e.online = false
}
func (p *Pool) Close() {
	select {
	case <-p.closed:
		return
	default:
		close(p.closed)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.entries {
		e.mu.Lock()
		if e.client != nil {
			e.client.Close()
		}
		e.online = false
		e.mu.Unlock()
	}
}
