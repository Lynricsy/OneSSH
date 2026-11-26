import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { geoGraticule10, geoNaturalEarth1, geoPath, type GeoSphere } from 'd3-geo'
import { feature, mesh } from 'topojson-client'
import type { GeometryCollection, Topology } from 'topojson-specification'
import type { Feature, Geometry, LineString } from 'geojson'
import { ArrowsIn, Minus, Plus } from '@phosphor-icons/react'
// 1:50m 精度：香港、新加坡等小面积国家/地区在 110m 数据里根本不存在，着色会落空
import world from 'world-atlas/countries-50m.json'
import { cn } from '@/lib/cn'
import { countryNameByNumeric } from '@/lib/countries'
import type { CountryStat } from '@/lib/use-locations'

type CountryProps = { name: string }
const topo = world as unknown as Topology<{ countries: GeometryCollection<CountryProps> }>
const COUNTRY_FEATURES = (
  feature(topo, topo.objects.countries) as unknown as { features: Array<Feature<Geometry, CountryProps>> }
).features
const BORDERS = mesh(topo, topo.objects.countries, (a, b) => a !== b)
const GRATICULE = geoGraticule10()
const SPHERE: GeoSphere = { type: 'Sphere' }

const W = 960
const H = 500
const MAX_ZOOM = 8

export type MapPoint = {
  key: string
  lat: number
  lon: number
  color: string
  title: string
  subtitle?: string
  lines?: string[]
  count?: number
}

export type MapOrigin = { lat: number; lon: number; title: string; subtitle?: string }

type Tip = { x: number; y: number; title: string; subtitle?: string; lines?: string[]; color?: string }

/** 服务器越多颜色越深：1 台 55%，之后每多一台加深一档，5 台封顶 */
export const countryFill = (count: number) =>
  count > 0
    ? `color-mix(in oklab, var(--accent) ${55 + Math.min(count - 1, 4) * 10}%, var(--map-land))`
    : 'var(--map-land)'

function clampView(k: number, x: number, y: number) {
  const kk = Math.min(MAX_ZOOM, Math.max(1, k))
  return { k: kk, x: Math.min(0, Math.max(W - W * kk, x)), y: Math.min(0, Math.max(H - H * kk, y)) }
}

