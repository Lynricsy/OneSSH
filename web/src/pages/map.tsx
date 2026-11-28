import { useEffect, useMemo, useState } from 'react'
import {
  ArrowCounterClockwise,
  ArrowsClockwise,
  Buildings,
  Clock,
  Crosshair,
  GlobeHemisphereEast,
  MapPin,
  MapPinLine,
  NavigationArrow,
  PencilSimple,
  Timer,
  WarningCircle,
  X,
} from '@phosphor-icons/react'
import { AnimatePresence, motion } from 'motion/react'
import { Link, useSearchParams } from 'react-router-dom'
import { useRefreshGeo, useResetGeo, useSetGeo } from '@/api/queries'
import type { GeoInfo, GeoManualPayload } from '@/api/types'
import { CountryFlag } from '@/components/country-flag'
import { LatencyPill, LocationCell } from '@/components/location-cell'
import { WorldMap, countryFill, type MapPoint } from '@/components/world-map'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { DataTable, type Column } from '@/components/ui/data-table'
import { Dialog } from '@/components/ui/dialog'
import { EmptyState } from '@/components/ui/empty-state'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { PageHeader } from '@/components/ui/page-header'
import { PageTransition } from '@/components/ui/page-transition'
import { Select } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { StatStrip } from '@/components/ui/stat-strip'
import { Switch } from '@/components/ui/switch'
import { cn } from '@/lib/cn'
import { countryName } from '@/lib/countries'
import { latencyTone, placeLabel, useServerLocations, type LocationGroup } from '@/lib/use-locations'

const VIEWS = [
  { value: 150, label: '亚太居中' },
  { value: 20, label: '欧非居中' },
  { value: -95, label: '美洲居中' },
]

/** 手动标注用的常见机房城市 */
const PRESETS: Array<{ city: string; code: string; region?: string; lat: number; lon: number }> = [
  { city: '北京', code: 'CN', lat: 39.9042, lon: 116.4074 },
  { city: '上海', code: 'CN', lat: 31.2304, lon: 121.4737 },
  { city: '广州', code: 'CN', lat: 23.1291, lon: 113.2644 },
  { city: '深圳', code: 'CN', lat: 22.5431, lon: 114.0579 },
  { city: '杭州', code: 'CN', lat: 30.2741, lon: 120.1551 },
  { city: '成都', code: 'CN', lat: 30.5728, lon: 104.0668 },
  { city: '青岛', code: 'CN', lat: 36.0671, lon: 120.3826 },
  { city: '乌兰察布', code: 'CN', lat: 40.9939, lon: 113.1328 },
  { city: '香港', code: 'HK', lat: 22.3193, lon: 114.1694 },
  { city: '台北', code: 'TW', lat: 25.033, lon: 121.5654 },
  { city: '东京', code: 'JP', lat: 35.6762, lon: 139.6503 },
  { city: '大阪', code: 'JP', lat: 34.6937, lon: 135.5023 },
  { city: '首尔', code: 'KR', lat: 37.5665, lon: 126.978 },
  { city: '新加坡', code: 'SG', lat: 1.3521, lon: 103.8198 },
  { city: '曼谷', code: 'TH', lat: 13.7563, lon: 100.5018 },
  { city: '孟买', code: 'IN', lat: 19.076, lon: 72.8777 },
  { city: '迪拜', code: 'AE', lat: 25.2048, lon: 55.2708 },
  { city: '法兰克福', code: 'DE', lat: 50.1109, lon: 8.6821 },
  { city: '伦敦', code: 'GB', lat: 51.5074, lon: -0.1278 },
  { city: '阿姆斯特丹', code: 'NL', lat: 52.3676, lon: 4.9041 },
  { city: '巴黎', code: 'FR', lat: 48.8566, lon: 2.3522 },
  { city: '莫斯科', code: 'RU', lat: 55.7558, lon: 37.6173 },
  { city: '洛杉矶', code: 'US', lat: 34.0522, lon: -118.2437 },
  { city: '圣何塞', code: 'US', lat: 37.3382, lon: -121.8863 },
  { city: '西雅图', code: 'US', lat: 47.6062, lon: -122.3321 },
  { city: '达拉斯', code: 'US', lat: 32.7767, lon: -96.797 },
  { city: '纽约', code: 'US', lat: 40.7128, lon: -74.006 },
  { city: '弗吉尼亚', code: 'US', lat: 39.0438, lon: -77.4874 },
  { city: '多伦多', code: 'CA', lat: 43.6532, lon: -79.3832 },
  { city: '圣保罗', code: 'BR', lat: -23.5505, lon: -46.6333 },
  { city: '悉尼', code: 'AU', lat: -33.8688, lon: 151.2093 },
]

