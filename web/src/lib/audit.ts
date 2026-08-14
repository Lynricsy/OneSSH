import type { Audit } from '@/api/types'

/** 把审计里存的 params_json 解成对象；坏 JSON 不当成崩溃，调用方按空参数处理 */
export function parseAuditParams(raw: string): Record<string, unknown> | null {
  if (!raw.trim()) return null
  try {
    const value: unknown = JSON.parse(raw)
    if (!value || typeof value !== 'object' || Array.isArray(value)) return null
    return value as Record<string, unknown>
  } catch {
    return null
  }
}

function textField(params: Record<string, unknown>, key: string): string {
  const value = params[key]
  return typeof value === 'string' ? value.trim() : ''
}

/**
 * 从一次调用的参数里抽出人能扫一眼的摘要：exec / job_start 用 command，
 * 文件操作用 path，grep/find 带上 pattern。和后端 callSummary 保持同一优先序。
 */
export function auditSummary(item: Pick<Audit, 'ParamsJSON'>): string {
  const params = parseAuditParams(item.ParamsJSON)
  if (!params) return ''

  const command = textField(params, 'command')
  if (command) return command

  const path = textField(params, 'path')
  const pattern = textField(params, 'pattern')
  if (path && pattern) return `${pattern}  ${path}`
  if (path) return path

  const src = textField(params, 'src_path')
  const dst = textField(params, 'dst_path')
  if (src && dst) return `${src} → ${dst}`
  if (src) return src

  for (const key of ['query', 'pattern', 'content', 'job_id', 'artifact_id'] as const) {
    const value = textField(params, key)
    if (value) return value
  }
  return ''
}

export function formatAuditParam(value: unknown): string {
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

/** command 已单独成块展示，参数条目里跳过；空值一并过滤 */
export function auditParamEntries(item: Pick<Audit, 'ParamsJSON'>): [string, unknown][] {
  const params = parseAuditParams(item.ParamsJSON)
  if (!params) return []
  const command = typeof params.command === 'string' ? params.command.trim() : ''
  return Object.entries(params).filter(
    ([key, value]) => !(key === 'command' && command) && value != null && value !== '',
  )
}
