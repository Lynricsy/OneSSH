import { useEffect, useMemo, useRef, useState } from 'react'
import * as DialogPrimitive from '@radix-ui/react-dialog'
import {
  ArrowRight,
  ChartLine,
  FolderOpen,
  GlobeHemisphereEast,
  HardDrives,
  Key,
  MagnifyingGlass,
  Moon,
  Plus,
  Sun,
  Terminal,
  Ticket,
  UploadSimple,
  type Icon,
} from '@phosphor-icons/react'
import { AnimatePresence, motion } from 'motion/react'
import { useNavigate } from 'react-router-dom'
import { useHosts } from '@/api/queries'
import { navGroups } from '@/components/layout/nav-items'
import { cn } from '@/lib/cn'
import { useTheme } from '@/lib/theme'

type Item = {
  id: string
  group: string
  label: string
  hint?: string
  icon: Icon
  keywords: string
  run: () => void
}

/** Ctrl/⌘ + K 唤起：跳页面、对任意主机直接开终端 / 文件 / 指标，或执行常用操作 */
export function CommandPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const navigate = useNavigate()
  const { data: hosts = [] } = useHosts()
  const { resolved, setMode } = useTheme()
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  const listRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (open) {
      setQuery('')
      setActive(0)
    }
  }, [open])

  const items = useMemo<Item[]>(() => {
    const go = (to: string) => () => navigate(to)
    const pages: Item[] = navGroups.flatMap((group) =>
      group.items.map((item) => ({
        id: `nav:${item.to}`,
        group: '页面',
        label: item.label,
        hint: group.title ?? '总览',
        icon: item.icon,
        keywords: `${item.to} ${group.title ?? ''}`,
        run: go(item.to),
      })),
    )
    const actions: Item[] = [
      { id: 'act:host', group: '快速操作', label: '添加主机', icon: Plus, keywords: 'new host add server', run: go('/hosts?new=1') },
      { id: 'act:upload', group: '快速操作', label: '上传私钥文件', icon: UploadSimple, keywords: 'key upload import 密钥', run: go('/keys?upload=1') },
      { id: 'act:key', group: '快速操作', label: '生成 ed25519 密钥', icon: Key, keywords: 'key generate 密钥', run: go('/keys?new=1') },
      { id: 'act:token', group: '快速操作', label: '创建 Agent 令牌', icon: Ticket, keywords: 'token mcp agent', run: go('/tokens?new=1') },
      {
        id: 'act:theme',
        group: '快速操作',
        label: resolved === 'dark' ? '切换到浅色主题' : '切换到深色主题',
        icon: resolved === 'dark' ? Sun : Moon,
        keywords: 'theme dark light 主题',
        run: () => setMode(resolved === 'dark' ? 'light' : 'dark'),
      },
    ]
    const hostItems: Item[] = hosts
      .filter((host) => !host.archived)
      .flatMap((host) => {
        const keywords = `${host.name} ${host.addr} ${host.username} ${host.tags.join(' ')}`
        return [
          { id: `t:${host.id}`, group: '主机', label: host.name, hint: '打开终端', icon: Terminal, keywords: `${keywords} terminal ssh 终端`, run: go(`/terminal?host=${encodeURIComponent(host.name)}`) },
          { id: `f:${host.id}`, group: '主机', label: host.name, hint: '浏览文件', icon: FolderOpen, keywords: `${keywords} files sftp 文件`, run: go(`/files?host=${host.id}`) },
          { id: `m:${host.id}`, group: '主机', label: host.name, hint: '查看指标', icon: ChartLine, keywords: `${keywords} metrics 指标`, run: go(`/metrics?host=${host.id}`) },
          { id: `g:${host.id}`, group: '主机', label: host.name, hint: '在地图中定位', icon: GlobeHemisphereEast, keywords: `${keywords} map 地图 位置`, run: go(`/map?host=${host.id}`) },
        ]
      })
    return [...pages, ...actions, ...hostItems]
  }, [hosts, navigate, resolved, setMode])

  const filtered = useMemo(() => {
    const terms = query.trim().toLowerCase().split(/\s+/).filter(Boolean)
    if (!terms.length) return items.filter((item) => item.group !== '主机' || item.hint === '打开终端')
    return items
      .filter((item) => {
        const hay = `${item.label} ${item.hint ?? ''} ${item.keywords}`.toLowerCase()
        return terms.every((term) => hay.includes(term))
      })
      .slice(0, 60)
  }, [items, query])

  useEffect(() => setActive(0), [query])
  useEffect(() => {
    listRef.current?.querySelector(`[data-index="${active}"]`)?.scrollIntoView({ block: 'nearest' })
  }, [active])

  const run = (item?: Item) => {
    if (!item) return
    onOpenChange(false)
    item.run()
  }

  let lastGroup = ''
  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <AnimatePresence>
        {open && (
          <DialogPrimitive.Portal forceMount>
            <DialogPrimitive.Overlay asChild forceMount>
              <motion.div
                className="fixed inset-0 z-50 bg-black/40 backdrop-blur-[3px]"
                initial={{ opacity: 0 }}
                animate={{ opacity: 1 }}
                exit={{ opacity: 0 }}
                transition={{ duration: 0.12 }}
              />
            </DialogPrimitive.Overlay>
            <DialogPrimitive.Content asChild forceMount>
              <motion.div
                className="fixed top-[14vh] left-1/2 z-50 w-[calc(100vw-2rem)] max-w-[600px] overflow-hidden rounded-[16px] border border-border bg-surface shadow-pop focus:outline-none"
                style={{ x: '-50%' }}
                initial={{ opacity: 0, y: -8, scale: 0.98 }}
                animate={{ opacity: 1, y: 0, scale: 1 }}
                exit={{ opacity: 0, y: -8, scale: 0.98 }}
                transition={{ type: 'spring', stiffness: 420, damping: 34 }}
              >
                <DialogPrimitive.Title className="sr-only">快速跳转</DialogPrimitive.Title>
                <DialogPrimitive.Description className="sr-only">搜索页面、主机或操作</DialogPrimitive.Description>
                <div className="flex items-center gap-3 border-b border-border px-4">
                  <MagnifyingGlass size={18} className="shrink-0 text-accent" />
                  <input
                    autoFocus
                    value={query}
                    onChange={(event) => setQuery(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === 'ArrowDown') {
                        event.preventDefault()
                        setActive((a) => Math.min(a + 1, filtered.length - 1))
                      } else if (event.key === 'ArrowUp') {
                        event.preventDefault()
                        setActive((a) => Math.max(a - 1, 0))
                      } else if (event.key === 'Enter') {
                        event.preventDefault()
                        run(filtered[active])
                      }
                    }}
                    placeholder="搜索主机、页面或操作…（例如：德国 终端）"
                    className="h-14 min-w-0 flex-1 bg-transparent text-[15px] text-text placeholder:text-faint focus:outline-none"
                  />
                  <kbd className="rounded-[6px] border border-border bg-surface-2 px-1.5 py-0.5 font-mono text-[10px] text-muted">Esc</kbd>
                </div>
                <div ref={listRef} className="max-h-[420px] overflow-y-auto p-2">
                  {filtered.length === 0 && (
                    <p className="px-3 py-10 text-center text-[13px] text-muted">没有找到匹配的页面、主机或操作</p>
                  )}
                  {filtered.map((item, index) => {
                    const header = item.group !== lastGroup ? item.group : null
                    lastGroup = item.group
                    const Glyph = item.icon
                    return (
                      <div key={item.id}>
                        {header && (
                          <p className="px-3 pt-2.5 pb-1 text-[11px] font-medium tracking-[0.08em] text-faint uppercase">{header}</p>
                        )}
                        <button
                          type="button"
                          data-index={index}
                          onMouseMove={() => setActive(index)}
                          onClick={() => run(item)}
                          className={cn(
                            'flex w-full items-center gap-3 rounded-[10px] px-3 py-2 text-left text-[13px] transition-colors',
                            index === active ? 'bg-accent-soft text-text' : 'text-text hover:bg-surface-2',
                          )}
                        >
                          <span
                            className={cn(
                              'grid size-8 shrink-0 place-items-center rounded-[9px] transition-colors',
                              index === active ? 'bg-accent text-accent-fg' : 'bg-surface-2 text-muted',
                            )}
                          >
                            {item.group === '主机' ? <HardDrives size={15} weight="duotone" /> : <Glyph size={15} weight="duotone" />}
                          </span>
                          <span className="min-w-0 flex-1">
                            <span className="block truncate font-medium">{item.label}</span>
                          </span>
                          {item.hint && (
                            <span className="inline-flex shrink-0 items-center gap-1 text-[12px] text-muted">
                              {item.group === '主机' && <Glyph size={13} />}
                              {item.hint}
                            </span>
                          )}
                          {index === active && <ArrowRight size={13} className="shrink-0 text-accent" />}
                        </button>
                      </div>
                    )
                  })}
                </div>
                <div className="flex items-center gap-4 border-t border-border bg-surface-2/50 px-4 py-2 text-[11px] text-muted">
                  <span><kbd className="font-mono">↑↓</kbd> 选择</span>
                  <span><kbd className="font-mono">Enter</kbd> 打开</span>
                  <span className="ml-auto">输入主机名可直接开终端、文件或指标</span>
                </div>
              </motion.div>
            </DialogPrimitive.Content>
          </DialogPrimitive.Portal>
        )}
      </AnimatePresence>
    </DialogPrimitive.Root>
  )
}
