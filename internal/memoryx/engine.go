package memoryx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"onessh/internal/store"
)

const (
	defaultImportance  = 0.5
	defaultRecallLimit = 8
	maxRecallLimit     = 50
	recencyHalfLife    = 72.0
)

type Engine struct {
	store    *store.Store
	embedder *Embedder
}

func New(st *store.Store, cfg EmbeddingConfig) *Engine {
	var embedder *Embedder
	if cfg.Enabled() {
		embedder = newEmbedder(cfg)
	}
	return &Engine{store: st, embedder: embedder}
}

type RememberInput struct {
	HostID     sql.NullInt64
	Content    string
	Source     string
	Veracity   string
	Importance float64
	TokenID    sql.NullInt64
}

type RecallResult struct {
	ID          int64   `json:"id"`
	Bank        string  `json:"bank"`
	HostID      *int64  `json:"host_id"`
	Content     string  `json:"content"`
	Source      string  `json:"source"`
	Importance  float64 `json:"importance"`
	Veracity    string  `json:"veracity"`
	Score       float64 `json:"score"`
	FTSScore    float64 `json:"fts_score"`
	DenseScore  float64 `json:"dense_score"`
	CreatedAt   int64   `json:"created_at"`
	RecallCount int64   `json:"recall_count"`
}

type SleepReport struct {
	Deduped int64 `json:"deduped"`
	Decayed int64 `json:"decayed"`
	Pruned  int64 `json:"pruned"`
}

func (e *Engine) Remember(ctx context.Context, in RememberInput) (id int64, deduped bool, embedded bool, err error) {
	content := strings.TrimSpace(in.Content)
	if content == "" {
		return 0, false, false, fmt.Errorf("content 不能为空")
	}
	importance := in.Importance
	if importance == 0 {
		importance = defaultImportance
	}
	importance = clamp(importance, 0, 1)
	veracity := in.Veracity
	if veracity == "" {
		veracity = "stated"
	}
	if !validVeracity(veracity) {
		return 0, false, false, fmt.Errorf("veracity 无效: %s", veracity)
	}
	source := in.Source
	if source == "" {
		source = "mcp"
	}
	existing, findErr := e.store.FindMemoryByContent(ctx, in.HostID, content)
	if findErr == nil {
		if importance > existing.Importance {
			existing.Importance = importance
		}
		existing.UpdatedAt = time.Now().Unix()
		if err := e.store.UpdateMemory(ctx, existing); err != nil {
			return 0, false, false, err
		}
		return existing.ID, true, len(existing.Embedding) != 0, nil
	}
	if !errors.Is(findErr, sql.ErrNoRows) {
		return 0, false, false, findErr
	}
	now := time.Now().Unix()
	memory := store.Memory{
		HostID: in.HostID, Content: content, Source: source, Importance: importance, Veracity: veracity,
		TokenID: in.TokenID, CreatedAt: now, UpdatedAt: now,
	}
	if e.embedder != nil {
		vector, embedErr := e.embedder.Embed(ctx, content)
		if embedErr != nil {
			log.Printf("memory embedding 写入失败，已退化为纯 FTS: %v", embedErr)
		} else {
			memory.Embedding = encodeVector(vector)
			memory.EmbeddingModel = sql.NullString{String: e.embedder.cfg.Model, Valid: true}
			embedded = true
		}
	}
	id, err = e.store.AddMemory(ctx, memory)
	return id, false, embedded, err
}

func (e *Engine) Update(ctx context.Context, memory store.Memory, newContent *string, newImportance *float64, newVeracity *string) error {
	contentChanged := false
	if newContent != nil {
		content := strings.TrimSpace(*newContent)
		if content == "" {
			return fmt.Errorf("content 不能为空")
		}
		if content != memory.Content {
			memory.Content = content
			contentChanged = true
		}
	}
	if newImportance != nil {
		memory.Importance = clamp(*newImportance, 0, 1)
	}
	if newVeracity != nil {
		if !validVeracity(*newVeracity) {
			return fmt.Errorf("veracity 无效: %s", *newVeracity)
		}
		memory.Veracity = *newVeracity
	}
	if contentChanged {
		memory.Embedding = nil
		memory.EmbeddingModel = sql.NullString{}
		if e.embedder != nil {
			vector, err := e.embedder.Embed(ctx, memory.Content)
			if err != nil {
				log.Printf("memory embedding 更新失败，已退化为纯 FTS: %v", err)
			} else {
				memory.Embedding = encodeVector(vector)
				memory.EmbeddingModel = sql.NullString{String: e.embedder.cfg.Model, Valid: true}
			}
		}
	}
	memory.UpdatedAt = time.Now().Unix()
	return e.store.UpdateMemory(ctx, memory)
}

