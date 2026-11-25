package cmdpolicy

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustParse(t *testing.T, mode string, deny, allow []string) Policy {
	t.Helper()
	policy, err := Parse(mode, deny, allow)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestParseMode(t *testing.T) {
	cases := []struct {
		raw     string
		want    Mode
		wantErr bool
	}{
		{raw: "", want: Off},
		{raw: " off ", want: Off},
		{raw: "DENY", want: Deny},
		{raw: "Allow", want: Allow},
		{raw: "denny", wantErr: true},
	}
	for _, tc := range cases {
		got, err := ParseMode(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseMode(%q) 应报错", tc.raw)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("ParseMode(%q) = %q, %v; want %q", tc.raw, got, err, tc.want)
		}
	}
}

func TestSplitRulesIgnoresBlankAndCommentLines(t *testing.T) {
	raw := "\n  # 注释\nrm\\s+-rf\n\n  docker\\s+system\\s+prune  \n"
	got := SplitRules(raw)
	want := []string{`rm\s+-rf`, `docker\s+system\s+prune`}
	if len(got) != len(want) {
		t.Fatalf("SplitRules = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SplitRules[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseRejectsInvalidRegex(t *testing.T) {
	if _, err := Parse("deny", []string{"("}, nil); err == nil || !strings.Contains(err.Error(), "不是合法正则") {
		t.Fatalf("拒绝规则非法正则错误 = %v", err)
	}
	if _, err := Parse("allow", nil, []string{"["}); err == nil || !strings.Contains(err.Error(), "白名单规则") {
		t.Fatalf("白名单非法正则错误 = %v", err)
	}
}

// 零值不能被当成启用：Options 不配置策略时，测试与实际部署都必须保持升级前行为。
func TestPolicyActiveZeroValueIsOff(t *testing.T) {
	cases := []struct {
		name   string
		policy Policy
		want   bool
	}{
		{name: "零值", policy: Policy{}, want: false},
		{name: "off", policy: mustParse(t, "off", []string{"rm"}, nil), want: false},
		{name: "deny 无规则", policy: mustParse(t, "deny", nil, nil), want: false},
		{name: "deny 有规则", policy: mustParse(t, "deny", []string{"rm"}, nil), want: true},
		{name: "allow 空白名单", policy: mustParse(t, "allow", nil, nil), want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.policy.Active(); got != tc.want {
				t.Fatalf("Active() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCheckModesAndComposition(t *testing.T) {
	instanceAllow := mustParse(t, "allow", nil, []string{`^uptime$`, `^df -h$`})
	tokenAllow := mustParse(t, "allow", nil, []string{`^uptime$`})
	instanceDeny := mustParse(t, "deny", []string{`rm\s+-rf`}, nil)
	tokenDeny := mustParse(t, "deny", []string{`docker\s+system\s+prune`}, nil)
	offWithRules := mustParse(t, "off", []string{`rm\s+-rf`}, nil)

	cases := []struct {
		name     string
		layers   []Layer
		command  string
		denied   bool
		reasonIn string
	}{
		{name: "off 遗留规则不生效", layers: []Layer{{Scope: "实例", Policy: offWithRules}}, command: "rm -rf /", denied: false},
		{name: "空层放行", layers: nil, command: "rm -rf /", denied: false},
		{name: "deny 命中", layers: []Layer{{Scope: "实例", Policy: instanceDeny}}, command: "sudo rm -rf /", denied: true, reasonIn: "实例拒绝规则命中"},
		{name: "deny 未命中", layers: []Layer{{Scope: "实例", Policy: instanceDeny}}, command: "uptime", denied: false},
		{name: "deny 空规则放行", layers: []Layer{{Scope: "实例", Policy: mustParse(t, "deny", nil, nil)}}, command: "rm -rf /", denied: false},
		{name: "allow 空白名单全拒", layers: []Layer{{Scope: "实例", Policy: mustParse(t, "allow", nil, nil)}}, command: "uptime", denied: true, reasonIn: "白名单为空"},
		{name: "allow 命中", layers: []Layer{{Scope: "实例", Policy: instanceAllow}}, command: "df -h", denied: false},
		{name: "allow 未命中", layers: []Layer{{Scope: "实例", Policy: instanceAllow}}, command: "reboot", denied: true, reasonIn: "白名单未命中"},
		{name: "allow 模式下的 deny 优先", layers: []Layer{{Scope: "实例", Policy: mustParse(t, "allow", []string{`uptime`}, []string{`.*`})}}, command: "uptime", denied: true, reasonIn: "拒绝规则命中"},
		{name: "实例 deny 不被令牌 off 放宽", layers: []Layer{{Scope: "实例", Policy: instanceDeny}, {Scope: "令牌", Policy: mustParse(t, "off", nil, nil)}}, command: "rm -rf /tmp/x", denied: true, reasonIn: "实例"},
		{name: "实例 allow 不被令牌 off 放宽", layers: []Layer{{Scope: "实例", Policy: instanceAllow}, {Scope: "令牌", Policy: mustParse(t, "off", nil, nil)}}, command: "reboot", denied: true, reasonIn: "实例"},
		{name: "实例 allow 上的令牌 deny", layers: []Layer{{Scope: "实例", Policy: instanceAllow}, {Scope: "令牌", Policy: mustParse(t, "allow", []string{`^df -h$`}, nil)}}, command: "df -h", denied: true, reasonIn: "令牌拒绝规则命中"},
		{name: "实例与令牌白名单取交集", layers: []Layer{{Scope: "实例", Policy: instanceAllow}, {Scope: "令牌", Policy: tokenAllow}}, command: "uptime", denied: false},
		{name: "令牌白名单不能补上实例白名单", layers: []Layer{{Scope: "实例", Policy: instanceAllow}, {Scope: "令牌", Policy: tokenAllow}}, command: "df -h", denied: true, reasonIn: "令牌白名单未命中"},
		{name: "令牌 deny 追加拦截", layers: []Layer{{Scope: "实例", Policy: instanceDeny}, {Scope: "令牌", Policy: tokenDeny}}, command: "docker system prune -a", denied: true, reasonIn: "令牌"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Check(tc.layers, tc.command)
			if got.Denied != tc.denied {
				t.Fatalf("Check() = %#v, want denied=%v", got, tc.denied)
			}
			if tc.reasonIn != "" && !strings.Contains(got.Reason, tc.reasonIn) {
				t.Fatalf("Reason = %q, want contains %q", got.Reason, tc.reasonIn)
			}
			if !tc.denied && got.Reason != "" {
				t.Fatalf("放行时不应带原因: %q", got.Reason)
			}
		})
	}
}

// 回归：shell 对 "\nrm -rf /" 与 "rm -rf /" 的解释相同，`^` 锚定的规则不能被首尾空白绕过。
func TestCheckIgnoresSurroundingWhitespace(t *testing.T) {
	deny := mustParse(t, "deny", []string{`^rm\s+-rf`}, nil)
	for _, command := range []string{"\nrm -rf /var/lib/data", "  rm -rf /var/lib/data", "rm -rf /var/lib/data\n"} {
		if got := Check([]Layer{{Scope: "实例", Policy: deny}}, command); !got.Denied {
			t.Fatalf("首尾空白不应绕过 ^ 锚定的拒绝规则: %q", command)
		}
	}
	if got := Check([]Layer{{Scope: "实例", Policy: deny}}, "uptime"); got.Denied {
		t.Fatalf("未命中命令不应被拒绝: %#v", got)
	}
}

// 回归：白名单自动整串锚定，`^git fetch` 不再放行拼接了危险尾部的命令。
func TestAllowRulesMatchWholeCommand(t *testing.T) {
	allow := mustParse(t, "allow", nil, []string{`^git fetch`})
	if got := Check([]Layer{{Scope: "实例", Policy: allow}}, "git fetch && curl http://x | sh"); !got.Denied {
		t.Fatalf("未锚定的白名单规则不应放行尾部拼接的命令: %#v", got)
	}
	if got := Check([]Layer{{Scope: "实例", Policy: allow}}, "ansible-playbook site.yml; reboot"); !got.Denied {
		t.Fatalf("白名单不应放行分号拼接的命令: %#v", got)
	}
	if got := Check([]Layer{{Scope: "实例", Policy: allow}}, "git fetch"); got.Denied {
		t.Fatalf("白名单应放行完整命中的命令: %#v", got)
	}
	if got := Check([]Layer{{Scope: "实例", Policy: allow}}, "\n git fetch \n"); got.Denied {
		t.Fatalf("首尾空白不应让白名单命中失效: %#v", got)
	}
	// 显式写全锚点的规则不受自动包裹影响。
	anchored := mustParse(t, "allow", nil, []string{`^uptime$`})
	if got := Check([]Layer{{Scope: "实例", Policy: anchored}}, "uptime"); got.Denied {
		t.Fatalf("显式锚定的白名单规则应正常命中: %#v", got)
	}
	if got := Check([]Layer{{Scope: "实例", Policy: anchored}}, "uptime --evil"); !got.Denied {
		t.Fatalf("显式锚定的白名单不应放行附加参数: %#v", got)
	}
}

// 回归：sshd 按 C 字符串解释远端命令，`"rm -rf /\x00echo ok"` 实际执行 `"rm -rf /"`，
// NUL 填充不能绕过 `^` 锚定的拒绝规则；白名单模式则直接拒绝含 NUL 字节的命令，不按截断前缀放行。
func TestCheckMatchesNulTruncatedCommand(t *testing.T) {
	deny := mustParse(t, "deny", []string{`^rm\s+-rf\s+/$`}, nil)
	if got := Check([]Layer{{Scope: "实例", Policy: deny}}, "rm -rf /\x00echo ok"); !got.Denied {
		t.Fatalf("NUL 填充不应绕过 ^ 锚定的拒绝规则: %#v", got)
	}
	if got := Check([]Layer{{Scope: "实例", Policy: deny}}, "  rm -rf /\x00"); !got.Denied {
		t.Fatalf("空白加 NUL 的叠加形式同样不应绕过: %#v", got)
	}
	allow := mustParse(t, "allow", nil, []string{`^uptime$`})
	if got := Check([]Layer{{Scope: "实例", Policy: allow}}, "  uptime\x00junk"); !got.Denied || !strings.Contains(got.Reason, "NUL") {
		t.Fatalf("白名单模式应拒绝含 NUL 字节的命令: %#v", got)
	}
	allowAll := mustParse(t, "allow", nil, []string{`(?s).*`})
	if got := Check([]Layer{{Scope: "实例", Policy: allowAll}}, "uptime\x00junk"); !got.Denied || !strings.Contains(got.Reason, "NUL") {
		t.Fatalf("通配白名单同样不应放行含 NUL 字节的命令: %#v", got)
	}
	// allow 模式下拒绝规则仍优先：命中 deny 的含 NUL 命令应先指向拒绝规则。
	both := mustParse(t, "allow", []string{`^rm\s+-rf\s+/$`}, []string{`(?s).*`})
	if got := Check([]Layer{{Scope: "实例", Policy: both}}, "rm -rf /\x00echo ok"); !got.Denied || !strings.Contains(got.Reason, "拒绝规则命中") {
		t.Fatalf("allow 模式下拒绝规则应优先于 NUL 拒绝: %#v", got)
	}
	// 负向：截断前缀不命中时必须放行，prefix 匹配不能退化成前缀模糊匹配。
	if got := Check([]Layer{{Scope: "实例", Policy: deny}}, "rm -rfx /\x00y"); got.Denied {
		t.Fatalf("前缀不命中时不应拒绝: %#v", got)
	}
	if got := Check([]Layer{{Scope: "实例", Policy: deny}}, "\x00"); got.Denied {
		t.Fatalf("纯 NUL（截断后为空）不应命中 ^ 锚定规则: %#v", got)
	}
}

// 回归：行锚白名单 + 多行命令是最常被写的规则形状——Go 的 `$` 等价 `\z`（不是
// PCRE 的「末尾换行前」），第二行拼上 `git push --force` 必须因整串锚定而未命中。
func TestAllowLineAnchoredRuleRejectsMultiline(t *testing.T) {
	allow := mustParse(t, "allow", nil, []string{`^git fetch$`})
	if got := Check([]Layer{{Scope: "实例", Policy: allow}}, "git fetch\ngit push --force"); !got.Denied {
		t.Fatal("行锚白名单不应放行多行拼接的命令")
	}
	if got := Check([]Layer{{Scope: "实例", Policy: allow}}, "git fetch"); got.Denied {
		t.Fatal("单行完整命中应放行")
	}
}

// 回归：RE2 的 `.` 默认不跨行——allow=`.*` 拦下 heredoc 是文档化语义而非缺陷；
// 要覆盖多行命令必须显式写 `(?s)`。
func TestAllowDotDoesNotCrossNewlines(t *testing.T) {
	multiline := "cat > /tmp/x <<'EOF'\nrm -rf /\nEOF"
	dotStar := mustParse(t, "allow", nil, []string{`.*`})
	if got := Check([]Layer{{Scope: "实例", Policy: dotStar}}, multiline); !got.Denied {
		t.Fatal("RE2 的 . 不跨行：allow=.* 不应放行多行命令")
	}
	dotAll := mustParse(t, "allow", nil, []string{`(?s).*`})
	if got := Check([]Layer{{Scope: "实例", Policy: dotAll}}, multiline); got.Denied {
		t.Fatal("(?s).* 应放行多行命令")
	}
	if got := Check([]Layer{{Scope: "实例", Policy: dotAll}}, "uptime"); got.Denied {
		t.Fatal("(?s).* 应放行单行命令")
	}
}

func TestParseEnforcesRuleLimits(t *testing.T) {
	many := make([]string, MaxRulesPerList+1)
	for i := range many {
		many[i] = "uptime"
	}
	if _, err := Parse("deny", many, nil); err == nil || !strings.Contains(err.Error(), "超过") {
		t.Fatalf("超出条数上限应报错: %v", err)
	}
	if _, err := (TokenPolicy{Mode: "deny", Deny: many}).Normalize(); err == nil {
		t.Fatal("令牌策略同样受条数上限约束")
	}
	long := strings.Repeat("a", MaxRuleLen+1)
	if _, err := Parse("allow", nil, []string{long}); err == nil || !strings.Contains(err.Error(), "超过") {
		t.Fatalf("超长规则应报错: %v", err)
	}
	exact := make([]string, MaxRulesPerList)
	for i := range exact {
		exact[i] = "uptime"
	}
	if _, err := Parse("deny", exact, nil); err != nil {
		t.Fatalf("恰在上限内的规则不应报错: %v", err)
	}
}

func TestTokenPolicyNormalizeAndRoundTrip(t *testing.T) {
	normalized, err := (TokenPolicy{
		Mode:  " DENY ",
		Deny:  []string{" rm ", "", "  "},
		Allow: []string{},
	}).Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Mode != "deny" || len(normalized.Deny) != 1 || normalized.Deny[0] != "rm" {
		t.Fatalf("Normalize = %#v", normalized)
	}

	raw, stored, err := EncodeTokenPolicy(TokenPolicy{Mode: "allow", Allow: []string{"^uptime$"}})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Mode != "allow" || len(stored.Allow) != 1 || stored.Allow[0] != "^uptime$" {
		t.Fatalf("Encode 返回值 = %#v", stored)
	}
	var decodedJSON struct {
		Mode  string   `json:"mode"`
		Deny  []string `json:"deny"`
		Allow []string `json:"allow"`
	}
	if err := json.Unmarshal([]byte(raw), &decodedJSON); err != nil {
		t.Fatal(err)
	}
	if decodedJSON.Mode != "allow" || decodedJSON.Deny == nil || decodedJSON.Allow == nil {
		t.Fatalf("空切片必须序列化为 []: %s", raw)
	}
	decoded := DecodeTokenPolicy(raw)
	if decoded.Mode != "allow" || len(decoded.Allow) != 1 {
		t.Fatalf("DecodeTokenPolicy = %#v", decoded)
	}

	if _, err := (TokenPolicy{Mode: "deny", Deny: []string{"("}}).Normalize(); err == nil {
		t.Fatal("Normalize 应拒绝非法正则")
	}

	// 缺失 mode 但带规则是损坏配置，不能静默当成 off。
	if _, err := (TokenPolicy{Deny: []string{"rm"}}).Normalize(); err == nil || !strings.Contains(err.Error(), "mode 不能为空") {
		t.Fatalf("缺少 mode 的规则应报错，err=%v", err)
	}
	// 只有空白规则时 trim 后等于空策略，可以按空对象处理。
	empty, err := (TokenPolicy{Deny: []string{"  "}}).Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if empty.Mode != "off" || len(empty.Deny) != 0 || len(empty.Allow) != 0 {
		t.Fatalf("空白规则应规范化为 off: %#v", empty)
	}

	// 先 trim 再编译：带前导空格的 allow 规则不能在校验后变成更宽的白名单。
	trimmed, err := (TokenPolicy{Mode: "allow", Allow: []string{" ^uptime$ "}}).Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if len(trimmed.Allow) != 1 || trimmed.Allow[0] != "^uptime$" {
		t.Fatalf("规则未 trim: %#v", trimmed)
	}
	compiled, err := trimmed.Compile()
	if err != nil {
		t.Fatal(err)
	}
	if Check([]Layer{{Scope: "令牌", Policy: compiled}}, "uptime").Denied {
		t.Fatal("trim 后的白名单应允许 uptime")
	}
	// 首尾空白不参与匹配：带空白的 uptime 与 uptime 是同一条命令，不应被白名单拦下。
	if Check([]Layer{{Scope: "令牌", Policy: compiled}}, " uptime ").Denied {
		t.Fatal("首尾空白不应让白名单未命中")
	}
}

func TestDecodeTokenPolicyDefaultsAndFailClosed(t *testing.T) {
	for _, raw := range []string{"", "  ", "{}"} {
		policy := DecodeTokenPolicy(raw)
		compiled, err := policy.Compile()
		if err != nil {
			t.Fatalf("DecodeTokenPolicy(%q) = %#v，无法编译: %v", raw, policy, err)
		}
		if policy.Mode != string(Off) || compiled.Active() {
			t.Fatalf("DecodeTokenPolicy(%q) = %#v，应为 off", raw, policy)
		}
	}
	for _, raw := range []string{`{"mode":"deny","deny":["("]}`, `{"mode":"bogus"}`, `not-json`, `null`, `{"deny":["rm"]}`, `{"mode":null,"deny":["rm"]}`, `{"mode":"","allow":["^uptime$"]}`} {
		policy := DecodeTokenPolicy(raw)
		compiled, err := policy.Compile()
		if err != nil {
			t.Fatalf("损坏策略应回退为可编译的全拒策略: %v", err)
		}
		if !compiled.Active() || !Check([]Layer{{Scope: "令牌", Policy: compiled}}, "uptime").Denied {
			t.Fatalf("DecodeTokenPolicy(%q) 未 fail-closed: %#v", raw, policy)
		}
	}
}

func TestSensitiveEnvAndEnvDenial(t *testing.T) {
	sensitive := SensitiveEnv([]string{"http_proxy", " ", "CUSTOM_KEY"})
	// 名单按「让后续进程加载或执行额外代码」的标准收录常见键：动态库、解释器启动、
	// git/Java/sudo 配置注入各占一族，缺一个键就留一条改命令解释方式的旁路。
	// HTTP_PROXY 是文档写明的合法用途，不在内置名单，经 extra 键追加。
	for _, key := range []string{
		"PATH", "BASH_ENV", "LD_PRELOAD", "LD_AUDIT", "GCONV_PATH",
		"NODE_OPTIONS", "PYTHONHOME", "PYTHONPATH", "PERL5LIB", "RUBYOPT", "RUBYLIB",
		"GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS", "GIT_EXEC_PATH",
		"JAVA_TOOL_OPTIONS", "_JAVA_OPTIONS", "JDK_JAVA_OPTIONS",
		"SUDO_ASKPASS", "OPENSSL_CONF", "SSL_CERT_FILE", "HOSTALIASES", "BASH_XTRACEFD",
	} {
		if _, ok := sensitive[key]; !ok {
			t.Fatalf("%s 不在敏感键集合中", key)
		}
	}
	if _, ok := sensitive["HTTP_PROXY"]; !ok {
		t.Fatal("ONESSH_COMMAND_ENV_KEYS 追加的 http_proxy 应以大写进入集合")
	}
	if _, ok := sensitive["HOME"]; ok {
		t.Fatal("HOME 不应被内置限制")
	}
	if _, denied := EnvDenial(sensitive, []string{"custom_key"}); !denied {
		t.Fatal("自定义敏感键应大小写无关")
	}
	// 回归：拒绝文案按调用方书写的原样键名返回，统一大写会让 unset 一个
	// 历史遗留的小写 path 键变成无效操作。
	if key, denied := EnvDenial(sensitive, []string{"path"}); !denied || key != "path" {
		t.Fatalf("小写残留键应按原样返回: %q, %v", key, denied)
	}

	// map 遍历顺序随机，EnvDenial 必须排序后返回，保证审计原因稳定。
	names := []string{"HOME", "path", "BASH_ENV"}
	var first string
	for i := 0; i < 30; i++ {
		key, denied := EnvDenial(sensitive, names)
		if !denied {
			t.Fatal("应命中敏感键")
		}
		if i == 0 {
			first = key
		} else if key != first {
			t.Fatalf("EnvDenial 结果不稳定: %q vs %q", first, key)
		}
	}
	if first != "BASH_ENV" {
		t.Fatalf("排序后首个敏感键 = %q", first)
	}
	if _, denied := EnvDenial(sensitive, []string{"HOME", "LANG"}); denied {
		t.Fatal("非敏感键不应命中")
	}
}
