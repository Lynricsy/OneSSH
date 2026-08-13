package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/execx"
	"onessh/internal/jobs"
	"onessh/internal/store"
)

type JobStartInput struct {
	Host    string            `json:"host" jsonschema:"SSH 主机名，取自 hosts_list"`
	Command string            `json:"command" jsonschema:"要在后台运行的 shell 命令；stdout 与 stderr 合并写入任务日志，stdin 为 /dev/null"`
	Cwd     string            `json:"cwd,omitempty" jsonschema:"工作目录，默认 ~；目录不存在任务会以退出码 97 失败"`
	Env     map[string]string `json:"env,omitempty" jsonschema:"额外导出的环境变量；不会读取 session_env 设置的会话环境"`
}
type JobStartOutput struct {
	JobID string `json:"job_id"`
	RunID string `json:"run_id"`
	PID   int64  `json:"pid"`
}
type JobIDInput struct {
	JobID string `json:"job_id" jsonschema:"job_start 返回的任务 ID"`
}
type JobListInput struct {
	Host string `json:"host,omitempty" jsonschema:"只列出该主机的任务，留空表示全部主机"`
}
type JobListOutput struct {
	Jobs []jobs.Status `json:"jobs"`
}
type JobLogsInput struct {
	JobID       string `json:"job_id" jsonschema:"job_start 返回的任务 ID"`
	TailLines   int    `json:"tail_lines,omitempty" jsonschema:"返回末尾多少行，默认 100，上限 5000"`
	Grep        string `json:"grep,omitempty" jsonschema:"扩展正则（grep -E），先过滤再取末尾 tail_lines 行"`
	OffsetBytes int64  `json:"offset_bytes,omitempty" jsonschema:"从日志的第几个字节开始读，字节序号从 1 起；大于 0 时忽略 grep 与 tail_lines。续读上次已读到 N 字节时传 N+1"`
}
type JobLogsOutput struct {
	Output string `json:"output"`
}
type JobKillInput struct {
	JobID  string `json:"job_id" jsonschema:"job_start 返回的任务 ID"`
	Signal string `json:"signal,omitempty" jsonschema:"信号名，仅支持 TERM（默认，优雅退出）或 KILL（强制）"`
}
type OKOutput struct {
	OK bool `json:"ok"`
}

