import type { GeoInfo, GeoLatency } from '@/api/types'
import { CountryFlag } from '@/components/country-flag'
import { cn } from '@/lib/cn'
import { isLocated, latencyTone, placeLabel } from '@/lib/use-locations'

export function LatencyPill({ item, className }: { item?: GeoLatency; className?: string }) {
  const tone = latencyTone(item)
  if (tone.level === 'none') return <span className={cn('text-[12px] text-faint', className)}>—</span>
  return (
    <span
      title={item?.error ?? (item?.method === 'tcp' ? '该端口没有返回 SSH 横幅，仅测得 TCP 握手时间' : '网关到 SSH 服务的响应时间')}
      className={cn('inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[11px] font-medium tabular-nums', className)}
      style={{ color: tone.color, background: `color-mix(in oklab, ${tone.color} 13%, transparent)` }}
    >
      <span className="size-1.5 rounded-full" style={{ background: tone.color }} />
      {tone.label}
    </span>
  )
}

/** 主机所在位置：国旗 + 城市 · 国家；未定位时说明原因 */
export function LocationCell({ info, latency, showLatency = true }: { info?: GeoInfo; latency?: GeoLatency; showLatency?: boolean }) {
  if (!info) return <span className="text-[12px] text-faint">定位中…</span>
  if (!isLocated(info)) {
    return (
      <span className="text-[12px] text-faint" title={info.error}>
        {info.source === 'private' ? '内网地址' : '未定位'}
      </span>
    )
  }
  return (
    <div className="min-w-0">
      <p className="flex min-w-0 items-center gap-1.5 text-[13px] text-text" title={[info.region, info.isp].filter(Boolean).join(' · ')}>
        <CountryFlag code={info.country_code} />
        <span className="truncate">{placeLabel(info)}</span>
      </p>
      {showLatency && latency && <LatencyPill item={latency} className="mt-1" />}
    </div>
  )
}
