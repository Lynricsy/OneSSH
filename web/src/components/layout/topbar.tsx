import { Check, Desktop, HardDrives, List, MagnifyingGlass, Moon, Sun } from '@phosphor-icons/react'
import { Link, useLocation } from 'react-router-dom'
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
  const { mode, resolved, setMode } = useTheme()
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
      </div>
    </header>
  )
}
