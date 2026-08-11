import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { api, del, post, put, uploadFile } from './client'
import type {
  Audit,
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
  audit: ['audit'] as const,
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
 * 批量操作收敛：后端没有批量端点，循环单条接口并全部落定（allSettled，永不 reject），
 * 按成败汇总提示后失效缓存。resolve 值是失败的 id 列表，调用方可据此保留选中项以便重试。
 */
function useBatchMutation<TId>(
  fn: (id: TId) => Promise<unknown>,
  key: readonly unknown[],
  verb: string,
) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (ids: TId[]) => {
      const results = await Promise.allSettled(ids.map((id) => fn(id)))
      return {
        failedIds: ids.filter((_, index) => results[index].status === 'rejected'),
        firstError: results.find((r): r is PromiseRejectedResult => r.status === 'rejected')?.reason,
      }
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

export const useAudit = () =>
  useQuery({ queryKey: queryKeys.audit, queryFn: () => api<Audit[]>('/audit?limit=100') })

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
