import { useMemo, useState } from 'react'
import { ChartLine } from '@phosphor-icons/react'
import { useNavigate } from 'react-router-dom'
import {
  Area,
  AreaChart,
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import { useHosts, useMetrics } from '@/api/queries'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { EmptyState } from '@/components/ui/empty-state'
import { PageHeader } from '@/components/ui/page-header'
import { PageTransition } from '@/components/ui/page-transition'
import { Select } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'

/** 系列元信息单点定义：图例取色、Tooltip 的中文名/单位/小数位都从这里来，避免各处各写一份 */
const SERIES = {
  cpu_pct: { label: 'CPU', color: 'var(--chart-cpu)', unit: '%', digits: 1 },
  mem_pct: { label: '内存', color: 'var(--chart-mem)', unit: '%', digits: 1 },
  load1: { label: 'Load 1', color: 'var(--chart-load)', unit: '', digits: 2 },
  disk_pct: { label: '磁盘', color: 'var(--chart-disk)', unit: '%', digits: 1 },
} as const

type SeriesKey = keyof typeof SERIES

const AXIS_TICK = { fontSize: 11 } as const
/** 左边距归零：Y 轴刻度靠 YAxis 的 width 让位，负边距会把「100%」这类刻度裁掉半个字 */
const CHART_MARGIN = { top: 8, right: 4, bottom: 0, left: 0 } as const

const TOOLTIP_TIME = new Intl.DateTimeFormat('zh-CN', {
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
})

const AXIS_TIME = new Intl.DateTimeFormat('zh-CN', {
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
})

/**
 * 时间轴用数值型 + scale=time：60 个采样点若按分类轴逐点打标签必然重叠，
 * 数值轴配合 minTickGap 才能让 recharts 自行抽稀到可读密度。
 * 两张图共用同一份配置，刻度位置才会上下对齐。
 */
const TIME_AXIS: React.ComponentProps<typeof XAxis> = {
  dataKey: 'ts',
  type: 'number',
  scale: 'time',
  domain: ['dataMin', 'dataMax'],
  tickFormatter: (value: number) => AXIS_TIME.format(value),
  minTickGap: 56,
  tickMargin: 8,
  tickLine: false,
  tick: { ...AXIS_TICK, fill: 'var(--muted)' },
  stroke: 'var(--border)',
}

type ChartTooltipEntry = {
  color?: string
  dataKey?: string | number
  value?: string | number
}

type ChartTooltipProps = {
  active?: boolean
  label?: string | number
  payload?: readonly ChartTooltipEntry[]
}

function ChartTooltip({ active, payload, label }: ChartTooltipProps) {
  if (!active || !payload?.length) return null

  return (
    <div className="min-w-[148px] rounded-[12px] border border-border bg-surface px-3 py-2 shadow-pop">
      <p className="mb-1.5 text-[11px] text-muted tabular-nums">
        {TOOLTIP_TIME.format(Number(label))}
      </p>
      <div className="space-y-1">
        {payload.map((item) => {
          const meta = SERIES[String(item.dataKey) as SeriesKey]
          const value = Number(item.value)
          return (
            <div key={String(item.dataKey)} className="flex items-center gap-2 text-xs">
              <span
                className="size-2 shrink-0 rounded-full"
                style={{ background: item.color ?? 'var(--muted)' }}
              />
              <span className="text-muted">{meta?.label ?? item.dataKey}</span>
              <span className="ml-auto tabular-nums text-text">
                {Number.isFinite(value) ? value.toFixed(meta?.digits ?? 1) : '—'}
                {meta?.unit}
              </span>
            </div>
          )
        })}
      </div>
    </div>
  )
}

/** recharts 自带 Legend 难以 token 化（内联字号与颜色），自绘一行小色点更可控 */
function Legend({ items }: { items: { color: string; label: string }[] }) {
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
      {items.map((item) => (
        <span key={item.label} className="flex items-center gap-1.5 text-[12px] text-muted">
          <span className="size-2 rounded-full" style={{ background: item.color }} />
          {item.label}
        </span>
      ))}
    </div>
  )
}

