package mcpserver

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/store"
)

func Handler(st *store.Store, s *Server, resolve ResourceResolver) http.Handler {
	return Bearer(st, resolve, mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return s.MCP },
		&mcp.StreamableHTTPOptions{
			Stateless:                    true,
			PropagateRequestCancellation: true,
		},
	))
}
