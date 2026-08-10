package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/searchx"
)

type GrepInput struct {
	Host       string `json:"host" jsonschema:"SSH 主机名，取自 hosts_list"`
	Pattern    string `json:"pattern" jsonschema:"搜索模式，默认按正则解释；含特殊字符的字面串请配合 literal=true"`
	Path       string `json:"path,omitempty" jsonschema:"远程文件或目录，默认登录用户家目录；尽量收窄以提速"`
	Glob       string `json:"glob,omitempty" jsonschema:"只搜索匹配该 glob 的文件，例如 *.go 或 **/*.test.ts"`
	IgnoreCase bool   `json:"ignoreCase,omitempty" jsonschema:"忽略大小写"`
	Literal    bool   `json:"literal,omitempty" jsonschema:"把 pattern 当作字面字符串而不是正则"`
	Context    int    `json:"context,omitempty" jsonschema:"每个匹配前后附带的上下文行数，最大 20；结果里 match=false 的行即上下文"`
	Limit      int    `json:"limit,omitempty" jsonschema:"最大匹配行数，默认 100，上限 2000；命中上限时 truncated=true"`
}

type FindInput struct {
	Host    string `json:"host" jsonschema:"SSH 主机名，取自 hosts_list"`
	Pattern string `json:"pattern" jsonschema:"路径 glob，例如 *.go、**/*.json 或 src/**/*.spec.ts；不含 / 时按文件名匹配"`
	Path    string `json:"path,omitempty" jsonschema:"远程搜索起始目录，默认登录用户家目录"`
	Limit   int    `json:"limit,omitempty" jsonschema:"最大结果数，默认 1000，上限 5000"`
}

func (s *Server) registerSearch(search *searchx.Manager) {
	register[GrepInput, searchx.GrepResult](s, &mcp.Tool{
		Name:  "grep",
		Title: "搜索远程文件内容",
		Description: "在远程主机上按内容搜索文件，返回结构化的路径、行号、列号、匹配行与上下文行，以及是否被 limit 截断。" +
			"这是排查日志和定位配置的首选，比在 exec 里拼 grep 更省心：引号转义、超时、输出上限都已处理。" +
			"远端有 ripgrep 时走原生路径，否则自动降级为网关侧 SFTP 遍历（engine 字段标明实际路径，降级时带 warning，大目录会更慢）。" +
			"两条路径都会跳过二进制、符号链接和超大文件，并遵守目录内的 .gitignore/.ignore/.rgignore。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GrepInput) (*mcp.CallToolResult, searchx.GrepResult, error) {
		if _, err := AuthorizedHost(ctx, in.Host); err != nil {
			return errorResult(err.Error()), searchx.GrepResult{}, nil
		}
		out, err := search.Grep(ctx, in.Host, searchx.GrepOptions{
			Pattern: in.Pattern, Path: in.Path, Glob: in.Glob, IgnoreCase: in.IgnoreCase,
			Literal: in.Literal, Context: in.Context, Limit: in.Limit,
		})
		if err != nil {
			return errorResult(err.Error()), searchx.GrepResult{}, nil
		}
		return nil, out, nil
	})
	register[FindInput, searchx.FindResult](s, &mcp.Tool{
		Name:  "find",
		Title: "查找远程路径",
		Description: "在远程主机上按 glob 查找文件和目录路径，只返回路径不读内容，适合先定位再用 file_read 或 grep 精查。" +
			"远端有 fd/fdfind 时走原生路径，否则自动降级为网关侧 SFTP 遍历（engine 字段标明实际路径）。要列单层目录用 file_list 更直接。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in FindInput) (*mcp.CallToolResult, searchx.FindResult, error) {
		if _, err := AuthorizedHost(ctx, in.Host); err != nil {
			return errorResult(err.Error()), searchx.FindResult{}, nil
		}
		out, err := search.Find(ctx, in.Host, searchx.FindOptions{Pattern: in.Pattern, Path: in.Path, Limit: in.Limit})
		if err != nil {
			return errorResult(err.Error()), searchx.FindResult{}, nil
		}
		return nil, out, nil
	})
}
