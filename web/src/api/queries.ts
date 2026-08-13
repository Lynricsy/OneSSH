import {
  keepPreviousData,
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import { toast } from 'sonner'
import { api, del, post, put, uploadFile } from './client'
import type {
  Audit,
  CommandOutputChunk,
  CommandRun,
  FileEntry,
  Host,
  HostPayload,
  JobStatus,
  KeyPayload,
  MemoryBankStat,
  MemoryRow,
  Metric,
  SSHKey,
  Token,
  TokenPayload,
} from './types'

export const queryKeys = {
  hosts: ['hosts'] as const,
  keys: ['keys'] as const,
  tokens: ['tokens'] as const,
  jobs: ['jobs'] as const,
  jobLogs: (id: string) => ['jobs', id, 'logs'] as const,
  audit: (filter: AuditFilter) => ['audit', filter] as const,
  auditTools: ['audit', 'tools'] as const,
  commandRuns: (filter: CommandRunFilter) => ['command-runs', 'list', filter] as const,
  commandRun: (id: string) => ['command-runs', 'detail', id] as const,
  commandOutput: (id: string, stream: string) => ['command-runs', 'output', id, stream] as const,
  metrics: (hostId: number, hours: number) => ['metrics', hostId, hours] as const,
  sftp: (hostId: number, path: string) => ['sftp', hostId, path] as const,
  /** 记忆列表与统计共用 ['memories'] 前缀：删除后一次前缀失效即可覆盖所有筛选与翻页缓存 */
  memories: ['memories'] as const,
  memoryList: (hostId: number | 'all', q: string, limit: number, offset: number) =>
    ['memories', 'list', hostId, q, limit, offset] as const,
  memoryStats: ['memories', 'stats'] as const,
}

/** 所有 mutation 的统一失败提示；成功提示由调用方按语义给 */
const onError = (e: unknown) => toast.error((e as Error).message)

/** mutation 收敛：成功后失效指定 key + 弹成功 toast */
function useInvalidatingMutation<TVars, TData>(
  fn: (vars: TVars) => Promise<TData>,
  key: readonly unknown[],
  successText?: string,
) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: fn,
    onSuccess: () => {
      if (successText) toast.success(successText)
      void qc.invalidateQueries({ queryKey: key })
    },
    onError,
  })
}

/**
 * 批量操作收敛：后端没有批量端点，循环单条接口。逐条顺序执行而不是 allSettled 并发——
 * 这些都是破坏性写操作，一次打满并发会给网关和远端造成尖峰，出错时也说不清做到了哪一步；
 * 顺序执行下失败列表与实际执行顺序一致。resolve 值是失败的 id 列表，调用方可据此保留选中项以便重试。
 */
function useBatchMutation<TId>(
  fn: (id: TId) => Promise<unknown>,
  key: readonly unknown[],
  verb: string,
) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (ids: TId[]) => {
      const failedIds: TId[] = []
      let firstError: unknown
      for (const id of ids) {
        try {
          await fn(id)
        } catch (error) {
          failedIds.push(id)
          firstError ??= error
        }
      }
      return { failedIds, firstError }
    },
    onSuccess: ({ failedIds, firstError }, ids) => {
      const ok = ids.length - failedIds.length
      if (failedIds.length === 0) toast.success(`已${verb} ${ok} 项`)
      else if (ok === 0) toast.error(`${verb}失败：${(firstError as Error)?.message ?? '未知错误'}`)
      else toast.warning(`已${verb} ${ok} 项，${failedIds.length} 项失败`)
      void qc.invalidateQueries({ queryKey: key })
    },
    onError,
  })
}

/* ── 查询 ───────────────────────────────────────────────────────── */

export const useHosts = () =>
  useQuery({ queryKey: queryKeys.hosts, queryFn: () => api<Host[]>('/hosts') })

export const useKeys = () =>
  useQuery({ queryKey: queryKeys.keys, queryFn: () => api<SSHKey[]>('/keys') })

export const useTokens = () =>
  useQuery({ queryKey: queryKeys.tokens, queryFn: () => api<Token[]>('/tokens') })

export const useJobs = () =>
  useQuery({
    queryKey: queryKeys.jobs,
    queryFn: () => api<JobStatus[]>('/jobs'),
    refetchInterval: 4000,
  })

export const useJobLogs = (id: string, running: boolean) =>
  useInfiniteQuery({
    queryKey: queryKeys.jobLogs(id),
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({
        offset_bytes: String(pageParam),
        limit_bytes: String(128 << 10),
      })
      return api<CommandOutputChunk>(`/jobs/${id}/logs?${params}`)
    },
    initialPageParam: 0,
    getNextPageParam: (last) => (last.complete ? undefined : last.next_offset_bytes),
    enabled: id !== '',
    retry: false,
    refetchInterval: running ? 2000 : false,
  })

