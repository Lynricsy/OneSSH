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
	Host     string `json:"host" jsonschema:"SSH 主机名，取自 hosts_list"`
	Command  string `json:"command" jsonschema:"原样交给远端 shell 执行的命令；不必自行 cd，工作目录由会话保持"`
	Session  string `json:"session,omitempty" jsonschema:"持久会话标签，默认 default；不同标签之间不共享工作目录和环境变量"`
	TimeoutS int    `json:"timeout_s,omitempty" jsonschema:"超时秒数，默认 60，上限 600；超时返回 timeout=true 且远端命令被中断"`
	MaxLines int    `json:"max_lines,omitempty" jsonschema:"output 返回的最大行数，默认 200；超出部分只在 artifact 中"`
	Tail     bool   `json:"tail,omitempty" jsonschema:"true 返回末尾若干行，默认返回开头若干行"`
}
type ExecOutput struct{ execx.Result }
type SessionEnvInput struct {
	Host    string            `json:"host" jsonschema:"SSH 主机名，取自 hosts_list"`
	Session string            `json:"session" jsonschema:"持久会话标签，留空表示 default"`
	Set     map[string]string `json:"set,omitempty" jsonschema:"要设置的环境变量；变量名必须匹配 [A-Za-z_][A-Za-z0-9_]*"`
	Unset   []string          `json:"unset,omitempty" jsonschema:"要删除的环境变量名"`
}
type SessionEnvOutput struct {
	Env map[string]string `json:"env"`
}
type OutputReadInput struct {
	ArtifactID string `json:"artifact_id" jsonschema:"exec 返回的 artifact_id，保留 7 天"`
	Offset     int    `json:"offset,omitempty" jsonschema:"起始序号，从 1 开始，默认 1；配合 grep 时按匹配结果计数"`
	Limit      int    `json:"limit,omitempty" jsonschema:"最大返回行数，默认 200，上限 5000"`
	Grep       string `json:"grep,omitempty" jsonschema:"Go 正则，只返回匹配的行"`
}
type OutputReadOutput struct {
	Content    string `json:"content"`
	TotalLines int    `json:"total_lines"`
}

func (s *Server) registerExec(runner *execx.Runner) {
	register[ExecInput, ExecOutput](s, &mcp.Tool{
		Name:  "exec",
		Title: "执行远程命令",
		Description: "在一台授权主机上同步执行 shell 命令，返回 stdout、stderr、合并后的 output、退出码与执行后的工作目录。" +
			"工作目录按 host+session 持久保存：这次 cd 到哪里，同一 session 的下次 exec 就从哪里开始；环境变量用 session_env 维护。" +
			"适合秒级到分钟级的命令，超过 timeout_s（默认 60、上限 600）会中断并返回 timeout=true，长任务改用 job_start。" +
			"输出超过 max_lines 或 256KiB 时 truncated=true 并给出 artifact_id，用 output_read 翻页或过滤，不要重跑命令加 head/tail。" +
			"读写文件、搜索、跨主机复制请优先用 file_read/file_write/file_edit/file_list/grep/find/file_transfer，它们比在 exec 里拼命令更不容易出错。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(true), IdempotentHint: false, OpenWorldHint: new(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ExecInput) (*mcp.CallToolResult, ExecOutput, error) {
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
	register[SessionEnvInput, SessionEnvOutput](s, &mcp.Tool{
		Name:  "session_env",
		Title: "设置会话环境变量",
		Description: "设置或删除某台主机某个持久 exec 会话的环境变量，返回该会话的全部环境变量。" +
			"设置后每次 exec 都会先 export 这些变量，适合固定 PATH、代理、语言等；一次性变量直接写在 exec 命令里即可。" +
			"job_start 不读取会话环境，需要在它自己的 env 参数里单独传。不要在这里存放密码或令牌。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(false), IdempotentHint: true, OpenWorldHint: new(false)},
	}, s.sessionEnv)
	register[OutputReadInput, OutputReadOutput](s, &mcp.Tool{
		Name:  "output_read",
		Title: "读取截断输出",
		Description: "读取 exec 因输出过大而落在网关本地的完整输出（artifact）。返回带原始行号的内容和总行数，可用 offset/limit 翻页、用 grep 正则过滤。" +
			"artifact 保留 7 天，过期后返回错误，此时重新执行命令即可。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in OutputReadInput) (*mcp.CallToolResult, OutputReadOutput, error) {
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