function ChartSkeleton({ height }: { height: number }) {
  return (
    <Card className="flex flex-col">
      <CardHeader>
        <Skeleton className="h-4 w-40" />
        <Skeleton className="h-3 w-24" />
      </CardHeader>
      <CardContent>
        <Skeleton style={{ height }} />
      </CardContent>
    </Card>
  )
}

export function MetricsPage() {
  const [host, setHost] = useState<number>()
  const hosts = useHosts()
  const metrics = useMetrics(host, 24)
  const navigate = useNavigate()

  const selected = hosts.data?.find((item) => item.id === host)
  const diskMount = metrics.data?.[0]?.disks[0]?.mount

  const chart = useMemo(
    () =>
      (metrics.data ?? []).map((x) => ({
        ts: x.ts,
        cpu_pct: x.cpu_pct,
        load1: x.load1,
        mem_pct: x.mem_total_kb ? (100 * (x.mem_used_kb || 0)) / x.mem_total_kb : undefined,
        disk_pct: x.disks[0]?.total_kb
          ? (100 * x.disks[0].used_kb) / x.disks[0].total_kb
          : undefined,
      })),
    [metrics.data],
  )

  return (
    <PageTransition>
      <PageHeader
        title="指标"
        subtitle="24 小时 CPU、内存、负载与磁盘趋势"
        actions={
          <Select
            className="w-52"
            value={host}
            onChange={setHost}
            options={(hosts.data ?? []).map((h) => ({ value: h.id, label: h.name }))}
            placeholder="选择主机"
          />
        }
      />

      {!host ? (
        // text-balance：中文按字断行，不均分的话说明文字常掉出一个两字尾行
        <EmptyState
          className="[&_p]:text-balance"
          icon={<ChartLine size={22} />}
          title="选择一台主机"
          // 副标题已经说了画什么，这里换成用户真正会踩的前提：只有开了监控的主机才有采样
          description="只有开启了「资源监控」的主机会持续采样，其余主机选中后为空。"
        />
      ) : metrics.isLoading ? (
        <div className="space-y-4">
          <ChartSkeleton height={280} />
          <ChartSkeleton height={260} />
        </div>
      ) : chart.length === 0 ? (
        // 「没数据」有两种成因，指向的动作完全不同，分开说才有用
        selected && !selected.monitor_enabled ? (
          <EmptyState
            className="[&_p]:text-balance"
            icon={<ChartLine size={22} />}
            title="该主机未开启资源监控"
            description="在主机页打开「资源监控」后，网关会按周期采集 CPU、内存、负载与磁盘。"
            action={
              <Button variant="outline" onClick={() => navigate('/hosts')}>
                去主机页开启
              </Button>
            }
          />
        ) : (
          <EmptyState
            className="[&_p]:text-balance"
            icon={<ChartLine size={22} />}
            title="暂无采样数据"
            description="监控已开启，正在等待首次采集；若持续为空，请确认主机当前可连通。"
          />
        )
      ) : (
        <div className="space-y-4">
          <Card>
            <CardHeader className="flex-wrap">
              <CardTitle>CPU 与内存占用</CardTitle>
              <Legend
                items={[
                  { color: SERIES.cpu_pct.color, label: 'CPU %' },
                  { color: SERIES.mem_pct.color, label: '内存 %' },
                ]}
              />
            </CardHeader>
            <CardContent>
              <ResponsiveContainer width="100%" height={280}>
                {/* 右边距留出与下图右轴等宽的空白，两张图的绘图区因此完全对齐，时间刻度上下成列 */}
                <AreaChart data={chart} margin={{ ...CHART_MARGIN, right: 44 }}>
                  <defs>
                    <linearGradient id="grad-cpu" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor={SERIES.cpu_pct.color} stopOpacity={0.22} />
                      <stop offset="100%" stopColor={SERIES.cpu_pct.color} stopOpacity={0} />
                    </linearGradient>
                    <linearGradient id="grad-mem" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor={SERIES.mem_pct.color} stopOpacity={0.18} />
                      <stop offset="100%" stopColor={SERIES.mem_pct.color} stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  {/* 只留横向网格：竖线对读数没帮助，却会把图面切碎 */}
                  <CartesianGrid vertical={false} stroke="var(--border)" />
                  <XAxis {...TIME_AXIS} />
                  <YAxis
                    domain={[0, 100]}
                    ticks={[0, 25, 50, 75, 100]}
                    width={40}
                    axisLine={false}
                    tickLine={false}
                    tickFormatter={(v: number) => `${v}%`}
                    tick={{ ...AXIS_TICK, fill: 'var(--muted)' }}
                  />
                  <Tooltip
                    content={<ChartTooltip />}
                    cursor={{ stroke: 'var(--border-strong)', strokeWidth: 1 }}
                  />
                  <Area
                    type="monotone"
                    dataKey="cpu_pct"
                    stroke={SERIES.cpu_pct.color}
                    strokeWidth={1.75}
                    fill="url(#grad-cpu)"
                    dot={false}
                    activeDot={{ r: 3, strokeWidth: 0 }}
                    // recharts 自带的 1.5s 描线动画会在每次容器尺寸变化时重放（窗口缩放、响应式断点切换都会触发），
                    // 页面本身已有 PageTransition 的淡入，这里保持静默即可
                    isAnimationActive={false}
                  />
                  <Area
                    type="monotone"
                    dataKey="mem_pct"
                    stroke={SERIES.mem_pct.color}
                    strokeWidth={1.75}
                    fill="url(#grad-mem)"
                    dot={false}
                    activeDot={{ r: 3, strokeWidth: 0 }}
                    isAnimationActive={false}
                  />
                </AreaChart>
              </ResponsiveContainer>
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="flex-wrap">
              <CardTitle>
                系统负载与磁盘占用
                {/* 只画首个挂载点，标题里点名是哪个盘，否则「磁盘」是句无从核对的话。
                    带上「挂载点」前缀：根分区的 mount 就是一个 "/"，裸挂在标题后像打错的斜杠 */}
                {diskMount && (
                  <span className="ml-2 text-[11px] font-normal text-muted">
                    挂载点 <span className="font-mono">{diskMount}</span>
                  </span>
                )}
              </CardTitle>
              <Legend
                items={[
                  { color: SERIES.load1.color, label: 'Load 1（左轴）' },
                  { color: SERIES.disk_pct.color, label: '磁盘 %（右轴）' },
                ]}
              />
            </CardHeader>
            <CardContent>
              <ResponsiveContainer width="100%" height={260}>
                {/* 负载（约 0–3）与磁盘占用（约 60–80%）量级差一个数量级，
                    共用一根 Y 轴会把负载压成一条贴底的直线，故拆双轴并给轴染上系列色 */}
                <LineChart data={chart} margin={{ ...CHART_MARGIN, right: 0 }}>
                  <CartesianGrid vertical={false} stroke="var(--border)" />
                  <XAxis {...TIME_AXIS} />
                  <YAxis
                    yAxisId="load"
                    width={40}
                    axisLine={false}
                    tickLine={false}
                    // 图例已标注左/右轴归属，刻度再染成系列色纯属重复，而 amber 在浅色底上不足 3:1
                    tick={{ ...AXIS_TICK, fill: 'var(--muted)' }}
                  />
                  <YAxis
                    yAxisId="disk"
                    orientation="right"
                    domain={[0, 100]}
                    ticks={[0, 25, 50, 75, 100]}
                    width={44}
                    axisLine={false}
                    tickLine={false}
                    tickFormatter={(v: number) => `${v}%`}
                    tick={{ ...AXIS_TICK, fill: 'var(--muted)' }}
                  />
                  <Tooltip
                    content={<ChartTooltip />}
                    cursor={{ stroke: 'var(--border-strong)', strokeWidth: 1 }}
                  />
                  <Line
                    yAxisId="load"
                    type="monotone"
                    dataKey="load1"
                    stroke={SERIES.load1.color}
                    dot={false}
                    strokeWidth={1.75}
                    activeDot={{ r: 3, strokeWidth: 0 }}
                    isAnimationActive={false}
                  />
                  <Line
                    yAxisId="disk"
                    type="monotone"
                    dataKey="disk_pct"
                    stroke={SERIES.disk_pct.color}
                    dot={false}
                    strokeWidth={1.75}
                    activeDot={{ r: 3, strokeWidth: 0 }}
                    isAnimationActive={false}
                  />
                </LineChart>
              </ResponsiveContainer>
            </CardContent>
          </Card>
        </div>
      )}
    </PageTransition>
  )
}
