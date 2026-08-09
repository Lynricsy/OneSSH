import { NavLink, useLocation } from 'react-router-dom'
import { CaretLineLeft, CaretLineRight } from '@phosphor-icons/react'
import { cn } from '@/lib/cn'
import { LogoTile } from '@/components/brand/logo'
import { Tooltip } from '@/components/ui/tooltip'
import { navGroups } from './nav-items'

export function Brand({ collapsed, className }: { collapsed?: boolean; className?: string }) {
  return (
    <div className={cn('flex items-center gap-2.5', collapsed ? 'justify-center' : 'px-3', className)}>
      <LogoTile className="size-8" />
      {!collapsed && <span className="text-[15px] font-semibold tracking-tight text-text">OneSSH</span>}
    </div>
  )
}

/** 导航列表：桌面侧边栏与移动端抽屉共用同一份渲染 */
export function NavList({
  collapsed,
  onNavigate,
}: {
  collapsed?: boolean
  onNavigate?: () => void
}) {
  // 折叠态要把导航项塞进 Tooltip 的 asChild 触发器，而 Radix 会把 NavLink 的
  // className 函数当字符串拼接掉，样式会整段失效——所以激活态自己算，className 保持字符串。
  const { pathname } = useLocation()

  return (
    <nav className="flex flex-col gap-6 px-3">
      {navGroups.map((group, gi) => (
        <div key={group.title ?? `group-${gi}`} className="space-y-0.5">
          {group.title && !collapsed && (
            <p className="px-2.5 pb-1.5 text-[11px] font-medium tracking-[0.08em] text-muted uppercase">
              {group.title}
            </p>
          )}
          {group.items.map(({ to, label, icon: Icon }) => {
            const isActive = to === '/' ? pathname === '/' : pathname.startsWith(to)
            const link = (
              <NavLink
                key={to}
                to={to}
                end={to === '/'}
                onClick={onNavigate}
                className={cn(
                  'relative flex h-9 items-center gap-2.5 rounded-[8px] px-2.5 text-[13px] font-medium transition-colors duration-150',
                  collapsed && 'justify-center px-0',
                  isActive ? 'bg-accent-soft text-accent' : 'text-muted hover:bg-surface-2 hover:text-text',
                )}
              >
                {/* 竖条偏移量必须等于 nav 的 px-3：正好贴在滚动容器左边缘，不会被 overflow 裁掉 */}
                {isActive && (
                  <span className="absolute top-1.5 bottom-1.5 -left-3 w-0.5 rounded-r-full bg-accent" />
                )}
                <Icon size={17} weight={isActive ? 'fill' : 'regular'} />
                {!collapsed && label}
              </NavLink>
            )
            return collapsed ? (
              <Tooltip key={to} content={label}>
                {link}
              </Tooltip>
            ) : (
              link
            )
          })}
        </div>
      ))}
    </nav>
  )
}

export function Sidebar({
  collapsed,
  onToggle,
}: {
  collapsed: boolean
  onToggle: () => void
}) {
  const toggle = (
    <button
      type="button"
      onClick={onToggle}
      aria-label={collapsed ? '展开侧边栏' : '折叠侧边栏'}
      className={cn(
        'flex h-9 w-full items-center gap-2.5 rounded-[8px] px-2.5 text-[13px] text-muted transition-colors hover:bg-surface-2 hover:text-text',
        collapsed && 'justify-center px-0',
      )}
    >
      {collapsed ? <CaretLineRight size={17} /> : <CaretLineLeft size={17} />}
      {!collapsed && '折叠'}
    </button>
  )

  return (
    <aside
      className={cn(
        'sticky top-0 hidden h-dvh shrink-0 flex-col border-r border-border bg-surface lg:flex',
        collapsed ? 'w-16' : 'w-62',
      )}
    >
      <div className="flex h-14 shrink-0 items-center">
        <Brand collapsed={collapsed} className="w-full" />
      </div>
      <div className="flex-1 overflow-y-auto pt-2 pb-4">
        <NavList collapsed={collapsed} />
      </div>
      {/* 顶边框把折叠钮与导航分开，避免它被误读成第五个分组 */}
      <div className="shrink-0 border-t border-border px-3 py-2">
        {collapsed ? <Tooltip content="展开侧边栏">{toggle}</Tooltip> : toggle}
      </div>
    </aside>
  )
}