/** 审计筛选：缺省即不过滤；tool/token/host 支持多值（OR），ok 单值 */
export type AuditFilter = { tool?: string[]; token?: number[]; host?: string[]; ok?: boolean }

export const useAudit = (filter: AuditFilter = {}) =>
  useQuery({
    queryKey: queryKeys.audit(filter),
    queryFn: () => {
      // 重复参数而不是逗号拼接：主机名允许含逗号，拼起来后端拆不回来
      const params = new URLSearchParams({ limit: '100' })
      for (const tool of filter.tool ?? []) params.append('tool', tool)
      for (const token of filter.token ?? []) params.append('token', String(token))
      for (const host of filter.host ?? []) params.append('host', host)
      if (filter.ok != null) params.set('ok', String(filter.ok))
      return api<Audit[]>(`/audit?${params}`)
    },
    // 切换筛选时旧数据留在原位渐隐，避免每改一个条件表格就塌成骨架屏
    placeholderData: keepPreviousData,
    // 活动页是盯梢用的：新的工具调用几秒内要出现，不能等用户改筛选才重拉
    refetchInterval: 4000,
  })

/**
 * 工具名全集：审计列表只回最近 100 条，从中累积出的选项会漏掉更早出现过的工具，
 * 于是有了独立端点。工具集合是部署期固定的，缓存久一点避免每次进页面都拉。
 */
export const useAuditTools = () =>
  useQuery({
    queryKey: queryKeys.auditTools,
    queryFn: () => api<string[]>('/audit/tools'),
    staleTime: 5 * 60_000,
  })

export type CommandRunFilter = {
  tool?: string[]
  token?: number[]
  host?: string[]
  status?: string[]
  query?: string
}

export const useCommandRuns = (filter: CommandRunFilter = {}) =>
  useInfiniteQuery({
    queryKey: queryKeys.commandRuns(filter),
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({ limit: '100' })
      for (const tool of filter.tool ?? []) params.append('tool', tool)
      for (const token of filter.token ?? []) params.append('token', String(token))
      for (const host of filter.host ?? []) params.append('host', host)
      for (const status of filter.status ?? []) params.append('status', status)
      if (filter.query) params.set('q', filter.query)
      if (pageParam > 0) params.set('before', String(pageParam))
      return api<CommandRun[]>(`/command-runs?${params}`)
    },
    initialPageParam: 0,
    getNextPageParam: (last) => (last.length < 100 ? undefined : last[last.length - 1]?.seq),
    placeholderData: keepPreviousData,
    refetchInterval: 3000,
  })

export const useCommandRun = (id: string | null) =>
  useQuery({
    queryKey: queryKeys.commandRun(id ?? ''),
    queryFn: () => api<CommandRun>(`/command-runs/${id}`),
    enabled: id != null,
    refetchInterval: (query) =>
      (query.state.data as CommandRun | undefined)?.status === 'running' ? 2000 : false,
  })

export const useCommandOutput = (
  id: string,
  stream: 'stdout' | 'stderr' | 'combined',
  enabled: boolean,
  running: boolean,
) =>
  useInfiniteQuery({
    queryKey: queryKeys.commandOutput(id, stream),
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({
        stream,
        offset_bytes: String(pageParam),
        limit_bytes: String(128 << 10),
      })
      return api<CommandOutputChunk>(`/command-runs/${id}/output?${params}`)
    },
    initialPageParam: 0,
    getNextPageParam: (last) => (last.complete ? undefined : last.next_offset_bytes),
    enabled,
    retry: false,
    refetchInterval: running ? 2000 : false,
  })

export const useMetrics = (hostId: number | undefined, hours: number) =>
  useQuery({
    queryKey: queryKeys.metrics(hostId ?? 0, hours),
    queryFn: () => api<Metric[]>(`/metrics/${hostId}?hours=${hours}`),
    enabled: hostId != null,
  })

export const useSftpList = (hostId: number | undefined, path: string) =>
  useQuery({
    queryKey: queryKeys.sftp(hostId ?? 0, path),
    queryFn: () => api<FileEntry[]>(`/sftp/${hostId}/list?path=${encodeURIComponent(path)}`),
    enabled: hostId != null,
    retry: false,
  })

/** hostId 缺省 = 全部记忆库，0 = 全局库，其余 = 该主机库 */
export type MemoryFilter = { hostId?: number; q?: string; limit?: number; offset?: number }

