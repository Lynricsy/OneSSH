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
 * 只提交当前档位用到的规则组：隐藏的规则组里若有一条拼错的正则，会让每次保存/
 * 批准都收到 400，而用户在当前档位看不见那个输入框，无从修复。未提交的草稿保留在
 * 表单本地状态里，切回档位时仍在；保存后从服务端回读则只含生效档位的规则。
 * 与后端语义一致：档位切到 off 后，遗留规则本就不生效。
 */
export function commandPolicyFromForm(values: CommandPolicyFormValues): CommandPolicy {
  return {
    mode: values.mode,
    deny: values.mode === 'off' ? [] : splitRules(values.deny),
    allow: values.mode === 'allow' ? splitRules(values.allow) : [],
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
