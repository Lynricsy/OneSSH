package mcpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/events"
	"onessh/internal/memoryx"
	"onessh/internal/store"
)

func TestMemoryToolsLifecycleAndAuthorization(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	allowed, err := st.CreateHost(ctx, store.Host{Name: "allowed", Addr: "127.0.0.1", Port: 22, Username: "user", AuthType: "password"})
	if err != nil {
		t.Fatal(err)
	}
	denied, err := st.CreateHost(ctx, store.Host{Name: "denied", Addr: "127.0.0.2", Port: 22, Username: "user", AuthType: "password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.CreateToken(ctx, store.TokenCreate{Name: "agent", Hash: store.TokenHash("secret"), HostIDs: []int64{allowed.ID}}); err != nil {
		t.Fatal(err)
	}
	deniedID, err := st.AddMemory(ctx, store.Memory{
		HostID: sql.NullInt64{Int64: denied.ID, Valid: true}, Content: "denied 部署目录", Source: "test",
		Importance: 0.5, Veracity: "stated", CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}

	server := &Server{
		MCP:    mcp.NewServer(&mcp.Implementation{Name: "OneSSH", Version: "test"}, nil),
		Store:  st,
		Memory: memoryx.New(st, memoryx.EmbeddingConfig{}),
		Events: events.New(),
	}
	server.registerMemory()
	resolve := func(*http.Request) (string, string) { return "", "" }
	httpServer := httptest.NewServer(Handler(st, server, resolve))
	defer httpServer.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "memory-test", Version: "1"}, nil)
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	session, err := client.Connect(callCtx, &mcp.StreamableClientTransport{
		Endpoint: httpServer.URL, HTTPClient: &http.Client{Transport: bearerTransport{token: "secret"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	call := func(name string, arguments any) *mcp.CallToolResult {
		t.Helper()
		result, err := session.CallTool(callCtx, &mcp.CallToolParams{Name: name, Arguments: arguments})
		if err != nil {
			t.Fatalf("调用 %s: %v", name, err)
		}
		return result
	}
	decode := func(result *mcp.CallToolResult, out any) {
		t.Helper()
		raw, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(raw, out); err != nil {
			t.Fatalf("解析结构化结果 %s: %v", raw, err)
		}
	}

	remember := call("memory_remember", map[string]any{
		"host": "allowed", "content": "部署目录在 /opt/app", "importance": 0.8,
	})
	if remember.IsError {
		t.Fatalf("写入失败: %#v", remember.Content)
	}
	var remembered MemoryRememberOutput
	decode(remember, &remembered)
	if remembered.ID == 0 || remembered.Bank != "allowed" || remembered.Deduped {
		t.Fatalf("写入结果异常: %#v", remembered)
	}

	recall := call("memory_recall", map[string]any{"host": "allowed", "query": "部署目录"})
	if recall.IsError {
		t.Fatalf("召回失败: %#v", recall.Content)
	}
	var recalled MemoryRecallOutput
	decode(recall, &recalled)
	if recalled.Engine != "fts" || len(recalled.Results) != 1 || recalled.Results[0].ID != remembered.ID || recalled.Results[0].Bank != "allowed" {
		t.Fatalf("召回结果异常: %#v", recalled)
	}

	global := call("memory_remember", map[string]any{"content": "全局发布规范", "importance": 0.6})
	var globalMemory MemoryRememberOutput
	decode(global, &globalMemory)
	if global.IsError || globalMemory.Bank != "global" {
		t.Fatalf("全局写入异常: %#v %#v", globalMemory, global.Content)
	}
	listed := call("memory_list", map[string]any{})
	var list MemoryListOutput
	decode(listed, &list)
	if listed.IsError || list.Bank != "global" || len(list.Memories) != 1 || list.Memories[0].ID != globalMemory.ID {
		t.Fatalf("全局列表异常: %#v", list)
	}

	zero := 0.0
	updated := call("memory_update", map[string]any{"id": remembered.ID, "importance": zero})
	if updated.IsError {
		t.Fatalf("更新失败: %#v", updated.Content)
	}
	memory, err := st.GetMemory(ctx, remembered.ID)
	if err != nil || memory.Importance != 0 {
		t.Fatalf("显式零重要度未保存: memory=%#v err=%v", memory, err)
	}

	statsResult := call("memory_stats", map[string]any{})
	var stats MemoryStatsOutput
	decode(statsResult, &stats)
	if statsResult.IsError || stats.Total != 2 || len(stats.Banks) != 2 {
		t.Fatalf("可见 bank 统计异常或泄漏 denied bank: %#v", stats)
	}
	if sleep := call("memory_sleep", map[string]any{}); sleep.IsError {
		t.Fatalf("全局维护失败: %#v", sleep.Content)
	}

	unauthorizedHost := call("memory_remember", map[string]any{"host": "denied", "content": "不应写入"})
	if !unauthorizedHost.IsError || !toolResultContains(unauthorizedHost, "host not authorized: denied") {
		t.Fatalf("未拒绝越权主机写入: %#v", unauthorizedHost)
	}
	unauthorizedID := call("memory_forget", map[string]any{"id": deniedID})
	if !unauthorizedID.IsError || !toolResultContains(unauthorizedID, "memory 不可访问") {
		t.Fatalf("未拒绝跨 bank 删除: %#v", unauthorizedID)
	}

	for _, id := range []int64{remembered.ID, globalMemory.ID} {
		forgotten := call("memory_forget", map[string]any{"id": id})
		if forgotten.IsError {
			t.Fatalf("删除 %d 失败: %#v", id, forgotten.Content)
		}
		var out MemoryForgetOutput
		decode(forgotten, &out)
		if !out.Deleted {
			t.Fatalf("删除结果异常: %#v", out)
		}
	}
}

func toolResultContains(result *mcp.CallToolResult, text string) bool {
	for _, content := range result.Content {
		if value, ok := content.(*mcp.TextContent); ok && strings.Contains(value.Text, text) {
			return true
		}
	}
	return false
}
