import { Check, Desktop, HardDrives, List, Moon, Sun } from '@phosphor-icons/react'
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

export function Topbar({ hostCount, onOpenNav }: { hostCount: number; onOpenNav: () => void }) {
  const { pathname } = useLocation()
  const { mode, resolved, setMode } = useTheme()
  const current =
    mode === 'system' ? <Desktop size={16} /> : resolved === 'dark' ? <Moon size={16} /> : <Sun size={16} />

  return (
    <header className="sticky top-0 z-30 flex h-14 shrink-0 items-center justify-between gap-3 border-b border-border bg-bg px-4 md:px-8">
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
      <div className="flex shrink-0 items-center gap-1">
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
