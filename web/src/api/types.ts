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
  monitor_enabled: boolean
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
    started_at: number
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
  Tool: string
  Host: { String: string; Valid: boolean }
  ParamsJSON: string
  OK: boolean
  DurationMS: number
  BytesOut: number
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
  monitor_enabled: boolean
}

export type KeyPayload = {
  name: string
  mode: 'generate' | 'import'
  private_key?: string
}

export type TokenPayload = {
  name: string
  all_hosts: boolean
  host_ids?: number[]
}
