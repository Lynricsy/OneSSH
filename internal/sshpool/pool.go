package sshpool

import (
	"context"
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"onessh/internal/cryptox"
	"onessh/internal/store"
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
	client, err := p.dial(ctx, h)
	if err != nil {
		e.online = false
		return nil, err
	}
	e.client = client
	e.online = true
	go p.keepalive(name, e, client)
	return client, nil
}
func (p *Pool) dial(ctx context.Context, h store.Host) (*ssh.Client, error) {
	var auth ssh.AuthMethod
	switch h.AuthType {
	case "password":
		plain, err := p.box.Open(h.PasswordEnc)
		if err != nil {
			return nil, err
		}
		auth = ssh.Password(string(plain))
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
		auth = ssh.PublicKeys(signer)
	default:
		return nil, fmt.Errorf("不支持的认证类型 %q", h.AuthType)
	}
	callback := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		fp := ssh.FingerprintSHA256(key)
		if !h.HostKeyFP.Valid || h.HostKeyFP.String == "" {
			if err := p.store.UpdateHostFingerprint(context.Background(), h.ID, &fp); err != nil {
				return fmt.Errorf("记录主机指纹: %w", err)
			}
			return nil
		}
		if h.HostKeyFP.String != fp {
			return fmt.Errorf("SSH 主机指纹变更，需管理员重置（期望 %s，收到 %s）", h.HostKeyFP.String, fp)
		}
		return nil
	}
	config := &ssh.ClientConfig{User: h.Username, Auth: []ssh.AuthMethod{auth}, HostKeyCallback: callback, Timeout: 15 * time.Second}
	addr := net.JoinHostPort(h.Addr, strconv.Itoa(h.Port))
	d := net.Dialer{Timeout: 15 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("连接 %s: %w", nameAddr(h), err)
	}
	cc, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("SSH 握手 %s: %w", nameAddr(h), err)
	}
	return ssh.NewClient(cc, chans, reqs), nil
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
