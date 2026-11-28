import { ArrowRight, GlobeHemisphereEast, WifiHigh } from '@phosphor-icons/react'
import { motion } from 'motion/react'
import { Link, useNavigate } from 'react-router-dom'
import { Card } from '@/components/ui/card'
import { CountryFlag } from '@/components/country-flag'
import { LatencyPill } from '@/components/location-cell'
import { WorldMap, type MapPoint } from '@/components/world-map'
import { Skeleton } from '@/components/ui/skeleton'
import { latencyTone, placeLabel, useServerLocations } from '@/lib/use-locations'

function PanelHead({ icon, title, subtitle, action }: { icon: React.ReactNode; title: string; subtitle: string; action?: React.ReactNode }) {
  return (
    <div className="mb-4 flex items-start justify-between gap-3">
      <div className="flex min-w-0 items-center gap-2.5">
        <span className="grid size-8 shrink-0 place-items-center rounded-[9px] bg-accent-soft text-accent">{icon}</span>
        <div className="min-w-0">
          <h2 className="truncate text-[14px] font-semibold text-text">{title}</h2>
          <p className="truncate text-[12px] text-muted">{subtitle}</p>
        </div>
      </div>
      {action}
    </div>
  )
}

/** 概览页的「服务器分布 + 网络延迟」一行；单独成块以便和地图数据一起按需加载 */
export default function DashboardGeo() {
  const navigate = useNavigate()
  const data = useServerLocations()
  const points: MapPoint[] = data.locations.map((loc) => ({
    key: loc.key,
    lat: loc.lat,
    lon: loc.lon,
    color: loc.latency != null ? latencyTone({ latency_ms: loc.latency }).color : '#14b8a6',
    title: loc.hosts.length > 1 ? `${loc.city || loc.country} ×${loc.hosts.length}` : loc.hosts[0].host,
    subtitle: `${loc.city ? `${loc.city} · ` : ''}${loc.country}`,
    lines: loc.hosts.map((h) => h.host),
    count: loc.hosts.length,
  }))
  const ranked = [...data.all].sort((a, b) => {
    const la = data.latencyById.get(a.host_id)?.latency_ms ?? Number.MAX_SAFE_INTEGER
    const lb = data.latencyById.get(b.host_id)?.latency_ms ?? Number.MAX_SAFE_INTEGER
    return la - lb
  })
  const maxLatency = Math.max(100, ...data.all.map((h) => data.latencyById.get(h.host_id)?.latency_ms ?? 0))

  return (
    <motion.div
      className="mt-4 grid gap-4 xl:grid-cols-3"
      initial={{ opacity: 0, y: 6 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.25 }}
    >
      <Card className="flex flex-col p-5 xl:col-span-2">
        <PanelHead
          icon={<GlobeHemisphereEast size={16} weight="bold" />}
          title="服务器分布"
          subtitle={`${data.countries.length} 个国家/地区 · ${data.cityCount} 座城市 · ${data.located.length} 台已定位`}
          action={
            <Link to="/map" className="inline-flex items-center gap-1 text-[12px] font-medium text-accent hover:text-accent-hover">
              打开地图 <ArrowRight size={12} />
            </Link>
          }
        />
        <div className="grid flex-1 gap-4 lg:grid-cols-[minmax(0,1fr)_200px]">
          <div className="overflow-hidden rounded-[12px] border border-border">
            {data.geo.isLoading ? (
              <Skeleton className="aspect-[960/500] w-full rounded-none" />
            ) : (
              <WorldMap
                points={points}
                countries={data.countryStats}
                origin={data.origin ? { lat: data.origin.lat, lon: data.origin.lon, title: 'OneSSH 网关', subtitle: placeLabel(data.origin) } : undefined}
                interactive={false}
                onSelect={(key) => {
                  const loc = data.locations.find((l) => l.key === key)
                  if (loc) navigate(`/map?host=${loc.hosts[0].host_id}`)
                }}
              />
            )}
          </div>
          <ul className="space-y-2">
            {data.countries.slice(0, 6).map((country) => (
              <li key={country.code || country.name} className="flex items-center gap-2.5 rounded-[10px] bg-surface-2/60 px-3 py-2">
                <CountryFlag code={country.code} className="h-[15px] w-5" />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[13px] font-medium text-text">{country.name}</span>
                  <span className="block truncate text-[11px] text-muted">{country.cities.join('、') || '—'}</span>
                </span>
                <b className="text-[15px] font-semibold text-text tabular-nums">{country.hosts.length}</b>
              </li>
            ))}
            {!data.geo.isLoading && data.countries.length === 0 && (
              <li className="rounded-[10px] bg-surface-2/60 px-3 py-6 text-center text-[12px] text-muted">暂无定位数据</li>
            )}
            {data.unlocated.length > 0 && (
              <li>
                <Link to="/map" className="block rounded-[10px] border border-dashed border-border-strong px-3 py-2 text-[12px] text-muted hover:text-text">
                  {data.unlocated.length} 台未定位，去标注 →
                </Link>
              </li>
            )}
          </ul>
        </div>
      </Card>

      <Card className="flex flex-col p-5">
        <PanelHead
          icon={<WifiHigh size={16} weight="bold" />}
          title="网络延迟"
          subtitle={data.avgLatency != null ? `平均 ${data.avgLatency} ms · 每分钟刷新` : '网关到 SSH 服务的响应时间'}
        />
        <ul className="space-y-2.5">
          {ranked.slice(0, 7).map((h) => {
            const item = data.latencyById.get(h.host_id)
            const tone = latencyTone(item)
            const ms = item?.latency_ms
            return (
              <li key={h.host_id}>
                <div className="flex items-center gap-2 text-[12px]">
                  <CountryFlag code={h.country_code} />
                  <span className="min-w-0 flex-1 truncate font-medium text-text">{h.host}</span>
                  <LatencyPill item={item} />
                </div>
                <div className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-surface-2">
                  <motion.div
                    className="h-full rounded-full"
                    style={{ background: tone.color }}
                    initial={{ width: 0 }}
                    animate={{ width: ms != null ? `${Math.max(4, (ms / maxLatency) * 100)}%` : '0%' }}
                    transition={{ duration: 0.6, ease: 'easeOut' }}
                  />
                </div>
              </li>
            )
          })}
          {data.latency.isLoading && Array.from({ length: 3 }, (_, i) => <Skeleton key={i} className="h-8 rounded-[8px]" />)}
          {!data.latency.isLoading && ranked.length === 0 && (
            <li className="py-6 text-center text-[12px] text-muted">暂无主机</li>
          )}
        </ul>
      </Card>
    </motion.div>
  )
}
