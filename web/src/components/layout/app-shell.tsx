import { useState } from 'react'
import { Outlet, useLocation } from 'react-router-dom'
import { useHosts } from '@/api/queries'
import { LogoTile } from '@/components/brand/logo'
import { Sheet } from '@/components/ui/sheet'
import { Spinner } from '@/components/ui/spinner'
import { Brand, NavList, Sidebar } from './sidebar'
import { Topbar } from './topbar'

const SIDEBAR_KEY = 'onessh-sidebar'

export function AppShell() {
  const { pathname } = useLocation()
  const hosts = useHosts()
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(SIDEBAR_KEY) === 'collapsed')
  const [navOpen, setNavOpen] = useState(false)

  const toggle = () =>
    setCollapsed((prev) => {
      localStorage.setItem(SIDEBAR_KEY, prev ? 'expanded' : 'collapsed')
      return !prev
    })

  // 首次拉取 /hosts 兼作鉴权探针：401 会由 client 广播事件，AuthGuard 负责跳转
  if (hosts.isLoading) {
    return (
      <div className="flex h-dvh flex-col items-center justify-center gap-4 bg-bg">
        <LogoTile className="size-11" />
        <Spinner className="size-5 text-accent" />
      </div>
    )
  }

  // 终端页自己吃满高度，不套内容区的 max-w 与 padding
  const isTerminal = pathname.startsWith('/terminal')

  return (
    <div className="flex min-h-dvh bg-bg">
      {/* 视觉样式全部放在 focus: 变体里：sr-only 会把 padding 归零，
          写成基础类会和它同优先级打架，聚焦时挤成一坨 */}
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:fixed focus:top-3 focus:left-3 focus:z-50 focus:rounded-[8px] focus:bg-accent focus:px-3 focus:py-2 focus:text-sm focus:font-medium focus:text-accent-fg focus:shadow-pop"
      >
        跳到主内容
      </a>
      <Sidebar collapsed={collapsed} onToggle={toggle} />
      <div className="flex min-w-0 flex-1 flex-col">
        <Topbar hostCount={hosts.data?.length ?? 0} onOpenNav={() => setNavOpen(true)} />
        <main
          id="main"
          className={isTerminal ? 'flex-1' : 'mx-auto w-full max-w-[1400px] flex-1 px-4 py-6 md:px-8'}
        >
          <Outlet />
        </main>
      </div>
      <Sheet
        open={navOpen}
        onOpenChange={setNavOpen}
        side="left"
        title="导航"
        header={<Brand className="px-0" />}
        width="264px"
      >
        <div className="py-4">
          <NavList onNavigate={() => setNavOpen(false)} />
        </div>
      </Sheet>
    </div>
  )
}
