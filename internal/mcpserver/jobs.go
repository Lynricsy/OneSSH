package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/jobs"
	"onessh/internal/store"
)

type JobStartInput struct {
	Host    string            `json:"host"`
	Command string            `json:"command"`
	Cwd     string            `json:"cwd,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}
type JobStartOutput struct {
	JobID string `json:"job_id"`
	PID   int64  `json:"pid"`
}
type JobIDInput struct {
	JobID string `json:"job_id"`
}
type JobListInput struct {
	Host string `json:"host,omitempty"`
}
type JobListOutput struct {
	Jobs []jobs.Status `json:"jobs"`
}
type JobLogsInput struct {
	JobID       string `json:"job_id"`
	TailLines   int    `json:"tail_lines,omitempty"`
	Grep        string `json:"grep,omitempty"`
	OffsetBytes int64  `json:"offset_bytes,omitempty"`
}
type JobLogsOutput struct {
	Output string `json:"output"`
}
type JobKillInput struct {
	JobID  string `json:"job_id"`
	Signal string `json:"signal,omitempty"`
}
type OKOutput struct {
	OK bool `json:"ok"`
}

func (s *Server) registerJobs(m *jobs.Manager) {
	register[JobStartInput, JobStartOutput](s, &mcp.Tool{Name: "job_start", Description: "非阻塞启动远程后台任务"}, func(ctx context.Context, _ *mcp.CallToolRequest, in JobStartInput) (*mcp.CallToolResult, JobStartOutput, error) {
		h, err := AuthorizedHost(ctx, in.Host)
		if err != nil {
			return errorResult(err.Error()), JobStartOutput{}, nil
		}
		p, _ := FromContext(ctx)
		if in.Cwd == "" {
			in.Cwd = "~"
		}
		j, err := m.Start(ctx, h, p.Token.ID, in.Command, in.Cwd, in.Env)
		if err != nil {
			return errorResult(err.Error()), JobStartOutput{}, nil
		}
		return nil, JobStartOutput{JobID: j.ID, PID: j.PID.Int64}, nil
	})
	register[JobListInput, JobListOutput](s, &mcp.Tool{Name: "job_list", Description: "列出并刷新当前令牌的后台任务"}, func(ctx context.Context, _ *mcp.CallToolRequest, in JobListInput) (*mcp.CallToolResult, JobListOutput, error) {
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
	register[JobIDInput, jobs.Status](s, &mcp.Tool{Name: "job_status", Description: "查询并刷新后台任务状态"}, func(ctx context.Context, _ *mcp.CallToolRequest, in JobIDInput) (*mcp.CallToolResult, jobs.Status, error) {
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
	register[JobLogsInput, JobLogsOutput](s, &mcp.Tool{Name: "job_logs", Description: "读取后台任务日志"}, func(ctx context.Context, _ *mcp.CallToolRequest, in JobLogsInput) (*mcp.CallToolResult, JobLogsOutput, error) {
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
	register[JobKillInput, OKOutput](s, &mcp.Tool{Name: "job_kill", Description: "终止后台任务进程或进程组"}, func(ctx context.Context, _ *mcp.CallToolRequest, in JobKillInput) (*mcp.CallToolResult, OKOutput, error) {
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
