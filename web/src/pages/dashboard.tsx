import { lazy, Suspense, useMemo, useState } from 'react'
import {
  ArrowRight,
  ArrowsClockwise,
  ChartLineUp,
  Cpu,
  GlobeHemisphereEast,
  HardDrive,
  HardDrives,
  Key,
  Lightning,
  Memory,
  Plus,
  Robot,
  ShieldCheck,
  Ticket,
} from '@phosphor-icons/react'
import { useQueries } from '@tanstack/react-query'
import { motion } from 'motion/react'
import { Link, useNavigate } from 'react-router-dom'
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
  type TooltipProps,
} from 'recharts'
import { api } from '@/api/client'
import { queryKeys, useAudit, useHosts, useKeys, useTokens } from '@/api/queries'
import type { Audit, Host, Metric } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { EmptyState } from '@/components/ui/empty-state'
import { HostConnection } from '@/components/host-connection'
import { PageTransition } from '@/components/ui/page-transition'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/cn'
import { staggerContainer, staggerItem } from '@/lib/motion'

// 分布地图连同世界边界数据按需加载，不拖慢概览页首屏
const DashboardGeo = lazy(() => import('@/components/dashboard-geo'))

/* ── 数据整形 ─────────────────────────────────────────────────────── */

const RANGES = [
  { hours: 1, label: '1 小时', bucketMs: 60_000 },
  { hours: 6, label: '6 小时', bucketMs: 5 * 60_000 },
  { hours: 24, label: '24 小时', bucketMs: 15 * 60_000 },
] as const

type MetricKey = 'cpu' | 'mem' | 'load'

const METRICS: Record<MetricKey, { label: string; unit: string; digits: number }> = {
  cpu: { label: 'CPU', unit: '%', digits: 1 },
  mem: { label: '内存', unit: '%', digits: 1 },
  load: { label: '负载', unit: '', digits: 2 },
}

/** 多主机折线用的色板：浅色与深色背景下都保持足够对比 */
const PALETTE = ['#14b8a6', '#818cf8', '#f59e0b', '#f43f5e', '#22c55e', '#06b6d4', '#a855f7', '#ec4899']

/** 采样间隔默认 60 秒；超过 5 分钟没有新点就不再算「新鲜」 */
const FRESH_MS = 5 * 60_000

const memPct = (m: Metric) =>
  m.mem_total_kb ? ((m.mem_used_kb ?? 0) / m.mem_total_kb) * 100 : undefined

/** 磁盘取最满的「真实」挂载点：/run、/dev/shm 这类 tmpfs 只有几十 MB，不能代表磁盘压力 */
const diskPct = (m: Metric) => {
  const real = m.disks.filter((d) => d.total_kb >= 1 << 20)
  const list = real.length ? real : m.disks
  let worst: number | undefined
  for (const d of list) {
    if (!d.total_kb) continue
    const pct = (d.used_kb / d.total_kb) * 100
    worst = worst == null ? pct : Math.max(worst, pct)
  }
  return worst
}

const metricValue = (m: Metric, key: MetricKey) =>
  key === 'cpu' ? m.cpu_pct : key === 'mem' ? memPct(m) : m.load1

type HealthStatus = 'healthy' | 'warning' | 'critical' | 'stale' | 'nodata'

const STATUS_META: Record<HealthStatus, { label: string; color: string; text: string }> = {
  healthy: { label: '健康', color: '#22c55e', text: 'text-success' },
  warning: { label: '偏高', color: '#f59e0b', text: 'text-warning' },
  critical: { label: '告警', color: '#f43f5e', text: 'text-danger' },
  stale: { label: '采样中断', color: '#94a3b8', text: 'text-muted' },
  nodata: { label: '无数据', color: '#cbd5e1', text: 'text-faint' },
}

type Snapshot = {
  host: Host
  color: string
  series: Metric[]
  latest?: Metric
  cpu?: number
  mem?: number
  disk?: number
  load?: number
  fresh: boolean
  status: HealthStatus
}

function snapshotOf(host: Host, series: Metric[], color: string, now: number): Snapshot {
  const latest = series[series.length - 1]
  if (!latest) return { host, color, series, fresh: false, status: 'nodata' }
  // CPU 需要两次采样做差分，刚重启后的首个点没有它：退回最近一个有值的点，而不是显示破折号
  const cpu = latest.cpu_pct ?? [...series].reverse().find((m) => m.cpu_pct != null)?.cpu_pct
  const mem = memPct(latest)
  const disk = diskPct(latest)
  const fresh = now - latest.ts < FRESH_MS
  const worst = Math.max(cpu ?? 0, mem ?? 0, disk ?? 0)
  const status: HealthStatus = !fresh ? 'stale' : worst >= 90 ? 'critical' : worst >= 75 ? 'warning' : 'healthy'
  return { host, color, series, latest, cpu, mem, disk, load: latest.load1, fresh, status }
}

