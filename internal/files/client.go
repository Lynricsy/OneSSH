package files

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"onessh/internal/sshpool"
)

type cached struct {
	ssh  *ssh.Client
	sftp *sftp.Client
}
type ClientPool struct {
	pool    *sshpool.Pool
	mu      sync.Mutex
	clients map[string]cached
}

func NewClientPool(pool *sshpool.Pool) *ClientPool {
	return &ClientPool{pool: pool, clients: make(map[string]cached)}
}
func (p *ClientPool) Get(ctx context.Context, host string) (*sftp.Client, error) {
	sshClient, err := p.pool.Get(ctx, host)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.clients[host]; ok && c.ssh == sshClient {
		return c.sftp, nil
	}
	if old, ok := p.clients[host]; ok {
		old.sftp.Close()
	}
	client, err := sftp.NewClient(sshClient)
	if err != nil {
		p.pool.Invalidate(host)
		return nil, fmt.Errorf("创建 SFTP 客户端: %w", err)
	}
	p.clients[host] = cached{ssh: sshClient, sftp: client}
	return client, nil
}
func (p *ClientPool) Invalidate(host string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.clients[host]; ok {
		c.sftp.Close()
		delete(p.clients, host)
	}
}
func (p *ClientPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.clients {
		c.sftp.Close()
	}
	p.clients = make(map[string]cached)
}
func resolvePath(c *sftp.Client, path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := c.Getwd()
	if err != nil {
		return "", err
	}
	if path == "~" {
		return home, nil
	}
	return home + "/" + strings.TrimPrefix(path, "~/"), nil
}
