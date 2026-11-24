package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/cmdpolicy"
)

// commandPolicy 是实例级命令策略：进程启动时从环境变量解析一次，
// 令牌级策略随每次调用从令牌配置读取，两层在 policyLayers 里按逻辑 AND 组合。
type commandPolicy struct {
	instance  cmdpolicy.Policy
	sensitive map[string]struct{}
}

func newCommandPolicy(instance cmdpolicy.Policy, extraEnvKeys []string) *commandPolicy {
	return &commandPolicy{instance: instance, sensitive: cmdpolicy.SensitiveEnv(extraEnvKeys)}
}

// policyLayers 组装实例级与令牌级策略。顺序不影响结论（先查全部拒绝规则，再查全部白名单），
// 实例级放前面只是让审计原因优先指向实例级规则。s.command 为 nil（测试用 &Server{…}
// 字面量构造、不经 New）时跳过实例层而不是 panic：零值策略等同 off，与 Options 不配置时一致。
func (s *Server) policyLayers(ctx context.Context) []cmdpolicy.Layer {
	layers := make([]cmdpolicy.Layer, 0, 2)
	if s.command != nil {
		layers = append(layers, cmdpolicy.Layer{Scope: "实例", Policy: s.command.instance})
	}
	if p, ok := FromContext(ctx); ok {
		// Bearer 中间件已编译过令牌策略，直接复用；未经中间件装载的调用
		// （测试手工构造的 Principal）现场编译，行为与从前一致。
		if p.commandPolicy != nil {
			layers = append(layers, cmdpolicy.Layer{Scope: "令牌", Policy: *p.commandPolicy})
			return layers
		}
		policy, err := p.Token.CommandPolicy.Compile()
		if err != nil {
			// 令牌策略在保存时已校验，这里再失败说明库里的记录被改过：
			// 按 fail-closed 处理，不能因为解析不出来就当成没有策略。
			policy = cmdpolicy.DenyAll()
		}
		layers = append(layers, cmdpolicy.Layer{Scope: "令牌", Policy: policy})
	}
	return layers
}

// policyActive 判断是否至少有一层启用了策略。全 off 时连环境变量检查都不做，
// 保证默认配置下的行为与引入策略之前完全一致。
func (s *Server) policyActive(ctx context.Context) bool {
	for _, layer := range s.policyLayers(ctx) {
		if layer.Policy.Active() {
			return true
		}
	}
	return false
}

// commandDenial 判定一条即将交给远端执行的命令，返回拒绝原因与是否拒绝。
// 调用点必须在创建命令执行记录、取 SSH 连接与 fan-out 之前。
func (s *Server) commandDenial(ctx context.Context, command string) (string, bool) {
	decision := cmdpolicy.Check(s.policyLayers(ctx), command)
	return decision.Reason, decision.Denied
}

// envDenial 判定要写入的环境变量名，返回第一个敏感键。
//
// session_env.set 与 job_start.env 都要过这里：它们不改命令文本，却能改下一条命令的
// 解释方式（PATH 换掉 ssh、BASH_ENV 注入启动脚本、LD_PRELOAD 劫持动态库），
// 只拦 command 等于留了一扇门。
func (s *Server) envDenial(ctx context.Context, names []string) (string, bool) {
	if s.command == nil || !s.policyActive(ctx) {
		return "", false
	}
	return cmdpolicy.EnvDenial(s.command.sensitive, names)
}

// envNames 把环境变量 map 转成键名切片。会话环境在策略启用前就可能写好，
// 所以 exec 每次执行前也要重新检查，不能只依赖 session_env.set 的写入时校验。
func envNames(env map[string]string) []string {
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	return names
}

// denialKey 让工具处理函数把「策略拒绝」的原因交给通用审计包装器。
// tools/call 的持久审计原来只有 ok 位，WebUI 只能靠错误文案反推是被拦还是执行失败。
type denialKey struct{}

type denial struct {
	reason string
}

// deny 返回策略拒绝的工具结果，并把原因记到审计包装器那里。
//
// 文案里必须写明「不是连接失败」：模型看到工具报错的第一反应是重试，而策略拒绝
// 原样重试永远不会成功，只会把同一段危险命令多发几遍。
func deny(ctx context.Context, reason string) *mcp.CallToolResult {
	if d, ok := ctx.Value(denialKey{}).(*denial); ok && d.reason == "" {
		d.reason = reason
	}
	return errorResult("网关命令策略拒绝了本次调用：" + reason +
		"。这是策略拦截，不是主机连不上；原样重试不会成功，需要改命令或由管理员调整策略。")
}
