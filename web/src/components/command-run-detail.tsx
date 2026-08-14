import { ArrowDown, Check, Copy, WarningCircle } from '@phosphor-icons/react'
import { useState } from 'react'
import { toast } from 'sonner'
import { useCommandOutput, useCommandRun } from '@/api/queries'
import type { CommandRun, CommandRunStatus } from '@/api/types'
import { Badge, Dot } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { EmptyState } from '@/components/ui/empty-state'
import { Spinner } from '@/components/ui/spinner'
import { formatBytes } from '@/lib/format'

function formatTime(ms: number) {
  return new Date(ms).toLocaleString('zh-CN', { hour12: false })
}

function formatDuration(ms: number) {
  if (ms < 1000) return `${ms} ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(2)} s`
  return `${Math.floor(ms / 60_000)}m ${Math.floor((ms % 60_000) / 1000)}s`
}

function RunStatus({ status }: { status: CommandRunStatus }) {
  switch (status) {
    case 'running':
      return (
        <Badge variant="accent">
          <Dot pulse />
          运行中
        </Badge>
      )
    case 'succeeded':
      return <Badge variant="success">成功</Badge>
    case 'failed':
      return <Badge variant="danger">失败</Badge>
    case 'timeout':
      return <Badge variant="warning">已超时</Badge>
    case 'cancelled':
      return <Badge variant="outline">已取消</Badge>
    case 'lost':
      return <Badge variant="warning">已失联</Badge>
  }
}

function CopyableBlock({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(true)
      toast.success('已复制')
      window.setTimeout(() => setCopied(false), 1600)
    } catch {
      toast.error('复制失败，请手动选择文本')
    }
  }
  return (
    <div>
      <div className="mb-1.5 flex items-center justify-between gap-2">
        <p className="text-[11px] tracking-wide text-muted uppercase">{label}</p>
        <button
          type="button"
          onClick={() => void copy()}
          className="rounded-[4px] p-0.5 text-muted transition-colors hover:bg-surface-2 hover:text-text"
          aria-label={`复制${label}`}
        >
          {copied ? <Check size={13} className="text-success" /> : <Copy size={13} />}
        </button>
      </div>
      <pre className="max-h-56 overflow-auto rounded-[8px] bg-surface-2 px-3 py-2.5 font-mono text-[12px] leading-[1.6] break-words whitespace-pre-wrap text-text">
        {value}
      </pre>
    </div>
  )
}

function OutputBlock({
  run,
  stream,
  label,
  fallback,
}: {
  run: CommandRun
  stream: 'stdout' | 'stderr' | 'combined'
  label: string
  fallback?: string
}) {
  const enabled = !run.output_expired && (run.output_available || run.status === 'running')
  const output = useCommandOutput(run.id, stream, enabled, run.status === 'running')
  const content = output.data?.pages.map((page) => page.content).join('') ?? fallback ?? ''
  const total = output.data?.pages[0]?.total_bytes

  return (
    <div>
      <div className="mb-1.5 flex items-center justify-between gap-2">
        <p className="text-[11px] tracking-wide text-muted uppercase">
          {label}
          {total != null ? ` · ${formatBytes(total)}` : ''}
        </p>
        {output.isFetching && <Spinner className="size-3.5 text-muted" />}
      </div>
      {run.output_expired ? (
        <div className="rounded-[8px] border border-border bg-surface-2 px-3 py-3 text-[13px] text-muted">
          输出已超过 7 天保留期，命令、状态和退出码仍会继续保留。
        </div>
      ) : output.isError && !content ? (
        <div className="flex items-start gap-2 rounded-[8px] border border-border bg-surface-2 px-3 py-3 text-[13px] text-muted">
          <WarningCircle className="mt-0.5 shrink-0 text-warning" size={15} />
          <span>
            {run.status === 'running'
              ? '等待第一段输出…'
              : (output.error as Error).message || '输出暂时无法读取'}
          </span>
        </div>
      ) : (
        <>
          <pre className="max-h-72 min-h-12 overflow-auto rounded-[8px] bg-[#0d1117] px-3 py-2.5 font-mono text-[12px] leading-[1.6] break-words whitespace-pre-wrap text-[#d8dee9]">
            {content || '（无输出）'}
          </pre>
          {output.hasNextPage && (
            <Button
              className="mt-2"
              size="sm"
              variant="outline"
              loading={output.isFetchingNextPage}
              onClick={() => void output.fetchNextPage()}
            >
              <ArrowDown size={14} />
              继续读取
            </Button>
          )}
        </>
      )}
    </div>
  )
}