func (e *Engine) Recall(ctx context.Context, hostID sql.NullInt64, includeGlobal bool, query string, limit int) ([]RecallResult, string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, "", fmt.Errorf("query 不能为空")
	}
	if limit == 0 {
		limit = defaultRecallLimit
	}
	limit = int(clamp(float64(limit), 1, maxRecallLimit))
	candidateLimit := max(limit*3, 50)
	match := ftsMatch(query)
	if match == "" {
		memories, err := e.store.SearchMemoriesLike(ctx, hostID, includeGlobal, query, candidateLimit)
		if err != nil {
			return nil, "like", err
		}
		candidates := make(map[int64]*recallCandidate, len(memories))
		for _, memory := range memories {
			copy := memory
			candidates[memory.ID] = &recallCandidate{memory: copy, fts: 0.6}
		}
		return e.finishRecall(ctx, candidates, limit, false, "like")
	}

	ftsRows, err := e.store.SearchMemoriesFTS(ctx, hostID, includeGlobal, match, candidateLimit)
	if err != nil {
		return nil, "fts", err
	}
	candidates := make(map[int64]*recallCandidate, len(ftsRows))
	normalizedFTS := normalizeBM25(ftsRows)
	for i := range ftsRows {
		memory := ftsRows[i].Memory
		candidates[memory.ID] = &recallCandidate{memory: memory, fts: normalizedFTS[i]}
	}

	hybrid := false
	if e.embedder != nil {
		queryVector, embedErr := e.embedder.Embed(ctx, query)
		if embedErr != nil {
			log.Printf("memory embedding 召回失败，已退化为纯 FTS: %v", embedErr)
		} else {
			hybrid = true
			vectors, err := e.store.ListMemoryVectors(ctx, hostID, includeGlobal, e.embedder.cfg.Model)
			if err != nil {
				return nil, "hybrid", err
			}
			dense := make([]denseCandidate, 0, len(vectors))
			for _, vector := range vectors {
				decoded, err := decodeVector(vector.Embedding)
				if err != nil {
					log.Printf("memory %d 的 embedding 无效，已跳过: %v", vector.ID, err)
					continue
				}
				dense = append(dense, denseCandidate{id: vector.ID, score: clamp(cosineSimilarity(queryVector, decoded), 0, 1)})
			}
			sort.Slice(dense, func(i, j int) bool {
				if dense[i].score == dense[j].score {
					return dense[i].id < dense[j].id
				}
				return dense[i].score > dense[j].score
			})
			if len(dense) > candidateLimit {
				dense = dense[:candidateLimit]
			}
			for _, item := range dense {
				candidate := candidates[item.id]
				if candidate == nil {
					memory, err := e.store.GetMemory(ctx, item.id)
					if err != nil {
						if errors.Is(err, sql.ErrNoRows) {
							continue
						}
						return nil, "hybrid", err
					}
					candidate = &recallCandidate{memory: memory}
					candidates[item.id] = candidate
				}
				candidate.dense = item.score
			}
		}
	}
	engine := "fts"
	if hybrid {
		engine = "hybrid"
	}
	return e.finishRecall(ctx, candidates, limit, hybrid, engine)
}

type recallCandidate struct {
	memory store.Memory
	fts    float64
	dense  float64
}

type denseCandidate struct {
	id    int64
	score float64
}

