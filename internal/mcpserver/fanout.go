package mcpserver

import (
	"context"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/execx"
)

type ExecManyInput struct {
	Hosts    []string `json:"hosts"`
	Command  string   `json:"command"`
	TimeoutS int      `json:"timeout_s,omitempty"`
}
type ExecManyItem struct {
	Host     string `json:"host"`
	ExitCode int    `json:"exit_code"`
	Timeout  bool   `json:"timeout"`
	Output   string `json:"output"`
	Error    string `json:"error,omitempty"`
}
type ExecManyOutput struct {
	Results []ExecManyItem `json:"results"`
}

func (s *Server) registerFanout() {
	register[ExecManyInput, ExecManyOutput](s, &mcp.Tool{Name: "exec_many", Description: "至多 16 并发地在多台主机执行同一命令"}, func(ctx context.Context, _ *mcp.CallToolRequest, in ExecManyInput) (*mcp.CallToolResult, ExecManyOutput, error) {
		if len(in.Hosts) == 0 {
			return errorResult("hosts 不能为空"), ExecManyOutput{}, nil
		}
		return nil, s.execMany(ctx, in), nil
	})
}

func (s *Server) execMany(ctx context.Context, in ExecManyInput) ExecManyOutput {
	timeout := in.TimeoutS
	if timeout <= 0 {
		timeout = 60
	}
	if timeout > 600 {
		timeout = 600
	}
	out := ExecManyOutput{Results: make([]ExecManyItem, len(in.Hosts))}
	sem := make(chan struct{}, 16)
	var wg sync.WaitGroup
	for i, name := range in.Hosts {
		i, name := i, name
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			item := ExecManyItem{Host: name, ExitCode: -1}
			if _, err := AuthorizedHost(ctx, name); err != nil {
				item.Error = err.Error()
				out.Results[i] = item
				return
			}
			client, err := s.Pool.Get(ctx, name)
			if err != nil {
				item.Error = err.Error()
				out.Results[i] = item
				return
			}
			res, err := s.Exec.Run(ctx, client, in.Command, "~", nil, execx.Options{Timeout: time.Duration(timeout) * time.Second, MaxLines: 200})
			if err != nil {
				item.Error = err.Error()
				out.Results[i] = item
				return
			}
			item.ExitCode = res.ExitCode
			item.Timeout = res.Timeout
			item.Output = res.Output
			if len(item.Output) > 4096 {
				item.Output = item.Output[:4096]
			}
			out.Results[i] = item
		}()
	}
	wg.Wait()
	return out
}
