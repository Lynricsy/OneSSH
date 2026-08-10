package memoryx

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"onessh/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func addTestMemory(t *testing.T, st *store.Store, memory store.Memory) int64 {
	t.Helper()
	if memory.Source == "" {
		memory.Source = "test"
	}
	if memory.Veracity == "" {
		memory.Veracity = "stated"
	}
	if memory.UpdatedAt == 0 {
		memory.UpdatedAt = memory.CreatedAt
	}
	id, err := st.AddMemory(context.Background(), memory)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRememberDeduplicatesAndKeepsMaximumImportance(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	engine := New(st, EmbeddingConfig{})
	bank := sql.NullInt64{Int64: 7, Valid: true}
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO hosts(id,name,addr,port,username,auth_type,monitor_enabled,created_at) VALUES(7,'host','127.0.0.1',22,'user','password',0,1)`); err != nil {
		t.Fatal(err)
	}
	firstID, deduped, _, err := engine.Remember(ctx, RememberInput{HostID: bank, Content: "  部署目录在 /opt/app  ", Importance: 0.4})
	if err != nil || deduped {
		t.Fatalf("首次写入异常: id=%d deduped=%v err=%v", firstID, deduped, err)
	}
	secondID, deduped, _, err := engine.Remember(ctx, RememberInput{HostID: bank, Content: "部署目录在 /opt/app", Importance: 0.9})
	if err != nil || !deduped || secondID != firstID {
		t.Fatalf("去重异常: first=%d second=%d deduped=%v err=%v", firstID, secondID, deduped, err)
	}
	memory, err := st.GetMemory(ctx, firstID)
	if err != nil {
		t.Fatal(err)
	}
	if memory.Importance != 0.9 {
		t.Fatalf("重要度未保留最大值: %v", memory.Importance)
	}
}

func TestRecallScoresImportanceAndRecency(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	engine := New(st, EmbeddingConfig{})
	now := time.Now()
	oldID := addTestMemory(t, st, store.Memory{Content: "nginx 配置说明 old", Importance: 1, CreatedAt: now.Add(-30 * 24 * time.Hour).Unix()})
	newID := addTestMemory(t, st, store.Memory{Content: "nginx 配置说明 new", Importance: 0.05, CreatedAt: now.Unix()})
	results, mode, err := engine.Recall(ctx, sql.NullInt64{}, false, "nginx 配置说明", 8)
	if err != nil {
		t.Fatal(err)
	}
	if mode != "fts" || len(results) != 2 {
		t.Fatalf("召回结果异常: mode=%s results=%#v", mode, results)
	}
	if results[0].ID != oldID || results[1].ID != newID || results[0].Score <= results[1].Score {
		t.Fatalf("打分未体现重要度与时近度公式: %#v", results)
	}
}

func TestRecallMergesHostAndGlobalWithoutCrossingBanks(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	engine := New(st, EmbeddingConfig{})
	for id, name := range map[int64]string{1: "allowed", 2: "denied"} {
		if _, err := st.DB.ExecContext(ctx, `INSERT INTO hosts(id,name,addr,port,username,auth_type,monitor_enabled,created_at) VALUES(?,?,?,22,'user','password',0,1)`, id, name, "127.0.0.1"); err != nil {
			t.Fatal(err)
		}
	}
	allowed := sql.NullInt64{Int64: 1, Valid: true}
	denied := sql.NullInt64{Int64: 2, Valid: true}
	addTestMemory(t, st, store.Memory{HostID: allowed, Content: "redis 配置目录 allowed", Importance: 0.5, CreatedAt: time.Now().Unix()})
	addTestMemory(t, st, store.Memory{Content: "redis 配置目录 global", Importance: 0.5, CreatedAt: time.Now().Unix()})
	addTestMemory(t, st, store.Memory{HostID: denied, Content: "redis 配置目录 denied", Importance: 0.5, CreatedAt: time.Now().Unix()})
	results, _, err := engine.Recall(ctx, allowed, true, "redis 配置目录", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("host+global 召回越界或缺失: %#v", results)
	}
	for _, result := range results {
		if result.HostID != nil && *result.HostID != allowed.Int64 {
			t.Fatalf("召回越过目标 bank: %#v", result)
		}
	}
}

func TestRecallShortQueryFallsBackToLike(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	engine := New(st, EmbeddingConfig{})
	id := addTestMemory(t, st, store.Memory{Content: "数据库位于 db01", Importance: 0.5, CreatedAt: time.Now().Unix()})
	results, mode, err := engine.Recall(ctx, sql.NullInt64{}, false, "数据", 8)
	if err != nil {
		t.Fatal(err)
	}
	if mode != "like" || len(results) != 1 || results[0].ID != id {
		t.Fatalf("短查询未走 LIKE: mode=%s results=%#v", mode, results)
	}
}

func TestSleepRunsDeterministicMaintenance(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	engine := New(st, EmbeddingConfig{})
	now := time.Now()
	addTestMemory(t, st, store.Memory{Content: "重复内容", Importance: 0.9, RecallCount: 2, CreatedAt: now.Add(-31 * 24 * time.Hour).Unix()})
	addTestMemory(t, st, store.Memory{Content: "重复内容", Importance: 0.8, RecallCount: 3, CreatedAt: now.Add(-32 * 24 * time.Hour).Unix()})
	addTestMemory(t, st, store.Memory{Content: "普通旧记忆", Importance: 0.5, CreatedAt: now.Add(-40 * 24 * time.Hour).Unix()})
	prunedID := addTestMemory(t, st, store.Memory{Content: "应清理记忆", Importance: 0.1, CreatedAt: now.Add(-100 * 24 * time.Hour).Unix()})
	addTestMemory(t, st, store.Memory{Content: "近期记忆", Importance: 0.5, CreatedAt: now.Unix()})
	report, err := engine.Sleep(ctx, sql.NullInt64{})
	if err != nil {
		t.Fatal(err)
	}
	if report != (SleepReport{Deduped: 1, Decayed: 3, Pruned: 1}) {
		t.Fatalf("维护统计异常: %#v", report)
	}
	if _, err := st.GetMemory(ctx, prunedID); err != sql.ErrNoRows {
		t.Fatalf("低分旧记忆未清理: %v", err)
	}
	duplicate, err := st.FindMemoryByContent(ctx, sql.NullInt64{}, "重复内容")
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.RecallCount != 5 || math.Abs(duplicate.Importance-0.81) > 1e-9 {
		t.Fatalf("去重汇总或衰减异常: %#v", duplicate)
	}
}

func TestEmbeddingHybridRecallAndFailureFallback(t *testing.T) {
	ctx := context.Background()
	failed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failed {
			http.Error(w, "unavailable", http.StatusInternalServerError)
			return
		}
		if r.URL.Path != "/embeddings" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("embedding 请求异常: path=%s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var request struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		vector := []float32{0, 1}
		if len(request.Input) > 0 && (request.Input[0] == "beta database" || request.Input[0] == "semantic target") {
			vector = []float32{1, 0}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"embedding": vector}}})
	}))
	defer server.Close()

	st := newTestStore(t)
	engine := New(st, EmbeddingConfig{APIURL: server.URL + "/", APIKey: "secret", Model: "test-embedding"})
	alphaID, _, embedded, err := engine.Remember(ctx, RememberInput{Content: "alpha deployment", Importance: 0.5})
	if err != nil || !embedded {
		t.Fatalf("alpha embedding 写入失败: embedded=%v err=%v", embedded, err)
	}
	betaID, _, embedded, err := engine.Remember(ctx, RememberInput{Content: "beta database", Importance: 0.5})
	if err != nil || !embedded {
		t.Fatalf("beta embedding 写入失败: embedded=%v err=%v", embedded, err)
	}
	results, mode, err := engine.Recall(ctx, sql.NullInt64{}, false, "semantic target", 8)
	if err != nil {
		t.Fatal(err)
	}
	if mode != "hybrid" || len(results) != 2 || results[0].ID != betaID || results[0].DenseScore != 1 {
		t.Fatalf("混合召回未按余弦排序: alpha=%d beta=%d mode=%s results=%#v", alphaID, betaID, mode, results)
	}
	failed = true
	fallbackID, _, embedded, err := engine.Remember(ctx, RememberInput{Content: "fallback deployment path", Importance: 0.5})
	if err != nil || embedded {
		t.Fatalf("端点失败不应中断写入: id=%d embedded=%v err=%v", fallbackID, embedded, err)
	}
	results, mode, err = engine.Recall(ctx, sql.NullInt64{}, false, "fallback deployment", 8)
	if err != nil {
		t.Fatal(err)
	}
	if mode != "fts" || len(results) == 0 || results[0].ID != fallbackID {
		t.Fatalf("端点失败未退化到 FTS: mode=%s results=%#v", mode, results)
	}
}

func TestVectorEncodingRoundTripAndRejectsMalformedBytes(t *testing.T) {
	input := []float32{1.25, -2.5, 0}
	decoded, err := decodeVector(encodeVector(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != len(input) {
		t.Fatalf("向量长度异常: %#v", decoded)
	}
	for i := range input {
		if decoded[i] != input[i] {
			t.Fatalf("向量值异常: input=%#v decoded=%#v", input, decoded)
		}
	}
	if _, err = decodeVector([]byte{1, 2, 3}); err == nil {
		t.Fatal("畸形向量未被拒绝")
	}
}
