import { useCallback, useEffect, useRef, useState } from 'react'
import {
  ArrowsLeftRight,
  CaretDown,
  CaretUp,
  CheckCircle,
  CloudArrowUp,
  Prohibit,
  Trash,
  WarningCircle,
  X,
} from '@phosphor-icons/react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { fsApi, uploadWithProgress, type Conflict, type Transfer, type UploadHandle } from '@/api/fs'
import { cn } from '@/lib/cn'
import { formatSize } from '@/lib/file-types'

export type UploadTask = {
  id: string
  hostId: number
  hostName: string
  dest: string
  name: string
  size: number
  loaded: number
  status: 'queued' | 'uploading' | 'done' | 'failed' | 'cancelled'
  error?: string
  startedAt?: number
  handle?: UploadHandle
  file: File
  overwrite: boolean
}

const UPLOAD_CONCURRENCY = 3
let seq = 0

/** 上传队列：最多 3 个并发，带进度、可取消；每完成一个刷新所在目录 */
export function useUploadQueue() {
  const qc = useQueryClient()
  const [tasks, setTasks] = useState<UploadTask[]>([])
  const tasksRef = useRef(tasks)
  tasksRef.current = tasks

  const patch = useCallback((id: string, update: Partial<UploadTask>) => {
    setTasks((list) => list.map((t) => (t.id === id ? { ...t, ...update } : t)))
  }, [])

  useEffect(() => {
    const running = tasks.filter((t) => t.status === 'uploading').length
    const next = tasks.filter((t) => t.status === 'queued').slice(0, UPLOAD_CONCURRENCY - running)
    for (const task of next) {
      const handle = uploadWithProgress(task.hostId, task.dest, task.file, (loaded) => patch(task.id, { loaded }), task.overwrite)
      patch(task.id, { status: 'uploading', handle, startedAt: Date.now() })
      handle.promise
        .then(() => {
          patch(task.id, { status: 'done', loaded: task.size })
          void qc.invalidateQueries({ queryKey: ['fs', task.hostId] })
        })
        .catch((error: Error) => {
          const cancelled = error.message === '已取消'
          patch(task.id, { status: cancelled ? 'cancelled' : 'failed', error: error.message })
          if (!cancelled) toast.error(`上传 ${task.name} 失败：${error.message}`)
        })
    }
  }, [tasks, patch, qc])

  const enqueue = useCallback((items: Array<{ hostId: number; hostName: string; dest: string; file: File; overwrite?: boolean }>) => {
    setTasks((list) => [
      ...list,
      ...items.map((item) => ({
        id: `up-${++seq}`,
        hostId: item.hostId,
        hostName: item.hostName,
        dest: item.dest,
        name: item.file.name,
        size: item.file.size,
        loaded: 0,
        status: 'queued' as const,
        file: item.file,
        overwrite: item.overwrite ?? true,
      })),
    ])
  }, [])

  const cancel = useCallback(
    (id: string) => {
      const task = tasksRef.current.find((t) => t.id === id)
      if (!task) return
      if (task.status === 'uploading') task.handle?.abort()
      else if (task.status === 'queued') patch(id, { status: 'cancelled' })
    },
    [patch],
  )

  const clearFinished = useCallback(() => setTasks((list) => list.filter((t) => t.status === 'queued' || t.status === 'uploading')), [])

  return { tasks, enqueue, cancel, clearFinished }
}