export function CommandRunDetail({ id }: { id: string }) {
  const detail = useCommandRun(id)
  if (detail.isLoading) {
    return (
      <div className="flex min-h-48 items-center justify-center">
        <Spinner />
      </div>
    )
  }
  if (detail.isError || !detail.data) {
    return (
      <EmptyState
        icon={<WarningCircle size={22} />}
        title="执行详情加载失败"
        description={(detail.error as Error)?.message ?? '记录不存在'}
      />
    )
  }
  const run = detail.data
  const outputBytes = run.stdout_bytes + run.stderr_bytes
  return (
    <div className="space-y-4">
      <dl className="grid grid-cols-2 gap-x-4 gap-y-3 text-[13px] sm:grid-cols-4">
        <div>
          <dt className="text-[11px] tracking-wide text-muted uppercase">状态</dt>
          <dd className="mt-1">
            <RunStatus status={run.status} />
          </dd>
        </div>
        <div>
          <dt className="text-[11px] tracking-wide text-muted uppercase">退出码</dt>
          <dd className="mt-1 font-mono tabular-nums">{run.exit_code ?? '—'}</dd>
        </div>
        <div>
          <dt className="text-[11px] tracking-wide text-muted uppercase">耗时</dt>
          <dd className="mt-1 tabular-nums">{formatDuration(run.duration_ms)}</dd>
        </div>
        <div>
          <dt className="text-[11px] tracking-wide text-muted uppercase">输出</dt>
          <dd className="mt-1 tabular-nums">{formatBytes(outputBytes)}</dd>
        </div>
        <div>
          <dt className="text-[11px] tracking-wide text-muted uppercase">主机</dt>
          <dd className="mt-1 truncate" title={run.host}>
            {run.host}
          </dd>
        </div>
        <div>
          <dt className="text-[11px] tracking-wide text-muted uppercase">工具</dt>
          <dd className="mt-1 font-mono">{run.tool}</dd>
        </div>
        <div>
          <dt className="text-[11px] tracking-wide text-muted uppercase">工作目录</dt>
          <dd className="mt-1 truncate font-mono" title={run.cwd}>
            {run.cwd}
          </dd>
        </div>
        <div>
          <dt className="text-[11px] tracking-wide text-muted uppercase">调用令牌</dt>
          <dd className="mt-1 truncate" title={run.token_name ?? '系统'}>
            {run.token_name ?? '系统'}
          </dd>
        </div>
      </dl>

      <CopyableBlock label="命令" value={run.command} />

      {run.error && (
        <div className="rounded-[8px] border border-danger/30 bg-danger/8 px-3 py-2.5 text-[13px] text-danger">
          {run.error}
        </div>
      )}
      {run.output_error && (
        <div className="rounded-[8px] border border-warning/30 bg-warning/8 px-3 py-2.5 text-[13px] text-warning">
          完整输出记录失败：{run.output_error}
        </div>
      )}

      {run.job_id ? (
        <OutputBlock run={run} stream="combined" label="合并日志" />
      ) : (
        <>
          <OutputBlock run={run} stream="stdout" label="stdout" fallback={run.stdout_preview} />
          <OutputBlock run={run} stream="stderr" label="stderr" fallback={run.stderr_preview} />
        </>
      )}

      <div className="grid gap-2 rounded-[8px] border border-border px-3 py-2.5 font-mono text-[11px] text-muted sm:grid-cols-2">
        <span title={run.id}>run_id: {run.id}</span>
        <span>开始: {formatTime(run.started_at)}</span>
        {run.job_id && <span title={run.job_id}>job_id: {run.job_id}</span>}
        {run.finished_at && <span>结束: {formatTime(run.finished_at)}</span>}
      </div>
    </div>
  )
}
