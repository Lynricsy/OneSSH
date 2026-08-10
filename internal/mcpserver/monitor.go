package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/monitor"
)

type HostStatusInput struct {
	Host  string `json:"host" jsonschema:"SSH 主机名，取自 hosts_list"`
	Fresh bool   `json:"fresh,omitempty" jsonschema:"true 表示立即登录主机现场采样；默认读取后台轮询的最近一条记录"`
}

func (s *Server) registerMonitor(mon *monitor.Manager) {
	register[HostStatusInput, monitor.Snapshot](s, &mcp.Tool{
		Name:  "host_status",
		Title: "读取主机资源指标",
		Description: "读取主机的 CPU 使用率、内存用量、1 分钟负载和各挂载点磁盘用量快照。" +
			"默认返回后台轮询存下的最近一条采样，很快但可能有分钟级延迟；需要当前真实状态时传 fresh=true 现场采集。" +
			"主机未开启监控或还没采过样时会提示改用 fresh=true。只看资源用这个，不要用 exec 跑 top/df 自己解析。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in HostStatusInput) (*mcp.CallToolResult, monitor.Snapshot, error) {
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