/** 把各主机不齐的采样点按时间桶对齐，供多序列图表共用一条 X 轴 */
function bucketize(snapshots: Snapshot[], key: MetricKey, bucketMs: number) {
  const rows = new Map<number, Record<string, number>>()
  const sums = new Map<number, { sum: number; n: number }>()
  for (const snap of snapshots) {
    const acc = new Map<number, { sum: number; n: number }>()
    for (const m of snap.series) {
      const value = metricValue(m, key)
      if (value == null || Number.isNaN(value)) continue
      const bucket = Math.floor(m.ts / bucketMs) * bucketMs
      const cur = acc.get(bucket) ?? { sum: 0, n: 0 }
      cur.sum += value
      cur.n += 1
      acc.set(bucket, cur)
    }
    for (const [bucket, { sum, n }] of acc) {
      const row = rows.get(bucket) ?? { ts: bucket }
      row[`h${snap.host.id}`] = sum / n
      rows.set(bucket, row)
      const total = sums.get(bucket) ?? { sum: 0, n: 0 }
      total.sum += sum / n
      total.n += 1
      sums.set(bucket, total)
    }
  }
  return [...rows.values()]
    .map((row): Record<string, number> & { ts: number; avg: number } => {
      const total = sums.get(row.ts)
      return { ...row, ts: row.ts, avg: total ? total.sum / total.n : 0 }
    })
    .sort((a, b) => a.ts - b.ts)
}

const average = (values: Array<number | undefined>) => {
  const list = values.filter((v): v is number => v != null && !Number.isNaN(v))
  return list.length ? list.reduce((a, b) => a + b, 0) / list.length : undefined
}

const fmtPct = (v?: number, digits = 0) => (v == null ? '—' : `${v.toFixed(digits)}%`)

const timeFmt = new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit' })

function ago(ms: number, now: number) {
  const diff = Math.max(0, now - ms)
  if (diff < 60_000) return `${Math.max(1, Math.round(diff / 1000))} 秒前`
  if (diff < 3_600_000) return `${Math.round(diff / 60_000)} 分钟前`
  if (diff < 86_400_000) return `${Math.round(diff / 3_600_000)} 小时前`
  return `${Math.round(diff / 86_400_000)} 天前`
}

/** 审计时间戳历史上有秒和毫秒两种口径，统一折成毫秒 */
const auditMs = (a: Audit) => (a.Ts < 1e12 ? a.Ts * 1000 : a.Ts)

function greeting(hour: number) {
  if (hour < 6) return '夜深了'
  if (hour < 11) return '早上好'
  if (hour < 13) return '中午好'
  if (hour < 18) return '下午好'
  return '晚上好'
}

/* ── 小组件 ─────────────────────────────────────────────────────── */

function ChartTip({
  active,
  payload,
  label,
  unit = '%',
  digits = 1,
  names,
}: TooltipProps<number, string> & { unit?: string; digits?: number; names?: Record<string, string> }) {
  if (!active || !payload?.length) return null
  return (
    <div className="min-w-[160px] rounded-[10px] border border-border bg-surface/95 px-3 py-2 text-[12px] shadow-pop backdrop-blur">
      {typeof label === 'number' && (
        <p className="mb-1.5 font-medium text-muted tabular-nums">{timeFmt.format(label)}</p>
      )}
      <div className="space-y-1">
        {payload.map((entry) => (
          <div key={String(entry.dataKey)} className="flex items-center justify-between gap-4">
            <span className="inline-flex min-w-0 items-center gap-1.5 text-text">
              <span className="size-2 shrink-0 rounded-full" style={{ background: entry.color }} />
              <span className="truncate">{names?.[String(entry.dataKey)] ?? entry.name}</span>
            </span>
            <span className="font-medium text-text tabular-nums">
              {typeof entry.value === 'number' ? entry.value.toFixed(digits) : entry.value}
              {unit}
            </span>
          </div>
        ))}
      </div>
    </div>
  )
}

function Panel({
  title,
  subtitle,
  icon,
  action,
  className,
  children,
}: {
  title: string
  subtitle?: string
  icon?: React.ReactNode
  action?: React.ReactNode
  className?: string
  children: React.ReactNode
}) {
  return (
    <motion.div variants={staggerItem} className={cn('min-w-0', className)}>
      <Card className="flex h-full flex-col p-5">
        <div className="mb-4 flex items-start justify-between gap-3">
          <div className="flex min-w-0 items-center gap-2.5">
            {icon && (
              <span className="grid size-8 shrink-0 place-items-center rounded-[9px] bg-accent-soft text-accent">
                {icon}
              </span>
            )}
            <div className="min-w-0">
              <h2 className="truncate text-[14px] font-semibold text-text">{title}</h2>
              {subtitle && <p className="truncate text-[12px] text-muted">{subtitle}</p>}
            </div>
          </div>
          {action}
        </div>
        <div className="min-h-0 flex-1">{children}</div>
      </Card>
    </motion.div>
  )
}

function Segmented<T extends string | number>({
  value,
  options,
  onChange,
  label,
}: {
  value: T
  options: Array<{ value: T; label: string }>
  onChange: (value: T) => void
  label: string
}) {
  return (
    <div role="radiogroup" aria-label={label} className="inline-flex rounded-[9px] border border-border bg-surface-2 p-0.5">
      {options.map((option) => (
        <button
          key={String(option.value)}
          type="button"
          role="radio"
          aria-checked={option.value === value}
          onClick={() => onChange(option.value)}
          className={cn(
            'rounded-[7px] px-2.5 py-1 text-[12px] font-medium transition-all duration-150',
            option.value === value
              ? 'bg-surface text-text shadow-card'
              : 'text-muted hover:text-text',
          )}
        >
          {option.label}
        </button>
      ))}
    </div>
  )
}

