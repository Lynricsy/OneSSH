package sshpool

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"onessh/internal/cryptox"
	"onessh/internal/store"
)

const testPassword = "secret"

func runPasswordHandshake(t *testing.T, serverConfig *ssh.ServerConfig) error {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	serverConfig.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan error, 1)
	go func() {
		rawConn, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		_ = rawConn.SetDeadline(time.Now().Add(5 * time.Second))
		defer rawConn.Close()
		conn, _, _, serverErr := ssh.NewServerConn(rawConn, serverConfig)
		if conn != nil {
			_ = conn.Close()
		}
		serverDone <- serverErr
	}()

	clientSide, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = clientSide.SetDeadline(time.Now().Add(5 * time.Second))
	defer clientSide.Close()
	auths, authCallback := passwordAuthentication(testPassword)
	clientConfig := &ssh.ClientConfig{
		User:            "test",
		Auth:            auths,
		AuthCallback:    authCallback,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	conn, _, _, clientErr := ssh.NewClientConn(clientSide, listener.Addr().String(), clientConfig)
	if conn != nil {
		_ = conn.Close()
	}
	_ = clientSide.Close()
	select {
	case <-serverDone:
	case <-time.After(6 * time.Second):
		t.Fatal("等待 SSH 测试服务端结束超时")
	}
	return clientErr
}
func newStalledHandshakePool(t *testing.T) (*Pool, <-chan net.Conn) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	box, err := cryptox.New(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	password, err := box.Seal([]byte(testPassword))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if _, err = st.CreateHost(ctx, store.Host{
		Name:        "stalled",
		Addr:        "127.0.0.1",
		Port:        port,
		Username:    "test",
		AuthType:    "password",
		PasswordEnc: password,
	}); err != nil {
		t.Fatal(err)
	}
	pool := New(st, box)
	t.Cleanup(func() {
		listener.Close()
		pool.Close()
		st.Close()
	})
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- conn
		}
	}()
	return pool, accepted
}

func waitForStalledHandshake(t *testing.T, accepted <-chan net.Conn) net.Conn {
	t.Helper()
	select {
	case conn := <-accepted:
		return conn
	case <-time.After(time.Second):
		t.Fatal("等待停滞 SSH 握手建立连接超时")
		return nil
	}
}

type directTCPIPRequest struct {
	Host       string
	Port       uint32
	OriginHost string
	OriginPort uint32
}

func startTestJumpServer(t *testing.T) (string, func()) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if string(password) != testPassword {
				return nil, errors.New("wrong password")
			}
			return nil, nil
		},
	}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	var (
		connMu     sync.Mutex
		serverConn net.Conn
		stopOnce   sync.Once
	)
	go func() {
		rawConn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		connMu.Lock()
		serverConn = rawConn
		connMu.Unlock()
		sshConn, channels, requests, handshakeErr := ssh.NewServerConn(rawConn, config)
		if handshakeErr != nil {
			rawConn.Close()
			return
		}
		defer sshConn.Close()
		go ssh.DiscardRequests(requests)
		for newChannel := range channels {
			if newChannel.ChannelType() != "direct-tcpip" {
				newChannel.Reject(ssh.UnknownChannelType, "仅支持 direct-tcpip")
				continue
			}
			var request directTCPIPRequest
			if err := ssh.Unmarshal(newChannel.ExtraData(), &request); err != nil {
				newChannel.Reject(ssh.ConnectionFailed, "解析转发目标失败")
				continue
			}
			target, dialErr := net.Dial("tcp", net.JoinHostPort(request.Host, strconv.Itoa(int(request.Port))))
			if dialErr != nil {
				newChannel.Reject(ssh.ConnectionFailed, dialErr.Error())
				continue
			}
			channel, channelRequests, acceptErr := newChannel.Accept()
			if acceptErr != nil {
				target.Close()
				continue
			}
			go ssh.DiscardRequests(channelRequests)
			go func() {
				done := make(chan struct{}, 2)
				go func() {
					_, _ = io.Copy(target, channel)
					done <- struct{}{}
				}()
				go func() {
					_, _ = io.Copy(channel, target)
					done <- struct{}{}
				}()
				<-done
				channel.Close()
				target.Close()
			}()
		}
	}()
	stop := func() {
		stopOnce.Do(func() {
			listener.Close()
			connMu.Lock()
			if serverConn != nil {
				serverConn.Close()
			}
			connMu.Unlock()
		})
	}
	return listener.Addr().String(), stop
}

