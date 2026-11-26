/// <reference types="vite/client" />
import countries from 'i18n-iso-countries'
import zh from 'i18n-iso-countries/langs/zh.json'

countries.registerLocale(zh)

// 国旗 SVG 以独立资源文件发布（不内联进 JS），只有真正显示的那几面会被浏览器加载
const FLAGS = import.meta.glob<string>('/node_modules/flag-icons/flags/4x3/*.svg', {
  eager: true,
  query: '?url&no-inline',
  import: 'default',
})

export function countryName(code?: string | null, fallback?: string) {
  if (!code) return fallback || '未知地区'
  return countries.getName(code.toUpperCase(), 'zh') || fallback || code.toUpperCase()
}

/** ISO 3166-1 数字代码，例如 SG → "702"；world-atlas 的国家要素以它为 id */
export const numericOf = (code?: string | null) =>
  code ? countries.alpha2ToNumeric(code.toUpperCase()) : undefined

export function countryNameByNumeric(id?: string | number) {
  if (id == null) return undefined
  const alpha2 = countries.numericToAlpha2(String(id).padStart(3, '0'))
  return alpha2 ? countryName(alpha2) : undefined
}

export function flagUrl(code?: string | null) {
  if (!code) return undefined
  return FLAGS[`/node_modules/flag-icons/flags/4x3/${code.toLowerCase()}.svg`]
}
