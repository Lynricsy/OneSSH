package mcpserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/memoryx"
	"onessh/internal/store"
)

type MemoryRememberInput struct {
	Host       string   `json:"host,omitempty" jsonschema:"SSH 主机名，留空写入全局记忆"`
	Content    string   `json:"content" jsonschema:"要长期保存的运维事实或经验"`
	Source     string   `json:"source,omitempty" jsonschema:"记忆来源，默认 mcp"`
	Importance *float64 `json:"importance,omitempty" jsonschema:"重要度，范围 0 到 1，默认 0.5"`
	Veracity   string   `json:"veracity,omitempty" jsonschema:"可信类型：stated、inferred、tool 或 unknown，默认 stated"`
}

type MemoryRememberOutput struct {
	ID       int64  `json:"id"`
	Bank     string `json:"bank"`
	Deduped  bool   `json:"deduped"`
	Embedded bool   `json:"embedded"`
}

type MemoryRecallInput struct {
	Host  string `json:"host,omitempty" jsonschema:"SSH 主机名，留空仅召回全局记忆"`
	Query string `json:"query" jsonschema:"要检索的问题或关键词"`
	Limit int    `json:"limit,omitempty" jsonschema:"最大结果数，默认 8，最大 50"`
}

type MemoryRecallOutput struct {
	Results []memoryx.RecallResult `json:"results"`
	Engine  string                 `json:"engine"`
}

type MemoryListInput struct {
	Host   string `json:"host,omitempty" jsonschema:"SSH 主机名，留空列出全局记忆"`
	Limit  int    `json:"limit,omitempty" jsonschema:"最大结果数，默认 50，最大 200"`
	Offset int    `json:"offset,omitempty" jsonschema:"分页偏移量，默认 0"`
}

type MemoryListOutput struct {
	Memories []store.Memory `json:"memories"`
	Bank     string         `json:"bank"`
}

type MemoryUpdateInput struct {
	ID         int64    `json:"id" jsonschema:"记忆 ID"`
	Content    *string  `json:"content,omitempty" jsonschema:"新的记忆正文"`
	Importance *float64 `json:"importance,omitempty" jsonschema:"新的重要度，范围 0 到 1"`
	Veracity   *string  `json:"veracity,omitempty" jsonschema:"新的可信类型：stated、inferred、tool 或 unknown"`
}

type MemoryUpdateOutput struct {
	ID       int64 `json:"id"`
	Embedded bool  `json:"embedded"`
}

type MemoryIDInput struct {
	ID int64 `json:"id" jsonschema:"记忆 ID"`
}

type MemoryForgetOutput struct {
	Deleted bool `json:"deleted"`
}

type MemoryStatsOutput struct {
	Total int64                  `json:"total"`
	Banks []MemoryBankStatOutput `json:"banks"`
}

type MemoryBankStatOutput struct {
	Bank        string `json:"bank"`
	Count       int64  `json:"count"`
	Embedded    int64  `json:"embedded"`
	LastWritten *int64 `json:"last_written"`
}

type MemorySleepInput struct {
	Host string `json:"host,omitempty" jsonschema:"SSH 主机名，留空维护全局记忆"`
}

