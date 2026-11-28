import { cn } from '@/lib/cn'
import { countryName, flagUrl } from '@/lib/countries'

/** Windows 不渲染国旗 emoji，统一用 SVG；找不到国旗时退回两位代码徽标 */
export function CountryFlag({ code, className }: { code?: string | null; className?: string }) {
  const url = flagUrl(code)
  const label = countryName(code)
  if (!url) {
    return (
      <span
        title={label}
        className={cn(
          'inline-grid h-3 w-4 shrink-0 place-items-center rounded-[2px] bg-surface-2 text-[7px] font-bold text-muted',
          className,
        )}
      >
        {code ? code.toUpperCase() : '?'}
      </span>
    )
  }
  return (
    <img
      src={url}
      alt={label}
      title={label}
      loading="lazy"
      className={cn(
        'inline-block h-3 w-4 shrink-0 rounded-[2px] object-cover shadow-[0_0_0_1px_rgba(0,0,0,0.12)]',
        className,
      )}
    />
  )
}
