import { Broadcast, Check, Copy, MagnifyingGlass, Pulse, WarningCircle } from '@phosphor-icons/react'
import { toast } from 'sonner'
import { AnimatePresence, motion, useReducedMotion } from 'motion/react'
import { useMemo, useState } from 'react'
import {
  useAudit,
  useAuditTools,
  useCommandRuns,
  useHosts,
  useTokens,
  type AuditFilter,
} from '@/api/queries'
import type { Audit, StreamEvent } from '@/api/types'
import { Badge, Dot } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { CommandRunDetail } from '@/components/command-run-detail'
import { DataTable, type Column } from '@/components/ui/data-table'
import { EmptyState } from '@/components/ui/empty-state'
import { Input } from '@/components/ui/input'
import { MultiSelect } from '@/components/ui/multi-select'
import { PageHeader } from '@/components/ui/page-header'
import { PageTransition } from '@/components/ui/page-transition'
import { Select } from '@/components/ui/select'
import { Sheet } from '@/components/ui/sheet'
import { Spinner } from '@/components/ui/spinner'
import { useEventStream } from '@/hooks/use-event-stream'
import { auditSummary, formatAuditParam, parseAuditParams } from '@/lib/audit'
import { cn } from '@/lib/cn'
import { formatBytes } from '@/lib/format'

/** 审计与事件流的时间戳是「毫秒」，与 lib/format 的「秒」契约不同，故本页单独格式化 */
const clock = (ms: number) => new Date(ms).toLocaleTimeString('zh-CN', { hour12: false })

/** 结果筛选仍是单选（只有成功/失败两个值），故保留 'all' 哨兵；工具/令牌/主机改为多选数组 */
const ALL = 'all'
const MAX_AUDIT_FILTER_VALUES = 100

/** 列定义与渲染无关联状态，放模块级避免每次渲染重建 */
const auditColumns: Column<Audit>[] = [
  {
    key: 'Ts',
    title: '时间',
    // 审计日志没有时间就失去一半价值，窄屏也保留，靠收紧单元格内边距让出空间
    className: 'w-[84px] font-mono text-[12px] text-muted tabular-nums',
    render: (item) => clock(item.Ts),
  },
  {
    key: 'Tool',
    title: '工具',
    className: 'w-[7.5rem] font-mono text-[13px]',
    render: (item) => (
      <span className="block truncate" title={item.Tool}>
        {item.Tool}
      </span>
    ),
  },
  {
    key: 'ParamsJSON',
    title: '调用',
    // 这列才是「Agent 跑了什么」：命令、路径、检索式。固定布局下它不设宽度、吃掉剩余空间，
    // 窄屏也留着，靠 truncate + title 看全句
    render: (item) => {
      const summary = auditSummary(item)
      return (
        <span className="block truncate font-mono text-[12px] text-muted" title={summary || undefined}>
          {summary || '—'}
        </span>
      )
    },
  },
  {
    key: 'TokenName',
    title: '调用令牌',
    className: 'hidden w-[10rem] text-muted xl:table-cell',
    render: (item) => {
      const label = tokenLabel(item)
      return (
        <span className="block truncate" title={label}>
          {label}
        </span>
      )
    },
  },
  {
    key: 'Host',
    title: '主机',
    className: 'hidden w-[9rem] lg:table-cell text-muted',
    render: (item) => {
      const host = item.Host?.Valid ? item.Host.String : '—'
      return (
        <span className="block truncate" title={host}>
          {host}
        </span>
      )
    },
  },
  {
    key: 'OK',
    title: '调用结果',
    className: 'w-[80px]',
    // 一屏几十行里全是绿 badge 会淹没真正需要注意的失败，成功态降级为中性文字 + 状态点
    render: (item) =>
      item.OK ? (
        <span className="inline-flex items-center gap-1.5 text-[12px] text-muted">
          <Dot className="text-success" />
          成功
        </span>
      ) : (
        <Badge variant="danger">失败</Badge>
      ),
  },
  {
    key: 'BytesOut',
    title: '输出',
    className: 'hidden lg:table-cell w-[88px] text-right text-muted tabular-nums',
    render: (item) => (item.BytesOut ? formatBytes(item.BytesOut) : '—'),
  },
  {
    key: 'DurationMS',
    title: '耗时',
    className: 'w-[92px] text-right tabular-nums',
    // 四位数以上的毫秒既难读又会撑宽列，统一在 1s 处进位
    render: (item) => formatDuration(item.DurationMS),
  },
]

