package searchx

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"onessh/internal/execx"
	"onessh/internal/searchcore"
	searchhelper "onessh/internal/searchx/helper"
)

const (
	probeTimeout      = 10 * time.Second
	stageTimeout      = 30 * time.Second
	cleanupTimeout    = 10 * time.Second
	helperPrefix      = ".onessh-search-helper-"
	helperStaleAge    = time.Hour
	grepHelperWarning = "远端缺少 rg，已上传临时 OneSSH 搜索 helper 在远端本地遍历（Go 正则语义，运行结束后已自动删除）"
	findHelperWarning = "远端缺少 fd/fdfind，已上传临时 OneSSH 搜索 helper 在远端本地遍历（运行结束后已自动删除）"
)

func (m *Manager) grepWithoutNative(ctx context.Context, host string, opt GrepOptions) (GrepResult, error) {
	if _, err := searchcore.CompileGrepPattern(opt); err != nil {
		return GrepResult{}, err
	}
	if _, err := searchcore.CompilePathMatcher(opt.Glob, false); err != nil {
		return GrepResult{}, errors.New("glob 无效: " + err.Error())
	}
	if out, handled, err := m.helperGrep(ctx, host, opt); handled {
		return out, err
	}
	sftpCtx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	return m.grepSFTP(sftpCtx, host, opt)
}

func (m *Manager) findWithoutNative(ctx context.Context, host string, opt FindOptions) (FindResult, error) {
	if _, err := searchcore.CompilePathMatcher(opt.Pattern, true); err != nil {
		return FindResult{}, errors.New("glob 无效: " + err.Error())
	}
	if out, handled, err := m.helperFind(ctx, host, opt); handled {
		return out, err
	}
	sftpCtx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	return m.findSFTP(sftpCtx, host, opt)
}

func (m *Manager) helperGrep(ctx context.Context, host string, opt GrepOptions) (GrepResult, bool, error) {
	resp, handled, err := m.runHelperSearch(ctx, host, searchcore.HelperRequest{
		Version: searchcore.ProtocolVersion,
		Op:      "grep",
		Grep:    &opt,
	})
	if !handled || err != nil {
		return GrepResult{}, handled, err
	}
	out := *resp.Grep
	out.Engine = "helper"
	out.Warning = grepHelperWarning
	return out, true, nil
}

func (m *Manager) helperFind(ctx context.Context, host string, opt FindOptions) (FindResult, bool, error) {
	resp, handled, err := m.runHelperSearch(ctx, host, searchcore.HelperRequest{
		Version: searchcore.ProtocolVersion,
		Op:      "find",
		Find:    &opt,
	})
	if !handled || err != nil {
		return FindResult{}, handled, err
	}
	out := *resp.Find
	out.Engine = "helper"
	out.Warning = findHelperWarning
	return out, true, nil
}

func (m *Manager) runHelperSearch(ctx context.Context, host string, request searchcore.HelperRequest) (searchcore.HelperResponse, bool, error) {
	if !m.HelperEnabled {
		return searchcore.HelperResponse{}, false, nil
	}
	client, payload, ok := m.probePlatform(ctx, host)
	if !ok {
		return searchcore.HelperResponse{}, false, nil
	}

	stageCtx, cancelStage := context.WithTimeout(ctx, stageTimeout)
	remotePath, ok := m.stageHelper(stageCtx, host, payload)
	cancelStage()
	if !ok {
		m.markHelperUnusable(host, client)
		return searchcore.HelperResponse{}, false, nil
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		if c, err := m.SFTP.Get(cleanupCtx, host); err == nil {
			_ = c.Remove(remotePath)
		}
	}()

	encoded, err := json.Marshal(request)
	if err != nil {
		m.markHelperUnusable(host, client)
		return searchcore.HelperResponse{}, false, nil
	}
	runCtx, cancelRun := context.WithTimeout(ctx, searchTimeout)
	stdout, exitCode, runErr := m.runHelper(runCtx, host, remotePath, encoded)
	runContextErr := runCtx.Err()
	cancelRun()
	if runContextErr != nil {
		return searchcore.HelperResponse{}, true, runContextErr
	}
	if runErr != nil || exitCode != 0 {
		m.markHelperUnusable(host, client)
		return searchcore.HelperResponse{}, false, nil
	}
	var resp searchcore.HelperResponse
	if err = json.Unmarshal(stdout, &resp); err != nil {
		m.markHelperUnusable(host, client)
		return searchcore.HelperResponse{}, false, nil
	}
	if err = searchcore.CheckProtocolVersion(resp.Version); err != nil {
		m.markHelperUnusable(host, client)
		return searchcore.HelperResponse{}, false, nil
	}
	if resp.Error != "" {
		return resp, true, errors.New(resp.Error)
	}
	if request.Op == "grep" && resp.Grep == nil || request.Op == "find" && resp.Find == nil {
		m.markHelperUnusable(host, client)
		return searchcore.HelperResponse{}, false, nil
	}
	return resp, true, nil
}

