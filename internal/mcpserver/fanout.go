package mcpserver

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/execx"
)

type ExecManyInput struct {
	Hosts    []string `json:"hosts" jsonschema:"目标主机名列表，取自 hosts_list；无权限的主机会在结果里单独报错"`
	Command  string   `json:"command" jsonschema:"在每台主机上执行的同一条 shell 命令"`
	TimeoutS int      `json:"timeout_s,omitempty" jsonschema:"每台主机各自的超时秒数，默认 60，上限 600"`
}
type ExecManyItem struct {
	Host     string `json:"host"`
	RunID    string `json:"run_id,omitempty"`
	ExitCode int    `json:"exit_code"`
	Timeout  bool   `json:"timeout"`
	Output   string `json:"output"`
	Error    string `json:"error,omitempty"`
}
type ExecManyOutput struct {
	Results []ExecManyItem `json:"results"`
}

func (out ExecManyOutput) auditCommandRunIDs() []string {
	ids := make([]string, 0, len(out.Results))
	for _, item := range out.Results {
		if item.RunID != "" {
			ids = append(ids, item.RunID)
		}
	}
	return ids
}

func (out ExecManyOutput) auditOutcome() (bool, *int64) {
	for _, item := range out.Results {
		if item.Error != "" || item.Timeout || item.ExitCode != 0 {
			return false, nil
		}
	}
	return true, nil
}

func (s *Server) registerFanout() {
	register[ExecManyInput, ExecManyOutput](s, &mcp.Tool{
		Name:  "exec_many",
		Title: "批量执行命令",
		Description: "在多台授权主机上并发执行同一条命令（并发上限 16），按输入顺序逐台返回退出码、超时标记和输出，单台失败或无权限只影响该条结果。" +
			"适合批量巡检、统一查版本、同时重载配置；不同主机要跑不同命令时请分别调用 exec。" +
			"与 exec 不同：固定在家目录执行，不使用持久会话，也不带会话环境变量；每台输出截断到 4096 字节且不会生成 artifact，需要完整输出请对单台再用 exec。" +
			"批量修改是高风险操作，先在一台上验证再铺开。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(true), IdempotentHint: false, OpenWorldHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ExecManyInput) (*mcp.CallToolResult, ExecManyOutput, error) {
		if len(in.Hosts) == 0 {
			return errorResult("hosts 不能为空"), ExecManyOutput{}, nil
		}
		if strings.TrimSpace(in.Command) == "" {
			return errorResult("command 不能为空"), ExecManyOutput{}, nil
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
			host, err := AuthorizedHost(ctx, name)
			if err != nil {
				item.Error = err.Error()
				out.Results[i] = item
				return
			}
			run, err := s.startCommandRun(ctx, "exec_many", host, in.Command, "~", "")
			if err != nil {
				item.Error = err.Error()
				out.Results[i] = item
				return
			}
			item.RunID = run.ID
			client, err := s.Pool.Get(ctx, name)
			if err != nil {
				item.Error = err.Error()
				if finishErr := s.finishCommandRun(ctx, run, execx.Result{}, err); finishErr != nil {
					item.Error += "; 记录执行结果失败: " + finishErr.Error()
				}
				out.Results[i] = item
				return
			}
			publisher := newCommandOutputPublisher(s, run)
			res, err := s.Exec.Run(ctx, client, in.Command, "~", nil, execx.Options{
				Timeout: time.Duration(timeout) * time.Second, MaxLines: 200,
				CaptureID: run.ID, OnOutput: publisher.Publish,
			})
			publisher.Finish()
			if finishErr := s.finishCommandRun(ctx, run, res, err); finishErr != nil {
				if err == nil {
					err = finishErr
				} else {
					item.Error = err.Error() + "; 记录执行结果失败: " + finishErr.Error()
				}
			}
			if err != nil {
				if item.Error == "" {
					item.Error = err.Error()
				}
				out.Results[i] = item
				return
			}
			item.ExitCode = res.ExitCode
			item.Timeout = res.Timeout
			item.Output = res.Output
			if len(item.Output) > 4096 {
				item.Output = string(execx.CompleteUTF8Prefix([]byte(item.Output[:4096]), true))
			}
			out.Results[i] = item
		}()
	}
	wg.Wait()
	return out
}
