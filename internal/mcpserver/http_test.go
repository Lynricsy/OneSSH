package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/store"
)

type bearerTransport struct {
	token string
}

func (t bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header = req.Header.Clone()
	req.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(req)
}

func TestHandlerUsesModernStatelessTransport(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = st.CreateToken(context.Background(), store.TokenCreate{Name: "agent", Hash: store.TokenHash("secret"), AllHosts: true}); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	cancelled := make(chan struct{}, 1)
	server := &Server{MCP: newProtocolServer("")}
	mcp.AddTool(server.MCP, &mcp.Tool{Name: "wait"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, Empty, error) {
		close(started)
		<-ctx.Done()
		cancelled <- struct{}{}
		return nil, Empty{}, ctx.Err()
	})

	resolve := func(*http.Request) (string, string) {
		return "https://onessh.example/mcp", "https://onessh.example/.well-known/oauth-protected-resource/mcp"
	}
	httpServer := httptest.NewServer(Handler(st, server, resolve))
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "onessh-test", Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   httpServer.URL,
		HTTPClient: &http.Client{Transport: bearerTransport{token: "secret"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if got := session.InitializeResult().ProtocolVersion; got != "2026-07-28" {
		t.Fatalf("协议版本 = %q，期望 2026-07-28", got)
	}
	instructions := session.InitializeResult().Instructions
	// instructions 是 Agent 唯一能在调用前读到的全局说明：授权入口、工具选型、长任务与截断输出的处置、
	// 以及记忆读写与安全红线都必须写进去，缺一项模型就会退回 exec 硬拼命令或凭空猜测。
	required := []string{
		"hosts_list", "manage_hosts", "job_start", "output_read", "file_edit", "exec_many",
		"memory_recall", "memory_remember", "memory_update", "memory_forget",
		"不得保存密码", "不得替代现场",
	}
	for _, keyword := range required {
		if !strings.Contains(instructions, keyword) {
			t.Fatalf("初始化提示词缺少 %q: %s", keyword, instructions)
		}
	}

	callCtx, cancelCall := context.WithCancel(context.Background())
	callDone := make(chan error, 1)
	go func() {
		_, callErr := session.CallTool(callCtx, &mcp.CallToolParams{Name: "wait"})
		callDone <- callErr
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("等待工具启动超时")
	}
	cancelCall()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP 请求取消未传播到工具")
	}
	select {
	case <-callDone:
	case <-time.After(5 * time.Second):
		t.Fatal("客户端工具调用未在取消后结束")
	}
}
