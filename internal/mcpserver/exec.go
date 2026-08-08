package mcpserver

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/execx"
)

type ExecInput struct {
	Host     string `json:"host" jsonschema:"SSH 主机名"`
	Command  string `json:"command" jsonschema:"原样执行的 shell 命令"`
	Session  string `json:"session,omitempty"`
	TimeoutS int    `json:"timeout_s,omitempty"`
	MaxLines int    `json:"max_lines,omitempty"`
	Tail     bool   `json:"tail,omitempty"`
}
type ExecOutput struct{ execx.Result }
type SessionEnvInput struct {
	Host    string            `json:"host"`
	Session string            `json:"session"`
	Set     map[string]string `json:"set,omitempty"`
	Unset   []string          `json:"unset,omitempty"`
}
type SessionEnvOutput struct {
	Env map[string]string `json:"env"`
}
type OutputReadInput struct {
	ArtifactID string `json:"artifact_id"`
	Offset     int    `json:"offset,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	Grep       string `json:"grep,omitempty"`
}
type OutputReadOutput struct {
	Content    string `json:"content"`
	TotalLines int    `json:"total_lines"`
}

func (s *Server) registerExec(runner *execx.Runner) {
	register[ExecInput, ExecOutput](s, &mcp.Tool{Name: "exec", Description: "在 SSH 主机执行命令，支持持久 cwd/env 会话与大输出 artifact"}, func(ctx context.Context, _ *mcp.CallToolRequest, in ExecInput) (*mcp.CallToolResult, ExecOutput, error) {
		h, err := AuthorizedHost(ctx, in.Host)
		if err != nil {
			return errorResult(err.Error()), ExecOutput{}, nil
		}
		if strings.TrimSpace(in.Command) == "" {
			return errorResult("command 不能为空"), ExecOutput{}, nil
		}
		p, _ := FromContext(ctx)
		label := in.Session
		if label == "" {
			label = "default"
		}
		state, err := s.Store.GetSession(ctx, p.Token.ID, h.ID, label)
		if err != nil {
			return nil, ExecOutput{}, err
		}
		client, err := s.Pool.Get(ctx, in.Host)
		if err != nil {
			return errorResult(err.Error()), ExecOutput{}, nil
		}
		timeout := in.TimeoutS
		if timeout <= 0 {
			timeout = 60
		}
		if timeout > 600 {
			timeout = 600
		}
		max := in.MaxLines
		if max <= 0 {
			max = 200
		}
		res, err := runner.Run(ctx, client, in.Command, state.Cwd, state.Env, execx.Options{Timeout: time.Duration(timeout) * time.Second, MaxLines: max, Tail: in.Tail, OnOutput: func(stream string, chunk []byte) {
			s.Events.Publish("exec_output", map[string]any{"host": in.Host, "stream": stream, "data": string(chunk)})
		}})
		if err != nil {
			return errorResult(err.Error()), ExecOutput{}, nil
		}
		if res.Cwd != "" && !res.Timeout {
			state.Cwd = res.Cwd
			_ = s.Store.SaveSession(context.Background(), state)
		}
		return nil, ExecOutput{Result: res}, nil
	})
	register[SessionEnvInput, SessionEnvOutput](s, &mcp.Tool{Name: "session_env", Description: "更新持久 exec 会话的环境变量"}, s.sessionEnv)
	register[OutputReadInput, OutputReadOutput](s, &mcp.Tool{Name: "output_read", Description: "按行读取网关 artifact 输出，可用 Go 正则过滤"}, func(ctx context.Context, _ *mcp.CallToolRequest, in OutputReadInput) (*mcp.CallToolResult, OutputReadOutput, error) {
		path, err := runner.ArtifactPath(in.ArtifactID)
		if err != nil {
			return errorResult(err.Error()), OutputReadOutput{}, nil
		}
		content, total, err := execx.ReadArtifact(path, in.Offset, in.Limit, in.Grep)
		if err != nil {
			return errorResult(err.Error()), OutputReadOutput{}, nil
		}
		return nil, OutputReadOutput{Content: content, TotalLines: total}, nil
	})
}
func (s *Server) sessionEnv(ctx context.Context, _ *mcp.CallToolRequest, in SessionEnvInput) (*mcp.CallToolResult, SessionEnvOutput, error) {
	h, err := AuthorizedHost(ctx, in.Host)
	if err != nil {
		return errorResult(err.Error()), SessionEnvOutput{}, nil
	}
	p, _ := FromContext(ctx)
	if in.Session == "" {
		in.Session = "default"
	}
	state, err := s.Store.GetSession(ctx, p.Token.ID, h.ID, in.Session)
	if err != nil {
		return nil, SessionEnvOutput{}, err
	}
	if state.Env == nil {
		state.Env = map[string]string{}
	}
	valid := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	for k, v := range in.Set {
		if !valid.MatchString(k) {
			return errorResult(fmt.Sprintf("环境变量名无效: %s", k)), SessionEnvOutput{}, nil
		}
		state.Env[k] = v
	}
	for _, k := range in.Unset {
		delete(state.Env, k)
	}
	if err := s.Store.SaveSession(ctx, state); err != nil {
		return nil, SessionEnvOutput{}, err
	}
	return nil, SessionEnvOutput{Env: state.Env}, nil
}