function useNow(intervalMs: number) {
  const [now, setNow] = useState(() => new Date())
  useEffect(() => {
    const timer = setInterval(() => setNow(new Date()), intervalMs)
    return () => clearInterval(timer)
  }, [intervalMs])
  return now
}

function localTime(timezone: string, now: Date) {
  if (!timezone) return null
  try {
    const time = new Intl.DateTimeFormat('zh-CN', { timeZone: timezone, hour: '2-digit', minute: '2-digit', hour12: false }).format(now)
    const offset =
      new Intl.DateTimeFormat('en-US', { timeZone: timezone, timeZoneName: 'shortOffset' })
        .formatToParts(now)
        .find((part) => part.type === 'timeZoneName')?.value ?? ''
    return { time, offset: offset.replace('GMT', 'UTC') || 'UTC' }
  } catch {
    return null
  }
}

const fmtKm = (km?: number) => (km == null ? '—' : km >= 1000 ? `${(km / 1000).toFixed(1)} 千公里` : `${Math.round(km)} 公里`)

const SOURCE_BADGE: Record<GeoInfo['source'], { label: string; variant: 'accent' | 'default' | 'warning' | 'outline' }> = {
  auto: { label: '自动定位', variant: 'accent' },
  manual: { label: '手动标注', variant: 'default' },
  private: { label: '内网地址', variant: 'outline' },
  unresolved: { label: '未定位', variant: 'warning' },
  origin: { label: '网关', variant: 'default' },
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
    <div role="radiogroup" aria-label={label} className="inline-flex rounded-[10px] border border-border bg-surface-2 p-0.5">
      {options.map((option) => (
        <button
          key={String(option.value)}
          type="button"
          role="radio"
          aria-checked={option.value === value}
          onClick={() => onChange(option.value)}
          className={cn(
            'rounded-[8px] px-2.5 py-1 text-[12px] font-medium transition-all duration-150',
            option.value === value ? 'bg-surface text-text shadow-card' : 'text-muted hover:text-text',
          )}
        >
          {option.label}
        </button>
      ))}
    </div>
  )
}

type EditState = { info: GeoInfo; origin: boolean; form: GeoManualPayload & { preset: string } }

function toForm(info: GeoInfo): EditState['form'] {
  return {
    preset: '',
    country_code: info.country_code,
    country: info.country || (info.country_code ? countryName(info.country_code) : ''),
    region: info.region,
    city: info.city,
    lat: info.lat,
    lon: info.lon,
  }
}

