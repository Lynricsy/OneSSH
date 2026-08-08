package mcpserver

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/store"
)

func Handler(st *store.Store, s *Server) http.Handler {
	return Bearer(st, mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.MCP }, nil))
}
