import { useMemo } from 'react'
import { geoDistance } from 'd3-geo'
import { useGeo, useGeoLatency } from '@/api/queries'
import type { GeoInfo, GeoLatency } from '@/api/types'
import { countryName, numericOf } from '@/lib/countries'

export type LocationGroup = {
  key: string
  lat: number
  lon: number
  city: string
  region: string
  country: string
  code: string
  hosts: GeoInfo[]
  /** 该地点各主机延迟的均值，没有测到时为 undefined */
  latency?: number
  distanceKm?: number
}

export type CountryGroup = {
  code: string
  name: string
  hosts: GeoInfo[]
  cities: string[]
  locationKeys: string[]
}

export type CountryStat = { count: number; hosts: string[] }

export const isLocated = (info?: GeoInfo) =>
  !!info && (info.source === 'auto' || info.source === 'manual') && !(info.lat === 0 && info.lon === 0)

export const distanceKm = (a: { lat: number; lon: number }, b: { lat: number; lon: number }) =>
  geoDistance([a.lon, a.lat], [b.lon, b.lat]) * 6371

export function placeLabel(info: Pick<GeoInfo, 'city' | 'region' | 'country' | 'country_code'>) {
  const country = countryName(info.country_code, info.country)
  const city = info.city || info.region
  return city && city !== country ? `${city} · ${country}` : country
}

export type LatencyTone = { color: string; label: string; level: 'great' | 'good' | 'fair' | 'poor' | 'jump' | 'down' | 'none' }

export function latencyTone(item?: Pick<GeoLatency, 'latency_ms' | 'via_jump' | 'error'> | { latency_ms?: number | null; via_jump?: boolean; error?: string }): LatencyTone {
  if (!item) return { color: '#94a3b8', label: '—', level: 'none' }
  if (item.via_jump) return { color: '#818cf8', label: '经跳板', level: 'jump' }
  if (item.error) return { color: '#94a3b8', label: '不可达', level: 'down' }
  const ms = item.latency_ms
  if (ms == null) return { color: '#94a3b8', label: '—', level: 'none' }
  if (ms <= 0) return { color: '#22c55e', label: '<1 ms', level: 'great' }
  if (ms <= 80) return { color: '#22c55e', label: `${ms} ms`, level: 'great' }
  if (ms <= 160) return { color: '#14b8a6', label: `${ms} ms`, level: 'good' }
  if (ms <= 260) return { color: '#f59e0b', label: `${ms} ms`, level: 'fair' }
  return { color: '#f43f5e', label: `${ms} ms`, level: 'poor' }
}

export function useServerLocations({ includeArchived = false }: { includeArchived?: boolean } = {}) {
  const geo = useGeo()
  const latency = useGeoLatency()

  const derived = useMemo(() => {
    const all = (geo.data?.hosts ?? []).filter((h) => includeArchived || !h.archived)
    const latencyById = new Map((latency.data ?? []).map((l) => [l.host_id, l]))
    const geoById = new Map(all.map((h) => [h.host_id, h]))
    const origin = geo.data?.origin && isLocated({ ...geo.data.origin, source: 'auto' }) ? geo.data.origin : undefined
    const located = all.filter(isLocated)
    const unlocated = all.filter((h) => !isLocated(h))

    // 同一城市的多台主机合并成一个点：坐标保留一位小数（约 10 km）
    const byKey = new Map<string, LocationGroup>()
    for (const h of located) {
      const key = `${h.lat.toFixed(1)},${h.lon.toFixed(1)}`
      const group = byKey.get(key) ?? {
        key,
        lat: h.lat,
        lon: h.lon,
        city: h.city || h.region,
        region: h.region,
        country: countryName(h.country_code, h.country),
        code: h.country_code,
        hosts: [],
      }
      group.hosts.push(h)
      byKey.set(key, group)
    }
    const locations = [...byKey.values()].map((group) => {
      const values = group.hosts
        .map((h) => latencyById.get(h.host_id)?.latency_ms)
        .filter((v): v is number => v != null)
      return {
        ...group,
        latency: values.length ? Math.round(values.reduce((a, b) => a + b, 0) / values.length) : undefined,
        distanceKm: origin ? distanceKm(origin, group) : undefined,
      }
    })

    const byCountry = new Map<string, CountryGroup>()
    for (const loc of locations) {
      const id = loc.code || loc.country
      const group = byCountry.get(id) ?? { code: loc.code, name: loc.country, hosts: [], cities: [], locationKeys: [] }
      group.hosts.push(...loc.hosts)
      if (loc.city && !group.cities.includes(loc.city)) group.cities.push(loc.city)
      group.locationKeys.push(loc.key)
      byCountry.set(id, group)
    }
    const countries = [...byCountry.values()].sort((a, b) => b.hosts.length - a.hosts.length || a.name.localeCompare(b.name))

    const countryStats = new Map<string, CountryStat>()
    for (const group of countries) {
      const numeric = numericOf(group.code)
      if (!numeric) continue
      countryStats.set(numeric, { count: group.hosts.length, hosts: group.hosts.map((h) => h.host) })
    }

    const measured = (latency.data ?? []).filter((l) => l.latency_ms != null && geoById.has(l.host_id))
    const avgLatency = measured.length
      ? Math.round(measured.reduce((a, b) => a + (b.latency_ms ?? 0), 0) / measured.length)
      : undefined
    const farthest = locations.reduce<LocationGroup | undefined>(
      (best, loc) => ((loc.distanceKm ?? -1) > (best?.distanceKm ?? -1) ? loc : best),
      undefined,
    )

    return {
      all,
      located,
      unlocated,
      locations,
      countries,
      countryStats,
      origin,
      geoById,
      latencyById,
      avgLatency,
      farthest,
      cityCount: new Set(locations.map((l) => `${l.code}:${l.city}`)).size,
      enabled: geo.data?.enabled ?? true,
    }
  }, [geo.data, latency.data, includeArchived])

  return { ...derived, geo, latency }
}