func (s *Server) registerJobs(m *jobs.Manager) {
	register[JobStartInput, JobStartOutput](s, &mcp.Tool{
		Name:  "job_start",
		Title: "启动后台任务",
		Description: "在远程主机用 setsid + nohup 启动后台任务，立即返回 job_id 与 PID，SSH 会话断开也不会中断。" +
			"适合部署、构建、迁移、抓包这类超过 exec 600 秒上限或需要长期运行的工作；几十秒内能跑完的命令直接用 exec。" +
			"输出合并写入远端 ~/.onessh/jobs/<job_id>/out.log，用 job_logs 读取、job_status 判断结束与退出码、job_kill 终止；日志文件不会自动清理。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(true), IdempotentHint: false, OpenWorldHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in JobStartInput) (*mcp.CallToolResult, JobStartOutput, error) {
		h, err := AuthorizedHost(ctx, in.Host)
		if err != nil {
			return errorResult(err.Error()), JobStartOutput{}, nil
		}
		p, _ := FromContext(ctx)
		if strings.TrimSpace(in.Command) == "" {
			return errorResult("command 不能为空"), JobStartOutput{}, nil
		}
		if in.Cwd == "" {
			in.Cwd = "~"
		}
		run, err := s.startCommandRun(ctx, "job_start", h, in.Command, in.Cwd, "")
		if err != nil {
			return nil, JobStartOutput{}, err
		}
		j, err := m.StartTracked(ctx, h, p.Token.ID, in.Command, in.Cwd, in.Env, run.ID)
		if err != nil {
			if finishErr := s.finishCommandRun(ctx, run, execx.Result{}, err); finishErr != nil {
				return nil, JobStartOutput{RunID: run.ID}, finishErr
			}
			return errorResult(err.Error()), JobStartOutput{RunID: run.ID}, nil
		}
		return nil, JobStartOutput{JobID: j.ID, RunID: run.ID, PID: j.PID.Int64}, nil
	})
	register[JobListInput, JobListOutput](s, &mcp.Tool{
		Name:        "job_list",
		Title:       "列出后台任务",
		Description: "列出当前令牌启动过的后台任务并逐个刷新状态，可用 host 过滤。看不到其他令牌的任务；刷新需要登录主机，任务多时会稍慢。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in JobListInput) (*mcp.CallToolResult, JobListOutput, error) {
		p, _ := FromContext(ctx)
		var hostID *int64
		if in.Host != "" {
			h, err := AuthorizedHost(ctx, in.Host)
			if err != nil {
				return errorResult(err.Error()), JobListOutput{}, nil
			}
			hostID = &h.ID
		}
		list, err := s.Store.ListJobs(ctx, &p.Token.ID, hostID)
		if err != nil {
			return nil, JobListOutput{}, err
		}
		out := JobListOutput{Jobs: make([]jobs.Status, 0, len(list))}
		for _, j := range list {
			st, e := m.Refresh(ctx, j)
			if e == nil {
				out.Jobs = append(out.Jobs, st)
			}
		}
		return nil, out, nil
	})
	register[JobIDInput, jobs.Status](s, &mcp.Tool{
		Name:  "job_status",
		Title: "查询后台任务状态",
		Description: "登录主机刷新并返回单个后台任务的状态：running 表示仍在运行，exited 带 exit_code，lost 表示进程已不在且没留下退出码（多为主机重启或被外部杀掉）。" +
			"log_bytes 是当前日志字节数，可作为 job_logs 的 offset_bytes 基准做增量读取。轮询长任务时优先查状态，不要反复拉全量日志。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in JobIDInput) (*mcp.CallToolResult, jobs.Status, error) {
		j, err := ownedJob(ctx, s.Store, in.JobID)
		if err != nil {
			return errorResult(err.Error()), jobs.Status{}, nil
		}
		st, err := m.Refresh(ctx, j)
		if err != nil {
			return errorResult(err.Error()), jobs.Status{}, nil
		}
		return nil, st, nil
	})
	register[JobLogsInput, JobLogsOutput](s, &mcp.Tool{
		Name:        "job_logs",
		Title:       "读取任务日志",
		Description: "读取后台任务的合并输出日志。默认返回末尾 100 行；grep 传扩展正则先过滤再取尾部；offset_bytes 用于从上次读到的位置继续增量读取，适合边跑边看。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in JobLogsInput) (*mcp.CallToolResult, JobLogsOutput, error) {
		j, err := ownedJob(ctx, s.Store, in.JobID)
		if err != nil {
			return errorResult(err.Error()), JobLogsOutput{}, nil
		}
		out, err := m.Logs(ctx, j, in.TailLines, in.Grep, in.OffsetBytes)
		if err != nil {
			return errorResult(err.Error()), JobLogsOutput{}, nil
		}
		return nil, JobLogsOutput{Output: out}, nil
	})
	register[JobKillInput, OKOutput](s, &mcp.Tool{
		Name:        "job_kill",
		Title:       "终止后台任务",
		Description: "终止后台任务：用 setsid 启动的任务按进程组终止，能一并带走子进程。先用默认的 TERM 让进程有机会清理，确认无效再用 KILL 强杀。任务已结束时调用无副作用。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(true), IdempotentHint: true, OpenWorldHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in JobKillInput) (*mcp.CallToolResult, OKOutput, error) {
		j, err := ownedJob(ctx, s.Store, in.JobID)
		if err != nil {
			return errorResult(err.Error()), OKOutput{}, nil
		}
		if in.Signal == "" {
			in.Signal = "TERM"
		}
		if err := m.Kill(ctx, j, in.Signal); err != nil {
			return errorResult(err.Error()), OKOutput{}, nil
		}
		return nil, OKOutput{OK: true}, nil
	})
}
func ownedJob(ctx context.Context, st *store.Store, id string) (store.Job, error) {
	p, _ := FromContext(ctx)
	j, err := st.GetJob(ctx, id)
	if err != nil {
		return j, fmt.Errorf("unknown job: %s", id)
	}
	if !store.JobOwnedBy(j, p.Token.ID) {
		return j, fmt.Errorf("job not authorized: %s", id)
	}
	return j, nil
}
