import { Broadcast, Check, Copy, MagnifyingGlass, Pulse } from '@phosphor-icons/react'
import { toast } from 'sonner'
import { AnimatePresence, motion, useReducedMotion } from 'motion/react'
import { useMemo, useState } from 'react'
import { useAudit, useAuditTools, useHosts, useTokens, type AuditFilter } from '@/api/queries'
import type { Audit, StreamEvent } from '@/api/types'
import { Badge, Dot } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { DataTable, type Column } from '@/components/ui/data-table'
import { Dialog } from '@/components/ui/dialog'
import { EmptyState } from '@/components/ui/empty-state'
import { Input } from '@/components/ui/input'
import { MultiSelect } from '@/components/ui/multi-select'
import { PageHeader } from '@/components/ui/page-header'
import { PageTransition } from '@/components/ui/page-transition'
import { Select } from '@/components/ui/select'
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
    render: (item) => item.Tool,
  },
  {
    key: 'ParamsJSON',
    title: '调用',
    // 这列才是「Agent 跑了什么」：命令、路径、检索式。窄屏也留着，靠 truncate + title 看全句
    className: 'min-w-[10rem] max-w-[28rem]',
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
    className: 'hidden max-w-[180px] text-muted xl:table-cell',
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
    className: 'hidden lg:table-cell text-muted',
    render: (item) => (item.Host?.Valid ? item.Host.String : '—'),
  },
  {
    key: 'OK',
    title: '结果',
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

function AuditDetail({ item }: { item: Audit }) {
  const params = parseAuditParams(item.ParamsJSON)
  const command = params && typeof params.command === 'string' ? params.command.trim() : ''
  const summary = command ? '' : auditSummary(item)
  const entries = params
    ? Object.entries(params).filter(([key, value]) => {
        if (key === 'command' && command) return false
        return value != null && value !== ''
      })
    : []

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
        </div>
      ) : !command && !summary ? (
        <p className="text-[13px] text-muted">此次调用没有记录额外参数。</p>
      ) : null}
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
      <PageHeader title="活动流" subtitle="每次工具调用的命令、参数与结果" />

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
                      tool_call 把命令/路径摊开；其余事件（尤其 exec_output）仍压成一行 JSON，
                      避免整块 stdout 把一屏挤成四五条。
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
            <CardTitle>审计记录</CardTitle>
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

      <Dialog
        open={selected != null}
        onOpenChange={(open) => {
          if (!open) setSelected(null)
        }}
        title={selected?.Tool ?? '调用详情'}
        description={
          selected
            ? `${clock(selected.Ts)}${selected.Host?.Valid ? ` · ${selected.Host.String}` : ''}`
            : undefined
        }
        size="lg"
      >
        {selected ? <AuditDetail item={selected} /> : null}
      </Dialog>
    </PageTransition>
  )
}
