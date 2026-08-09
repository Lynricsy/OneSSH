import { ChartLine, HardDrives, Pulse } from '@phosphor-icons/react'
import { useQueries } from '@tanstack/react-query'
import { motion } from 'motion/react'
import { Link, useNavigate } from 'react-router-dom'
import { Area, AreaChart, ResponsiveContainer, YAxis } from 'recharts'
import { api } from '@/api/client'
import { queryKeys, useHosts, useMetrics } from '@/api/queries'
import type { Host, Metric } from '@/api/types'
import { Badge, Dot } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { HostConnection } from '@/components/host-connection'
import { EmptyState } from '@/components/ui/empty-state'
import { PageHeader } from '@/components/ui/page-header'
import { PageTransition } from '@/components/ui/page-transition'
import { Skeleton } from '@/components/ui/skeleton'
import { StatCard, StatGroup } from '@/components/ui/stat-card'
import { formatPct } from '@/lib/format'
import { staggerContainer, staggerItem } from '@/lib/motion'

function MetricValue({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <p className="text-[11px] font-medium tracking-wide text-muted uppercase">{label}</p>
      <p className="mt-1 truncate text-[17px] leading-none font-medium text-text tabular-nums">{value}</p>
    </div>
  )
}

function HostPulseCard({ host }: { host: Host }) {
  const { data: series = [] } = useMetrics(host.id, 1)
  const metric = series[series.length - 1]
  const memory = metric?.mem_total_kb
    ? `${Math.round(((metric.mem_used_kb ?? 0) / metric.mem_total_kb) * 100)}%`
    : '—'
  const load = metric?.load1 == null ? '—' : metric.load1.toFixed(2)
  const gradientId = `spark-${host.id}`

  return (
    <motion.div variants={staggerItem} className="h-full">
      <Card className="flex h-full flex-col overflow-hidden p-4">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <p className="truncate text-sm font-medium text-text">{host.name}</p>
            <HostConnection host={host} />
          </div>
          {metric ? (
            <Badge variant="accent">已采样</Badge>
          ) : host.monitor_enabled ? (
            <Badge>
              <Dot pulse />
              等待采样
            </Badge>
          ) : (
            <Badge variant="outline">未监控</Badge>
          )}
        </div>

        {metric ? (
          <>
            <div className="mt-5 grid grid-cols-3 gap-3">
              <MetricValue label="CPU" value={formatPct(metric.cpu_pct)} />
              <MetricValue label="内存" value={memory} />
              <MetricValue label="负载" value={load} />
            </div>
            {/* 单个采样点画不出线，只会留一条空白带——数据够两点才铺图。
                出血到卡片下沿：省掉一个灰底容器，渐变自己收尾 */}
            {series.length > 1 && (
              <div className="-mx-4 -mb-4 mt-5 h-14">
                <ResponsiveContainer width="100%" height="100%">
                  <AreaChart data={series} margin={{ top: 3, right: 0, bottom: 0, left: 0 }}>
                    <defs>
                      <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
                        <stop offset="0%" stopColor="var(--chart-cpu)" stopOpacity={0.28} />
                        <stop offset="100%" stopColor="var(--chart-cpu)" stopOpacity={0} />
                      </linearGradient>
                    </defs>
                    {/* 留 5 个点的上下余量：既不会把平稳曲线贴到边框上，也不会把小波动放大成假峰 */}
                    <YAxis
                      hide
                      domain={[
                        (min: number) => Math.max(0, min - 5),
                        (max: number) => Math.min(100, max + 5),
                      ]}
                    />
                    <Area
                      type="monotone"
                      dataKey="cpu_pct"
                      stroke="var(--chart-cpu)"
                      strokeWidth={1.5}
                      fill={`url(#${gradientId})`}
                      dot={false}
                      isAnimationActive={false}
                    />
                  </AreaChart>
                </ResponsiveContainer>
              </div>
            )}
          </>
        ) : (
          // 一排破折号既难看又不解释原因，直接换成一句说明——顺带区分「没开监控」和「开了还没采到」
          <p className="mt-auto rounded-[8px] bg-surface-2 px-3 py-2.5 text-[12px] leading-5 text-muted">
            {host.monitor_enabled ? (
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

export function DashboardPage() {
  const navigate = useNavigate()
  const { data: hosts = [], isLoading } = useHosts()
  const metricQueries = useQueries({
    queries: hosts.map((host) => ({
      queryKey: queryKeys.metrics(host.id, 1),
      queryFn: () => api<Metric[]>(`/metrics/${host.id}?hours=1`),
    })),
  })

  const sampledHosts = metricQueries.filter((query) => (query.data?.length ?? 0) > 0).length
  const monitoredHosts = hosts.filter((host) => host.monitor_enabled).length
  const coverage = hosts.length ? Math.round((monitoredHosts / hosts.length) * 100) : 0

  return (
    <PageTransition>
      <PageHeader title="运行概览" subtitle="主机资源与网关状态" />

      {isLoading ? (
        <>
          <Skeleton className="h-[92px] rounded-[12px] md:h-[104px]" />
          <div className="mt-8 mb-3 h-5 w-24">
            <Skeleton className="h-4 w-24" />
          </div>
          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
            {Array.from({ length: 3 }, (_, index) => (
              <Skeleton key={index} className="h-[168px] rounded-[12px]" />
            ))}
          </div>
        </>
      ) : (
        <>
          <StatGroup>
            <StatCard title="管理主机" value={hosts.length} suffix="台" icon={<HardDrives size={16} />} />
            <StatCard title="有新鲜指标" value={sampledHosts} suffix="台" icon={<Pulse size={16} />} />
            <StatCard title="监控覆盖" value={coverage} suffix="%" icon={<ChartLine size={16} />} />
          </StatGroup>

          <div className="mt-8 mb-3 flex items-baseline gap-2">
            <h2 className="text-sm font-semibold text-text">主机脉搏</h2>
            <span className="text-[12px] text-muted tabular-nums">近 1 小时 · {hosts.length} 台</span>
          </div>

          {hosts.length ? (
            <motion.div
              className="grid items-stretch gap-4 md:grid-cols-2 xl:grid-cols-3"
              variants={staggerContainer}
              initial="hidden"
              animate="visible"
            >
              {hosts.map((host) => (
                <HostPulseCard key={host.id} host={host} />
              ))}
            </motion.div>
          ) : (
            <Card>
              <EmptyState
                icon={<HardDrives size={22} />}
                title="还没有 SSH 主机"
                description="添加第一台主机后，这里会显示实时资源脉搏。"
                action={
                  <Button variant="primary" onClick={() => navigate('/hosts')}>
                    添加第一台主机
                  </Button>
                }
              />
            </Card>
          )}
        </>
      )}
    </PageTransition>
  )
}