/** 服务端复制/移动任务：有任务在跑时每秒轮询，结束后刷新相关目录 */
export function useTransfers() {
  const qc = useQueryClient()
  const seen = useRef(new Map<string, string>())
  const query = useQuery({
    queryKey: ['transfers'],
    queryFn: fsApi.transfers,
    refetchInterval: (q) => ((q.state.data as Transfer[] | undefined)?.some((t) => t.status === 'running') ? 1000 : 10_000),
  })

  useEffect(() => {
    for (const t of query.data ?? []) {
      const prev = seen.current.get(t.id)
      seen.current.set(t.id, t.status)
      if (prev === 'running' && t.status !== 'running') {
        void qc.invalidateQueries({ queryKey: ['fs', t.dst_host_id] })
        if (t.op === 'move' || t.src_host_id === t.dst_host_id) void qc.invalidateQueries({ queryKey: ['fs', t.src_host_id] })
        const verb = t.op === 'move' ? '移动' : '复制'
        if (t.status === 'done') toast.success(`${verb}完成：${t.done_files} 个文件 → ${t.dst_host}`)
        else if (t.status === 'partial') toast.warning(`${verb}部分完成：${t.errors.length} 项失败`)
        else if (t.status === 'failed') toast.error(`${verb}失败：${t.errors[0] ?? '未知错误'}`)
      }
    }
  }, [query.data, qc])

  const start = useCallback(
    async (body: { src_host_id: number; src_paths: string[]; dst_host_id: number; dst_dir: string; op: 'copy' | 'move'; conflict: Conflict }) => {
      try {
        const job = await fsApi.startTransfer(body)
        seen.current.set(job.id, 'running')
        qc.setQueryData<Transfer[]>(['transfers'], (old = []) => [job, ...old])
        void qc.invalidateQueries({ queryKey: ['transfers'] })
        return job
      } catch (error) {
        toast.error((error as Error).message)
        return undefined
      }
    },
    [qc],
  )

  const cancel = useCallback(
    async (id: string, remove = false) => {
      await fsApi.cancelTransfer(id, remove).catch((e: Error) => toast.error(e.message))
      void qc.invalidateQueries({ queryKey: ['transfers'] })
    },
    [qc],
  )

  return { transfers: query.data ?? [], start, cancel }
}

function Bar({ value, tone }: { value: number; tone: string }) {
  return (
    <div className="h-1.5 overflow-hidden rounded-full bg-surface-2">
      <div className="h-full rounded-full transition-[width] duration-300" style={{ width: `${Math.min(100, Math.max(0, value * 100))}%`, background: tone }} />
    </div>
  )
}

const speed = (bytes: number, startedAt?: number) => {
  if (!startedAt) return ''
  const secs = (Date.now() - startedAt) / 1000
  return secs > 0.5 ? `${formatSize(bytes / secs)}/s` : ''
}

