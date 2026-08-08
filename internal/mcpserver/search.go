package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/searchx"
)

type GrepInput struct {
	Host       string `json:"host" jsonschema:"SSH 主机名"`
	Pattern    string `json:"pattern" jsonschema:"正则表达式或字面字符串"`
	Path       string `json:"path,omitempty" jsonschema:"远程文件或目录，默认当前用户家目录"`
	Glob       string `json:"glob,omitempty" jsonschema:"文件 glob 过滤，例如 *.go 或 **/*.test.ts"`
	IgnoreCase bool   `json:"ignoreCase,omitempty" jsonschema:"忽略大小写"`
	Literal    bool   `json:"literal,omitempty" jsonschema:"将 pattern 视为字面字符串"`
	Context    int    `json:"context,omitempty" jsonschema:"每个匹配前后的上下文行数，最大 20"`
	Limit      int    `json:"limit,omitempty" jsonschema:"最大匹配行数，默认 100，最大 2000"`
}

type FindInput struct {
	Host    string `json:"host" jsonschema:"SSH 主机名"`
	Pattern string `json:"pattern" jsonschema:"文件 glob，例如 *.go、**/*.json 或 src/**/*.spec.ts"`
	Path    string `json:"path,omitempty" jsonschema:"远程搜索目录，默认当前用户家目录"`
	Limit   int    `json:"limit,omitempty" jsonschema:"最大结果数，默认 1000，最大 5000"`
}

func (s *Server) registerSearch(search *searchx.Manager) {
	register[GrepInput, searchx.GrepResult](s, &mcp.Tool{
		Name:        "grep",
		Description: "使用远程 ripgrep 搜索文件内容，返回路径、行号、列号和上下文；遵循 .gitignore",
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
		Name:        "find",
		Description: "使用远程 fd 按 glob 查找路径；遵循 .gitignore",
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
