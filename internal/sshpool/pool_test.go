package sshpool

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
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
