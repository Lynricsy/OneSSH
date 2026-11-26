/// <reference types="vite/client" />
import countries from 'i18n-iso-countries'
import zh from 'i18n-iso-countries/langs/zh.json'

countries.registerLocale(zh)

// 国旗地址：生产构建由 vite.config 的 resolve.alias 把 virtual:flags 指向 flags.prod.ts（glob 出带哈希的资源），
// 开发模式指向 flags.dev.ts 直接拼 /node_modules 路径——eager glob 在 dev 下会让首屏发出约 270 个模块请求。
import { FLAG_PREFIX, flagAsset } from 'virtual:flags'

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
  if (!code || !/^[a-z]{2}$/i.test(code)) return undefined
  return flagAsset(`${FLAG_PREFIX}${code.toLowerCase()}.svg`)
}
