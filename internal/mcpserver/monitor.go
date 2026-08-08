package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/monitor"
)

type HostStatusInput struct {
	Host  string `json:"host"`
	Fresh bool   `json:"fresh,omitempty"`
}

func (s *Server) registerMonitor(mon *monitor.Manager) {
	register[HostStatusInput, monitor.Snapshot](s, &mcp.Tool{Name: "host_status", Description: "读取主机资源指标；fresh=true 现场采样"}, func(ctx context.Context, _ *mcp.CallToolRequest, in HostStatusInput) (*mcp.CallToolResult, monitor.Snapshot, error) {
		h, err := AuthorizedHost(ctx, in.Host)
		if err != nil {
			return errorResult(err.Error()), monitor.Snapshot{}, nil
		}
		if in.Fresh {
			out, err := mon.Fresh(ctx, h)
			if err != nil {
				return errorResult(err.Error()), out, nil
			}
			return nil, out, nil
		}
		metric, err := s.Store.LatestMetric(ctx, h.ID)
		if err != nil {
			return errorResult("暂无监控数据，可使用 fresh=true"), monitor.Snapshot{}, nil
		}
		return nil, monitor.FromMetric(metric), nil
	})
}