func TestGetCancelsStalledSSHHandshakeThroughJumpHost(t *testing.T) {
	jumpAddr, stopJump := startTestJumpServer(t)
	defer stopJump()
	jumpHost, jumpPortText, err := net.SplitHostPort(jumpAddr)
	if err != nil {
		t.Fatal(err)
	}
	jumpPort, err := strconv.Atoi(jumpPortText)
	if err != nil {
		t.Fatal(err)
	}
	targetListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer targetListener.Close()
	targetAccepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := targetListener.Accept()
		if acceptErr == nil {
			targetAccepted <- conn
		}
	}()

	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	box, err := cryptox.New(bytes.Repeat([]byte{10}, 32))
	if err != nil {
		t.Fatal(err)
	}
	password, err := box.Seal([]byte(testPassword))
	if err != nil {
		t.Fatal(err)
	}
	jump, err := st.CreateHost(ctx, store.Host{
		Name:        "jump",
		Addr:        jumpHost,
		Port:        jumpPort,
		Username:    "test",
		AuthType:    "password",
		PasswordEnc: password,
	})
	if err != nil {
		t.Fatal(err)
	}
	targetPort := targetListener.Addr().(*net.TCPAddr).Port
	if _, err = st.CreateHost(ctx, store.Host{
		Name:        "target",
		Addr:        "127.0.0.1",
		Port:        targetPort,
		Username:    "test",
		AuthType:    "password",
		PasswordEnc: password,
		JumpHostID:  sql.NullInt64{Int64: jump.ID, Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	pool := New(st, box)
	defer func() {
		pool.Close()
		st.Close()
	}()

	callCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, getErr := pool.Get(callCtx, "target")
		result <- getErr
	}()
	targetConn := waitForStalledHandshake(t, targetAccepted)
	defer targetConn.Close()
	if !pool.IsOnline("jump") {
		t.Fatal("测试未通过跳板连接目标")
	}
	cancel()
	select {
	case err = <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("取消跳板握手错误 = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("取消后跳板 SSH 握手未返回")
	}
}

func TestGetCancelsStalledSSHHandshake(t *testing.T) {
	pool, accepted := newStalledHandshakePool(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := pool.Get(ctx, "stalled")
		result <- err
	}()
	serverConn := waitForStalledHandshake(t, accepted)
	defer serverConn.Close()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("取消握手错误 = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("取消后 SSH 握手未返回")
	}
}

func TestGetTimesOutStalledSSHHandshake(t *testing.T) {
	pool, accepted := newStalledHandshakePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := pool.Get(ctx, "stalled")
		result <- err
	}()
	serverConn := waitForStalledHandshake(t, accepted)
	defer serverConn.Close()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("握手超时错误 = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("超过上下文期限后 SSH 握手未返回")
	}
}

func TestPasswordAuthenticationHandshake(t *testing.T) {
	t.Run("password only", func(t *testing.T) {
		attempts := 0
		err := runPasswordHandshake(t, &ssh.ServerConfig{
			PasswordCallback: func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
				attempts++
				if string(password) != testPassword {
					return nil, errors.New("wrong password")
				}
				return nil, nil
			},
		})
		if err != nil || attempts != 1 {
			t.Fatalf("password 握手 = %v，尝试次数 = %d", err, attempts)
		}
	})

	t.Run("keyboard interactive only", func(t *testing.T) {
		attempts := 0
		err := runPasswordHandshake(t, &ssh.ServerConfig{
			KeyboardInteractiveCallback: func(_ ssh.ConnMetadata, challenge ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
				attempts++
				answers, err := challenge("设备认证", "", []string{"请输入凭据："}, []bool{false})
				if err != nil {
					return nil, err
				}
				if len(answers) != 1 || answers[0] != testPassword {
					return nil, errors.New("wrong answer")
				}
				return nil, nil
			},
		})
		if err != nil || attempts != 1 {
			t.Fatalf("keyboard-interactive 握手 = %v，尝试次数 = %d", err, attempts)
		}
	})

	t.Run("password rejected then keyboard interactive", func(t *testing.T) {
		passwordAttempts := 0
		keyboardAttempts := 0
		err := runPasswordHandshake(t, &ssh.ServerConfig{
			PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) {
				passwordAttempts++
				return nil, errors.New("password method disabled")
			},
			KeyboardInteractiveCallback: func(_ ssh.ConnMetadata, challenge ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
				keyboardAttempts++
				answers, err := challenge("", "", []string{"Password:"}, []bool{false})
				if err != nil || len(answers) != 1 || answers[0] != testPassword {
					return nil, errors.New("wrong answer")
				}
				return nil, nil
			},
		})
		if err != nil || passwordAttempts != 1 || keyboardAttempts != 1 {
			t.Fatalf("回退握手 = %v，password = %d，keyboard-interactive = %d", err, passwordAttempts, keyboardAttempts)
		}
	})
}

func TestPasswordAuthenticationRejectsMultiFactorContinuation(t *testing.T) {
	keyboardAttempted := false
	err := runPasswordHandshake(t, &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if string(password) != testPassword {
				return nil, errors.New("wrong password")
			}
			return nil, &ssh.PartialSuccessError{Next: ssh.ServerAuthCallbacks{
				KeyboardInteractiveCallback: func(_ ssh.ConnMetadata, _ ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
					keyboardAttempted = true
					return nil, nil
				},
			}}
		},
	})
	if err == nil || !strings.Contains(err.Error(), "多因素") {
		t.Fatalf("多因素握手错误 = %v", err)
	}
	if keyboardAttempted {
		t.Fatal("密码已部分成功后仍进入 keyboard-interactive")
	}
}

func TestPasswordChallengeOnlyAnswersOneHiddenField(t *testing.T) {
	challenge := passwordChallenge(testPassword)
	answers, err := challenge("自定义认证", "", []string{"请输入凭据："}, []bool{false})
	if err != nil || len(answers) != 1 || answers[0] != testPassword {
		t.Fatalf("隐藏字段响应 = %#v, %v", answers, err)
	}
	if answers, err = challenge("", "", []string{"验证码："}, []bool{false}); err == nil || answers != nil {
		t.Fatalf("第二轮提示未被拒绝：%#v, %v", answers, err)
	}

	for _, test := range []struct {
		name      string
		questions []string
		echos     []bool
	}{
		{name: "可回显字段", questions: []string{"Username:"}, echos: []bool{true}},
		{name: "多个字段", questions: []string{"Password:", "OTP:"}, echos: []bool{false, false}},
		{name: "缺少回显元数据", questions: []string{"Password:"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			answers, err := passwordChallenge(testPassword)("", "", test.questions, test.echos)
			if err == nil || answers != nil {
				t.Fatalf("非法提示未被拒绝：%#v, %v", answers, err)
			}
		})
	}
}