func (e *Engine) finishRecall(ctx context.Context, candidates map[int64]*recallCandidate, limit int, hybrid bool, engine string) ([]RecallResult, string, error) {
	wVec := 0.0
	if hybrid {
		wVec = 0.5
	}
	wFTS, wImportance := 0.3, 0.2
	weightTotal := wVec + wFTS + wImportance
	now := time.Now()
	results := make([]RecallResult, 0, len(candidates))
	for _, candidate := range candidates {
		memory := candidate.memory
		base := (candidate.dense*wVec + candidate.fts*wFTS + clamp(memory.Importance, 0, 1)*wImportance) / weightTotal
		ageHours := now.Sub(time.Unix(memory.CreatedAt, 0)).Hours()
		if ageHours < 0 {
			ageHours = 0
		}
		recency := math.Exp(-math.Ln2 * ageHours / recencyHalfLife)
		score := base * (0.7 + 0.3*recency) * veracityWeight(memory.Veracity)
		var hostID *int64
		bank := "global"
		if memory.HostID.Valid {
			value := memory.HostID.Int64
			hostID = &value
			bank = ""
		}
		results = append(results, RecallResult{
			ID: memory.ID, Bank: bank, HostID: hostID, Content: memory.Content, Source: memory.Source,
			Importance: memory.Importance, Veracity: memory.Veracity, Score: round4(score),
			FTSScore: round4(candidate.fts), DenseScore: round4(candidate.dense), CreatedAt: memory.CreatedAt,
			RecallCount: memory.RecallCount + 1,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if results[i].CreatedAt != results[j].CreatedAt {
			return results[i].CreatedAt > results[j].CreatedAt
		}
		return results[i].ID < results[j].ID
	})
	if len(results) > limit {
		results = results[:limit]
	}
	ids := make([]int64, len(results))
	for i := range results {
		ids[i] = results[i].ID
	}
	if err := e.store.TouchMemoryRecalls(ctx, ids, now.Unix()); err != nil {
		return nil, engine, err
	}
	return results, engine, nil
}

// Sleep 对单个 bank 依次执行确定性维护：完全内容去重、30 天未使用记忆按 0.9 衰减（最低 0.05），
// 再清理 90 天前且从未召回、重要度不高于 0.1 的记忆。该流程不调用 LLM。
func (e *Engine) Sleep(ctx context.Context, hostID sql.NullInt64) (SleepReport, error) {
	var report SleepReport
	var err error
	report.Deduped, err = e.store.DedupeMemories(ctx, hostID)
	if err != nil {
		return report, err
	}
	now := time.Now()
	report.Decayed, err = e.store.DecayMemories(ctx, hostID, now.Add(-30*24*time.Hour).Unix())
	if err != nil {
		return report, err
	}
	report.Pruned, err = e.store.PruneMemories(ctx, hostID, now.Add(-90*24*time.Hour).Unix())
	return report, err
}

func ftsMatch(query string) string {
	tokens := strings.Fields(query)
	phrases := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if utf8.RuneCountInString(token) < 3 {
			continue
		}
		phrases = append(phrases, `"`+strings.ReplaceAll(token, `"`, `""`)+`"`)
	}
	return strings.Join(phrases, " OR ")
}

func normalizeBM25(rows []store.MemoryWithRank) []float64 {
	if len(rows) == 0 {
		return nil
	}
	minRank, maxRank := rows[0].Rank, rows[0].Rank
	for _, row := range rows[1:] {
		minRank = math.Min(minRank, row.Rank)
		maxRank = math.Max(maxRank, row.Rank)
	}
	scores := make([]float64, len(rows))
	if minRank == maxRank {
		for i := range scores {
			scores[i] = 1
		}
		return scores
	}
	for i, row := range rows {
		scores[i] = clamp((maxRank-row.Rank)/(maxRank-minRank), 0, 1)
	}
	return scores
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		left, right := float64(a[i]), float64(b[i])
		dot += left * right
		normA += left * left
		normB += right * right
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func validVeracity(value string) bool {
	switch value {
	case "stated", "inferred", "tool", "unknown":
		return true
	default:
		return false
	}
}

func veracityWeight(value string) float64 {
	switch value {
	case "stated":
		return 1
	case "inferred":
		return 0.7
	case "tool":
		return 0.5
	default:
		return 0.8
	}
}

func clamp(value, low, high float64) float64 {
	return math.Min(high, math.Max(low, value))
}

func round4(value float64) float64 {
	return math.Round(value*10000) / 10000
}