export function MapPage() {
  const [params, setParams] = useSearchParams()
  const [centerLon, setCenterLon] = useState<number>(() => {
    const saved = Number(localStorage.getItem('onessh-map-center'))
    return VIEWS.some((v) => v.value === saved) ? saved : 150
  })
  const [includeArchived, setIncludeArchived] = useState(false)
  const [showArcs, setShowArcs] = useState(true)
  const [selectedKey, setSelectedKey] = useState<string | null>(null)
  const [editing, setEditing] = useState<EditState | null>(null)
  const now = useNow(30_000)
  const data = useServerLocations({ includeArchived })
  const refresh = useRefreshGeo()
  const setGeo = useSetGeo()
  const resetGeo = useResetGeo()

  useEffect(() => localStorage.setItem('onessh-map-center', String(centerLon)), [centerLon])

  // 从命令面板或主机页带 ?host=ID 进来：直接选中该主机所在的地点
  useEffect(() => {
    const id = Number(params.get('host'))
    if (!id || !data.locations.length) return
    const location = data.locations.find((loc) => loc.hosts.some((h) => h.host_id === id))
    if (location) setSelectedKey(location.key)
    params.delete('host')
    setParams(params, { replace: true })
  }, [params, setParams, data.locations])

  const points: MapPoint[] = useMemo(
    () =>
      data.locations.map((loc) => ({
        key: loc.key,
        lat: loc.lat,
        lon: loc.lon,
        color: loc.latency != null ? latencyTone({ latency_ms: loc.latency }).color : '#14b8a6',
        title: loc.hosts.length > 1 ? `${loc.city || loc.country} ×${loc.hosts.length}` : loc.hosts[0].host,
        subtitle: `${loc.city ? `${loc.city} · ` : ''}${loc.country}${loc.latency != null ? ` · ${loc.latency} ms` : ''}`,
        lines: loc.hosts.map((h) => `${h.host}  ${h.ip}`),
        count: loc.hosts.length,
      })),
    [data.locations],
  )

  const selected = data.locations.find((loc) => loc.key === selectedKey)
  const maxCountry = Math.max(1, ...data.countries.map((c) => c.hosts.length))

  const openEdit = (info: GeoInfo, origin = false) => setEditing({ info, origin, form: toForm(info) })

  const columns: Column<GeoInfo>[] = [
    {
      key: 'host',
      title: '主机',
      className: 'min-w-[170px]',
      render: (info) => (
        <div className="min-w-0">
          <p className="flex items-center gap-2 truncate font-medium text-text">
            {info.host}
            {info.archived && <Badge variant="outline">已归档</Badge>}
          </p>
          <p className="truncate font-mono text-[12px] text-muted">{info.ip || info.addr}</p>
        </div>
      ),
    },
    {
      key: 'place',
      title: '位置',
      className: 'min-w-[180px]',
      render: (info) => <LocationCell info={info} showLatency={false} />,
    },
    {
      key: 'isp',
      title: '运营商 / ASN',
      className: 'hidden min-w-[180px] lg:table-cell',
      render: (info) =>
        info.isp || info.asn ? (
          <div className="min-w-0 text-[12px]">
            <p className="truncate text-text" title={info.isp}>{info.isp || info.org}</p>
            <p className="truncate font-mono text-muted" title={info.asn}>{info.asn}</p>
          </div>
        ) : (
          <span className="text-[12px] text-faint">—</span>
        ),
    },
    {
      key: 'time',
      title: '当地时间',
      className: 'hidden w-[130px] md:table-cell',
      render: (info) => {
        const t = localTime(info.timezone, now)
        return t ? (
          <div className="text-[12px]">
            <p className="font-medium text-text tabular-nums">{t.time}</p>
            <p className="text-muted">{t.offset}</p>
          </div>
        ) : (
          <span className="text-[12px] text-faint">—</span>
        )
      },
    },
    {
      key: 'latency',
      title: '延迟',
      className: 'w-[110px]',
      render: (info) => <LatencyPill item={data.latencyById.get(info.host_id)} />,
    },
    {
      key: 'distance',
      title: '距网关',
      className: 'hidden w-[110px] xl:table-cell',
      render: (info) => {
        const loc = data.locations.find((l) => l.hosts.some((h) => h.host_id === info.host_id))
        return <span className="text-[12px] text-muted tabular-nums">{fmtKm(loc?.distanceKm)}</span>
      },
    },
    {
      key: 'source',
      title: '来源',
      className: 'w-[100px]',
      render: (info) => (
        <span title={info.error}>
          <Badge variant={SOURCE_BADGE[info.source].variant}>{SOURCE_BADGE[info.source].label}</Badge>
        </span>
      ),
    },
    {
      key: 'actions',
      title: '操作',
      className: 'w-[120px] text-right',
      render: (info) => (
        <div className="flex justify-end gap-1">
          <Button size="sm" variant="ghost" onClick={() => openEdit(info)} title="手动标注位置">
            <PencilSimple size={14} />
            标注
          </Button>
        </div>
      ),
    },
  ]

  const loading = data.geo.isLoading
  const total = data.all.length

  return (
    <PageTransition>
      <PageHeader
        title="服务器分布"
        subtitle="按公网 IP 自动定位 · 国家/地区颜色越深表示服务器越多"
        actions={
          <>
            <Segmented label="地图视角" value={centerLon} onChange={setCenterLon} options={VIEWS} />
            <Button variant="outline" onClick={() => refresh.mutate()} loading={refresh.isPending}>
              {!refresh.isPending && <ArrowsClockwise size={15} />}
              重新定位
            </Button>
          </>
        }
      />

      <StatStrip
        items={[
          {
            label: '覆盖国家/地区',
            value: loading ? '…' : data.countries.length,
            hint: data.countries.slice(0, 3).map((c) => c.name).join('、') || '—',
            icon: <GlobeHemisphereEast size={18} weight="duotone" />,
            tone: '#14b8a6',
          },
          {
            label: '城市 / 机房',
            value: loading ? '…' : data.cityCount,
            hint: `${data.locations.length} 个落点`,
            icon: <Buildings size={18} weight="duotone" />,
            tone: '#6366f1',
          },
          {
            label: '已定位主机',
            value: loading ? '…' : `${data.located.length} / ${total}`,
            hint: data.unlocated.length ? `${data.unlocated.length} 台待标注` : '全部已定位',
            icon: <MapPin size={18} weight="duotone" />,
            tone: data.unlocated.length ? '#f59e0b' : '#22c55e',
          },
          {
            label: '平均延迟',
            value: data.avgLatency == null ? '—' : `${data.avgLatency} ms`,
            hint: '网关 → SSH 服务响应',
            icon: <Timer size={18} weight="duotone" />,
            tone: data.avgLatency == null ? '#94a3b8' : latencyTone({ latency_ms: data.avgLatency }).color,
          },
          {
            label: '最远落点',
            value: fmtKm(data.farthest?.distanceKm),
            hint: data.farthest ? `${data.farthest.city || data.farthest.country}` : '需要网关定位',
            icon: <NavigationArrow size={18} weight="duotone" />,
            tone: '#f43f5e',
          },
        ]}
      />

      {data.geo.isError ? (
        <Card>
          <EmptyState
            icon={<WarningCircle size={22} />}
            title="位置数据加载失败"
            description={(data.geo.error as Error).message}
            action={<Button variant="outline" onClick={() => void data.geo.refetch()}>重试</Button>}
          />
        </Card>
      ) : (
        <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_340px]">
          <Card className="relative overflow-hidden p-0">
            {loading ? (
              <Skeleton className="aspect-[960/500] w-full rounded-none" />
            ) : (
              <WorldMap
                points={points}
                countries={data.countryStats}
                origin={
                  data.origin
                    ? {
                        lat: data.origin.lat,
                        lon: data.origin.lon,
                        title: 'OneSSH 网关（你在这里）',
                        subtitle: `${placeLabel(data.origin)} · ${data.origin.ip}`,
                      }
                    : undefined
                }
                centerLon={centerLon}
                showArcs={showArcs}
                selectedKey={selectedKey}
                onSelect={setSelectedKey}
              >
                <div className="pointer-events-none absolute bottom-3 left-3 flex flex-wrap items-end gap-2">
                  <div className="pointer-events-auto rounded-[10px] border border-border bg-surface/85 px-3 py-2 text-[11px] text-muted shadow-card backdrop-blur">
                    <p className="mb-1.5 font-medium text-text">服务器数量</p>
                    <div className="flex items-center gap-2">
                      {[1, 2, 3, 5].map((n) => (
                        <span key={n} className="inline-flex items-center gap-1">
                          <span className="h-2.5 w-4 rounded-[3px]" style={{ background: countryFill(n) }} />
                          {n === 5 ? '5+' : n}
                        </span>
                      ))}
                    </div>
                    <p className="mt-2 mb-1 font-medium text-text">延迟</p>
                    <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
                      {[
                        ['#22c55e', '≤80'],
                        ['#14b8a6', '≤160'],
                        ['#f59e0b', '≤260'],
                        ['#f43f5e', '更高'],
                      ].map(([color, label]) => (
                        <span key={label} className="inline-flex items-center gap-1">
                          <span className="size-2 rounded-full" style={{ background: color }} />
                          {label}
                        </span>
                      ))}
                      {data.origin && (
                        <span className="inline-flex items-center gap-1">
                          <span className="size-2 rotate-45 rounded-[1px] bg-[var(--accent-2)]" />
                          网关
                        </span>
                      )}
                    </div>
                  </div>
                  <label className="pointer-events-auto inline-flex items-center gap-2 rounded-[10px] border border-border bg-surface/85 px-3 py-2 text-[11px] text-muted shadow-card backdrop-blur">
                    <Switch checked={showArcs} onCheckedChange={setShowArcs} aria-label="显示连线" />
                    连线
                  </label>
                  <label className="pointer-events-auto inline-flex items-center gap-2 rounded-[10px] border border-border bg-surface/85 px-3 py-2 text-[11px] text-muted shadow-card backdrop-blur">
                    <Switch checked={includeArchived} onCheckedChange={setIncludeArchived} aria-label="包含已归档" />
                    含归档
                  </label>
                </div>

                <AnimatePresence>
                  {selected && (
                    <motion.div
                      key={selected.key}
                      initial={{ opacity: 0, y: 8 }}
                      animate={{ opacity: 1, y: 0 }}
                      exit={{ opacity: 0, y: 8 }}
                      transition={{ duration: 0.18 }}
                      className="absolute right-3 bottom-3 w-[300px] max-w-[calc(100%-1.5rem)] rounded-[12px] border border-border bg-surface/95 p-3.5 shadow-pop backdrop-blur"
                    >
                      <SelectedCard loc={selected} now={now} onClose={() => setSelectedKey(null)} latency={data.latencyById} />
                    </motion.div>
                  )}
                </AnimatePresence>
              </WorldMap>
            )}
            <p className="border-t border-border px-4 py-2 text-[11px] text-faint">
              滚轮缩放 · 拖动平移 · 双击放大 · 点击落点查看详情。位置由 ip-api.com 按公网 IP 推断，仅供参考；地图为示意图，不代表任何边界立场。
              {!data.enabled && ' 当前已设置 ONESSH_GEOIP=off，只显示手动标注的位置。'}
            </p>
          </Card>

          <Card className="flex max-h-[calc(100dvh-12rem)] min-h-[320px] flex-col p-0">
            <div className="flex items-center justify-between border-b border-border px-4 py-3">
              <div>
                <h2 className="text-[14px] font-semibold text-text">地区分布</h2>
                <p className="text-[12px] text-muted">点击地区在地图上定位</p>
              </div>
              <Badge variant="accent">{data.countries.length} 个地区</Badge>
            </div>
            <div className="flex-1 space-y-2 overflow-y-auto p-3">
              {loading &&
                Array.from({ length: 3 }, (_, i) => <Skeleton key={i} className="h-[72px] rounded-[10px]" />)}
              {!loading && data.countries.length === 0 && (
                <p className="px-2 py-8 text-center text-[13px] text-muted">还没有定位到任何主机</p>
              )}
              {data.countries.map((country) => {
                const active = selected && country.locationKeys.includes(selected.key)
                return (
                  <button
                    key={country.code || country.name}
                    type="button"
                    onClick={() => setSelectedKey(active ? null : country.locationKeys[0])}
                    className={cn(
                      'w-full rounded-[11px] border p-3 text-left transition-all duration-150',
                      active
                        ? 'border-accent/50 bg-accent-soft shadow-[0_0_0_3px_var(--accent-soft)]'
                        : 'border-border bg-surface hover:border-border-strong hover:bg-surface-2/60',
                    )}
                  >
                    <div className="flex items-center gap-2.5">
                      <CountryFlag code={country.code} className="h-[15px] w-5" />
                      <span className="min-w-0 flex-1 truncate text-[13px] font-semibold text-text">{country.name}</span>
                      <span className="text-[12px] text-muted tabular-nums">
                        <b className="text-[15px] font-semibold text-text">{country.hosts.length}</b> 台
                      </span>
                    </div>
                    <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-surface-2">
                      <motion.div
                        className="h-full rounded-full"
                        style={{ background: 'linear-gradient(90deg, var(--accent), var(--accent-2))' }}
                        initial={{ width: 0 }}
                        animate={{ width: `${(country.hosts.length / maxCountry) * 100}%` }}
                        transition={{ duration: 0.6, ease: 'easeOut' }}
                      />
                    </div>
                    <div className="mt-2 flex flex-wrap gap-1">
                      {country.hosts.map((h) => (
                        <span
                          key={h.host_id}
                          className="inline-flex items-center gap-1 rounded-full bg-surface-2 px-2 py-0.5 text-[11px] text-text"
                        >
                          <span
                            className="size-1.5 rounded-full"
                            style={{ background: latencyTone(data.latencyById.get(h.host_id)).color }}
                          />
                          {h.host}
                          {h.city && <span className="text-muted">· {h.city}</span>}
                        </span>
                      ))}
                    </div>
                  </button>
                )
              })}

              {data.unlocated.length > 0 && (
                <div className="rounded-[11px] border border-dashed border-border-strong p-3">
                  <p className="flex items-center gap-1.5 text-[12px] font-medium text-text">
                    <MapPinLine size={14} className="text-warning" />
                    待标注 {data.unlocated.length} 台
                  </p>
                  <ul className="mt-2 space-y-1">
                    {data.unlocated.map((h) => (
                      <li key={h.host_id} className="flex items-center justify-between gap-2 text-[12px]">
                        <span className="min-w-0 truncate text-text" title={h.error}>
                          {h.host}
                          <span className="ml-1.5 text-muted">{h.source === 'private' ? '内网' : '定位失败'}</span>
                        </span>
                        <button type="button" className="shrink-0 font-medium text-accent hover:underline" onClick={() => openEdit(h)}>
                          标注
                        </button>
                      </li>
                    ))}
                  </ul>
                </div>
              )}
            </div>
          </Card>
        </div>
      )}

      <Card className="mt-4 overflow-hidden p-0">
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-5 py-3.5">
          <div>
            <h2 className="text-[14px] font-semibold text-text">位置明细</h2>
            <p className="text-[12px] text-muted">IP、运营商、当地时间与网络延迟；定位不准可手动标注</p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {data.origin && (
              <p
                className="inline-flex items-center gap-1.5 rounded-full border border-border bg-surface-2/60 px-3 py-1 text-[12px] text-muted"
                title="连线与距离都以这里为起点"
              >
                <Crosshair size={13} className="text-[var(--accent-2)]" />
                {data.origin.source === 'manual' ? '网关位置（手动）：' : '网关出口：'}
                <CountryFlag code={data.origin.country_code} />
                <span className="text-text">{placeLabel(data.origin)}</span>
                {data.origin.ip && <span className="font-mono">{data.origin.ip}</span>}
              </p>
            )}
            <Button
              size="sm"
              variant="ghost"
              title="开着代理 / VPN 时自动检测到的是代理出口，可以改成你的真实位置"
              onClick={() =>
                openEdit(
                  data.origin ?? {
                    host_id: 0, host: 'OneSSH 网关', addr: '', archived: false, ip: '', country_code: '', country: '', region: '',
                    city: '', lat: 0, lon: 0, isp: '', org: '', asn: '', timezone: '', source: 'origin', updated_at: 0,
                  },
                  true,
                )
              }
            >
              <PencilSimple size={14} />
              设置网关位置
            </Button>
          </div>
        </div>
        <DataTable
          columns={columns}
          rows={data.all}
          rowKey={(info) => info.host_id}
          loading={loading}
          onRowClick={(info) => {
            const loc = data.locations.find((l) => l.hosts.some((h) => h.host_id === info.host_id))
            if (loc) {
              setSelectedKey(loc.key)
              window.scrollTo({ top: 0, behavior: 'smooth' })
            }
          }}
          empty={
            <EmptyState
              icon={<GlobeHemisphereEast size={22} />}
              title="还没有主机"
              description="添加主机后会自动按公网 IP 显示在地图上。"
              action={
                <Link to="/hosts?new=1">
                  <Button variant="primary">添加主机</Button>
                </Link>
              }
            />
          }
        />
      </Card>

      <Dialog
        open={editing != null}
        onOpenChange={(open) => !open && setEditing(null)}
        title={editing ? (editing.origin ? '设置网关位置' : `标注位置 · ${editing.info.host}`) : '标注位置'}
        description={
          editing?.origin
            ? '开着代理 / VPN 时自动检测到的是代理出口；改成你的真实所在城市，连线与距离会以这里为起点'
            : '自动定位不准或是内网主机时，手动指定它所在的城市'
        }
        footer={
          editing && (
            <>
              {editing.info.source === 'manual' && (
                <Button
                  variant="ghost"
                  className="mr-auto"
                  loading={resetGeo.isPending}
                  onClick={async () => {
                    await resetGeo.mutateAsync(editing.origin ? 'origin' : editing.info.host_id)
                    setEditing(null)
                  }}
                >
                  <ArrowCounterClockwise size={14} />
                  恢复自动定位
                </Button>
              )}
              <Button variant="ghost" onClick={() => setEditing(null)}>
                取消
              </Button>
              <Button
                variant="primary"
                loading={setGeo.isPending}
                disabled={!editing.form.lat && !editing.form.lon}
                onClick={async () => {
                  const { preset: _preset, ...payload } = editing.form
                  await setGeo.mutateAsync({ hostId: editing.origin ? 'origin' : editing.info.host_id, payload })
                  setEditing(null)
                }}
              >
                保存位置
              </Button>
            </>
          )
        }
      >
        {editing && (
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="常用机房城市" hint="选一个即可自动填好下面的字段" className="sm:col-span-2">
              {(id) => (
                <Select
                  id={id}
                  value={editing.form.preset || undefined}
                  placeholder="选择城市…"
                  onChange={(value) => {
                    const preset = PRESETS.find((p) => `${p.code}:${p.city}` === value)
                    if (!preset) return
                    setEditing({
                      ...editing,
                      form: {
                        preset: value,
                        country_code: preset.code,
                        country: countryName(preset.code),
                        region: preset.region ?? '',
                        city: preset.city,
                        lat: preset.lat,
                        lon: preset.lon,
                      },
                    })
                  }}
                  options={PRESETS.map((p) => ({ value: `${p.code}:${p.city}`, label: `${p.city} · ${countryName(p.code)}` }))}
                />
              )}
            </Field>
            {(
              [
                ['country_code', '国家代码', 'CN / SG / US'],
                ['country', '国家/地区', '中国'],
                ['city', '城市', '上海'],
                ['region', '省 / 州', '可不填'],
              ] as const
            ).map(([key, label, placeholder]) => (
              <Field key={key} label={label}>
                {(id) => (
                  <Input
                    id={id}
                    value={String(editing.form[key] ?? '')}
                    placeholder={placeholder}
                    maxLength={key === 'country_code' ? 2 : 64}
                    onChange={(event) =>
                      setEditing({
                        ...editing,
                        form: {
                          ...editing.form,
                          preset: '',
                          [key]: key === 'country_code' ? event.target.value.toUpperCase() : event.target.value,
                        },
                      })
                    }
                  />
                )}
              </Field>
            ))}
            {(
              [
                ['lat', '纬度', '-90 ~ 90'],
                ['lon', '经度', '-180 ~ 180'],
              ] as const
            ).map(([key, label, hint]) => (
              <Field key={key} label={label} required hint={hint}>
                {(id) => (
                  <Input
                    id={id}
                    type="number"
                    step="0.0001"
                    value={Number.isFinite(editing.form[key]) ? editing.form[key] : ''}
                    onChange={(event) =>
                      setEditing({ ...editing, form: { ...editing.form, preset: '', [key]: Number(event.target.value) } })
                    }
                    className="tabular-nums"
                  />
                )}
              </Field>
            ))}
          </div>
        )}
      </Dialog>
    </PageTransition>
  )
}

