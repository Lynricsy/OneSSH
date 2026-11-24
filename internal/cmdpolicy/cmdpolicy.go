// Package cmdpolicy 实现按命令原文判定的命令策略。
//
// 它是护栏，不是沙箱：只匹配收到的原始字符串，不做 shell 解析，也不理解管道、引号拼接
// 或变量展开。`r""m -rf /`、`sh -c '…'`、`curl … | base64 -d | sh` 都能绕过去。目标是在
// 误操作和明文 prompt injection 面前多拦一道，而不是抵御有意构造绕过的攻击者——那需要
// 目标机侧的 ForceCommand 或受限账号，网关做不到，也不应该装做做到。
package cmdpolicy

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Mode 是策略档位。off 完全不检查，与未引入策略时的行为一致。
type Mode string

const (
	Off   Mode = "off"
	Deny  Mode = "deny"
	Allow Mode = "allow"
)

// ParseMode 解析档位；空串按 off 处理，未知值直接报错而不是退回 off，
// 因为把 deny 拼错成 denny 却静默不拦，比启动失败危险得多。
func ParseMode(raw string) (Mode, error) {
	switch mode := Mode(strings.ToLower(strings.TrimSpace(raw))); mode {
	case "", Off:
		return Off, nil
	case Deny:
		return Deny, nil
	case Allow:
		return Allow, nil
	default:
		return Off, fmt.Errorf("未知命令策略档位 %q；可选值：off、deny、allow", raw)
	}
}

// SplitRules 把环境变量里的多行规则拆成切片：一行一条正则，空行与 # 开头的注释行忽略。
func SplitRules(raw string) []string {
	rules := make([]string, 0, strings.Count(raw, "\n")+1)
	for line := range strings.SplitSeq(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rules = append(rules, line)
	}
	return rules
}

// Rule 是一条编译好的规则；原文保留，供审计与错误提示指认是哪一条命中。
type Rule struct {
	Source string
	re     *regexp.Regexp
}

// Match 判断规则是否命中命令的任一等价执行形式，见 commandForms。
func (r Rule) Match(command string) bool { return r.matchForms(commandForms(command)) }

func (r Rule) matchForms(forms []string) bool {
	for _, form := range forms {
		if r.re.MatchString(form) {
			return true
		}
	}
	return false
}

// commandForms 返回命令的等价执行形式：原文、去首尾空白的形式、NUL 截断前缀、
// 以及截断前缀去首尾空白的形式（后三者仅在与原文不同时收录）。sshd 把远端命令
// 交给用户 shell 时按 C 字符串解释，`"rm -rf /\x00echo ok"` 实际执行的是
// `"rm -rf /"`；shell 对 `"\nrm -rf /"` 与 `"rm -rf /"` 的解释也完全相同。
// 只匹配原文时，`^` 锚定的规则会被前导换行或 NUL 填充绕过——这恰是模型生成
// 命令最常见的形态。文档语义是「首尾空白、NUL 及其后的内容不参与匹配」；
// Check 对每条命令求值一次，多条规则共用同一组形式，避免逐规则重复扫描。
func commandForms(command string) []string {
	prefix := command
	if i := strings.IndexByte(command, 0); i >= 0 {
		prefix = command[:i]
	}
	forms := []string{command}
	if prefix != command {
		forms = append(forms, prefix)
	}
	for _, form := range [2]string{strings.TrimSpace(command), strings.TrimSpace(prefix)} {
		if form != command && !slices.Contains(forms, form) {
			forms = append(forms, form)
		}
	}
	return forms
}

// Policy 是一层策略：实例级（来自环境变量）或令牌级（来自令牌配置）。
// 两层不是二选一，而是按逻辑 AND 组合，见 Check。
type Policy struct {
	Mode  Mode
	Deny  []Rule
	Allow []Rule
}

// Active 判断这一层是否启用。零值、off 与「没有拒绝规则的 deny」都不改变行为：
// deny 没有规则时拦不住任何命令，如果仍算启用，敏感环境变量检查会莫名其妙生效，
// 也与启动日志里「deny 但拒绝规则为空：等同于 off」矛盾。
// allow 即使白名单为空也算启用——空白名单等于全拒，不能被当成关闭。
func (p Policy) Active() bool {
	switch p.Mode {
	case Allow:
		return true
	case Deny:
		return len(p.Deny) > 0
	default:
		return false
	}
}

