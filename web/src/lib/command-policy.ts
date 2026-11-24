import type { CommandPolicy, CommandPolicyMode } from '@/api/types'

/** 表单用多行文本编辑规则；提交时再拆成数组，避免 textarea ↔ 数组来回同步 */
export type CommandPolicyFormValues = {
  mode: CommandPolicyMode
  deny: string
  allow: string
}

export const defaultCommandPolicyForm: CommandPolicyFormValues = {
  mode: 'off',
  deny: '',
  allow: '',
}

/** 后端空串/缺省都按 off 处理，前端同样兜底，避免 Select 出现空值 */
export function commandPolicyToForm(policy?: CommandPolicy): CommandPolicyFormValues {
  const mode: CommandPolicyMode =
    policy?.mode === 'deny' || policy?.mode === 'allow' ? policy.mode : 'off'
  return {
    mode,
    deny: (policy?.deny ?? []).join('\n'),
    allow: (policy?.allow ?? []).join('\n'),
  }
}

export function splitRules(text: string): string[] {
  return text
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
}

/**
 * 即使当前档位用不到某一组规则也原样提交：切回该档位时规则还在，
 * 后端 Normalize 会把空行去掉并保存两份规则。
 */
export function commandPolicyFromForm(values: CommandPolicyFormValues): CommandPolicy {
  return {
    mode: values.mode,
    deny: splitRules(values.deny),
    allow: splitRules(values.allow),
  }
}

export function commandPolicyActive(policy?: CommandPolicy): boolean {
  // 与后端 Policy.Active 对齐：deny 没有规则时拦不住任何命令，不应当显示成已启用。
  return policy?.mode === 'allow' || (policy?.mode === 'deny' && (policy.deny?.length ?? 0) > 0)
}

export function commandPolicySummary(policy?: CommandPolicy): string {
  if (!commandPolicyActive(policy)) return ''
  const deny = policy?.deny.length ?? 0
  const allow = policy?.allow.length ?? 0
  return policy?.mode === 'allow'
    ? `白名单 ${allow} 条${deny > 0 ? ` · 拒绝 ${deny} 条` : ''}`
    : `拒绝 ${deny} 条`
}