function Sparkline({ data, dataKey, color, id }: { data: object[]; dataKey: string; color: string; id: string }) {
  if (data.length < 2) return <div className="h-full" />
  return (
    <ResponsiveContainer width="100%" height="100%">
      <AreaChart data={data} margin={{ top: 2, right: 0, bottom: 0, left: 0 }}>
        <defs>
          <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={color} stopOpacity={0.35} />
            <stop offset="100%" stopColor={color} stopOpacity={0} />
          </linearGradient>
        </defs>
        <YAxis hide domain={[0, (max: number) => Math.max(10, max * 1.15)]} />
        <Area
          type="monotone"
          dataKey={dataKey}
          stroke={color}
          strokeWidth={1.75}
          fill={`url(#${id})`}
          dot={false}
          isAnimationActive={false}
        />
      </AreaChart>
    </ResponsiveContainer>
  )
}

function KpiCard({
  title,
  value,
  suffix,
  hint,
  icon,
  tone,
  children,
}: {
  title: string
  value: string
  suffix?: string
  hint: React.ReactNode
  icon: React.ReactNode
  tone: string
  children?: React.ReactNode
}) {
  return (
    <motion.div variants={staggerItem} className="min-w-0">
      <Card className="group relative flex h-full flex-col overflow-hidden p-4 transition-all duration-200 hover:-translate-y-0.5 hover:shadow-pop">
        <div
          className="pointer-events-none absolute -top-10 -right-10 size-28 rounded-full opacity-[0.12] blur-2xl transition-opacity duration-300 group-hover:opacity-25"
          style={{ background: tone }}
        />
        <div className="flex items-center justify-between gap-2">
          <p className="text-[12px] font-medium text-muted">{title}</p>
          <span
            className="grid size-8 place-items-center rounded-[9px] text-white shadow-sm"
            style={{ background: `linear-gradient(135deg, ${tone}, color-mix(in oklab, ${tone} 60%, #ffffff))` }}
          >
            {icon}
          </span>
        </div>
        <p className="mt-2 flex items-baseline gap-1">
          <span className="text-[28px] leading-none font-semibold tracking-tight text-text tabular-nums">{value}</span>
          {suffix && <span className="text-[12px] text-muted">{suffix}</span>}
        </p>
        <div className="mt-1.5 text-[12px] text-muted">{hint}</div>
        {children && <div className="-mx-4 -mb-4 mt-3 h-12">{children}</div>}
      </Card>
    </motion.div>
  )
}

function Ring({ value, color, label }: { value?: number; color: string; label: string }) {
  const size = 58
  const stroke = 6
  const r = (size - stroke) / 2
  const c = 2 * Math.PI * r
  const pct = value == null ? 0 : Math.min(100, Math.max(0, value))
  return (
    <div className="flex flex-col items-center gap-1">
      <div className="relative" style={{ width: size, height: size }}>
        <svg width={size} height={size} className="-rotate-90">
          <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="var(--surface-2)" strokeWidth={stroke} />
          <circle
            cx={size / 2}
            cy={size / 2}
            r={r}
            fill="none"
            stroke={color}
            strokeWidth={stroke}
            strokeLinecap="round"
            strokeDasharray={c}
            strokeDashoffset={c * (1 - pct / 100)}
            style={{ transition: 'stroke-dashoffset 600ms ease-out' }}
          />
        </svg>
        <span className="absolute inset-0 grid place-items-center text-[12px] font-semibold text-text tabular-nums">
          {value == null ? '—' : `${Math.round(pct)}%`}
        </span>
      </div>
      <span className="text-[11px] text-muted">{label}</span>
    </div>
  )
}

const ringColor = (v?: number) => (v == null ? '#cbd5e1' : v >= 90 ? '#f43f5e' : v >= 75 ? '#f59e0b' : '#14b8a6')

function HostTile({ snap, now }: { snap: Snapshot; now: number }) {
  const meta = STATUS_META[snap.status]
  const series = snap.series.map((m) => ({ ts: m.ts, cpu: m.cpu_pct ?? 0 }))
  return (
    <motion.div variants={staggerItem} className="min-w-0">
      <Card className="group relative flex h-full flex-col overflow-hidden p-4 transition-all duration-200 hover:-translate-y-0.5 hover:shadow-pop">
        <span className="absolute inset-x-0 top-0 h-0.5" style={{ background: meta.color }} />
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <p className="flex items-center gap-2 truncate text-[14px] font-semibold text-text">
              <span className="size-2 shrink-0 rounded-full" style={{ background: snap.color }} />
              {snap.host.name}
            </p>
            <HostConnection host={snap.host} />
          </div>
          <span
            className={cn('inline-flex shrink-0 items-center gap-1.5 rounded-full px-2 py-0.5 text-[11px] font-medium', meta.text)}
            style={{ background: `color-mix(in oklab, ${meta.color} 14%, transparent)` }}
          >
            <span className={cn('size-1.5 rounded-full', snap.status === 'healthy' && 'animate-pulse')} style={{ background: meta.color }} />
            {snap.host.monitor_enabled || snap.latest ? meta.label : '未监控'}
          </span>
        </div>

        {snap.latest ? (
          <>
            <div className="mt-4 flex items-center justify-around">
              <Ring value={snap.cpu} color={ringColor(snap.cpu)} label="CPU" />
              <Ring value={snap.mem} color={ringColor(snap.mem)} label="内存" />
              <Ring value={snap.disk} color={ringColor(snap.disk)} label="磁盘" />
            </div>
            <div className="mt-3 flex items-center justify-between text-[11px] text-muted">
              <span>
                负载 <b className="font-medium text-text tabular-nums">{snap.load == null ? '—' : snap.load.toFixed(2)}</b>
              </span>
              <span className="tabular-nums">{ago(snap.latest.ts, now)}采样</span>
            </div>
            <div className="-mx-4 -mb-4 mt-2 h-12">
              <Sparkline data={series} dataKey="cpu" color={snap.color} id={`tile-${snap.host.id}`} />
            </div>
          </>
        ) : (
          <p className="mt-auto rounded-[10px] bg-surface-2 px-3 py-2.5 text-[12px] leading-5 text-muted">
            {snap.host.monitor_enabled ? (
              '资源监控已开启，正在等待首个采样点。'
            ) : (
              <>
                未开启资源监控，
                <Link to="/hosts" className="text-accent hover:underline">
                  去主机设置
                </Link>
                中打开后即可采集。
              </>
            )}
          </p>
        )}
      </Card>
    </motion.div>
  )
}

