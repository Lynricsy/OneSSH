import type { Icon } from '@phosphor-icons/react'
import { useLocation } from 'react-router-dom'
import { navItemForPath } from '@/components/layout/nav-items'

/**
 * 页面标题：图标取自导航定义（与侧边栏同一个），各页面无需各自传入，
 * 保证标题区与导航永远是同一套视觉语言。
 */
export function PageHeader({
  title,
  subtitle,
  actions,
  icon,
}: {
  title: string
  subtitle?: string
  actions?: React.ReactNode
  icon?: Icon
}) {
  const { pathname } = useLocation()
  const Glyph = icon ?? navItemForPath(pathname)?.icon
  return (
    <div className="mb-6 flex flex-wrap items-center justify-between gap-3">
      <div className="flex min-w-0 items-center gap-3.5">
        {Glyph && (
          <span className="page-header-icon grid size-11 shrink-0 place-items-center rounded-[13px] text-accent">
            <Glyph size={22} weight="duotone" />
          </span>
        )}
        <div className="min-w-0 space-y-0.5">
          <h1 className="truncate text-[21px] leading-7 font-semibold tracking-tight text-text">{title}</h1>
          {subtitle && <p className="truncate text-[13px] text-muted">{subtitle}</p>}
        </div>
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  )
}