function tokenLabel(item: Audit): string {
  const id = item.TokenID?.Valid ? `#${item.TokenID.Int64}` : ''
  if (item.TokenName?.Valid) return `${item.TokenName.String}${id ? ` · ${id}` : ''}`
  if (id) return `已删除令牌 · ${id}`
  return '系统'
}

function formatDuration(ms: number): string {
  return ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(2)} s`
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value)
}

function EventPayload({ event }: { event: StreamEvent }) {
  const data = event.data
  if (event.type === 'tool_call' && isRecord(data)) {
    const summary = typeof data.summary === 'string' ? data.summary : ''
    const host = typeof data.host === 'string' ? data.host : ''
    const duration = typeof data.duration_ms === 'number' ? formatDuration(data.duration_ms) : ''
    const bits = [data.ok === false ? '失败' : '成功', host, duration].filter(Boolean)
    return (
      <div className="mt-1.5 space-y-1">
        {summary ? (
          <p className="font-mono text-[12px] leading-[1.6] break-words text-text">{summary}</p>
        ) : null}
        <p className="text-[12px] text-muted">{bits.join(' · ')}</p>
      </div>
    )
  }
  if (event.type === 'command_started' && isRecord(data)) {
    const command = typeof data.command === 'string' ? data.command : ''
    const host = typeof data.host === 'string' ? data.host : ''
    const runID = typeof data.run_id === 'string' ? data.run_id.slice(0, 8) : ''
    return (
      <div className="mt-1.5 space-y-1">
        <p className="font-mono text-[12px] leading-[1.6] break-words text-text">{command}</p>
        <p className="text-[12px] text-muted">{['开始执行', host, runID].filter(Boolean).join(' · ')}</p>
      </div>
    )
  }
  if (event.type === 'command_output' && isRecord(data)) {
    const content = typeof data.data === 'string' ? data.data : ''
    const stream = typeof data.stream === 'string' ? data.stream : 'output'
    const runID = typeof data.run_id === 'string' ? data.run_id.slice(0, 8) : ''
    return (
      <div className="mt-1.5">
        <p className="mb-1 text-[11px] font-mono text-muted">
          {[stream, runID].filter(Boolean).join(' · ')}
        </p>
        <pre className="max-h-24 overflow-auto font-mono text-[12px] leading-[1.6] break-words whitespace-pre-wrap text-text">
          {content}
        </pre>
      </div>
    )
  }
  if (event.type === 'command_finished' && isRecord(data)) {
    const status = typeof data.status === 'string' ? data.status : ''
    const exitCode = typeof data.exit_code === 'number' ? `退出码 ${data.exit_code}` : ''
    const host = typeof data.host === 'string' ? data.host : ''
    const runID = typeof data.run_id === 'string' ? data.run_id.slice(0, 8) : ''
    return (
      <p className="mt-1.5 text-[12px] text-muted">
        {[status, exitCode, host, runID].filter(Boolean).join(' · ')}
      </p>
    )
  }
  return (
    <pre className="mt-1.5 max-h-24 overflow-auto font-mono text-[12px] leading-[1.6] break-words whitespace-pre-wrap text-muted">
      {JSON.stringify(event.data)}
    </pre>
  )
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
          aria-label={`复制${label}`}
          className="rounded-[4px] p-0.5 text-muted transition-colors hover:bg-surface-2 hover:text-text"
        >
          {copied ? <Check size={13} className="text-success" /> : <Copy size={13} />}
        </button>
      </div>
      <pre className="rounded-[8px] bg-surface-2 px-3 py-2.5 font-mono text-[12px] leading-[1.6] break-words whitespace-pre-wrap text-text">
        {value}
      </pre>
    </div>
  )
}

/** command 已单独成块展示，参数条目里跳过；空值一并过滤 */
function auditParamEntries(item: Audit): [string, unknown][] {
  const params = parseAuditParams(item.ParamsJSON)
  if (!params) return []
  const command = typeof params.command === 'string' ? params.command.trim() : ''
  return Object.entries(params).filter(
    ([key, value]) => !(key === 'command' && command) && value != null && value !== '',
  )
}

function ParamsList({ entries }: { entries: [string, unknown][] }) {
  return (
    <dl className="divide-y divide-border rounded-[8px] border border-border">
      {entries.map(([key, value]) => (
        <div key={key} className="grid gap-1 px-3 py-2 sm:grid-cols-[8rem_1fr] sm:gap-3">
          <dt className="font-mono text-[12px] text-muted">{key}</dt>
          <dd className="font-mono text-[12px] leading-[1.6] break-words whitespace-pre-wrap">
            {formatAuditParam(value)}
          </dd>
        </div>
      ))}
    </dl>
  )
}

function AuditDetail({ item }: { item: Audit }) {
  const params = parseAuditParams(item.ParamsJSON)
  const command = params && typeof params.command === 'string' ? params.command.trim() : ''
  const summary = command ? '' : auditSummary(item)
  const entries = auditParamEntries(item)

  return (
    <div className="space-y-4">
      <dl className="grid grid-cols-2 gap-x-4 gap-y-3 text-[13px] sm:grid-cols-3">
        <div>
          <dt className="text-[11px] tracking-wide text-muted uppercase">结果</dt>
          <dd className="mt-1">
            {item.OK ? (
              <span className="inline-flex items-center gap-1.5 text-muted">
                <Dot className="text-success" />
                成功
              </span>
            ) : (
              <Badge variant="danger">失败</Badge>
            )}
          </dd>
        </div>
        <div>
          <dt className="text-[11px] tracking-wide text-muted uppercase">耗时</dt>
          <dd className="mt-1 tabular-nums">{formatDuration(item.DurationMS)}</dd>
        </div>
        <div>
          <dt className="text-[11px] tracking-wide text-muted uppercase">输出</dt>
          <dd className="mt-1 tabular-nums text-muted">
            {item.BytesOut ? formatBytes(item.BytesOut) : '—'}
          </dd>
        </div>
        <div className="col-span-2 sm:col-span-1">
          <dt className="text-[11px] tracking-wide text-muted uppercase">令牌</dt>
          <dd className="mt-1 truncate" title={tokenLabel(item)}>
            {tokenLabel(item)}
          </dd>
        </div>
      </dl>

      {command ? (
        <CopyableBlock label="命令" value={command} />
      ) : summary ? (
        <CopyableBlock label="调用" value={summary} />
      ) : null}

      {entries.length > 0 ? (
        <div>
          <p className="mb-1.5 text-[11px] tracking-wide text-muted uppercase">参数</p>
          <ParamsList entries={entries} />
        </div>
      ) : !command && !summary ? (
        <p className="text-[13px] text-muted">此次调用没有记录额外参数。</p>
      ) : null}
    </div>
  )
}

/**
 * 匹配到命令执行记录时，状态/耗时/输出/令牌/命令已由 CommandRunDetail 展示，
 * 审计侧只补它没有的：工具调用级结果（与命令退出码语义不同）和原始调用参数。
 */
function AuditSupplement({ item }: { item: Audit }) {
  const entries = auditParamEntries(item)
  return (
    <section className="space-y-3 rounded-[8px] border border-border px-3 py-2.5">
      <div className="flex items-center justify-between gap-3">
        <p className="text-[11px] tracking-wide text-muted uppercase">审计信息</p>
        {item.OK ? (
          <span className="inline-flex items-center gap-1.5 text-[12px] text-muted">
            <Dot className="text-success" />
            调用成功
          </span>
        ) : (
          <Badge variant="danger">调用失败</Badge>
        )}
      </div>
      {entries.length > 0 ? (
        <ParamsList entries={entries} />
      ) : (
        <p className="text-[12px] text-muted">此次调用没有记录额外参数。</p>
      )}
    </section>
  )
}

const commandTools = new Set(['exec', 'exec_many', 'job_start'])

function AuditSheetContent({ item }: { item: Audit }) {
  const params = parseAuditParams(item.ParamsJSON)
  const command = params && typeof params.command === 'string' ? params.command.trim() : ''
  const explicitRunIDs = item.RunIDs ?? []
  const needsLegacyMatch = commandTools.has(item.Tool) && explicitRunIDs.length === 0 && command !== ''
  const candidates = useCommandRuns(
    {
      tool: [item.Tool],
      token: item.TokenID?.Valid ? [item.TokenID.Int64] : undefined,
      host: item.Host?.Valid ? [item.Host.String] : undefined,
      query: command,
    },
    needsLegacyMatch,
  )
  const inferredRunIDs = useMemo(() => {
    if (!needsLegacyMatch) return []
    const earliest = item.Ts - Math.max(0, item.DurationMS) - 5_000
    const latest = item.Ts + 2_000
    return (candidates.data?.pages.flat() ?? [])
      .filter(
        (run) =>
          run.command.trim() === command &&
          run.started_at >= earliest &&
          run.started_at <= latest,
      )
      .sort((left, right) => left.started_at - right.started_at)
      .map((run) => run.id)
  }, [candidates.data, command, item.DurationMS, item.Ts, needsLegacyMatch])
  const runIDs = explicitRunIDs.length > 0 ? explicitRunIDs : inferredRunIDs
  const [preferredRunID, setPreferredRunID] = useState('')
  const activeRunID = runIDs.includes(preferredRunID) ? preferredRunID : runIDs[0]

  if (needsLegacyMatch && candidates.isLoading) {
    return (
      <div className="flex min-h-56 items-center justify-center gap-2 text-[13px] text-muted">
        <Spinner className="size-4" />
        正在关联命令执行记录…
      </div>
    )
  }

  if (activeRunID) {
    return (
      <div className="space-y-4">
        {explicitRunIDs.length === 0 && (
          <div className="rounded-[8px] border border-border bg-surface-2 px-3 py-2 text-[12px] text-muted">
            这是关联字段加入前的旧审计记录，已按命令、令牌和执行时间匹配。
          </div>
        )}
        {runIDs.length > 1 && (
          <div>
            <p className="mb-2 text-[11px] tracking-wide text-muted uppercase">
              批量执行 · {runIDs.length} 台主机
            </p>
            <div className="flex flex-wrap gap-2" role="tablist" aria-label="选择命令执行记录">
              {runIDs.map((runID, index) => (
                <Button
                  key={runID}
                  size="sm"
                  variant={runID === activeRunID ? 'primary' : 'outline'}
                  role="tab"
                  aria-selected={runID === activeRunID}
                  onClick={() => setPreferredRunID(runID)}
                >
                  执行 {index + 1} · {runID.slice(0, 8)}
                </Button>
              ))}
            </div>
          </div>
        )}
        <CommandRunDetail id={activeRunID} />
        <AuditSupplement item={item} />
      </div>
    )
  }

  return (
    <div className="space-y-4">
      {needsLegacyMatch && !candidates.isError && (
        <div className="flex items-start gap-2 rounded-[8px] border border-warning/30 bg-warning/8 px-3 py-2.5 text-[13px] text-warning">
          <WarningCircle className="mt-0.5 shrink-0" size={15} />
          <span>这条旧审计记录没有找到可关联的完整输出，仍可查看当时记录的调用参数。</span>
        </div>
      )}
      <AuditDetail item={item} />
    </div>
  )
}

export function ActivityPage() {
  const { events, status } = useEventStream()
  const [tool, setTool] = useState<string[]>([])
  const [token, setToken] = useState<number[]>([])
  const [host, setHost] = useState<string[]>([])
  const [result, setResult] = useState(ALL)
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState<Audit | null>(null)
  const filter: AuditFilter = {
    tool,
    token,
    host,
    ok: result === ALL ? undefined : result === 'ok',
  }
  const audit = useAudit(filter)
  const auditTools = useAuditTools()
  const hosts = useHosts()
  const tokens = useTokens()
  const needle = query.trim().toLowerCase()
  const rows = useMemo(() => {
    const list = audit.data ?? []
    if (!needle) return list
    // 只在当前页 100 条里搜命令/参数：审计接口没有全文检索，避免把「没搜到」说成「从没发生」
    return list.filter((row) => {
      const hay = `${row.Tool} ${auditSummary(row)} ${row.ParamsJSON} ${row.Host?.Valid ? row.Host.String : ''}`
      return hay.toLowerCase().includes(needle)
    })
  }, [audit.data, needle])
  const filtered = tool.length > 0 || token.length > 0 || host.length > 0 || result !== ALL || needle !== ''

  // 全量列表请求加载或失败时仍从当前审计结果回退；已选项也始终保留在选项中。
  const toolNames = new Set(auditTools.data ?? [])
  for (const row of audit.data ?? []) toolNames.add(row.Tool)
  for (const name of tool) toolNames.add(name)
  const toolOptions = [...toolNames].sort().map((name) => ({ value: name, label: name }))

  const reduceMotion = useReducedMotion()
  /**
   * 后端 SSE 在推出第一条事件前不会 flush 响应头，浏览器的 readyState 会长期停在
   * CONNECTING、onopen 迟迟不触发——对用户来说「已订阅但还没事件」与「已连接」是同一件事，
   * 因此展示层只区分「监听中 / 已断开」。
   */
  const broken = status === 'error'

  return (
    <PageTransition>
      <PageHeader title="活动与审计" subtitle="实时事件、每次工具调用及关联的命令输出" />

      {/* 两栏在 xl 起固定为一屏高：事件与审计都是「持续刷新的流」，各自内部滚动比把页面拉成几千像素更好用 */}
      <div className="grid gap-4 xl:h-[calc(100dvh-11.5rem)] xl:min-h-[520px] xl:grid-cols-[2fr_3fr]">
        <Card className="flex max-h-[60dvh] min-h-0 min-w-0 flex-col xl:max-h-none">
          <CardHeader>
            <CardTitle>实时事件</CardTitle>
            <span
              className={cn(
                'flex items-center gap-1.5 text-[12px]',
                broken ? 'text-danger' : 'text-success',
              )}
            >
              <Dot pulse={!broken} />
              {broken ? '已断开' : '监听中'}
              {events.length > 0 && (
                <span className="text-muted tabular-nums">· {events.length} 条</span>
              )}
            </span>
          </CardHeader>
          <CardContent className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto p-3">
            {events.length === 0 ? (
              <EmptyState
                className="m-auto [&_p]:text-balance"
                icon={<Broadcast size={22} />}
                title="等待事件…"
                description="工具调用与后台任务状态会在发生的瞬间推送到这里。"
              />
            ) : (
              <AnimatePresence initial={false}>
                {events.map((event) => (
                  <motion.article
                    key={event.id}
                    className="shrink-0 rounded-[8px] bg-surface-2 px-3 py-2.5"
                    initial={reduceMotion ? false : { opacity: 0, y: -6 }}
                    animate={{ opacity: 1, y: 0 }}
                    transition={{ duration: 0.18, ease: 'easeOut' }}
                  >
                    <div className="flex items-center justify-between gap-3">
                      <Badge variant="outline" className="font-mono">
                        {event.type}
                      </Badge>
                      <time
                        dateTime={new Date(event.ts).toISOString()}
                        className="text-[12px] text-muted tabular-nums"
                      >
                        {clock(event.ts)}
                      </time>
                    </div>
                    {/*
                      tool_call 与 command_* 展示人能直接阅读的摘要和输出；未知事件才回退 JSON。
                    */}
                    <EventPayload event={event} />
                  </motion.article>
                ))}
              </AnimatePresence>
            )}
          </CardContent>
        </Card>

        <Card className="flex max-h-[60dvh] min-h-0 min-w-0 flex-col xl:max-h-none">
          <CardHeader>
            <CardTitle>审计与命令记录</CardTitle>
            {audit.data && audit.data.length > 0 && (
              <span className="text-[12px] text-muted tabular-nums">
                {filtered ? `筛选出 ${rows.length} 条` : `最近 ${rows.length} 条`}
              </span>
            )}
          </CardHeader>
          {/* 工具/令牌/主机支持多选 + 搜索；结果仅两个值，保留单选下拉；改动即查，不设「查询」按钮 */}
          <div className="grid grid-cols-2 gap-2 border-b border-border p-3 lg:grid-cols-4">
            <div className="col-span-2 lg:col-span-4">
              <label htmlFor="audit-filter-query" className="sr-only">
                搜索命令或参数
              </label>
              <Input
                id="audit-filter-query"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="搜索命令、路径或参数…"
                spellCheck={false}
                prefix={<MagnifyingGlass size={14} />}
              />
            </div>
            <label htmlFor="audit-filter-tool" className="sr-only">
              按工具筛选
            </label>
            <MultiSelect
              id="audit-filter-tool"
              value={tool}
              onChange={setTool}
              placeholder="全部工具"
              searchPlaceholder="搜索工具…"
              options={toolOptions}
              maxSelected={MAX_AUDIT_FILTER_VALUES}
            />
            <label htmlFor="audit-filter-token" className="sr-only">
              按调用令牌筛选
            </label>
            <MultiSelect
              id="audit-filter-token"
              value={token}
              onChange={setToken}
              placeholder="全部令牌"
              searchPlaceholder="搜索令牌…"
              options={(tokens.data ?? []).map((item) => ({ value: item.id, label: item.name }))}
              maxSelected={MAX_AUDIT_FILTER_VALUES}
            />
            <label htmlFor="audit-filter-host" className="sr-only">
              按主机筛选
            </label>
            <MultiSelect
              id="audit-filter-host"
              value={host}
              onChange={setHost}
              placeholder="全部主机"
              searchPlaceholder="搜索主机…"
              options={(hosts.data ?? []).map((item) => ({ value: item.name, label: item.name }))}
              maxSelected={MAX_AUDIT_FILTER_VALUES}
            />
            <label htmlFor="audit-filter-result" className="sr-only">
              按结果筛选
            </label>
            <Select
              id="audit-filter-result"
              value={result}
              onChange={setResult}
              options={[
                { value: ALL, label: '全部结果' },
                { value: 'ok', label: '成功' },
                { value: 'fail', label: '失败' },
              ]}
            />
          </div>
          <CardContent className="flex min-h-0 flex-1 flex-col p-0">
            <DataTable
              stickyHeader
              // 固定布局：审计数据持续刷新，auto 布局下列宽随内容跳动、长命令会把时间/结果列挤变形；
              // 固定后列宽稳定，超长内容只在各自单元格内截断
              fixedLayout
              // 表格自身即滚动容器（overflow-x-auto 会让 y 轴一并变成 auto）；窄屏收紧单元格内边距，
              // 让「时间」列不用被砍掉也能塞下四列
              className={cn(
                'min-h-0 flex-1 transition-opacity duration-150 [&_td]:px-3 [&_th]:px-3 sm:[&_td]:px-4 sm:[&_th]:px-4',
                audit.isPlaceholderData && 'opacity-60',
              )}
              columns={auditColumns}
              rows={rows}
              rowKey={(item) => item.ID}
              loading={audit.isLoading}
              onRowClick={setSelected}
              empty={
                filtered ? (
                  <EmptyState
                    className="[&_p]:text-balance"
                    icon={<Pulse size={22} />}
                    title="没有匹配的审计记录"
                    description={
                      needle
                        ? '当前已加载的记录里没有匹配的命令或参数，调整关键词再试。'
                        : '当前筛选条件下暂无记录，调整条件再试。'
                    }
                  />
                ) : (
                  <EmptyState
                    className="[&_p]:text-balance"
                    icon={<Pulse size={22} />}
                    title="暂无审计记录"
                    description="Agent 通过 MCP 网关调用工具后，每一次调用都会记录在这里。点开一行可看完整命令和参数。"
                  />
                )
              }
            />
          </CardContent>
        </Card>
      </div>

      <Sheet
        open={selected != null}
        onOpenChange={(open) => {
          if (!open) setSelected(null)
        }}
        title={selected?.Tool ?? '调用详情'}
        width="min(860px, 100vw)"
        header={
          selected ? (
            <div>
              <p className="truncate text-sm font-semibold text-text">{selected.Tool}</p>
              <p className="mt-0.5 truncate text-[12px] text-muted">
                {clock(selected.Ts)}
                {selected.Host?.Valid ? ` · ${selected.Host.String}` : ''}
              </p>
            </div>
          ) : undefined
        }
      >
        <div className="p-4 sm:p-5">{selected ? <AuditSheetContent item={selected} /> : null}</div>
      </Sheet>
    </PageTransition>
  )
}
