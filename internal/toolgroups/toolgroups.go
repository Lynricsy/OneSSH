// Package toolgroups 定义 MCP 工具的分组。
//
// 分组是 ONESSH_DISABLED_TOOLS 与令牌级 denylist 的唯一取值来源，也是 mcpserver 决定
// 「注册哪些工具」和「服务器提示词里能提到哪些工具」的依据。之所以按组而不是按单个工具名
// 开关：模型在调用前只能读到 instructions 与 tools/list，两者必须一致；按组裁剪才能让
// 提示词里的整段说明跟着工具一起消失，而不是留下半句指向不存在的工具。
package toolgroups

import (
	"fmt"
	"strings"
)

type Group string

const (
	Hosts   Group = "hosts"
	Exec    Group = "exec"
	Fanout  Group = "fanout"
	Jobs    Group = "jobs"
	Files   Group = "files"
	Search  Group = "search"
	Monitor Group = "monitor"
	Image   Group = "image"
	Memory  Group = "memory"
)

// Definition 把分组与它暴露的工具绑在一起：错误提示、文档和 mcpserver 的覆盖测试都读这张表，
// 新增工具时必须同时归组，否则该工具既无法被运维关闭，也不会出现在任何说明里。
type Definition struct {
	Group Group
	Tools []string
}

// All 的顺序决定错误提示与文档中的展示次序。
var All = []Definition{
	{Hosts, []string{"hosts_list", "hosts_manage_list", "host_create", "host_update", "host_test", "host_reset_fingerprint", "host_delete"}},
	{Exec, []string{"exec", "session_env", "output_read"}},
	{Fanout, []string{"exec_many"}},
	{Jobs, []string{"job_start", "job_list", "job_status", "job_logs", "job_kill"}},
	{Files, []string{"file_read", "file_write", "file_edit", "file_list", "file_transfer"}},
	{Search, []string{"grep", "find"}},
	{Monitor, []string{"host_status"}},
	{Image, []string{"image_view"}},
	{Memory, []string{"memory_remember", "memory_recall", "memory_list", "memory_update", "memory_forget", "memory_stats", "memory_sleep"}},
}

// Disabled 是被禁用的分组集合；零值（nil）表示全部启用。
type Disabled map[Group]bool

// Enabled 判断某个分组当前是否对外暴露。
func (d Disabled) Enabled(g Group) bool { return !d[g] }

// Names 按 All 的顺序列出被禁用的分组名，用于启动日志。
func (d Disabled) Names() []string {
	names := make([]string, 0, len(d))
	for _, def := range All {
		if d[def.Group] {
			names = append(names, string(def.Group))
		}
	}
	return names
}

// HidesTool 判断工具名是否落在被禁用的分组里，供 MCP Apps 卡片等按工具名组织的能力复用。
func (d Disabled) HidesTool(name string) bool {
	if len(d) == 0 {
		return false
	}
	for _, def := range All {
		if !d[def.Group] {
			continue
		}
		for _, tool := range def.Tools {
			if tool == name {
				return true
			}
		}
	}
	return false
}

// Parse 解析逗号分隔的分组名。未知分组直接报错而不是静默忽略：拼错一个名字就意味着
// 运维以为已经关闭的工具仍然暴露在 tools/list 里。
func Parse(spec string) (Disabled, error) {
	var disabled Disabled
	for _, field := range strings.Split(spec, ",") {
		name := strings.ToLower(strings.TrimSpace(field))
		if name == "" {
			continue
		}
		group, ok := lookup(name)
		if !ok {
			return nil, fmt.Errorf("未知工具组 %q；可选值：%s", name, strings.Join(names(), "、"))
		}
		if disabled == nil {
			disabled = Disabled{}
		}
		disabled[group] = true
	}
	return disabled, nil
}

func lookup(name string) (Group, bool) {
	for _, def := range All {
		if string(def.Group) == name {
			return def.Group, true
		}
	}
	return "", false
}

func names() []string {
	all := make([]string, 0, len(All))
	for _, def := range All {
		all = append(all, string(def.Group))
	}
	return all
}

// ParseList 解析分组名列表（令牌 / OAuth 同意页传入的 JSON 数组）。规则与 Parse 相同：
// 未知分组名直接报错，避免静默忽略后以为已经关掉的工具仍暴露给该令牌。
func ParseList(groupNames []string) (Disabled, error) {
	var disabled Disabled
	for _, field := range groupNames {
		name := strings.ToLower(strings.TrimSpace(field))
		if name == "" {
			continue
		}
		group, ok := lookup(name)
		if !ok {
			return nil, fmt.Errorf("未知工具组 %q；可选值：%s", name, strings.Join(names(), "、"))
		}
		if disabled == nil {
			disabled = Disabled{}
		}
		disabled[group] = true
	}
	return disabled, nil
}

// NormalizeList 校验分组名并把结果按 All 顺序去重，写入数据库时保持稳定序列化。
func NormalizeList(groupNames []string) ([]string, error) {
	disabled, err := ParseList(groupNames)
	if err != nil {
		return nil, err
	}
	return disabled.Names(), nil
}

// Union 合并多份禁用集合；实例级与令牌级 denylist 取并集，令牌无法重新打开进程已关闭的组。
func Union(parts ...Disabled) Disabled {
	var out Disabled
	for _, part := range parts {
		for group, on := range part {
			if !on {
				continue
			}
			if out == nil {
				out = Disabled{}
			}
			out[group] = true
		}
	}
	return out
}

// GroupNames 返回 All 中的全部分组名，供管理 API / 授权页展示可选值。
func GroupNames() []string { return names() }

// AllDisabled 返回禁用全部分组的集合。令牌 denylist 解析失败时用它做 fail-closed 回退，
// 避免静默忽略后放开本应关闭的工具。
func AllDisabled() Disabled {
	out := make(Disabled, len(All))
	for _, def := range All {
		out[def.Group] = true
	}
	return out
}
