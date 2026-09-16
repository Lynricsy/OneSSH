package mcpserver

import (
	"context"
	"database/sql"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/store"
	"onessh/internal/toolgroups"
)

// effectiveDisabled 合并进程级 ONESSH_DISABLED_TOOLS 与当前 Bearer 令牌的 denylist。
// 令牌只能再收紧，不能重新打开实例已关闭的组。
// 令牌 denylist 解析失败时 fail-closed：禁用全部 MCP 工具组，绝不静默放开。
func (s *Server) effectiveDisabled(ctx context.Context) toolgroups.Disabled {
	tokenDisabled := toolgroups.Disabled(nil)
	if p, ok := FromContext(ctx); ok {
		parsed, err := toolgroups.ParseList(p.Token.DisabledTools)
		if err != nil {
			tokenDisabled = toolgroups.AllDisabled()
		} else {
			tokenDisabled = parsed
		}
	}
	return toolgroups.Union(s.disabledTools, tokenDisabled)
}

// installTokenDenylist 在已注册的工具之上按请求过滤：tools/list 与 resources/list 隐藏，
// tools/call 与 resources/read 拒绝，initialize 的 instructions 与暴露面保持一致。
func (s *Server) installTokenDenylist() {
	s.MCP.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			disabled := s.effectiveDisabled(ctx)
			if len(disabled) == 0 {
				return next(ctx, method, req)
			}
			switch method {
			case "tools/call":
				name := callToolName(req)
				// 解析不出工具名时 fail-closed：有 denylist 生效就不能放行空名绕过。
				if name == "" {
					s.auditDeniedTool(ctx, "(unparseable)")
					return errorResult("tool not authorized"), nil
				}
				if disabled.HidesTool(name) {
					s.auditDeniedTool(ctx, name)
					return errorResult("tool not authorized: " + name), nil
				}
			case "resources/read":
				if uri := readResourceURI(req); uri != "" {
					if tool, ok := s.apps.toolNameForURI(uri); ok && disabled.HidesTool(tool) {
						s.auditDeniedTool(ctx, tool)
						return nil, mcp.ResourceNotFoundError(uri)
					}
				}
			}
			result, err := next(ctx, method, req)
			if err != nil {
				return result, err
			}
			switch method {
			case "initialize":
				if initResult, ok := result.(*mcp.InitializeResult); ok && initResult != nil {
					initResult.Instructions = serverInstructions(disabled)
				}
			case "server/discover":
				// SEP-2575：现代客户端用 discover 代替 initialize 拿 instructions。
				if discovered, ok := result.(*mcp.DiscoverResult); ok && discovered != nil {
					discovered.Instructions = serverInstructions(disabled)
				}
			case "tools/list":
				if listed, ok := result.(*mcp.ListToolsResult); ok && listed != nil {
					listed.Tools = filterTools(listed.Tools, disabled)
				}
			case "resources/list":
				if listed, ok := result.(*mcp.ListResourcesResult); ok && listed != nil {
					listed.Resources = filterResources(listed.Resources, disabled, s.apps)
				}
			case "resources/templates/list":
				if listed, ok := result.(*mcp.ListResourceTemplatesResult); ok && listed != nil {
					listed.ResourceTemplates = filterTemplates(listed.ResourceTemplates, disabled, s.apps)
				}
			}
			return result, nil
		}
	})
}

func callToolName(req mcp.Request) string {
	switch p := req.GetParams().(type) {
	case *mcp.CallToolParamsRaw:
		if p != nil {
			return p.Name
		}
	case *mcp.CallToolParams:
		if p != nil {
			return p.Name
		}
	}
	return ""
}

func readResourceURI(req mcp.Request) string {
	if p, ok := req.GetParams().(*mcp.ReadResourceParams); ok && p != nil {
		return p.URI
	}
	return ""
}

func filterTools(tools []*mcp.Tool, disabled toolgroups.Disabled) []*mcp.Tool {
	if len(tools) == 0 {
		return tools
	}
	out := make([]*mcp.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool == nil || disabled.HidesTool(tool.Name) {
			continue
		}
		out = append(out, tool)
	}
	return out
}

func filterResources(resources []*mcp.Resource, disabled toolgroups.Disabled, apps *appCatalog) []*mcp.Resource {
	if len(resources) == 0 {
		return resources
	}
	out := make([]*mcp.Resource, 0, len(resources))
	for _, resource := range resources {
		if resource == nil {
			continue
		}
		tool, ok := apps.toolNameForURI(resource.URI)
		if !ok {
			tool = strings.TrimPrefix(resource.Name, "onessh-app-")
		}
		if tool != "" && disabled.HidesTool(tool) {
			continue
		}
		out = append(out, resource)
	}
	return out
}

// filterTemplates 在无任何仍允许的 MCP App 卡片时隐藏 onessh-app-legacy，
// 避免受限令牌拿到一张只能解析到被拒绝卡片的模板表面。
func filterTemplates(templates []*mcp.ResourceTemplate, disabled toolgroups.Disabled, apps *appCatalog) []*mcp.ResourceTemplate {
	if len(templates) == 0 {
		return templates
	}
	out := make([]*mcp.ResourceTemplate, 0, len(templates))
	for _, template := range templates {
		if template == nil {
			continue
		}
		if template.Name == "onessh-app-legacy" && !apps.hasPermittedApp(disabled) {
			continue
		}
		out = append(out, template)
	}
	return out
}

func (s *Server) auditDeniedTool(ctx context.Context, toolName string) {
	if s.Store == nil {
		return
	}
	a := store.Audit{
		Ts:         store.NowAudit(),
		Tool:       toolName,
		ParamsJSON: "{}",
		OK:         false,
	}
	if p, ok := FromContext(ctx); ok && p.Token.ID != 0 {
		a.TokenID = sql.NullInt64{Int64: p.Token.ID, Valid: true}
		a.TokenName = sql.NullString{String: p.Token.Name, Valid: true}
	}
	_ = s.Store.AddAudit(context.Background(), a)
	if s.Events != nil {
		s.Events.Publish("tool_call", map[string]any{
			"tool": toolName, "ok": false, "duration_ms": int64(0), "denied": true,
		})
	}
}
