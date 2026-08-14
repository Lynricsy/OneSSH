/** 后端契约类型：字段名与 Go 侧 JSON tag 一一对应，禁止改名 */

export type Host = {
  id: number
  name: string
  addr: string
  port: number
  username: string
  auth_type: 'key' | 'password'
  key_id?: number
  hostkey_fp?: string
  /** 跳板主机 id；缺省表示直连 */
  jump_host_id?: number
  monitor_enabled: boolean
  /** 分组标签，后端保证非 null */
  tags: string[]
  created_at: number
}

export type SSHKey = {
  id: number
  name: string
  public_key: string
  created_at: number
}

export type Token = {
  id: number
  name: string
  all_hosts: boolean
  manage_hosts: boolean
  host_ids?: number[]
  created_at: number
  /** 仅创建响应携带的一次性明文 */
  token?: string
}

export type JobStatus = {
  job: {
    id: string
    host_id: number
    command: string
    cwd: string
    status: string
    exit_code?: number
    started_at: number
    finished_at?: number
  }
  log_bytes: number
}

export type Metric = {
  host_id: number
  ts: number
  cpu_pct?: number
  mem_used_kb?: number
  mem_total_kb?: number
  load1?: number
  disks: Array<{ mount: string; used_kb: number; total_kb: number }>
}

export type FileEntry = {
  name: string
  size: number
  mode: string
  mtime: number
  directory: boolean
  symlink_target?: string
}

export type Audit = {
  ID: number
  Ts: number
  TokenID: { Int64: number; Valid: boolean }
  TokenName: { String: string; Valid: boolean }
  Tool: string
  Host: { String: string; Valid: boolean }
  ParamsJSON: string
  RunIDs?: string[]
  OK: boolean
  DurationMS: number
  BytesOut: number
}

export type CommandRunStatus =
  | 'running'
  | 'succeeded'
  | 'failed'
  | 'timeout'
  | 'cancelled'
  | 'lost'

export type CommandRun = {
  seq: number
  id: string
  token_id: number | null
  token_name: string | null
  tool: 'exec' | 'exec_many' | 'job_start' | string
  host_id: number | null
  host: string
  command: string
  cwd: string
  session: string | null
  job_id: string | null
  status: CommandRunStatus
  exit_code: number | null
  stdout_preview?: string
  stderr_preview?: string
  stdout_bytes: number
  stderr_bytes: number
  output_available: boolean
  output_expired: boolean
  output_error: string | null
  error: string | null
  started_at: number
  finished_at: number | null
  duration_ms: number
}

export type CommandOutputChunk = {
  content: string
  offset_bytes: number
  next_offset_bytes: number
  total_bytes: number
  complete: boolean
}

/** 记忆行：host_id 为 null 即全局记忆库；host_name 由后端 LEFT JOIN 带出 */
export type MemoryRow = {
  id: number
  host_id: number | null
  host_name: string | null
  content: string
  source: string
  /** 0–1 连续值 */
  importance: number
  /** stated | inferred | tool | unknown，后端可扩展故不收窄为联合类型 */
  veracity: string
  created_at: number
  updated_at: number
  recall_count: number
}

/** 按记忆库聚合的统计；host_id 为 null 即全局库 */
export type MemoryBankStat = {
  host_id: number | null
  host_name: string | null
  count: number
  /** 已生成向量的条数，未配置 embedding 时恒为 0 */
  embedded: number
  last_written: number | null
}

/** SSE 事件流载荷 */
export type StreamEvent = {
  type: string
  ts: number
  data: unknown
}

/** 主机表单载荷（创建/编辑共用） */
export type HostPayload = {
  name: string
  username: string
  addr: string
  port: number
  auth_type: 'key' | 'password'
  key_id?: number
  password?: string
  /** 跳板主机名；空/省略表示直连 */
  jump_host?: string
  monitor_enabled: boolean
  tags?: string[]
}

export type KeyPayload = {
  name: string
  mode: 'generate' | 'import'
  private_key?: string
}

export type TokenPayload = {
  name: string
  all_hosts: boolean
  manage_hosts: boolean
  host_ids?: number[]
}

/** OAuth 授权页上下文：GET /oauth/authorization?<浏览器入口的原始查询串> */
export type OAuthAuthorizationInfo = {
  client_name: string
  client_uri?: string
  redirect_uri: string
  requested_scopes: string[]
  hosts: Host[]
}

/** 授权决定：POST /oauth/authorization，query 必须是未经改写的原始查询串 */
export type OAuthDecisionPayload = {
  query: string
  decision: 'approve' | 'deny'
  all_hosts: boolean
  manage_hosts: boolean
  host_ids?: number[]
}

/** 无论批准还是拒绝，后端都给出下一跳；拒绝时带 error=access_denied */
export type OAuthDecisionResult = { redirect_uri: string }