export function WorldMap({
  points,
  countries,
  origin,
  centerLon = 150,
  interactive = true,
  showArcs = true,
  selectedKey,
  onSelect,
  className,
  children,
}: {
  points: MapPoint[]
  countries: Map<string, CountryStat>
  origin?: MapOrigin
  /** 地图中央经线：150 为亚太居中（中国常用的世界地图视角），20 为欧非居中 */
  centerLon?: number
  interactive?: boolean
  showArcs?: boolean
  selectedKey?: string | null
  onSelect?: (key: string | null) => void
  className?: string
  /** 叠加在地图上的内容（图例、选中详情等） */
  children?: React.ReactNode
}) {
  const uid = useId().replace(/:/g, '')
  const wrapRef = useRef<HTMLDivElement>(null)
  const svgRef = useRef<SVGSVGElement>(null)
  const [view, setView] = useState({ k: 1, x: 0, y: 0 })
  const [tip, setTip] = useState<Tip | null>(null)
  const [dragging, setDragging] = useState(false)
  const drag = useRef<{ x: number; y: number; vx: number; vy: number; moved: boolean } | null>(null)

  const { path, project } = useMemo(() => {
    const projection = geoNaturalEarth1().rotate([-centerLon, 0]).fitExtent(
      [
        [6, 6],
        [W - 6, H - 6],
      ],
      SPHERE,
    )
    return { path: geoPath(projection), project: (lon: number, lat: number) => projection([lon, lat]) }
  }, [centerLon])

  useEffect(() => setView({ k: 1, x: 0, y: 0 }), [centerLon])

  const toSvg = (clientX: number, clientY: number): [number, number] => {
    const rect = svgRef.current?.getBoundingClientRect()
    if (!rect || !rect.width) return [W / 2, H / 2]
    return [((clientX - rect.left) / rect.width) * W, ((clientY - rect.top) / rect.height) * H]
  }

  const zoomAt = (factor: number, px = W / 2, py = H / 2) =>
    setView((v) => {
      const k = Math.min(MAX_ZOOM, Math.max(1, v.k * factor))
      return clampView(k, px - (px - v.x) * (k / v.k), py - (py - v.y) * (k / v.k))
    })

  // 滚轮缩放必须注册为非 passive 才能阻止页面跟着滚动
  useEffect(() => {
    const svg = svgRef.current
    if (!svg || !interactive) return
    const onWheel = (event: WheelEvent) => {
      event.preventDefault()
      const [px, py] = toSvg(event.clientX, event.clientY)
      zoomAt(Math.exp(-event.deltaY * 0.0015), px, py)
    }
    svg.addEventListener('wheel', onWheel, { passive: false })
    return () => svg.removeEventListener('wheel', onWheel)
  }, [interactive])

  const showTip = (event: React.MouseEvent, content: Omit<Tip, 'x' | 'y'>) => {
    if (drag.current?.moved) return
    const rect = wrapRef.current?.getBoundingClientRect()
    if (!rect) return
    setTip({ ...content, x: event.clientX - rect.left, y: event.clientY - rect.top })
  }

  const scale = view.k
  const markerR = 5.5 / scale
  const showLabels = points.length <= 10 || scale >= 2.2
  const ordered = [...points].sort((a, b) => Number(a.key === selectedKey) - Number(b.key === selectedKey))
  const originXY = origin ? project(origin.lon, origin.lat) : null

  return (
    <div ref={wrapRef} className={cn('relative w-full select-none', className)}>
      <svg
        ref={svgRef}
        viewBox={`0 0 ${W} ${H}`}
        role="img"
        aria-label="服务器分布地图"
        className={cn('block h-auto w-full touch-none', interactive && (dragging ? 'cursor-grabbing' : 'cursor-grab'))}
        style={{ aspectRatio: `${W} / ${H}` }}
        onPointerDown={(event) => {
          if (!interactive || event.button !== 0) return
          drag.current = { x: event.clientX, y: event.clientY, vx: view.x, vy: view.y, moved: false }
        }}
        onPointerMove={(event) => {
          const d = drag.current
          if (!d) return
          const rect = svgRef.current?.getBoundingClientRect()
          if (!rect) return
          const dx = ((event.clientX - d.x) / rect.width) * W
          const dy = ((event.clientY - d.y) / rect.height) * H
          if (!d.moved && Math.abs(dx) + Math.abs(dy) < 4) return
          if (!d.moved) {
            d.moved = true
            setDragging(true)
            setTip(null)
          }
          setView((v) => clampView(v.k, d.vx + dx, d.vy + dy))
        }}
        onPointerUp={() => {
          // moved 标记保留到 click 事件之后再清，免得拖拽结束时误触发选点
          setTimeout(() => (drag.current = null), 0)
          setDragging(false)
        }}
        onPointerLeave={() => {
          drag.current = null
          setDragging(false)
          setTip(null)
        }}
        onDoubleClick={(event) => {
          if (!interactive) return
          const [px, py] = toSvg(event.clientX, event.clientY)
          zoomAt(1.8, px, py)
        }}
        onClick={(event) => {
          if (event.target === event.currentTarget && !drag.current?.moved) onSelect?.(null)
        }}
      >
        <defs>
          <radialGradient id={`ocean-${uid}`} cx="50%" cy="42%" r="72%">
            <stop offset="0%" stopColor="var(--map-ocean-1)" />
            <stop offset="100%" stopColor="var(--map-ocean-2)" />
          </radialGradient>
          <filter id={`glow-${uid}`} x="-100%" y="-100%" width="300%" height="300%">
            <feGaussianBlur stdDeviation="2.4" />
          </filter>
        </defs>
        <g transform={`translate(${view.x},${view.y}) scale(${scale})`}>
          <path d={path(SPHERE) ?? ''} fill={`url(#ocean-${uid})`} stroke="var(--map-edge)" strokeWidth={1} vectorEffect="non-scaling-stroke" />
          <path d={path(GRATICULE) ?? ''} fill="none" stroke="var(--map-grid)" strokeWidth={0.6} vectorEffect="non-scaling-stroke" />
          {COUNTRY_FEATURES.map((country, index) => {
            const id = String(country.id ?? '')
            const stat = countries.get(id)
            return (
              <path
                key={`${id}-${index}`}
                d={path(country) ?? ''}
                className={cn('map-country', stat && 'map-country-active')}
                style={{ fill: countryFill(stat?.count ?? 0) }}
                onMouseMove={(event) =>
                  showTip(event, {
                    title: countryNameByNumeric(id) ?? country.properties?.name ?? '未知地区',
                    subtitle: stat ? `${stat.count} 台服务器` : '暂无服务器',
                    lines: stat?.hosts.slice(0, 6),
                    color: stat ? 'var(--accent)' : undefined,
                  })
                }
                onMouseLeave={() => setTip(null)}
              />
            )
          })}
          <path d={path(BORDERS) ?? ''} fill="none" stroke="var(--map-border)" strokeWidth={0.6} vectorEffect="non-scaling-stroke" pointerEvents="none" />

          {showArcs &&
            origin &&
            points.map((point) => {
              const line: LineString = {
                type: 'LineString',
                coordinates: [
                  [origin.lon, origin.lat],
                  [point.lon, point.lat],
                ],
              }
              const d = path(line)
              if (!d) return null
              const active = !selectedKey || selectedKey === point.key
              return (
                <path
                  key={`arc-${point.key}`}
                  d={d}
                  fill="none"
                  stroke={point.color}
                  strokeOpacity={active ? 0.7 : 0.18}
                  strokeWidth={active ? 1.6 : 1}
                  strokeLinecap="round"
                  vectorEffect="non-scaling-stroke"
                  className="map-arc"
                  pointerEvents="none"
                />
              )
            })}

          {originXY && origin && (
            <g
              transform={`translate(${originXY[0]},${originXY[1]})`}
              onMouseMove={(event) => showTip(event, { title: origin.title, subtitle: origin.subtitle, color: 'var(--accent-2)' })}
              onMouseLeave={() => setTip(null)}
            >
              <circle r={markerR * 2.4} fill="var(--accent-2)" className="map-pulse" />
              <rect
                x={-markerR}
                y={-markerR}
                width={markerR * 2}
                height={markerR * 2}
                rx={markerR * 0.4}
                transform="rotate(45)"
                fill="var(--accent-2)"
                stroke="var(--surface)"
                strokeWidth={1.6}
                vectorEffect="non-scaling-stroke"
              />
            </g>
          )}

          {ordered.map((point) => {
            const xy = project(point.lon, point.lat)
            if (!xy) return null
            const selected = point.key === selectedKey
            const r = markerR * (selected ? 1.35 : 1) * (point.count && point.count > 1 ? 1.2 : 1)
            return (
              <g
                key={point.key}
                transform={`translate(${xy[0]},${xy[1]})`}
                className="cursor-pointer"
                onClick={(event) => {
                  event.stopPropagation()
                  if (!drag.current?.moved) onSelect?.(selected ? null : point.key)
                }}
                onMouseMove={(event) =>
                  showTip(event, { title: point.title, subtitle: point.subtitle, lines: point.lines, color: point.color })
                }
                onMouseLeave={() => setTip(null)}
              >
                <circle r={r * 2.8} fill={point.color} className="map-pulse" />
                <circle r={r * 1.9} fill={point.color} opacity={0.35} filter={`url(#glow-${uid})`} />
                {selected && (
                  <circle r={r * 2.1} fill="none" stroke={point.color} strokeWidth={2} vectorEffect="non-scaling-stroke" />
                )}
                <circle r={r} fill={point.color} stroke="var(--surface)" strokeWidth={2} vectorEffect="non-scaling-stroke" />
                {point.count && point.count > 1 ? (
                  <text textAnchor="middle" dy="0.36em" fontSize={r * 1.25} fontWeight={700} fill="#fff" pointerEvents="none">
                    {point.count}
                  </text>
                ) : null}
                {(showLabels || selected) && (
                  <text
                    x={r + 5 / scale}
                    dy="0.35em"
                    fontSize={11.5 / scale}
                    fontWeight={600}
                    className="map-label"
                    strokeWidth={3.2 / scale}
                    pointerEvents="none"
                  >
                    {point.title}
                  </text>
                )}
              </g>
            )
          })}
        </g>
      </svg>

      {interactive && (
        <div className="absolute top-3 right-3 flex flex-col overflow-hidden rounded-[10px] border border-border bg-surface/85 shadow-card backdrop-blur">
          {[
            { label: '放大', icon: <Plus size={14} weight="bold" />, run: () => zoomAt(1.5) },
            { label: '缩小', icon: <Minus size={14} weight="bold" />, run: () => zoomAt(1 / 1.5) },
            { label: '复位', icon: <ArrowsIn size={14} weight="bold" />, run: () => setView({ k: 1, x: 0, y: 0 }) },
          ].map((button) => (
            <button
              key={button.label}
              type="button"
              aria-label={button.label}
              title={button.label}
              onClick={button.run}
              className="grid size-8 place-items-center text-muted transition-colors hover:bg-surface-2 hover:text-text [&+button]:border-t [&+button]:border-border"
            >
              {button.icon}
            </button>
          ))}
        </div>
      )}

      {children}

      {tip && (
        <div
          className="pointer-events-none absolute z-20 max-w-[240px] min-w-[140px] rounded-[10px] border border-border bg-surface/95 px-3 py-2 text-[12px] shadow-pop backdrop-blur"
          style={{
            left: Math.min(Math.max(tip.x, 90), (wrapRef.current?.clientWidth ?? 9999) - 90),
            top: tip.y,
            transform: 'translate(-50%, calc(-100% - 14px))',
          }}
        >
          <p className="flex items-center gap-1.5 font-semibold text-text">
            {tip.color && <span className="size-2 shrink-0 rounded-full" style={{ background: tip.color }} />}
            <span className="truncate">{tip.title}</span>
          </p>
          {tip.subtitle && <p className="mt-0.5 text-muted">{tip.subtitle}</p>}
          {tip.lines && tip.lines.length > 0 && (
            <ul className="mt-1.5 space-y-0.5 border-t border-border pt-1.5">
              {tip.lines.map((line) => (
                <li key={line} className="truncate font-mono text-[11px] text-text">
                  {line}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  )
}
