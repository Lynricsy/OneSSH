package searchx

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/pkg/sftp"
	"onessh/internal/searchcore"
)

const (
	grepFallbackWarning = "远端缺少 rg，已使用 SFTP 降级搜索；大型目录上的性能可能低于原生 rg"
	findFallbackWarning = "远端缺少 fd/fdfind，已使用 SFTP 降级搜索；大型目录上的性能可能低于原生 fd"
)

type sftpSearchFS struct{ client *sftp.Client }

func (f sftpSearchFS) Getwd() (string, error)                 { return f.client.Getwd() }
func (f sftpSearchFS) Lstat(name string) (os.FileInfo, error) { return f.client.Lstat(name) }
func (f sftpSearchFS) ReadDir(name string) ([]os.FileInfo, error) {
	return f.client.ReadDir(name)
}
func (f sftpSearchFS) Open(name string) (io.ReadCloser, error) { return f.client.Open(name) }

func (m *Manager) openSearchFS(ctx context.Context, host string) (searchcore.FS, func(), error) {
	sshClient, err := m.Pool.Get(ctx, host)
	if err != nil {
		return nil, nil, err
	}
	session, err := sshClient.NewSession()
	if err != nil {
		return nil, nil, err
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return nil, nil, err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return nil, nil, err
	}
	if err = session.RequestSubsystem("sftp"); err != nil {
		session.Close()
		return nil, nil, fmt.Errorf("启动 SFTP 子系统: %w", err)
	}

	watchDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = session.Close()
		case <-watchDone:
		}
	}()
	client, err := sftp.NewClientPipe(stdout, stdin)
	if err != nil {
		close(watchDone)
		session.Close()
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, fmt.Errorf("创建 SFTP 搜索客户端: %w", err)
	}
	cleanup := func() {
		close(watchDone)
		_ = client.Close()
		_ = session.Close()
	}
	return sftpSearchFS{client: client}, cleanup, nil
}

func (m *Manager) grepSFTP(ctx context.Context, host string, opt GrepOptions) (GrepResult, error) {
	fs, cleanup, err := m.openSearchFS(ctx, host)
	if err != nil {
		return GrepResult{}, err
	}
	defer cleanup()
	out, err := searchcore.Grep(ctx, fs, opt)
	if err != nil {
		return GrepResult{}, err
	}
	out.Engine = "sftp"
	out.Warning = grepFallbackWarning
	return out, nil
}

func (m *Manager) findSFTP(ctx context.Context, host string, opt FindOptions) (FindResult, error) {
	fs, cleanup, err := m.openSearchFS(ctx, host)
	if err != nil {
		return FindResult{}, err
	}
	defer cleanup()
	out, err := searchcore.Find(ctx, fs, opt)
	if err != nil {
		return FindResult{}, err
	}
	out.Engine = "sftp"
	out.Warning = findFallbackWarning
	return out, nil
}