/** 右下角浮动面板：上传与服务器间复制/移动统一展示 */
export function TransferPanel({
  uploads,
  transfers,
  onCancelUpload,
  onClearUploads,
  onCancelTransfer,
}: {
  uploads: UploadTask[]
  transfers: Transfer[]
  onCancelUpload: (id: string) => void
  onClearUploads: () => void
  onCancelTransfer: (id: string, remove?: boolean) => void
}) {
  const [open, setOpen] = useState(true)
  const [dismissed, setDismissed] = useState<Set<string>>(new Set())
  const visibleTransfers = transfers.filter((t) => !dismissed.has(t.id) && (t.status === 'running' || Date.now() / 1000 - (t.finished_at ?? 0) < 600))
  const activeUploads = uploads.filter((u) => u.status === 'queued' || u.status === 'uploading').length
  const activeTransfers = visibleTransfers.filter((t) => t.status === 'running').length
  const active = activeUploads + activeTransfers
  // 有新任务时展开；全部结束 4 秒后自动收起成一条，不再遮挡文件列表
  useEffect(() => {
    if (active > 0) {
      setOpen(true)
      return
    }
    const timer = window.setTimeout(() => setOpen(false), 4000)
    return () => window.clearTimeout(timer)
  }, [active])
  if (uploads.length === 0 && visibleTransfers.length === 0) return null

  return (
    <div className="fixed right-4 bottom-4 z-40 w-[380px] max-w-[calc(100vw-2rem)] overflow-hidden rounded-[14px] border border-border bg-surface/95 shadow-pop backdrop-blur">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2.5 border-b border-border px-3.5 py-2.5 text-left"
      >
        <span className="grid size-7 place-items-center rounded-[8px] bg-accent-soft text-accent">
          <ArrowsLeftRight size={15} weight="bold" />
        </span>
        <span className="flex-1 text-[13px] font-semibold text-text">
          传输队列
          <span className="ml-2 text-[12px] font-normal text-muted">
            {activeUploads + activeTransfers > 0 ? `${activeUploads + activeTransfers} 项进行中` : '全部完成'}
          </span>
        </span>
        {open ? <CaretDown size={14} className="text-muted" /> : <CaretUp size={14} className="text-muted" />}
      </button>
      {open && (
        <div className="max-h-[360px] space-y-1.5 overflow-y-auto p-2">
          {visibleTransfers.map((t) => {
            const ratio = t.total_bytes ? t.done_bytes / t.total_bytes : t.total_files ? t.done_files / t.total_files : t.status === 'running' ? 0 : 1
            const tone = t.status === 'failed' ? '#f43f5e' : t.status === 'partial' ? '#f59e0b' : t.status === 'cancelled' ? '#94a3b8' : 'var(--accent)'
            return (
              <div key={t.id} className="rounded-[10px] border border-border bg-surface px-3 py-2">
                <div className="flex items-center gap-2 text-[12px]">
                  <span className={cn('rounded-[5px] px-1.5 py-0.5 text-[10.5px] font-semibold', t.op === 'move' ? 'bg-[var(--accent-2)]/15 text-[var(--accent-2)]' : 'bg-accent-soft text-accent')}>
                    {t.op === 'move' ? '移动' : '复制'}
                  </span>
                  <span className="min-w-0 flex-1 truncate text-text">
                    {t.src_paths.length === 1 ? t.src_paths[0].split('/').pop() : `${t.src_paths.length} 项`}
                    <span className="text-muted"> · {t.src_host} → {t.dst_host}</span>
                  </span>
                  {t.status === 'running' ? (
                    <button type="button" title="取消" onClick={() => onCancelTransfer(t.id)} className="text-muted hover:text-danger">
                      <Prohibit size={14} />
                    </button>
                  ) : (
                    <button
                      type="button"
                      title="移除"
                      onClick={() => {
                        setDismissed((s) => new Set(s).add(t.id))
                        onCancelTransfer(t.id, true)
                      }}
                      className="text-muted hover:text-text"
                    >
                      <X size={13} />
                    </button>
                  )}
                </div>
                <div className="mt-1.5">
                  <Bar value={ratio} tone={tone} />
                </div>
                <p className="mt-1 flex justify-between gap-2 text-[11px] text-muted">
                  <span className="truncate" title={t.current || t.dst_dir}>
                    {t.status === 'running'
                      ? t.current
                        ? `正在处理 ${t.current.split('/').pop()}`
                        : '统计中…'
                      : t.status === 'done'
                        ? `完成 · ${t.done_files} 个文件${t.skipped ? ` · 跳过 ${t.skipped}` : ''}`
                        : t.status === 'cancelled'
                          ? '已取消'
                          : `${t.errors.length} 项失败：${t.errors[0] ?? ''}`}
                  </span>
                  <span className="shrink-0 tabular-nums">
                    {t.total_bytes ? `${formatSize(t.done_bytes)} / ${formatSize(t.total_bytes)}` : `${t.done_files}/${t.total_files}`}
                  </span>
                </p>
              </div>
            )
          })}
          {uploads
            .slice()
            .reverse()
            .map((u) => {
              const tone = u.status === 'failed' ? '#f43f5e' : u.status === 'cancelled' ? '#94a3b8' : u.status === 'done' ? '#22c55e' : 'var(--accent)'
              return (
                <div key={u.id} className="rounded-[10px] border border-border bg-surface px-3 py-2">
                  <div className="flex items-center gap-2 text-[12px]">
                    {u.status === 'done' ? (
                      <CheckCircle size={14} weight="fill" className="text-success" />
                    ) : u.status === 'failed' ? (
                      <WarningCircle size={14} weight="fill" className="text-danger" />
                    ) : (
                      <CloudArrowUp size={14} className="text-accent" />
                    )}
                    <span className="min-w-0 flex-1 truncate text-text" title={u.dest}>
                      {u.name}
                      <span className="text-muted"> → {u.hostName}</span>
                    </span>
                    {(u.status === 'uploading' || u.status === 'queued') && (
                      <button type="button" title="取消" onClick={() => onCancelUpload(u.id)} className="text-muted hover:text-danger">
                        <Prohibit size={14} />
                      </button>
                    )}
                  </div>
                  <div className="mt-1.5">
                    <Bar value={u.size ? u.loaded / u.size : u.status === 'done' ? 1 : 0} tone={tone} />
                  </div>
                  <p className="mt-1 flex justify-between gap-2 text-[11px] text-muted">
                    <span className="truncate">
                      {u.status === 'queued' ? '排队中' : u.status === 'uploading' ? speed(u.loaded, u.startedAt) || '上传中' : u.status === 'done' ? '已完成' : u.error}
                    </span>
                    <span className="shrink-0 tabular-nums">
                      {formatSize(u.loaded)} / {formatSize(u.size)}
                    </span>
                  </p>
                </div>
              )
            })}
          {uploads.some((u) => u.status !== 'queued' && u.status !== 'uploading') && (
            <button type="button" onClick={onClearUploads} className="flex w-full items-center justify-center gap-1.5 py-1 text-[12px] text-muted hover:text-text">
              <Trash size={12} />
              清除已结束的上传
            </button>
          )}
        </div>
      )}
    </div>
  )
}
