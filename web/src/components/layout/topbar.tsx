import { Check, Desktop, GearSix, HardDrives, List, LockKey, MagnifyingGlass, Moon, SignOut, Sun, UserCircle } from '@phosphor-icons/react'
import { useQueryClient } from '@tanstack/react-query'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { logout, useAccount } from '@/api/account'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useTheme, type ThemeMode } from '@/lib/theme'
import { titleForPath } from './nav-items'

const modes: { value: ThemeMode; label: string; icon: React.ReactNode }[] = [
  { value: 'light', label: '浅色', icon: <Sun size={15} /> },
  { value: 'dark', label: '深色', icon: <Moon size={15} /> },
  { value: 'system', label: '跟随系统', icon: <Desktop size={15} /> },
]

export function Topbar({
  hostCount,
  onOpenNav,
  onOpenSearch,
}: {
  hostCount: number
  onOpenNav: () => void
  onOpenSearch: () => void
}) {
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const account = useAccount()
  const username = account.data?.username ?? 'admin'
  const { mode, resolved, setMode } = useTheme()
  const signOut = async () => {
    await logout()
    qc.clear()
    navigate('/login', { replace: true })
  }
  const current =
    mode === 'system' ? <Desktop size={16} /> : resolved === 'dark' ? <Moon size={16} /> : <Sun size={16} />

  return (
    <header className="sticky top-0 z-30 flex h-14 shrink-0 items-center justify-between gap-3 border-b border-border bg-bg/70 px-4 backdrop-blur-xl md:px-8">
      <div className="flex min-w-0 items-center gap-1.5">
        <Button
          variant="ghost"
          size="icon"
          className="-ml-2 lg:hidden"
          onClick={onOpenNav}
          aria-label="打开导航"
        >
          <List size={18} />
        </Button>
        {/* 只是位置指示：页面里紧接着就是 h1，标题权重必须让给 h1，否则两行标题互相打架 */}
        <span className="truncate text-[13px] text-muted">{titleForPath(pathname)}</span>
      </div>
      <button
        type="button"
        onClick={onOpenSearch}
        className="group mx-auto hidden h-9 w-full max-w-[380px] items-center gap-2.5 rounded-[10px] border border-border bg-surface/70 px-3 text-[13px] text-faint shadow-card transition-all hover:border-accent/40 hover:text-muted md:flex"
      >
        <MagnifyingGlass size={15} className="text-muted transition-colors group-hover:text-accent" />
        <span className="flex-1 truncate text-left">搜索主机、页面或操作…</span>
        <kbd className="rounded-[6px] border border-border bg-surface-2 px-1.5 py-0.5 font-mono text-[10px] text-muted">Ctrl K</kbd>
      </button>
      <div className="flex shrink-0 items-center gap-1">
        <Button variant="ghost" size="icon" className="md:hidden" onClick={onOpenSearch} aria-label="搜索">
          <MagnifyingGlass size={17} />
        </Button>
        <Link
          to="/hosts"
          className="inline-flex items-center gap-1.5 rounded-[8px] px-2 py-1.5 text-[12px] text-muted transition-colors hover:bg-surface-2 hover:text-text"
        >
          <HardDrives size={14} />
          <span className="tabular-nums">{hostCount}</span>
          <span className="hidden sm:inline">台主机</span>
        </Link>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" aria-label="切换主题">
              {current}
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            {modes.map((m) => (
              <DropdownMenuItem key={m.value} onSelect={() => setMode(m.value)}>
                {m.icon}
                <span className="flex-1">{m.label}</span>
                {mode === m.value && <Check size={14} className="text-accent" />}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button type="button" aria-label="账户菜单" className="ml-1 inline-flex h-9 items-center gap-2 rounded-[10px] py-1 pr-2.5 pl-1 text-[12.5px] text-text transition-colors hover:bg-surface-2">
              <span className="grid size-7 place-items-center rounded-full bg-linear-to-br from-accent to-[var(--accent-2)] text-[12px] font-bold text-white uppercase">
                {username.slice(0, 1)}
              </span>
              <span className="hidden max-w-[110px] truncate font-medium sm:inline">{username}</span>
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-56">
            <div className="px-2.5 py-2">
              <p className="text-[13px] font-semibold text-text">{username}</p>
              <p className="text-[11.5px] text-muted">管理员 · {account.data?.sessions.length ?? 1} 个登录设备</p>
            </div>
            <DropdownMenuItem onSelect={() => navigate('/settings?tab=security')}>
              <LockKey size={15} />
              修改密码
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => navigate('/settings?tab=sessions')}>
              <UserCircle size={15} />
              登录设备与记录
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => navigate('/settings')}>
              <GearSix size={15} />
              系统设置
            </DropdownMenuItem>
            <DropdownMenuItem danger onSelect={() => void signOut()}>
              <SignOut size={15} />
              退出登录
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>
  )
}