function SelectedCard({
  loc,
  now,
  onClose,
  latency,
}: {
  loc: LocationGroup
  now: Date
  onClose: () => void
  latency: Map<number, import('@/api/types').GeoLatency>
}) {
  const tz = loc.hosts[0]?.timezone
  const t = tz ? localTime(tz, now) : null
  return (
    <div>
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="flex items-center gap-2 text-[14px] font-semibold text-text">
            <CountryFlag code={loc.code} className="h-[15px] w-5" />
            <span className="truncate">{loc.city || loc.country}</span>
          </p>
          <p className="mt-0.5 truncate text-[12px] text-muted">
            {[loc.region !== loc.city ? loc.region : '', loc.country].filter(Boolean).join(' · ')}
          </p>
        </div>
        <button type="button" onClick={onClose} aria-label="关闭" className="rounded-[6px] p-1 text-muted hover:bg-surface-2 hover:text-text">
          <X size={14} />
        </button>
      </div>
      <div className="mt-3 grid grid-cols-3 gap-2 text-center">
        {[
          { icon: <Clock size={13} />, label: '当地时间', value: t ? t.time : '—' },
          { icon: <Timer size={13} />, label: '平均延迟', value: loc.latency != null ? `${loc.latency} ms` : '—' },
          { icon: <NavigationArrow size={13} />, label: '距网关', value: fmtKm(loc.distanceKm) },
        ].map((item) => (
          <div key={item.label} className="rounded-[9px] bg-surface-2/70 px-1.5 py-2">
            <p className="inline-flex items-center gap-1 text-[10.5px] text-muted">{item.icon}{item.label}</p>
            <p className="mt-0.5 text-[13px] font-semibold text-text tabular-nums">{item.value}</p>
          </div>
        ))}
      </div>
      <ul className="mt-3 space-y-1.5">
        {loc.hosts.map((h) => (
          <li key={h.host_id} className="flex items-center gap-2 rounded-[9px] border border-border px-2.5 py-2">
            <span className="min-w-0 flex-1">
              <span className="block truncate text-[13px] font-medium text-text">{h.host}</span>
              <span className="block truncate font-mono text-[11px] text-muted" title={h.isp}>
                {h.ip}
                {h.isp && ` · ${h.isp}`}
              </span>
            </span>
            <LatencyPill item={latency.get(h.host_id)} />
          </li>
        ))}
      </ul>
      <div className="mt-3 flex gap-1.5">
        <Link to={`/terminal?host=${encodeURIComponent(loc.hosts[0].host)}`} className="flex-1">
          <Button size="sm" variant="primary" className="w-full">终端</Button>
        </Link>
        <Link to={`/metrics?host=${loc.hosts[0].host_id}`} className="flex-1">
          <Button size="sm" variant="outline" className="w-full">指标</Button>
        </Link>
        <Link to={`/files?host=${loc.hosts[0].host_id}`} className="flex-1">
          <Button size="sm" variant="outline" className="w-full">文件</Button>
        </Link>
      </div>
    </div>
  )
}