func (s *Server) registerMemory() {
	register[MemoryRememberInput, MemoryRememberOutput](s, &mcp.Tool{
		Name:        "memory_remember",
		Description: "保存一条跨会话持久的运维记忆；可写入指定 SSH 主机 bank 或全局 bank，相同正文自动去重",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in MemoryRememberInput) (*mcp.CallToolResult, MemoryRememberOutput, error) {
		hostID, bank, err := memoryBank(ctx, in.Host)
		if err != nil {
			return errorResult(err.Error()), MemoryRememberOutput{}, nil
		}
		principal, _ := FromContext(ctx)
		importance := 0.5
		if in.Importance != nil {
			importance = *in.Importance
		}
		id, deduped, embedded, err := s.Memory.Remember(ctx, memoryx.RememberInput{
			HostID: hostID, Content: in.Content, Source: in.Source, Importance: importance,
			ImportanceSet: in.Importance != nil, Veracity: in.Veracity,
			TokenID: sql.NullInt64{Int64: principal.Token.ID, Valid: principal.Token.ID != 0},
		})
		if err != nil {
			return errorResult(err.Error()), MemoryRememberOutput{}, nil
		}
		return nil, MemoryRememberOutput{ID: id, Bank: bank, Deduped: deduped, Embedded: embedded}, nil
	})

	register[MemoryRecallInput, MemoryRecallOutput](s, &mcp.Tool{
		Name:        "memory_recall",
		Description: "召回持久运维记忆；混合打分结合全文检索、重要度、时近度与可选语义向量，指定主机时同时检索全局 bank",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in MemoryRecallInput) (*mcp.CallToolResult, MemoryRecallOutput, error) {
		hostID, bank, err := memoryBank(ctx, in.Host)
		if err != nil {
			return errorResult(err.Error()), MemoryRecallOutput{}, nil
		}
		results, engine, err := s.Memory.Recall(ctx, hostID, hostID.Valid, in.Query, in.Limit)
		if err != nil {
			return errorResult(err.Error()), MemoryRecallOutput{}, nil
		}
		for i := range results {
			if results[i].HostID != nil {
				results[i].Bank = bank
			}
		}
		return nil, MemoryRecallOutput{Results: results, Engine: engine}, nil
	})

	register[MemoryListInput, MemoryListOutput](s, &mcp.Tool{
		Name:        "memory_list",
		Description: "按写入时间倒序列出单个主机 bank 或全局 bank 的持久记忆",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in MemoryListInput) (*mcp.CallToolResult, MemoryListOutput, error) {
		hostID, bank, err := memoryBank(ctx, in.Host)
		if err != nil {
			return errorResult(err.Error()), MemoryListOutput{}, nil
		}
		if in.Limit == 0 {
			in.Limit = 50
		}
		if in.Limit < 1 {
			in.Limit = 1
		}
		if in.Limit > 200 {
			in.Limit = 200
		}
		if in.Offset < 0 {
			return errorResult("offset 不能为负数"), MemoryListOutput{}, nil
		}
		memories, err := s.Store.ListMemories(ctx, hostID, in.Limit, in.Offset)
		if err != nil {
			return errorResult(err.Error()), MemoryListOutput{}, nil
		}
		return nil, MemoryListOutput{Memories: memories, Bank: bank}, nil
	})

	register[MemoryUpdateInput, MemoryUpdateOutput](s, &mcp.Tool{
		Name:        "memory_update",
		Description: "更新一条有权访问的持久记忆；正文变化时会以 best-effort 方式重新生成语义向量",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in MemoryUpdateInput) (*mcp.CallToolResult, MemoryUpdateOutput, error) {
		if in.Content == nil && in.Importance == nil && in.Veracity == nil {
			return errorResult("至少提供 content、importance 或 veracity 之一"), MemoryUpdateOutput{}, nil
		}
		memory, err := s.authorizedMemory(ctx, in.ID)
		if err != nil {
			return errorResult(err.Error()), MemoryUpdateOutput{}, nil
		}
		if err = s.Memory.Update(ctx, memory, in.Content, in.Importance, in.Veracity); err != nil {
			return errorResult(err.Error()), MemoryUpdateOutput{}, nil
		}
		memory, err = s.Store.GetMemory(ctx, in.ID)
		if err != nil {
			return errorResult(err.Error()), MemoryUpdateOutput{}, nil
		}
		return nil, MemoryUpdateOutput{ID: in.ID, Embedded: len(memory.Embedding) != 0}, nil
	})

	register[MemoryIDInput, MemoryForgetOutput](s, &mcp.Tool{
		Name:        "memory_forget",
		Description: "永久删除一条有权访问的持久记忆",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in MemoryIDInput) (*mcp.CallToolResult, MemoryForgetOutput, error) {
		if _, err := s.authorizedMemory(ctx, in.ID); err != nil {
			return errorResult(err.Error()), MemoryForgetOutput{}, nil
		}
		if err := s.Store.DeleteMemory(ctx, in.ID); err != nil {
			return errorResult(err.Error()), MemoryForgetOutput{}, nil
		}
		return nil, MemoryForgetOutput{Deleted: true}, nil
	})

	register[Empty, MemoryStatsOutput](s, &mcp.Tool{
		Name:        "memory_stats",
		Description: "统计当前令牌可见的全局与主机记忆 bank，包括记忆数、向量数和最后写入时间",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, MemoryStatsOutput, error) {
		principal, ok := FromContext(ctx)
		if !ok {
			return errorResult("unauthorized"), MemoryStatsOutput{}, nil
		}
		visible := make(map[int64]string, len(principal.Hosts))
		for name, host := range principal.Hosts {
			visible[host.ID] = name
		}
		stats, err := s.Store.MemoryStats(ctx)
		if err != nil {
			return errorResult(err.Error()), MemoryStatsOutput{}, nil
		}
		out := MemoryStatsOutput{Banks: make([]MemoryBankStatOutput, 0, len(stats))}
		for _, stat := range stats {
			bank := "global"
			if stat.HostID.Valid {
				var allowed bool
				bank, allowed = visible[stat.HostID.Int64]
				if !allowed {
					continue
				}
			}
			var lastWritten *int64
			if stat.LastWritten.Valid {
				value := stat.LastWritten.Int64
				lastWritten = &value
			}
			out.Total += stat.Count
			out.Banks = append(out.Banks, MemoryBankStatOutput{
				Bank: bank, Count: stat.Count, Embedded: stat.Embedded, LastWritten: lastWritten,
			})
		}
		return nil, out, nil
	})

	register[MemorySleepInput, memoryx.SleepReport](s, &mcp.Tool{
		Name:        "memory_sleep",
		Description: "对单个记忆 bank 执行无 LLM 的确定性维护：去重、衰减长期未使用记忆并清理低分旧记忆",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in MemorySleepInput) (*mcp.CallToolResult, memoryx.SleepReport, error) {
		hostID, _, err := memoryBank(ctx, in.Host)
		if err != nil {
			return errorResult(err.Error()), memoryx.SleepReport{}, nil
		}
		report, err := s.Memory.Sleep(ctx, hostID)
		if err != nil {
			return errorResult(err.Error()), memoryx.SleepReport{}, nil
		}
		return nil, report, nil
	})
}

func memoryBank(ctx context.Context, host string) (sql.NullInt64, string, error) {
	if host == "" {
		if _, ok := FromContext(ctx); !ok {
			return sql.NullInt64{}, "", toolError("unauthorized")
		}
		return sql.NullInt64{}, "global", nil
	}
	authorized, err := AuthorizedHost(ctx, host)
	if err != nil {
		return sql.NullInt64{}, "", err
	}
	return sql.NullInt64{Int64: authorized.ID, Valid: true}, authorized.Name, nil
}

func (s *Server) authorizedMemory(ctx context.Context, id int64) (store.Memory, error) {
	memory, err := s.Store.GetMemory(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Memory{}, fmt.Errorf("memory 不存在: %d", id)
	}
	if err != nil {
		return store.Memory{}, err
	}
	principal, ok := FromContext(ctx)
	if !ok {
		return store.Memory{}, fmt.Errorf("memory 不可访问: %d", id)
	}
	if !memory.HostID.Valid {
		return memory, nil
	}
	for _, host := range principal.Hosts {
		if host.ID == memory.HostID.Int64 {
			return memory, nil
		}
	}
	return store.Memory{}, fmt.Errorf("memory 不可访问: %d", id)
}