// Parse 校验并编译一层策略。非法正则与超限规则在解析阶段就报错：静默跳过一条拼错的规则，
// 等于把运维以为已经拦住的命令放出去。拒绝规则按原文子串匹配；白名单规则自动整串锚定，
// 见 compile。
func Parse(mode string, deny, allow []string) (Policy, error) {
	parsed, err := ParseMode(mode)
	if err != nil {
		return Policy{}, err
	}
	policy := Policy{Mode: parsed}
	if policy.Deny, err = compile("拒绝", deny, false); err != nil {
		return Policy{}, err
	}
	if policy.Allow, err = compile("白名单", allow, true); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

// 规则规模上限：正则随每个请求反复执行，无上限的策略等于把一条误配的大正则
// 放大成每个请求的 CPU 开销。条数与长度都在解析阶段拒绝，与非法正则同样 fail-fast。
const (
	MaxRulesPerList = 100
	MaxRuleLen      = 512
)

func compile(kind string, patterns []string, anchored bool) ([]Rule, error) {
	if len(patterns) > MaxRulesPerList {
		return nil, fmt.Errorf("%s规则超过 %d 条上限", kind, MaxRulesPerList)
	}
	rules := make([]Rule, 0, len(patterns))
	for _, pattern := range patterns {
		if len(pattern) > MaxRuleLen {
			return nil, fmt.Errorf("%s规则 %q 超过 %d 字节上限", kind, pattern, MaxRuleLen)
		}
		source := pattern
		if anchored {
			// 白名单按整条命令精确匹配：不锚定的 `^git fetch` 会放行
			// `git fetch && curl … | sh`，与运维对「白名单」的直觉相反。自动包裹
			// \A(?:…)\z 让每条规则天然整串锚定；规则里已写的 ^…$ 与外层锚点一致，不受影响。
			// 注意 RE2 的 `.` 默认不匹配换行：想覆盖 heredoc 这类多行命令，写 `(?s).*`。
			pattern = `\A(?:` + pattern + `)\z`
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("%s规则 %q 不是合法正则: %w", kind, source, err)
		}
		rules = append(rules, Rule{Source: source, re: re})
	}
	return rules, nil
}

// Layer 给一层策略带上来源，审计里要能看出是实例级还是令牌级拦下的。
type Layer struct {
	Scope  string
	Policy Policy
}

// Decision 是一次判定的结果。Denied 为 false 时 Reason 为空。
type Decision struct {
	Denied bool
	Reason string
}

// DenyAll 返回拒绝全部命令的策略，用于配置损坏时的 fail-closed 回退。
func DenyAll() Policy { return Policy{Mode: Allow} }

// Check 按多层策略判定一条命令。
//
// 组合语义是逻辑 AND，与工具组 denylist 的并集规则同源：任何一层的拒绝规则命中就拒绝；
// 处在白名单模式的每一层都必须放行。因此令牌只能再加拒绝规则或缩小白名单——
// 实例级 allow 不会被令牌的 off/deny 放宽，令牌的 allow 也只是在实例白名单之上再收窄。
// 白名单模式下空规则集等于全拒：忘记填规则不应变成放行一切。
// 未启用的层（零值、off、没有拒绝规则的 deny）整层跳过，规则文本不会被误用：
// 把档位切到 off 后，配置里遗留的规则必须立即失效。
func Check(layers []Layer, command string) Decision {
	forms := commandForms(command)
	for _, layer := range layers {
		if !layer.Policy.Active() {
			continue
		}
		for _, rule := range layer.Policy.Deny {
			if rule.matchForms(forms) {
				return Decision{Denied: true, Reason: fmt.Sprintf("%s拒绝规则命中：%s", layer.Scope, rule.Source)}
			}
		}
	}
	for _, layer := range layers {
		if layer.Policy.Mode != Allow {
			continue
		}
		if len(layer.Policy.Allow) == 0 {
			return Decision{Denied: true, Reason: layer.Scope + "白名单为空，拒绝全部命令"}
		}
		matched := false
		for _, rule := range layer.Policy.Allow {
			if rule.matchForms(forms) {
				matched = true
				break
			}
		}
		if !matched {
			return Decision{Denied: true, Reason: layer.Scope + "白名单未命中"}
		}
	}
	return Decision{}
}

// builtinSensitiveEnvKeys 是能改变「下一条命令怎么解释」的环境变量：命令原文一个字没变，
// 执行语义却变了，命令正则拦不住，所以按键名单独处理。命令正则表达不了结构化环境变量，
// 这也是「敏感键」与「独立正则」两条路里选前者的原因：键是闭集，值可以是任意字符串。
// 收录标准是「让后续进程加载或执行额外代码/配置」，不是「可能含敏感值」：黑名单天然补不完，
// 键名落网但语义相关的键（GIT_CONFIG_KEY_0/VALUE_0 等）靠拦住触发它们的开关（GIT_CONFIG_COUNT）。
var builtinSensitiveEnvKeys = []string{
	"PATH",           // 换掉 ssh / sudo / git，顺手换掉你以为正在跑的程序
	"ENV",            // sh 的非交互启动脚本
	"BASH_ENV",       // bash 的非交互启动脚本
	"SHELLOPTS",      // 打开 xtrace 等选项
	"BASHOPTS",       //
	"PROMPT_COMMAND", //
	"PS4",            //
	"IFS",            // 改分词，改变同一行命令的实际含义
	"LD_PRELOAD",     // 劫持动态库
	"LD_LIBRARY_PATH",
	"LD_AUDIT",   // glibc 动态库审计钩子，与 LD_PRELOAD 同级
	"GCONV_PATH", // glibc 字符集转换模块加载路径
	"PYTHONSTARTUP",
	"PYTHONPATH",
	"PYTHONHOME", // 劫持标准库路径
	"PERL5OPT",
	"PERL5LIB",          // PERL5OPT 的同族
	"RUBYOPT",           // -r 加载任意库
	"NODE_OPTIONS",      // --require 可加载任意 JS
	"GIT_SSH",           // git 走哪个 ssh
	"GIT_SSH_COMMAND",   //
	"GIT_EXEC_PATH",     // 换掉 git 子命令的二进制查找路径
	"GIT_CONFIG_GLOBAL", // git 配置注入：core.pager / diff.external 都能执行命令
	"GIT_CONFIG_SYSTEM", //
	"GIT_CONFIG_COUNT",  // GIT_CONFIG_KEY_n/VALUE_n 的开关，单独拦编号键没有意义
	"RUBYLIB",           // 改 Ruby 库搜索路径
	"JAVA_TOOL_OPTIONS", // JVM 参数注入，可启用 -javaagent
	"_JAVA_OPTIONS",     // 同上，老变量
	"JDK_JAVA_OPTIONS",  // 同上，JDK 9+ 变量
	"SSH_ASKPASS",       //
	"SUDO_ASKPASS",      // 配合 sudo -A 执行任意取密程序
	"OPENSSL_CONF",      // 劫持 openssl 行为
	"SSL_CERT_FILE",     // 换掉证书校验
	"HOSTALIASES",       // glibc 主机别名文件，改写解析结果
	"BASH_XTRACEFD",     // xtrace 重定向到任意 fd
}

// SensitiveEnv 返回敏感环境变量名集合：内置键加上运维显式追加的键，统一大写比较。
func SensitiveEnv(extra []string) map[string]struct{} {
	out := make(map[string]struct{}, len(builtinSensitiveEnvKeys)+len(extra))
	for _, key := range builtinSensitiveEnvKeys {
		out[key] = struct{}{}
	}
	for _, key := range extra {
		if key = strings.ToUpper(strings.TrimSpace(key)); key != "" {
			out[key] = struct{}{}
		}
	}
	return out
}

// EnvDenial 返回第一个敏感键，按调用方书写的原样（仅去首尾空白）而非统一大写：
// 残留会话环境的拒绝文案要能指到实际存储的键名——统一大写成 PATH 会让
// unset 一个历史遗留的小写 path 键变成无效操作。比较仍不分大小写。
// 先排序再判断，保证同样的输入永远得到同样的结论——
// session_env 的入参是 map，遍历顺序随机会让审计里记录的原因每次都不一样。
func EnvDenial(sensitive map[string]struct{}, names []string) (string, bool) {
	keys := make([]string, 0, len(names))
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	for _, name := range keys {
		if _, ok := sensitive[strings.ToUpper(name)]; ok {
			return name, true
		}
	}
	return "", false
}

// TokenPolicy 是令牌上持久化的策略配置，同时是 REST 与 OAuth 同意页交换的 JSON 形状。
type TokenPolicy struct {
	Mode  string   `json:"mode"`
	Deny  []string `json:"deny"`
	Allow []string `json:"allow"`
}

// DenyAllTokenPolicy 是令牌策略解码失败时的回退：allow 模式且白名单为空，等于全拒。
// 与工具组 denylist 的 fail-closed 一致——配置损坏不能变成放开。
func DenyAllTokenPolicy() TokenPolicy {
	return TokenPolicy{Mode: string(Allow), Deny: []string{}, Allow: []string{}}
}

// Compile 编译令牌策略。令牌策略在保存时已校验，这里再失败说明库里的记录被改过。
// 空 mode 只在没有任何规则时表示 off；带规则却没有档位（含 "mode":null）是损坏配置，
// 不能静默当成 off，否则一条缺少 mode 的记录会把限制全部放空。
func (t TokenPolicy) Compile() (Policy, error) {
	if strings.TrimSpace(t.Mode) == "" && (len(t.Deny) > 0 || len(t.Allow) > 0) {
		return Policy{}, fmt.Errorf("command_policy.mode 不能为空：有规则时必须指定 off、deny 或 allow")
	}
	return Parse(t.Mode, t.Deny, t.Allow)
}

// Normalize 校验并规范化令牌策略：先 trim 规则再编译，保证“校验的字符串”与
// “落库后实际生效的字符串”一致；否则 `" ^uptime$"` 会按带空格校验、
// 却以无空格保存，反而扩大白名单。最后把档位转小写、空切片写成 []。
func (t TokenPolicy) Normalize() (TokenPolicy, error) {
	normalized := TokenPolicy{Mode: t.Mode, Deny: trimRules(t.Deny), Allow: trimRules(t.Allow)}
	if _, err := normalized.Compile(); err != nil {
		return TokenPolicy{}, err
	}
	mode, err := ParseMode(normalized.Mode)
	if err != nil {
		return TokenPolicy{}, err
	}
	normalized.Mode = string(mode)
	return normalized, nil
}

func trimRules(rules []string) []string {
	out := make([]string, 0, len(rules))
	for _, rule := range rules {
		if rule = strings.TrimSpace(rule); rule != "" {
			out = append(out, rule)
		}
	}
	return out
}

// EncodeTokenPolicy 校验并序列化令牌策略，同时返回规范化的值供调用方写回结构体。
func EncodeTokenPolicy(t TokenPolicy) (string, TokenPolicy, error) {
	normalized, err := t.Normalize()
	if err != nil {
		return "", TokenPolicy{}, err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", TokenPolicy{}, err
	}
	return string(raw), normalized, nil
}

// DecodeTokenPolicy 解析数据库里的令牌策略。空串与 {} 表示未配置（off）；
// 内容损坏（包括 JSON null）时返回 fail-closed 的值而不是报错，避免一条坏记录让令牌整体不可用，
// 也不能让一条被改成 null 的记录变成「没有策略」。
func DecodeTokenPolicy(raw string) TokenPolicy {
	switch trimmed := strings.TrimSpace(raw); trimmed {
	case "", "{}":
		return TokenPolicy{Mode: string(Off), Deny: []string{}, Allow: []string{}}
	case "null":
		return DenyAllTokenPolicy()
	}
	var policy TokenPolicy
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		return DenyAllTokenPolicy()
	}
	normalized, err := policy.Normalize()
	if err != nil {
		return DenyAllTokenPolicy()
	}
	return normalized
}
