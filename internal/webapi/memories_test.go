package webapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"onessh/internal/memoryx"
	"onessh/internal/store"
)

func TestMemoryAdminAPIListFilterStatsAndDelete(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	host, err := st.CreateHost(ctx, store.Host{Name: "production", Addr: "127.0.0.1", Port: 22, Username: "user", AuthType: "password"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	add := func(memory store.Memory) int64 {
		t.Helper()
		memory.Source = "test"
		memory.Importance = 0.5
		memory.Veracity = "stated"
		memory.CreatedAt = now
		memory.UpdatedAt = now
		id, err := st.AddMemory(ctx, memory)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	globalID := add(store.Memory{Content: "nginx 全局发布规范"})
	hostID := sql.NullInt64{Int64: host.ID, Valid: true}
	hostMemoryID := add(store.Memory{HostID: hostID, Content: "nginx 主机部署目录"})
	add(store.Memory{HostID: hostID, Content: "database 主机备份目录"})

	api := NewAPI(st, nil, nil, nil, nil, nil, nil, nil, memoryx.New(st, memoryx.EmbeddingConfig{}), nil).Handler()
	call := func(method, path string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, nil)
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		return response
	}

	all := call(http.MethodGet, "/memories")
	var allRows []store.MemoryAdminRow
	if all.Code != http.StatusOK || json.Unmarshal(all.Body.Bytes(), &allRows) != nil || len(allRows) != 3 {
		t.Fatalf("全部记忆响应异常: code=%d body=%s", all.Code, all.Body.String())
	}
	global := call(http.MethodGet, "/memories?host_id=0")
	var globalRows []store.MemoryAdminRow
	if global.Code != http.StatusOK || json.Unmarshal(global.Body.Bytes(), &globalRows) != nil || len(globalRows) != 1 || globalRows[0].ID != globalID || globalRows[0].HostID != nil {
		t.Fatalf("全局筛选异常: code=%d body=%s rows=%#v", global.Code, global.Body.String(), globalRows)
	}
	filtered := call(http.MethodGet, fmt.Sprintf("/memories?host_id=%d&q=nginx", host.ID))
	var filteredRows []store.MemoryAdminRow
	if filtered.Code != http.StatusOK || json.Unmarshal(filtered.Body.Bytes(), &filteredRows) != nil || len(filteredRows) != 1 || filteredRows[0].ID != hostMemoryID || filteredRows[0].HostName == nil || *filteredRows[0].HostName != host.Name {
		t.Fatalf("主机关键词筛选异常: code=%d body=%s rows=%#v", filtered.Code, filtered.Body.String(), filteredRows)
	}

	statsResponse := call(http.MethodGet, "/memories/stats")
	var stats []memoryAdminBankStat
	if statsResponse.Code != http.StatusOK || json.Unmarshal(statsResponse.Body.Bytes(), &stats) != nil || len(stats) != 2 {
		t.Fatalf("统计响应异常: code=%d body=%s", statsResponse.Code, statsResponse.Body.String())
	}
	var total int64
	for _, stat := range stats {
		total += stat.Count
		if stat.HostID != nil && (stat.HostName == nil || *stat.HostName != host.Name) {
			t.Fatalf("主机统计未带名称: %#v", stat)
		}
	}
	if total != 3 {
		t.Fatalf("统计总数 = %d", total)
	}

	deleted := call(http.MethodDelete, fmt.Sprintf("/memories/%d", hostMemoryID))
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("删除状态 = %d body=%s", deleted.Code, deleted.Body.String())
	}
	missing := call(http.MethodDelete, fmt.Sprintf("/memories/%d", hostMemoryID))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("重复删除状态 = %d body=%s", missing.Code, missing.Body.String())
	}
	invalid := call(http.MethodGet, "/memories?host_id=-1")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("非法筛选状态 = %d body=%s", invalid.Code, invalid.Body.String())
	}
}