/* ── 页面 ───────────────────────────────────────────────────────── */

export function DashboardPage() {
  const navigate = useNavigate()
  const { data: allHosts = [], isLoading } = useHosts()
  const keys = useKeys()
  const tokens = useTokens()
  const audit = useAudit({})
  const [rangeHours, setRangeHours] = useState<number>(1)
  const [trendMetric, setTrendMetric] = useState<MetricKey>('cpu')
  const range = RANGES.find((r) => r.hours === rangeHours) ?? RANGES[0]

  // 概览只看在役主机：归档主机不再采样，计入覆盖率只会把数字拉低
  const hosts = useMemo(() => allHosts.filter((host) => !host.archived), [allHosts])
  const metricQueries = useQueries({
    queries: hosts.map((host) => ({
      queryKey: queryKeys.metrics(host.id, range.hours),
      queryFn: () => api<Metric[]>(`/metrics/${host.id}?hours=${range.hours}`),
      refetchInterval: 60_000,
    })),
  })
  const fetching = metricQueries.some((q) => q.isFetching)
  const now = Date.now()

  const snapshots = hosts.map((host, index) =>
    snapshotOf(host, metricQueries[index]?.data ?? [], PALETTE[index % PALETTE.length], now),
  )
  const sampled = snapshots.filter((s) => s.latest)
  const freshCount = snapshots.filter((s) => s.fresh).length
  const monitored = hosts.filter((h) => h.monitor_enabled).length
  const coverage = hosts.length ? Math.round((monitored / hosts.length) * 100) : 0
  const avgCpu = average(sampled.map((s) => s.cpu))
  const avgMem = average(sampled.map((s) => s.mem))
  const avgDisk = average(sampled.map((s) => s.disk))
  const lastSample = Math.max(0, ...sampled.map((s) => s.latest?.ts ?? 0))

  const cpuTrend = bucketize(snapshots, 'cpu', range.bucketMs)
  const memTrend = bucketize(snapshots, 'mem', range.bucketMs)
  const trend = trendMetric === 'cpu' ? cpuTrend : trendMetric === 'mem' ? memTrend : bucketize(snapshots, 'load', range.bucketMs)
  // 主机太多时折线会糊成一片：只画最近值最高的 6 台，其余由平均线代表
  const trendHosts = [...sampled]
    .sort((a, b) => (metricValue(b.latest!, trendMetric) ?? 0) - (metricValue(a.latest!, trendMetric) ?? 0))
    .slice(0, 6)
  const trendNames = Object.fromEntries([
    ['avg', '平均'],
    ...trendHosts.map((s) => [`h${s.host.id}`, s.host.name]),
  ])

  const statusCounts = (Object.keys(STATUS_META) as HealthStatus[])
    .map((status) => ({ status, ...STATUS_META[status], value: snapshots.filter((s) => s.status === status).length }))
    .filter((item) => item.value > 0)
  const healthyPct = hosts.length
    ? Math.round((snapshots.filter((s) => s.status === 'healthy').length / hosts.length) * 100)
    : 0

  const diskBars = sampled
    .filter((s) => s.disk != null)
    .map((s) => ({ name: s.host.name, value: s.disk as number }))
    .sort((a, b) => b.value - a.value)
    .slice(0, 8)

  const audits = audit.data ?? []
  const toolCounts = new Map<string, { ok: number; failed: number; denied: number }>()
  for (const a of audits) {
    const cur = toolCounts.get(a.Tool) ?? { ok: 0, failed: 0, denied: 0 }
    const outcome = a.Outcome || (a.OK ? 'ok' : 'failed')
    cur[outcome] += 1
    toolCounts.set(a.Tool, cur)
  }
  const toolBars = [...toolCounts.entries()]
    .map(([tool, c]) => ({ tool, ...c, total: c.ok + c.failed + c.denied }))
    .sort((a, b) => b.total - a.total)
    .slice(0, 6)
  const okRate = audits.length
    ? Math.round((audits.filter((a) => (a.Outcome || (a.OK ? 'ok' : 'failed')) === 'ok').length / audits.length) * 100)
    : null
  const recent = [...audits].sort((a, b) => auditMs(b) - auditMs(a)).slice(0, 5)

  const keyCount = keys.data?.length ?? 0
  const keyHosts = hosts.filter((h) => h.auth_type === 'key').length

  if (isLoading) {
    return (
      <PageTransition>
        <Skeleton className="h-[148px] rounded-[16px]" />
        <div className="mt-5 grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
          {Array.from({ length: 4 }, (_, i) => (
            <Skeleton key={i} className="h-[150px] rounded-[12px]" />
          ))}
        </div>
        <div className="mt-5 grid gap-4 xl:grid-cols-3">
          <Skeleton className="h-[340px] rounded-[12px] xl:col-span-2" />
          <Skeleton className="h-[340px] rounded-[12px]" />
        </div>
      </PageTransition>
    )
  }

  return (
    <PageTransition>
      {/* ── 顶部横幅 ── */}
      <section className="relative overflow-hidden rounded-[16px] border border-border bg-surface shadow-card">
        <div className="dashboard-hero pointer-events-none absolute inset-0" />
        <div className="relative flex flex-wrap items-end justify-between gap-5 p-6 md:p-7">
          <div className="min-w-0">
            <p className="inline-flex items-center gap-1.5 rounded-full border border-accent/30 bg-accent-soft px-2.5 py-0.5 text-[11px] font-medium text-accent">
              <span className="relative flex size-1.5">
                <span className="absolute inline-flex size-full animate-ping rounded-full bg-accent opacity-60" />
                <span className="relative inline-flex size-1.5 rounded-full bg-accent" />
              </span>
              网关运行中
            </p>
            <h1 className="mt-3 text-[24px] font-semibold tracking-tight text-text md:text-[28px]">
              {greeting(new Date().getHours())}，欢迎回到 OneSSH
            </h1>
            <p className="mt-1.5 text-[13px] text-muted">
              {hosts.length} 台在役主机 · {freshCount} 台有新鲜指标 · 监控覆盖 {coverage}%
              {lastSample > 0 && <> · 最近采样 {ago(lastSample, now)}</>}
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Segmented
              label="时间范围"
              value={rangeHours}
              onChange={setRangeHours}
              options={RANGES.map((r) => ({ value: r.hours, label: r.label }))}
            />
            <Button
              variant="outline"
              size="icon"
              aria-label="刷新指标"
              title="刷新指标"
              onClick={() => metricQueries.forEach((q) => void q.refetch())}
            >
              <ArrowsClockwise size={16} className={cn(fetching && 'animate-spin')} />
            </Button>
            <Button variant="primary" onClick={() => navigate('/hosts')}>
              <Plus size={15} />
              添加主机
            </Button>
          </div>
        </div>
      </section>

      {!hosts.length ? (
        <Card className="mt-5">
          {allHosts.length ? (
            <EmptyState
              icon={<HardDrives size={22} />}
              title="主机均已归档"
              description="归档主机不再采样，取消归档后会重新出现在这里。"
              action={
                <Button variant="primary" onClick={() => navigate('/hosts')}>
                  管理主机
                </Button>
              }
            />
          ) : (
            <EmptyState
              icon={<HardDrives size={22} />}
              title="还没有 SSH 主机"
              description="添加第一台主机后，这里会显示实时资源图表与 Agent 活动。可以在添加时直接上传私钥文件。"
              action={
                <Button variant="primary" onClick={() => navigate('/hosts')}>
                  添加第一台主机
                </Button>
              }
            />
          )}
        </Card>
      ) : (
        <motion.div variants={staggerContainer} initial="hidden" animate="visible">
          {/* ── KPI ── */}
          <div className="mt-5 grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
            <KpiCard
              title="在役主机"
              value={String(hosts.length)}
              suffix="台"
              tone="#14b8a6"
              icon={<HardDrives size={16} weight="fill" />}
              hint={
                <span className="flex flex-wrap items-center gap-x-2">
                  <span className="text-success">● {freshCount} 在线</span>
                  <span className="text-faint">● {hosts.length - freshCount} 无新鲜数据</span>
                </span>
              }
            >
              <div className="flex h-full items-end gap-1 px-4 pb-3">
                {snapshots.slice(0, 24).map((s) => (
                  <span
                    key={s.host.id}
                    title={`${s.host.name} · ${STATUS_META[s.status].label}`}
                    className="h-2.5 flex-1 rounded-full"
                    style={{ background: STATUS_META[s.status].color }}
                  />
                ))}
              </div>
            </KpiCard>
            <KpiCard
              title="平均 CPU"
              value={avgCpu == null ? '—' : avgCpu.toFixed(1)}
              suffix={avgCpu == null ? undefined : '%'}
              tone="#6366f1"
              icon={<Cpu size={16} weight="fill" />}
              hint={`峰值主机 ${[...sampled].sort((a, b) => (b.cpu ?? 0) - (a.cpu ?? 0))[0]?.host.name ?? '—'}`}
            >
              <Sparkline data={cpuTrend} dataKey="avg" color="#6366f1" id="kpi-cpu" />
            </KpiCard>
            <KpiCard
              title="平均内存"
              value={avgMem == null ? '—' : avgMem.toFixed(1)}
              suffix={avgMem == null ? undefined : '%'}
              tone="#f59e0b"
              icon={<Memory size={16} weight="fill" />}
              hint={`${sampled.filter((s) => (s.mem ?? 0) >= 75).length} 台内存偏高（≥75%）`}
            >
              <Sparkline data={memTrend} dataKey="avg" color="#f59e0b" id="kpi-mem" />
            </KpiCard>
            <KpiCard
              title="平均磁盘"
              value={avgDisk == null ? '—' : avgDisk.toFixed(1)}
              suffix={avgDisk == null ? undefined : '%'}
              tone="#f43f5e"
              icon={<HardDrive size={16} weight="fill" />}
              hint="取各主机最满的数据盘"
            >
              <div className="flex h-full items-end px-4 pb-3">
                <div className="h-2 w-full overflow-hidden rounded-full bg-surface-2">
                  <div
                    className="h-full rounded-full transition-[width] duration-700"
                    style={{
                      width: `${Math.min(100, avgDisk ?? 0)}%`,
                      background: 'linear-gradient(90deg, #14b8a6, #f59e0b 70%, #f43f5e)',
                    }}
                  />
                </div>
              </div>
            </KpiCard>
          </div>

          {/* ── 趋势 + 健康分布 ── */}
          <div className="mt-4 grid gap-4 xl:grid-cols-3">
            <Panel
              className="xl:col-span-2"
              title="资源趋势"
              subtitle={`近 ${range.label} · 每 ${range.bucketMs / 60_000} 分钟聚合 · ${trendHosts.length} 台主机`}
              icon={<ChartLineUp size={16} weight="bold" />}
              action={
                <Segmented
                  label="指标"
                  value={trendMetric}
                  onChange={setTrendMetric}
                  options={(Object.keys(METRICS) as MetricKey[]).map((key) => ({ value: key, label: METRICS[key].label }))}
                />
              }
            >
              {trend.length > 1 ? (
                <div className="h-[280px]">
                  <ResponsiveContainer width="100%" height="100%">
                    <AreaChart data={trend} margin={{ top: 10, right: 8, bottom: 0, left: 0 }}>
                      <defs>
                        {trendHosts.map((s) => (
                          <linearGradient key={s.host.id} id={`trend-${s.host.id}`} x1="0" y1="0" x2="0" y2="1">
                            <stop offset="0%" stopColor={s.color} stopOpacity={0.22} />
                            <stop offset="100%" stopColor={s.color} stopOpacity={0} />
                          </linearGradient>
                        ))}
                      </defs>
                      <CartesianGrid vertical={false} stroke="var(--border)" strokeDasharray="3 4" />
                      <XAxis
                        dataKey="ts"
                        type="number"
                        domain={['dataMin', 'dataMax']}
                        tickFormatter={(v: number) => timeFmt.format(v)}
                        tick={{ fill: 'var(--muted)', fontSize: 11 }}
                        axisLine={false}
                        tickLine={false}
                        minTickGap={40}
                      />
                      <YAxis
                        tick={{ fill: 'var(--muted)', fontSize: 11 }}
                        axisLine={false}
                        tickLine={false}
                        width={48}
                        tickCount={5}
                        // 百分比轴按实际峰值自适应（最低 10%、最高 100%），低负载时曲线不会被压成一条贴底的线
                        domain={
                          trendMetric === 'load'
                            ? [0, 'auto']
                            : [0, (max: number) => Math.min(100, Math.max(10, Math.ceil((max * 1.25) / 5) * 5))]
                        }
                        tickFormatter={(v: number) => `${v}${METRICS[trendMetric].unit}`}
                      />
                      <Tooltip
                        content={
                          <ChartTip
                            unit={METRICS[trendMetric].unit}
                            digits={METRICS[trendMetric].digits}
                            names={trendNames}
                          />
                        }
                        cursor={{ stroke: 'var(--border-strong)', strokeDasharray: '3 3' }}
                      />
                      {trendHosts.map((s) => (
                        <Area
                          key={s.host.id}
                          type="monotone"
                          dataKey={`h${s.host.id}`}
                          name={s.host.name}
                          stroke={s.color}
                          strokeWidth={2}
                          fill={`url(#trend-${s.host.id})`}
                          dot={false}
                          connectNulls
                          activeDot={{ r: 3.5, strokeWidth: 0 }}
                        />
                      ))}
                      {trendHosts.length > 1 && (
                        <Area
                          type="monotone"
                          dataKey="avg"
                          name="平均"
                          stroke="var(--text)"
                          strokeOpacity={0.55}
                          strokeDasharray="5 4"
                          strokeWidth={1.5}
                          fill="transparent"
                          dot={false}
                        />
                      )}
                    </AreaChart>
                  </ResponsiveContainer>
                </div>
              ) : (
                <div className="grid h-[280px] place-items-center rounded-[10px] bg-surface-2/60 text-[13px] text-muted">
                  所选时间范围内采样点不足，开启监控后稍等几分钟即可看到趋势。
                </div>
              )}
              {trendHosts.length > 0 && (
                <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1.5 text-[12px] text-muted">
                  {trendHosts.map((s) => (
                    <span key={s.host.id} className="inline-flex items-center gap-1.5">
                      <span className="size-2 rounded-full" style={{ background: s.color }} />
                      {s.host.name}
                    </span>
                  ))}
                  {trendHosts.length > 1 && (
                    <span className="inline-flex items-center gap-1.5">
                      <span className="h-0 w-3 border-t-2 border-dashed border-text/50" />
                      平均
                    </span>
                  )}
                </div>
              )}
            </Panel>

            <Panel title="主机健康" subtitle="按 CPU / 内存 / 磁盘最高值分级" icon={<ShieldCheck size={16} weight="bold" />}>
              <div className="relative h-[200px]">
                <ResponsiveContainer width="100%" height="100%">
                  <PieChart>
                    <Pie
                      data={statusCounts}
                      dataKey="value"
                      nameKey="label"
                      innerRadius="68%"
                      outerRadius="92%"
                      paddingAngle={statusCounts.length > 1 ? 3 : 0}
                      cornerRadius={6}
                      stroke="none"
                      startAngle={90}
                      endAngle={-270}
                    >
                      {statusCounts.map((item) => (
                        <Cell key={item.status} fill={item.color} />
                      ))}
                    </Pie>
                    <Tooltip content={<ChartTip unit=" 台" digits={0} />} />
                  </PieChart>
                </ResponsiveContainer>
                <div className="pointer-events-none absolute inset-0 grid place-items-center">
                  <div className="text-center">
                    <p className="text-[28px] leading-none font-semibold text-text tabular-nums">{healthyPct}%</p>
                    <p className="mt-1 text-[11px] text-muted">健康占比</p>
                  </div>
                </div>
              </div>
              <div className="mt-4 grid grid-cols-2 gap-2">
                {(Object.keys(STATUS_META) as HealthStatus[]).map((status) => {
                  const count = snapshots.filter((s) => s.status === status).length
                  return (
                    <div
                      key={status}
                      className={cn(
                        'flex items-center justify-between rounded-[9px] bg-surface-2/70 px-2.5 py-1.5 text-[12px]',
                        count === 0 && 'opacity-50',
                      )}
                    >
                      <span className="inline-flex items-center gap-1.5 text-muted">
                        <span className="size-2 rounded-full" style={{ background: STATUS_META[status].color }} />
                        {STATUS_META[status].label}
                      </span>
                      <b className="font-semibold text-text tabular-nums">{count}</b>
                    </div>
                  )
                })}
              </div>
            </Panel>
          </div>

          {/* ── 分布地图 + 网络延迟 ── */}
          <Suspense fallback={<Skeleton className="mt-4 h-[360px] rounded-[12px]" />}>
            <DashboardGeo />
          </Suspense>

          {/* ── 磁盘 + Agent 活动 ── */}
          <div className="mt-4 grid gap-4 xl:grid-cols-3">
            <Panel title="磁盘使用率" subtitle="各主机最满的数据盘" icon={<HardDrive size={16} weight="bold" />}>
              {diskBars.length ? (
                <div className="flex h-full items-center">
                <div className="w-full" style={{ height: Math.max(120, diskBars.length * 46) }}>
                  <ResponsiveContainer width="100%" height="100%">
                    <BarChart data={diskBars} layout="vertical" margin={{ top: 4, right: 40, bottom: 4, left: 0 }} barSize={16}>
                      <XAxis type="number" domain={[0, 100]} hide />
                      <YAxis
                        type="category"
                        dataKey="name"
                        width={96}
                        tick={{ fill: 'var(--text)', fontSize: 12 }}
                        axisLine={false}
                        tickLine={false}
                      />
                      <Tooltip cursor={{ fill: 'var(--surface-2)' }} content={<ChartTip unit="%" digits={1} />} />
                      <Bar
                        dataKey="value"
                        name="磁盘"
                        radius={[0, 6, 6, 0]}
                        background={{ fill: 'var(--surface-2)', radius: 6 }}
                        label={{ position: 'right', fill: 'var(--muted)', fontSize: 11, formatter: (v: number) => `${v.toFixed(0)}%` }}
                      >
                        {diskBars.map((d) => (
                          <Cell key={d.name} fill={ringColor(d.value)} />
                        ))}
                      </Bar>
                    </BarChart>
                  </ResponsiveContainer>
                </div>
                </div>
              ) : (
                <div className="grid h-[160px] place-items-center text-[13px] text-muted">暂无磁盘数据</div>
              )}
            </Panel>

            <Panel
              className="xl:col-span-2"
              title="Agent 活动"
              subtitle={
                audits.length
                  ? `最近 ${audits.length} 次工具调用 · 成功率 ${okRate ?? 0}%`
                  : '还没有 Agent 调用记录'
              }
              icon={<Robot size={16} weight="bold" />}
              action={
                <Link
                  to="/activity"
                  className="inline-flex items-center gap-1 text-[12px] font-medium text-accent hover:text-accent-hover"
                >
                  全部活动 <ArrowRight size={12} />
                </Link>
              }
            >
              {audits.length ? (
                <div className="grid gap-5 lg:grid-cols-5">
                  <div className="lg:col-span-3" style={{ height: Math.max(200, toolBars.length * 40) }}>
                    <ResponsiveContainer width="100%" height="100%">
                      {/* 工具名普遍偏长（hosts_manage_list），横向条形把名称放在 Y 轴上才不会互相压字 */}
                      <BarChart data={toolBars} layout="vertical" margin={{ top: 4, right: 12, bottom: 0, left: 0 }} barSize={16}>
                        <CartesianGrid horizontal={false} stroke="var(--border)" strokeDasharray="3 4" />
                        <XAxis
                          type="number"
                          allowDecimals={false}
                          tick={{ fill: 'var(--muted)', fontSize: 11 }}
                          axisLine={false}
                          tickLine={false}
                        />
                        <YAxis
                          type="category"
                          dataKey="tool"
                          width={136}
                          tick={{ fill: 'var(--text)', fontSize: 11, fontFamily: 'var(--font-mono)' }}
                          axisLine={false}
                          tickLine={false}
                        />
                        <Tooltip
                          cursor={{ fill: 'var(--surface-2)' }}
                          content={<ChartTip unit=" 次" digits={0} names={{ ok: '成功', failed: '失败', denied: '策略拒绝' }} />}
                        />
                        <Bar dataKey="ok" stackId="a" fill="#14b8a6" name="成功" />
                        <Bar dataKey="failed" stackId="a" fill="#f43f5e" name="失败" />
                        <Bar dataKey="denied" stackId="a" fill="#f59e0b" name="策略拒绝" radius={[0, 6, 6, 0]} />
                      </BarChart>
                    </ResponsiveContainer>
                  </div>
                  <ul className="space-y-1.5 lg:col-span-2">
                    {recent.map((a) => {
                      const outcome = a.Outcome || (a.OK ? 'ok' : 'failed')
                      return (
                        <li
                          key={a.ID}
                          className="flex items-center gap-2.5 rounded-[9px] bg-surface-2/60 px-2.5 py-2 text-[12px]"
                        >
                          <span
                            className={cn(
                              'size-2 shrink-0 rounded-full',
                              outcome === 'ok' ? 'bg-success' : outcome === 'denied' ? 'bg-warning' : 'bg-danger',
                            )}
                          />
                          <span className="min-w-0 flex-1">
                            <span className="block truncate font-mono font-medium text-text">{a.Tool}</span>
                            <span className="block truncate text-[11px] text-muted">
                              {a.Host.Valid ? a.Host.String : '网关'}
                              {a.TokenName.Valid && ` · ${a.TokenName.String}`}
                            </span>
                          </span>
                          <span className="shrink-0 text-[11px] text-muted tabular-nums">{ago(auditMs(a), now)}</span>
                        </li>
                      )
                    })}
                  </ul>
                </div>
              ) : (
                <div className="grid h-[200px] place-items-center rounded-[10px] bg-surface-2/60 text-center text-[13px] text-muted">
                  <div>
                    <Lightning size={22} className="mx-auto mb-2 text-faint" />
                    在令牌页创建 Agent 令牌并接入 /mcp 后，调用记录会出现在这里。
                  </div>
                </div>
              )}
            </Panel>
          </div>

          {/* ── 快捷入口 ── */}
          <div className="mt-4 grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
            {[
              { to: '/keys', icon: <Key size={18} weight="duotone" />, title: 'SSH 密钥', value: `${keyCount} 把`, hint: `${keyHosts} 台主机用密钥登录` },
              { to: '/tokens', icon: <Ticket size={18} weight="duotone" />, title: 'Agent 令牌', value: `${tokens.data?.length ?? 0} 个`, hint: '管理 MCP 访问范围' },
              { to: '/metrics', icon: <ChartLineUp size={18} weight="duotone" />, title: '详细指标', value: `${sampled.length} 台`, hint: '查看单机 CPU / 内存 / 磁盘曲线' },
              { to: '/map', icon: <GlobeHemisphereEast size={18} weight="duotone" />, title: '分布地图', value: `${hosts.length} 台`, hint: '服务器位置、延迟与当地时间' },
            ].map((item) => (
              <motion.div key={item.to} variants={staggerItem}>
                <Link
                  to={item.to}
                  className="group flex items-center gap-3 rounded-[12px] border border-border bg-surface p-4 shadow-card transition-all duration-200 hover:-translate-y-0.5 hover:border-accent/40 hover:shadow-pop"
                >
                  <span className="grid size-10 shrink-0 place-items-center rounded-[10px] bg-accent-soft text-accent">
                    {item.icon}
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="flex items-baseline gap-2">
                      <span className="text-[13px] font-semibold text-text">{item.title}</span>
                      <span className="text-[12px] text-muted tabular-nums">{item.value}</span>
                    </span>
                    <span className="block truncate text-[12px] text-muted">{item.hint}</span>
                  </span>
                  <ArrowRight size={14} className="text-faint transition-transform group-hover:translate-x-0.5 group-hover:text-accent" />
                </Link>
              </motion.div>
            ))}
          </div>

          {/* ── 主机脉搏 ── */}
          <div className="mt-8 mb-3 flex items-baseline justify-between gap-2">
            <div className="flex items-baseline gap-2">
              <h2 className="text-[15px] font-semibold text-text">主机脉搏</h2>
              <span className="text-[12px] text-muted tabular-nums">
                近 {range.label} · {hosts.length} 台
              </span>
            </div>
            <Link to="/hosts" className="inline-flex items-center gap-1 text-[12px] font-medium text-accent hover:text-accent-hover">
              管理主机 <ArrowRight size={12} />
            </Link>
          </div>
          <div className="grid items-stretch gap-4 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
            {snapshots.map((snap) => (
              <HostTile key={snap.host.id} snap={snap} now={now} />
            ))}
          </div>
        </motion.div>
      )}
    </PageTransition>
  )
}