/**
 * 翻页与搜索沿用上一页数据（keepPreviousData）：记忆列表是阅读型页面，
 * 每改一次筛选就整块塌成骨架屏会让人丢失阅读位置，改为原地渐隐更稳。
 */
export const useMemories = ({ hostId, q = '', limit = 50, offset = 0 }: MemoryFilter = {}) =>
  useQuery({
    queryKey: queryKeys.memoryList(hostId ?? 'all', q, limit, offset),
    queryFn: () => {
      const params = new URLSearchParams({ limit: String(limit), offset: String(offset) })
      if (hostId != null) params.set('host_id', String(hostId))
      if (q) params.set('q', q)
      return api<MemoryRow[]>(`/memories?${params}`)
    },
    placeholderData: keepPreviousData,
  })

export const useMemoryStats = () =>
  useQuery({ queryKey: queryKeys.memoryStats, queryFn: () => api<MemoryBankStat[]>('/memories/stats') })

/* ── 主机 ───────────────────────────────────────────────────────── */

export const useSaveHost = (id?: number) =>
  useInvalidatingMutation<HostPayload, Host>(
    (v) => (id ? put<Host>(`/hosts/${id}`, v) : post<Host>('/hosts', v)),
    queryKeys.hosts,
    '主机已保存',
  )

export const useDeleteHost = () =>
  useInvalidatingMutation<number, void>((id) => del(`/hosts/${id}`), queryKeys.hosts, '主机已删除')

export const useDeleteHosts = () =>
  useBatchMutation<number>((id) => del(`/hosts/${id}`), queryKeys.hosts, '删除')

export const useResetFingerprint = () =>
  useInvalidatingMutation<number, unknown>(
    (id) => post(`/hosts/${id}/reset-fingerprint`, {}),
    queryKeys.hosts,
    '指纹已重置',
  )

/** 测试连接不改数据，失败提示由 toast.promise 承担，故不走统一 onError */
export const useTestHost = () =>
  useMutation({ mutationFn: (id: number) => post<{ output?: string }>(`/hosts/${id}/test`, {}) })

/* ── 密钥 / 令牌 / 任务 ─────────────────────────────────────────── */

export const useCreateKey = () =>
  useInvalidatingMutation<KeyPayload, SSHKey>((v) => post<SSHKey>('/keys', v), queryKeys.keys, '密钥已创建')

export const useDeleteKey = () =>
  useInvalidatingMutation<number, void>((id) => del(`/keys/${id}`), queryKeys.keys, '密钥已删除')

export const useDeleteKeys = () =>
  useBatchMutation<number>((id) => del(`/keys/${id}`), queryKeys.keys, '删除')

export const useCreateToken = () =>
  useInvalidatingMutation<TokenPayload, Token>((v) => post<Token>('/tokens', v), queryKeys.tokens)

export const useDeleteToken = () =>
  useInvalidatingMutation<number, void>((id) => del(`/tokens/${id}`), queryKeys.tokens, '令牌已删除')

export const useDeleteTokens = () =>
  useBatchMutation<number>((id) => del(`/tokens/${id}`), queryKeys.tokens, '删除')

export const useKillJob = () =>
  useInvalidatingMutation<string, unknown>(
    (id) => post(`/jobs/${id}/kill`, {}),
    queryKeys.jobs,
    '已发送终止信号',
  )

export const useKillJobs = () =>
  useBatchMutation<string>((id) => post(`/jobs/${id}/kill`, {}), queryKeys.jobs, '终止')

/* ── 记忆 ───────────────────────────────────────────────────────── */

/** 删除同时改变列表与库统计，故失效整个 ['memories'] 前缀而不是单条列表 key */
export const useDeleteMemory = () =>
  useInvalidatingMutation<number, void>(
    (id) => del(`/memories/${id}`),
    queryKeys.memories,
    '记忆已删除',
  )

export const useDeleteMemories = () =>
  useBatchMutation<number>((id) => del(`/memories/${id}`), queryKeys.memories, '删除')

/* ── SFTP 上传 ──────────────────────────────────────────────────── */

export function useUpload(hostId: number | undefined, path: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (file: File) => {
      if (hostId == null) throw new Error('请先选择主机')
      return uploadFile(hostId, path.replace(/\/$/, '') + '/', file)
    },
    onSuccess: () => {
      toast.success('上传完成')
      void qc.invalidateQueries({ queryKey: queryKeys.sftp(hostId ?? 0, path) })
    },
    onError,
  })
}
