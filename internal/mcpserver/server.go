package mcpserver

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/events"
	"onessh/internal/execx"
	"onessh/internal/files"
	"onessh/internal/hostmanager"
	"onessh/internal/jobs"
	"onessh/internal/monitor"
	"onessh/internal/searchx"
	"onessh/internal/sshpool"
	"onessh/internal/store"
)

type Server struct {
	MCP         *mcp.Server
	Store       *store.Store
	Pool        *sshpool.Pool
	Events      *events.Bus
	HostManager *hostmanager.Manager
	Exec        *execx.Runner
	Files       *files.Manager
	Jobs        *jobs.Manager
	Monitor     *monitor.Manager
}

func New(st *store.Store, pool *sshpool.Pool, bus *events.Bus, hosts *hostmanager.Manager, dataDir string, pollInterval time.Duration) *Server {
	s := &Server{Store: st, Pool: pool, Events: bus, HostManager: hosts, Exec: execx.New(dataDir)}
	s.Jobs = jobs.New(st, pool, s.Exec, bus)
	s.Files = files.New(pool, s.Exec)
	s.Monitor = monitor.New(st, pool, s.Exec, pollInterval)
	s.MCP = mcp.NewServer(&mcp.Implementation{Name: "OneSSH", Version: "dev"}, nil)
	s.registerHosts()
	s.registerExec(s.Exec)
	s.registerJobs(s.Jobs)
	s.registerFiles(s.Files)
	s.registerSearch(searchx.New(pool))
	s.registerImage(s.Files)
	s.registerMonitor(s.Monitor)
	s.registerFanout()
	s.Monitor.Start(context.Background())
	return s
}
func (s *Server) Close() {
	s.Monitor.Stop()
	s.Files.Clients.Close()
}

type Empty struct{}

func errorResult(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: message}}, IsError: true}
}
func register[In, Out any](s *Server, tool *mcp.Tool, handler mcp.ToolHandlerFor[In, Out]) {
	mcp.AddTool(s.MCP, tool, func(ctx context.Context, req *mcp.CallToolRequest, in In) (result *mcp.CallToolResult, out Out, err error) {
		started := time.Now()
		result, out, err = handler(ctx, req, in)
		ok := err == nil && (result == nil || !result.IsError)
		params := redactedJSON(in)
		host := hostOf(in)
		p, _ := FromContext(ctx)
		a := store.Audit{Ts: store.NowAudit(), Tool: tool.Name, ParamsJSON: params, OK: ok, DurationMS: time.Since(started).Milliseconds()}
		if p.Token.ID != 0 {
			a.TokenID = sql.NullInt64{Int64: p.Token.ID, Valid: true}
		}
		if host != "" {
			a.Host = sql.NullString{String: host, Valid: true}
		}
		if raw, e := json.Marshal(out); e == nil {
			a.BytesOut = int64(len(raw))
		}
		_ = s.Store.AddAudit(context.Background(), a)
		s.Events.Publish("tool_call", map[string]any{"tool": tool.Name, "host": host, "ok": ok, "duration_ms": a.DurationMS})
		return
	})
}
func redactedJSON(v any) string {
	var raw any
	b, _ := json.Marshal(v)
	_ = json.Unmarshal(b, &raw)
	redact(raw)
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(raw)
	return string(bytes.TrimSuffix(out.Bytes(), []byte("\n")))
}
func redact(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			switch k {
			case "password":
				x[k] = "<redacted>"
			case "content", "edits":
				b, _ := json.Marshal(val)
				x[k] = fmt.Sprintf("<len=%d>", len(b))
			default:
				redact(val)
			}
		}
	case []any:
		for _, v := range x {
			redact(v)
		}
	}
}
func hostOf(v any) string {
	rv := reflect.Indirect(reflect.ValueOf(v))
	if rv.IsValid() && rv.Kind() == reflect.Struct {
		for _, name := range []string{"Host", "Name"} {
			f := rv.FieldByName(name)
			if f.IsValid() && f.Kind() == reflect.String && f.String() != "" {
				return f.String()
			}
		}
	}
	return ""
}
