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
	Host       string   `json:"host,omitempty" jsonschema:"SSH 主机名，写入该主机 bank；留空写入全局 bank，只放确实跨主机通用的规则"`
	Content    string   `json:"content" jsonschema:"要长期保存的结论：简洁、自包含、脱离当前对话也能看懂；禁止写入密码、私钥和令牌"`
	Source     string   `json:"source,omitempty" jsonschema:"记忆来源标记，默认 mcp"`
	Importance *float64 `json:"importance,omitempty" jsonschema:"重要度 0 到 1，默认 0.5；影响召回排序与长期保留，长期关键约束用 0.8 以上"`
	Veracity   string   `json:"veracity,omitempty" jsonschema:"可信类型：stated 用户明确陈述、inferred 自行推断、tool 工具直接观测、unknown 存疑，默认 stated"`
}

type MemoryRememberOutput struct {
	ID       int64  `json:"id"`
	Bank     string `json:"bank"`
	Deduped  bool   `json:"deduped"`
	Embedded bool   `json:"embedded"`
}

type MemoryRecallInput struct {
	Host  string `json:"host,omitempty" jsonschema:"SSH 主机名；指定后同时召回该主机 bank 与全局 bank，留空只召回全局 bank"`
	Query string `json:"query" jsonschema:"当前要解决的具体问题或关键词，越贴近现场问题命中越准"`
	Limit int    `json:"limit,omitempty" jsonschema:"最大结果数，默认 8，上限 50"`
}

type MemoryRecallOutput struct {
	Results []memoryx.RecallResult `json:"results"`
	Engine  string                 `json:"engine"`
}

type MemoryListInput struct {
	Host   string `json:"host,omitempty" jsonschema:"SSH 主机名，留空列出全局 bank；不会合并全局与主机 bank"`
	Limit  int    `json:"limit,omitempty" jsonschema:"最大结果数，默认 50，上限 200"`
	Offset int    `json:"offset,omitempty" jsonschema:"分页偏移量，默认 0"`
}

type MemoryListOutput struct {
	Memories []store.Memory `json:"memories"`
	Bank     string         `json:"bank"`
}

type MemoryUpdateInput struct {
	ID         int64    `json:"id" jsonschema:"记忆 ID，取自 memory_recall 或 memory_list"`
	Content    *string  `json:"content,omitempty" jsonschema:"新的记忆正文，省略表示不改；正文变化会重建语义向量"`
	Importance *float64 `json:"importance,omitempty" jsonschema:"新的重要度，0 到 1，省略表示不改"`
	Veracity   *string  `json:"veracity,omitempty" jsonschema:"新的可信类型：stated、inferred、tool 或 unknown，省略表示不改"`
}

type MemoryUpdateOutput struct {
	ID       int64 `json:"id"`
	Embedded bool  `json:"embedded"`
}

type MemoryIDInput struct {
	ID int64 `json:"id" jsonschema:"记忆 ID，取自 memory_recall 或 memory_list"`
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
	Host string `json:"host,omitempty" jsonschema:"SSH 主机名，只维护该主机 bank；留空维护全局 bank"`
}

func (s *Server) registerMemory() {
	register[MemoryRememberInput, MemoryRememberOutput](s, &mcp.Tool{
		Name:  "memory_remember",
		Title: "保存长期记忆",
		Description: "把一条以后仍然有用的运维结论写进跨会话持久记忆：部署路径、服务拓扑、故障原因与修复手段、环境约束、用户明确的偏好。" +
			"写成简洁自包含的一句话，指定 host 存进该主机 bank，只有真正跨主机通用的规则才留空写全局 bank。" +
			"同一 bank 内正文完全相同会自动去重并返回 deduped=true。" +
			"不要保存密码、私钥、令牌，也不要保存一次性命令输出、未经验证的猜测和低价值重复信息。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(false), IdempotentHint: true, OpenWorldHint: new(false)},
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
		Name:  "memory_recall",
		Title: "召回长期记忆",
		Description: "按当前问题检索持久记忆。开始一台主机上的实质性工作前先召回一次，避免重复踩坑、重复摸索路径。" +
			"混合打分融合全文检索、重要度、时近度以及可选的语义向量（engine 字段标明实际使用的检索路径）；指定 host 时会同时检索该主机 bank 与全局 bank。" +
			"记忆可能已经过期，只能作为线索，结论仍须用文件、命令输出或监控数据现场验证；没召回到结果就正常调查，不要当成事实不存在。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(false)},
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
		Title:       "浏览记忆",
		Description: "按写入时间倒序分页列出某一个 bank 的记忆，用于人工核对与清理，不做相关性排序，也不会把主机 bank 和全局 bank 合并。按问题找内容请用 memory_recall。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(false)},
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
		Name:  "memory_update",
		Title: "更新记忆",
		Description: "修正一条已有记忆的正文、重要度或可信类型。事实发生变化时优先更新原记录，而不是删掉重写或再存一条相似的，避免记忆库里出现互相矛盾的版本。" +
			"正文变化会尽力重建语义向量，失败也不影响更新本身。只能改当前令牌有权访问的记忆。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(true), IdempotentHint: true, OpenWorldHint: new(false)},
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
		Title:       "删除记忆",
		Description: "永久删除一条记忆，不可撤销。仅在记录确认错误、已经失效或本就不该保存（例如误存了敏感信息）时使用；只是内容变了请用 memory_update。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(true), IdempotentHint: true, OpenWorldHint: new(false)},
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
		Title:       "记忆库统计",
		Description: "统计当前令牌可见的全局 bank 与各主机 bank：记忆条数、已生成向量数、最后写入时间。用于判断某个 bank 是否值得 memory_sleep 整理，或确认记忆是否写进了预期的 bank。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(false)},
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
		Name:  "memory_sleep",
		Title: "整理记忆库",
		Description: "对单个 bank 执行确定性维护，不调用 LLM：合并完全相同的正文、把 30 天未被使用的记忆重要度按 0.9 衰减（下限 0.05）、删除 90 天前且从未被召回、重要度不超过 0.1 的记忆，返回三项的处理条数。" +
			"属于偶尔执行的清理动作，不需要在日常任务里调用；会真实删除低价值记忆，重要内容请先用 memory_update 提高 importance。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(true), IdempotentHint: false, OpenWorldHint: new(false)},
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
