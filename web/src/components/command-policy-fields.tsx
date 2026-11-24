import type { CommandPolicyFormValues } from '@/lib/command-policy'
import { splitRules } from '@/lib/command-policy'
import { Field } from '@/components/ui/field'
import { Select } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { Badge } from '@/components/ui/badge'

const modeOptions = [
  { value: 'off' as const, label: 'off · 不检查' },
  { value: 'deny' as const, label: 'deny · 只拦命中的规则' },
  { value: 'allow' as const, label: 'allow · 白名单，默认全拒' },
]

/**
 * 实例级与令牌级共用同一套档位语义：令牌策略只能收紧实例策略，
 * 但输入框本身不做合并——合并只在网关执行时按逻辑 AND 计算。
 */
export function CommandPolicyFields({
  value,
  onChange,
}: {
  value: CommandPolicyFormValues
  onChange: (value: CommandPolicyFormValues) => void
}) {
  const update = (patch: Partial<CommandPolicyFormValues>) => onChange({ ...value, ...patch })
  const denyCount = splitRules(value.deny).length
  const allowCount = splitRules(value.allow).length

  return (
    <div className="space-y-4 rounded-[10px] border border-border bg-surface-2/60 p-3.5">
      <Field
        label="命令策略"
        hint="默认 off，与升级前完全一致；deny 只拦命中的模式，allow 是白名单、空白名单全拒。正则只匹配收到的原始命令，不是 shell 沙箱。"
      >
        {(id) => (
          <Select
            id={id}
            value={value.mode}
            onChange={(mode) => update({ mode })}
            options={modeOptions}
          />
        )}
      </Field>

      {value.mode !== 'off' && (
        <>
          <Field
            label={value.mode === 'allow' ? '拒绝规则（优先于白名单）' : '拒绝规则'}
            hint="每行一条 Go 正则；命中的命令在送到主机前被拒绝。留空表示不加额外拒绝规则。单列表上限 100 条、单条 512 字节，超出保存时会报错。"
          >
            {(id) => (
              <Textarea
                id={id}
                rows={3}
                value={value.deny}
                onChange={(event) => update({ deny: event.target.value })}
                placeholder={'rm\\s+-rf\\s+/\ndocker\\s+system\\s+prune'}
                spellCheck={false}
              />
            )}
          </Field>

          {value.mode === 'allow' && (
            <Field
              label="白名单"
              hint="每行一条 Go 正则，按整条命令精确匹配（无需手写 ^…$，`.` 不跨行、多行命令用 `(?s)`）；未命中任何一条即拒绝。空白名单等于拒绝全部命令，请至少写一条。单列表上限 100 条、单条 512 字节。"
            >
              {(id) => (
                <Textarea
                  id={id}
                  rows={3}
                  value={value.allow}
                  onChange={(event) => update({ allow: event.target.value })}
                  placeholder={'^uptime$\n^df -h$'}
                  spellCheck={false}
                />
              )}
            </Field>
          )}

          <div className="flex flex-wrap items-center gap-2 text-[12px] text-muted">
            <Badge variant="outline">拒绝 {denyCount} 条</Badge>
            {value.mode === 'allow' && <Badge variant="outline">白名单 {allowCount} 条</Badge>}
            {value.mode === 'deny' && denyCount === 0 && (
              <span className="text-warning">deny 没有规则时等同于 off</span>
            )}
            {value.mode === 'allow' && allowCount === 0 && (
              <span className="text-danger">空白名单会拒绝全部命令</span>
            )}
          </div>
        </>
      )}
    </div>
  )
}