func (m *Manager) probePlatform(ctx context.Context, host string) (*ssh.Client, []byte, bool) {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	client, err := m.Pool.Get(probeCtx, host)
	if err != nil {
		return nil, nil, false
	}
	m.mu.Lock()
	state, cached := m.helper[host]
	m.mu.Unlock()
	if cached && state.client == client {
		if !state.usable {
			return client, nil, false
		}
		payload, ok := searchhelper.Payload(state.goos, state.goarch)
		return client, payload, ok
	}

	lines := make([]string, 0, 2)
	_, exitCode, _, err := m.stream(probeCtx, host, "uname -s && uname -m", func(line string) bool {
		lines = append(lines, strings.TrimSpace(line))
		return true
	})
	state = helperState{client: client}
	if err == nil && exitCode == 0 && len(lines) >= 2 {
		state.goos, state.goarch, state.usable = normalizePlatform(lines[0], lines[1])
		if state.usable {
			_, state.usable = searchhelper.Payload(state.goos, state.goarch)
		}
	}
	m.mu.Lock()
	m.helper[host] = state
	m.mu.Unlock()
	if !state.usable {
		return client, nil, false
	}
	payload, ok := searchhelper.Payload(state.goos, state.goarch)
	return client, payload, ok
}

func normalizePlatform(osName, arch string) (goos, goarch string, ok bool) {
	if strings.ToLower(strings.TrimSpace(osName)) != "linux" {
		return "", "", false
	}
	switch strings.ToLower(strings.TrimSpace(arch)) {
	case "x86_64", "amd64":
		return "linux", "amd64", true
	case "aarch64", "arm64":
		return "linux", "arm64", true
	default:
		return "", "", false
	}
}

func (m *Manager) markHelperUnusable(host string, client *ssh.Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.helper[host]
	if ok && state.client == client {
		state.usable = false
		m.helper[host] = state
	}
}

func (m *Manager) stageHelper(ctx context.Context, host string, payload []byte) (string, bool) {
	c, err := m.SFTP.Get(ctx, host)
	if err != nil {
		return "", false
	}
	now := time.Now()
	if entries, err := c.ReadDir("/tmp"); err == nil {
		for _, entry := range entries {
			if staleHelper(entry.Name(), entry.ModTime(), now) {
				_ = c.Remove("/tmp/" + entry.Name())
			}
		}
	}
	var suffix [16]byte
	if _, err = rand.Read(suffix[:]); err != nil {
		return "", false
	}
	remotePath := "/tmp/" + helperPrefix + hex.EncodeToString(suffix[:])
	file, err := c.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		_ = c.Remove(remotePath)
		return remotePath, false
	}
	_, writeErr := io.Copy(file, bytes.NewReader(payload))
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = c.Remove(remotePath)
		return remotePath, false
	}
	if err = c.Chmod(remotePath, 0o700); err != nil {
		_ = c.Remove(remotePath)
		return remotePath, false
	}
	return remotePath, true
}

func staleHelper(name string, modTime, now time.Time) bool {
	return strings.HasPrefix(name, helperPrefix) && now.Sub(modTime) > helperStaleAge
}

func (m *Manager) runHelper(ctx context.Context, host, remotePath string, request []byte) (stdout []byte, exitCode int, err error) {
	client, err := m.Pool.Get(ctx, host)
	if err != nil {
		return nil, 0, err
	}
	session, err := client.NewSession()
	if err != nil {
		return nil, 0, err
	}
	defer session.Close()
	stdin, err := session.StdinPipe()
	if err != nil {
		return nil, 0, err
	}
	stdoutPipe, err := session.StdoutPipe()
	if err != nil {
		return nil, 0, err
	}
	if err = session.Start("cd \"$HOME\" 2>/dev/null || exit 1; exec " + execx.SHQ(remotePath)); err != nil {
		return nil, 0, err
	}
	closed := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = session.Close()
		case <-closed:
		}
	}()
	if _, err = stdin.Write(request); err != nil {
		_ = stdin.Close()
		close(closed)
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, err
	}
	if err = stdin.Close(); err != nil {
		close(closed)
		return nil, 0, err
	}
	stdout, readErr := io.ReadAll(io.LimitReader(stdoutPipe, 4<<20))
	waitErr := session.Wait()
	close(closed)
	if ctx.Err() != nil {
		return nil, 0, ctx.Err()
	}
	if readErr != nil {
		return nil, 0, readErr
	}
	if waitErr == nil {
		return stdout, 0, nil
	}
	var exitErr *ssh.ExitError
	if errors.As(waitErr, &exitErr) {
		return stdout, exitErr.ExitStatus(), nil
	}
	return nil, 0, waitErr
}
