import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  ArrowClockwise,
  ArrowsOut,
  Broadcast,
  ClipboardText,
  Columns,
  Copy,
  CornersIn,
  DownloadSimple,
  Eraser,
  FolderOpen,
  GearSix,
  GridFour,
  Lightning,
  MagnifyingGlass,
  Minus,
  PencilSimple,
  Plugs,
  Plus,
  Rows,
  SquareHalf,
  Terminal as TerminalIcon,
  Trash,
  X,
} from '@phosphor-icons/react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { toast } from 'sonner'
import { uploadWithProgress } from '@/api/fs'
import { useHosts } from '@/api/queries'
import { CountryFlag } from '@/components/country-flag'
import { LatencyPill } from '@/components/location-cell'
import { Button } from '@/components/ui/button'
import { Dialog } from '@/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { Tooltip } from '@/components/ui/tooltip'
import { cn } from '@/lib/cn'
import { formatSize } from '@/lib/file-types'
import { DEFAULT_SNIPPETS, sessionName, terminals, THEMES, useTerminals, type Snippet, type TermSession, type TermStatus } from '@/lib/terminal-manager'
import { useServerLocations } from '@/lib/use-locations'

type LayoutMode = 'single' | 'columns' | 'rows' | 'grid'
const PANE_COUNT: Record<LayoutMode, number> = { single: 1, columns: 2, rows: 2, grid: 4 }

const STATUS: Record<TermStatus, { label: string; color: string; pulse?: boolean }> = {
  connecting: { label: '连接中', color: '#f59e0b', pulse: true },
  connected: { label: '已连接', color: '#22c55e' },
  closed: { label: '已断开', color: '#94a3b8' },
  error: { label: '连接失败', color: '#f43f5e' },
}

function useClock(ms = 1000) {
  const [, setTick] = useState(0)
  useEffect(() => {
    const t = setInterval(() => setTick((x) => x + 1), ms)
    return () => clearInterval(t)
  }, [ms])
}

const duration = (from?: number, to?: number) => {
  if (!from) return '—'
  const s = Math.max(0, Math.floor(((to ?? Date.now()) - from) / 1000))
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  return h ? `${h}:${String(m).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}` : `${m}:${String(s % 60).padStart(2, '0')}`
}

function loadSnippets(): Snippet[] {
  try {
    const raw = JSON.parse(localStorage.getItem('onessh-term-snippets') ?? 'null') as Snippet[] | null
    return Array.isArray(raw) ? raw : DEFAULT_SNIPPETS
  } catch {
    return DEFAULT_SNIPPETS
  }
}

/* ── 单个窗格：把会话的 DOM 容器挂进来，离开时停回停车场 ── */

function TermPane({
  session,
  focused,
  onFocus,
  showHeader,
  onDropFiles,
}: {
  session?: TermSession
  focused: boolean
  onFocus: () => void
  showHeader: boolean
  onDropFiles: (session: TermSession, files: File[]) => void
}) {
  const mountRef = useRef<HTMLDivElement>(null)
  const [dragging, setDragging] = useState(false)
  const id = session?.id

  useEffect(() => {
    const mount = mountRef.current
    if (!id || !mount) return
    terminals.attach(id, mount)
    const ro = new ResizeObserver(() => terminals.fit(id))
    ro.observe(mount)
    return () => {
      ro.disconnect()
      terminals.detach(id, mount)
    }
  }, [id])

  useEffect(() => {
    if (focused && id) terminals.focus(id)
  }, [focused, id])

  const theme = (THEMES[terminals.settings.theme] ?? THEMES.onessh).theme
  const st = session ? STATUS[session.status] : null

  return (
    <section
      onMouseDown={onFocus}
      onDragOver={(event) => {
        if (!session || !Array.from(event.dataTransfer.types).includes('Files')) return
        event.preventDefault()
        setDragging(true)
      }}
      onDragLeave={() => setDragging(false)}
      onDrop={(event) => {
        if (!session) return
        event.preventDefault()
        setDragging(false)
        const files = Array.from(event.dataTransfer.files)
        if (files.length) onDropFiles(session, files)
      }}
      className={cn(
        'relative flex min-h-0 min-w-0 flex-col overflow-hidden rounded-[12px] border transition-shadow',
        focused && showHeader ? 'border-accent/60 shadow-[0_0_0_3px_var(--accent-soft)]' : 'border-border',
      )}
      style={{ background: theme.background }}
    >
      {showHeader && session && st && (
        <div className="flex items-center gap-2 border-b border-white/10 bg-black/20 px-2.5 py-1 text-[11.5px] text-white/80">
          <span className={cn('size-1.5 rounded-full', st.pulse && 'animate-pulse')} style={{ background: st.color }} />
          <span className="min-w-0 flex-1 truncate font-medium">
            {sessionName(session)}
            {session.title && <span className="text-white/45"> · {session.title}</span>}
          </span>
          <button type="button" title="关闭" onClick={() => terminals.close(session.id)} className="rounded p-0.5 text-white/50 hover:bg-white/10 hover:text-white">
            <X size={11} weight="bold" />
          </button>
        </div>
      )}
      <div className="relative min-h-0 flex-1 p-1.5">
        <div ref={mountRef} className="h-full w-full" />
        {!session && <div className="absolute inset-0 grid place-items-center text-[12px] text-white/40">点击上方标签放到这个窗格</div>}
        {session && (session.status === 'closed' || session.status === 'error') && (
          <div className="absolute right-3 bottom-3 flex items-center gap-2 rounded-[10px] border border-white/10 bg-black/70 px-3 py-2 text-[12px] text-white shadow-xl backdrop-blur">
            <span>{session.status === 'error' ? '连接失败' : '会话已结束'}</span>
            <Button size="sm" variant="primary" onClick={() => void terminals.connect(session.id)}>
              <ArrowClockwise size={13} />
              重新连接
            </Button>
            <Button size="sm" variant="ghost" className="text-white/70 hover:bg-white/10 hover:text-white" onClick={() => terminals.close(session.id)}>
              关闭
            </Button>
          </div>
        )}
        {dragging && (
          <div className="pointer-events-none absolute inset-2 grid place-items-center rounded-[10px] border-2 border-dashed border-[#2dd4bf]/70 bg-[#2dd4bf]/10">
            <p className="rounded-full bg-black/70 px-4 py-1.5 text-[13px] text-white">松开上传到 {session?.host}:{session?.cwd || '~'}</p>
          </div>
        )}
      </div>
    </section>
  )
}

/* ── 页面 ── */

export function TerminalPage() {
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const { data: allHosts = [] } = useHosts()
  const hosts = useMemo(() => allHosts.filter((h) => !h.archived), [allHosts])
  const geo = useServerLocations()
  const { sessions, settings, broadcast } = useTerminals()
  const [activeId, setActiveId] = useState<string | null>(() => sessions[sessions.length - 1]?.id ?? null)
  const [layout, setLayout] = useState<LayoutMode>(() => (localStorage.getItem('onessh-term-layout') as LayoutMode) || 'single')
  const [panes, setPanes] = useState<Array<string | null>>([])
  const [focusPane, setFocusPane] = useState(0)
  const [sidebar, setSidebar] = useState(() => localStorage.getItem('onessh-term-sidebar') !== '0')
  const [snippets, setSnippets] = useState<Snippet[]>(loadSnippets)
  const [editingSnippet, setEditingSnippet] = useState<Snippet | null>(null)
  const [renaming, setRenaming] = useState<TermSession | null>(null)
  const [renameValue, setRenameValue] = useState('')
  const [search, setSearch] = useState<{ open: boolean; q: string; hits: Array<{ row: number; col: number }>; i: number }>({ open: false, q: '', hits: [], i: 0 })
  const [dragTab, setDragTab] = useState<string | null>(null)
  const rootRef = useRef<HTMLDivElement>(null)
  useClock()

  useEffect(() => localStorage.setItem('onessh-term-layout', layout), [layout])
  useEffect(() => localStorage.setItem('onessh-term-sidebar', sidebar ? '1' : '0'), [sidebar])
  useEffect(() => localStorage.setItem('onessh-term-snippets', JSON.stringify(snippets)), [snippets])

  const open = useCallback((host: string, cwd?: string) => {
    const id = terminals.create(host, { cwd })
    setActiveId(id)
    return id
  }, [])

  // ?host=&cwd= 直达：文件管理器「在终端中打开」、命令面板、地图都会带着它跳过来
  useEffect(() => {
    const host = params.get('host')
    if (!host || !hosts.some((h) => h.name === host)) return
    open(host, params.get('cwd') || undefined)
    params.delete('host')
    params.delete('cwd')
    setParams(params, { replace: true })
  }, [params, setParams, hosts, open])

  // 当前会话被关闭：退到最后一个
  useEffect(() => {
    if (activeId && sessions.some((s) => s.id === activeId)) return
    setActiveId(sessions[sessions.length - 1]?.id ?? null)
  }, [sessions, activeId])

  // 维护分屏窗格里的会话：保留仍存在的，空位按标签顺序补齐，焦点窗格始终显示当前会话
  useEffect(() => {
    const count = PANE_COUNT[layout]
    setPanes((prev) => {
      const alive = new Set(sessions.map((s) => s.id))
      const next: Array<string | null> = Array.from({ length: count }, (_, i) => (prev[i] && alive.has(prev[i]!) ? prev[i] : null))
      const fi = Math.min(focusPane, count - 1)
      if (activeId && !next.includes(activeId)) next[fi] = activeId
      for (let i = 0; i < count; i++) {
        if (next[i]) continue
        const free = sessions.find((s) => !next.includes(s.id))
        if (free) next[i] = free.id
      }
      return next.length === prev.length && next.every((v, i) => v === prev[i]) ? prev : next
    })
  }, [layout, sessions, activeId, focusPane])

  const active = sessions.find((s) => s.id === activeId)
  const activate = (id: string) => {
    setActiveId(id)
    const idx = panes.indexOf(id)
    if (idx >= 0) setFocusPane(idx)
    else if (layout !== 'single') setPanes((p) => p.map((v, i) => (i === focusPane ? id : v)))
  }

  const closeSession = (s: TermSession) => {
    if (settings.confirmClose && s.status === 'connected' && !window.confirm(`关闭与 ${sessionName(s)} 的会话？正在运行的前台程序会被终止。`)) return
    terminals.close(s.id)
  }

  // 页面级快捷键：Alt+数字切换、Ctrl+Shift+F 搜索（终端内其余按键全部交给 shell）
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.altKey && /^Digit[1-9]$/.test(event.code)) {
        const s = sessions[Number(event.code.slice(5)) - 1]
        if (s) {
          event.preventDefault()
          activate(s.id)
        }
      } else if ((event.ctrlKey || event.metaKey) && event.shiftKey && event.code === 'KeyF') {
        event.preventDefault()
        setSearch((st) => ({ ...st, open: true }))
      }
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  })

  const runSearch = (q: string, step = 0) => {
    if (!active) return
    const hits = q === search.q && search.hits.length ? search.hits : terminals.search(active.id, q)
    if (!hits.length) {
      setSearch({ open: true, q, hits: [], i: 0 })
      return
    }
    const i = q === search.q ? (search.i + step + hits.length) % hits.length : hits.length - 1
    setSearch({ open: true, q, hits, i })
    terminals.reveal(active.id, hits[i], q.length)
  }

  const exportLog = () => {
    if (!active) return
    const text = terminals.exportText(active.id)
    const url = URL.createObjectURL(new Blob([text], { type: 'text/plain;charset=utf-8' }))
    const a = document.createElement('a')
    a.href = url
    a.download = `${active.host}-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-')}.log`
    a.click()
    URL.revokeObjectURL(url)
  }

  const uploadToSession = (s: TermSession, files: File[]) => {
    const host = hosts.find((h) => h.name === s.host)
    if (!host) return
    const dir = s.cwd || '~'
    for (const file of files) {
      const dest = `${dir.replace(/\/+$/, '')}/${file.name}`
      const id = toast.loading(`上传 ${file.name} → ${s.host}:${dest}`)
      uploadWithProgress(host.id, dest, file, (loaded, total) => toast.loading(`上传 ${file.name}：${Math.round((loaded / Math.max(1, total)) * 100)}%`, { id })).promise
        .then(() => toast.success(`已上传到 ${dest}`, { id }))
        .catch((e: Error) => toast.error(`上传失败：${e.message}`, { id }))
    }
  }

  const toggleFullscreen = () => {
    if (document.fullscreenElement) void document.exitFullscreen()
    else void rootRef.current?.requestFullscreen()
  }

  const hostPicker = (trigger: React.ReactNode) => (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>{trigger}</DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-[60vh] overflow-y-auto">
        <DropdownMenuLabel>新建会话</DropdownMenuLabel>
        {hosts.map((h) => (
          <DropdownMenuItem key={h.id} onSelect={() => open(h.name)}>
            <CountryFlag code={geo.geoById.get(h.id)?.country_code} />
            <span className="flex-1">{h.name}</span>
            <span className="font-mono text-[11px] text-faint">{h.username}</span>
          </DropdownMenuItem>
        ))}
        {active && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={() => open(active.host, active.cwd)}>
              <Copy size={14} />
              复制当前会话（{active.host}）
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )

  const count = PANE_COUNT[layout]
  const visiblePanes = sessions.length ? panes.slice(0, count) : []

  return (
    <div ref={rootRef} className="flex h-[calc(100dvh-3.5rem)] flex-col gap-2 overflow-hidden bg-bg px-3 py-3 md:px-5">
      {/* 顶栏：标签 + 工具 */}
      <div className="flex shrink-0 items-center gap-2">
        <div className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto rounded-[12px] border border-border bg-surface p-1 shadow-card">
          <span className="mx-1.5 grid size-7 shrink-0 place-items-center rounded-[8px] bg-accent-soft text-accent">
            <TerminalIcon size={15} weight="bold" />
          </span>
          {sessions.length === 0 && <span className="px-2 text-[12.5px] text-muted">还没有会话</span>}
          {sessions.map((s, index) => {
            const st = STATUS[s.status]
            const isActive = s.id === activeId
            const loc = geo.geoById.get(hosts.find((h) => h.name === s.host)?.id ?? -1)
            return (
              <div
                key={s.id}
                draggable
                onDragStart={(event) => {
                  event.dataTransfer.setData('text/x-term-tab', s.id)
                  setDragTab(s.id)
                }}
                onDragEnd={() => setDragTab(null)}
                onDragOver={(event) => dragTab && event.preventDefault()}
                onDrop={(event) => {
                  const id = event.dataTransfer.getData('text/x-term-tab')
                  if (id) terminals.move(id, index)
                }}
                onClick={() => activate(s.id)}
                onDoubleClick={() => {
                  setRenaming(s)
                  setRenameValue(s.label ?? '')
                }}
                onAuxClick={(event) => event.button === 1 && closeSession(s)}
                title={`${s.host}${s.cwd ? `:${s.cwd}` : ''} · 双击重命名 · Alt+${index + 1}`}
                className={cn(
                  'group flex max-w-[220px] min-w-[132px] shrink-0 cursor-pointer items-center gap-1.5 rounded-[9px] px-2.5 py-1.5 text-[12.5px] transition-colors',
                  isActive ? 'bg-accent-soft text-text shadow-[inset_0_0_0_1px_var(--accent-soft)]' : 'text-muted hover:bg-surface-2 hover:text-text',
                  dragTab === s.id && 'opacity-50',
                )}
              >
                <span className={cn('size-1.5 shrink-0 rounded-full', st.pulse && 'animate-pulse')} style={{ background: st.color }} />
                <CountryFlag code={loc?.country_code} />
                <span className="min-w-0 flex-1 truncate font-medium">{sessionName(s)}</span>
                {(s.unread || s.bell) && !isActive && <span className={cn('size-1.5 shrink-0 rounded-full', s.bell ? 'bg-warning' : 'bg-accent')} title={s.bell ? '响铃' : '有新输出'} />}
                <button
                  type="button"
                  aria-label="关闭会话"
                  onClick={(event) => {
                    event.stopPropagation()
                    closeSession(s)
                  }}
                  className="rounded-[4px] p-0.5 text-faint opacity-0 group-hover:opacity-100 hover:bg-surface hover:text-text"
                >
                  <X size={11} weight="bold" />
                </button>
              </div>
            )
          })}
          {hostPicker(
            <button type="button" className="ml-0.5 inline-flex h-8 shrink-0 items-center gap-1 rounded-[9px] px-2 text-[12.5px] font-medium text-accent hover:bg-accent-soft">
              <Plus size={14} weight="bold" />
              新会话
            </button>,
          )}
        </div>

        <div className="flex shrink-0 items-center gap-1 rounded-[12px] border border-border bg-surface p-1 shadow-card">
          {(
            [
              ['single', '单窗格', <SquareHalf size={15} />],
              ['columns', '左右分屏', <Columns size={15} />],
              ['rows', '上下分屏', <Rows size={15} />],
              ['grid', '四宫格', <GridFour size={15} />],
            ] as const
          ).map(([mode, label, icon]) => (
            <Tooltip key={mode} content={label}>
              <button type="button" aria-label={label} onClick={() => setLayout(mode)} className={cn('grid size-8 place-items-center rounded-[8px] transition-colors', layout === mode ? 'bg-accent-soft text-accent' : 'text-muted hover:bg-surface-2 hover:text-text')}>
                {icon}
              </button>
            </Tooltip>
          ))}
          <span className="mx-0.5 h-5 w-px bg-border" />
          <Tooltip content={broadcast ? '关闭同步输入' : '同步输入：键盘输入同时发送到所有已连接的会话'}>
            <button type="button" onClick={() => terminals.setBroadcast(!broadcast)} className={cn('grid size-8 place-items-center rounded-[8px] transition-colors', broadcast ? 'bg-warning/15 text-warning' : 'text-muted hover:bg-surface-2 hover:text-text')}>
              <Broadcast size={15} weight={broadcast ? 'fill' : 'regular'} />
            </button>
          </Tooltip>
          <Tooltip content="搜索输出 (Ctrl+Shift+F)">
            <button type="button" disabled={!active} onClick={() => setSearch((s) => ({ ...s, open: !s.open }))} className="grid size-8 place-items-center rounded-[8px] text-muted hover:bg-surface-2 hover:text-text disabled:opacity-40">
              <MagnifyingGlass size={15} />
            </button>
          </Tooltip>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button type="button" aria-label="终端设置" className="grid size-8 place-items-center rounded-[8px] text-muted hover:bg-surface-2 hover:text-text">
                <GearSix size={15} />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-64">
              <DropdownMenuLabel>字号</DropdownMenuLabel>
              <div className="flex items-center gap-2 px-2 pb-2">
                <Button size="icon" variant="outline" className="size-7" onClick={() => terminals.updateSettings({ fontSize: Math.max(10, settings.fontSize - 1) })}>
                  <Minus size={12} />
                </Button>
                <span className="flex-1 text-center text-[13px] font-medium tabular-nums">{settings.fontSize}px</span>
                <Button size="icon" variant="outline" className="size-7" onClick={() => terminals.updateSettings({ fontSize: Math.min(26, settings.fontSize + 1) })}>
                  <Plus size={12} />
                </Button>
              </div>
              <DropdownMenuSeparator />
              <DropdownMenuLabel>配色</DropdownMenuLabel>
              <div className="grid grid-cols-2 gap-1 px-1.5 pb-2">
                {Object.entries(THEMES).map(([key, t]) => (
                  <button
                    key={key}
                    type="button"
                    onClick={() => terminals.updateSettings({ theme: key })}
                    className={cn('flex items-center gap-1.5 rounded-[7px] border px-2 py-1 text-left text-[11.5px]', settings.theme === key ? 'border-accent text-text' : 'border-border text-muted hover:text-text')}
                  >
                    <span className="flex h-3.5 w-6 shrink-0 overflow-hidden rounded-[3px] border border-black/10" style={{ background: t.theme.background }}>
                      <span className="m-auto size-1.5 rounded-full" style={{ background: t.theme.green }} />
                      <span className="m-auto size-1.5 rounded-full" style={{ background: t.theme.blue }} />
                    </span>
                    <span className="truncate">{t.label}</span>
                  </button>
                ))}
              </div>
              <DropdownMenuSeparator />
              <DropdownMenuLabel>光标</DropdownMenuLabel>
              <div className="flex gap-1 px-1.5 pb-2">
                {(['block', 'bar', 'underline'] as const).map((c) => (
                  <button key={c} type="button" onClick={() => terminals.updateSettings({ cursorStyle: c })} className={cn('flex-1 rounded-[7px] border py-1 text-[11.5px]', settings.cursorStyle === c ? 'border-accent text-text' : 'border-border text-muted')}>
                    {c === 'block' ? '方块' : c === 'bar' ? '竖线' : '下划线'}
                  </button>
                ))}
              </div>
              <DropdownMenuSeparator />
              {(
                [
                  ['copyOnSelect', '选中即复制'],
                  ['rightClickPaste', '右键粘贴 / 复制'],
                  ['cursorBlink', '光标闪烁'],
                  ['confirmClose', '关闭已连接会话前确认'],
                ] as const
              ).map(([key, label]) => (
                <label key={key} className="flex cursor-pointer items-center justify-between gap-2 px-2 py-1.5 text-[12.5px] text-text">
                  {label}
                  <Switch checked={settings[key]} onCheckedChange={(v) => terminals.updateSettings({ [key]: v })} />
                </label>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
          <Tooltip content={sidebar ? '收起快捷命令' : '展开快捷命令'}>
            <button type="button" onClick={() => setSidebar((v) => !v)} className={cn('grid size-8 place-items-center rounded-[8px] transition-colors', sidebar ? 'bg-accent-soft text-accent' : 'text-muted hover:bg-surface-2 hover:text-text')}>
              <Lightning size={15} weight={sidebar ? 'fill' : 'regular'} />
            </button>
          </Tooltip>
          <Tooltip content="全屏">
            <button type="button" onClick={toggleFullscreen} className="grid size-8 place-items-center rounded-[8px] text-muted hover:bg-surface-2 hover:text-text">
              {document.fullscreenElement ? <CornersIn size={15} /> : <ArrowsOut size={15} />}
            </button>
          </Tooltip>
        </div>
      </div>

      {broadcast && (
        <div className="flex shrink-0 items-center gap-2 rounded-[10px] border border-warning/40 bg-warning/10 px-3 py-1.5 text-[12.5px] text-text">
          <Broadcast size={15} weight="fill" className="text-warning" />
          同步输入已开启：你在任意窗格里敲的每个键都会同时发送到 <b>{sessions.filter((s) => s.status === 'connected').length}</b> 个已连接的会话。
          <button type="button" onClick={() => terminals.setBroadcast(false)} className="ml-auto font-medium text-warning hover:underline">
            关闭
          </button>
        </div>
      )}

      <div className="flex min-h-0 flex-1 gap-2">
        <div className="relative flex min-w-0 flex-1 flex-col gap-2">
          {sessions.length === 0 ? (
            <div className="flex min-h-0 flex-1 flex-col items-center justify-center overflow-auto rounded-[14px] border border-border bg-surface p-8 shadow-card">
              <span className="grid size-14 place-items-center rounded-[16px] bg-accent-soft text-accent">
                <TerminalIcon size={28} weight="duotone" />
              </span>
              <h2 className="mt-4 text-[18px] font-semibold text-text">选择一台服务器开始</h2>
              <p className="mt-1 text-[13px] text-muted">会话在后台保持连接：切换页面或标签都不会断开，支持分屏与同步输入。</p>
              <div className="mt-6 grid w-full max-w-3xl gap-3 sm:grid-cols-2 lg:grid-cols-3">
                {hosts.map((h) => {
                  const loc = geo.geoById.get(h.id)
                  return (
                    <button key={h.id} type="button" onClick={() => open(h.name)} className="group flex items-center gap-3 rounded-[12px] border border-border bg-surface p-3.5 text-left shadow-card transition-all hover:-translate-y-0.5 hover:border-accent/50 hover:shadow-pop">
                      <span className="grid size-10 shrink-0 place-items-center rounded-[10px] bg-surface-2 text-muted group-hover:bg-accent group-hover:text-accent-fg">
                        <Plugs size={18} weight="duotone" />
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="flex items-center gap-1.5 text-[13.5px] font-semibold text-text">
                          <CountryFlag code={loc?.country_code} />
                          <span className="truncate">{h.name}</span>
                        </span>
                        <span className="block truncate font-mono text-[11.5px] text-muted">
                          {h.username}@{loc?.city || h.addr}
                        </span>
                      </span>
                      <LatencyPill item={geo.latencyById.get(h.id)} />
                    </button>
                  )
                })}
              </div>
            </div>
          ) : (
            <div
              className={cn(
                'grid min-h-0 flex-1 gap-2',
                layout === 'columns' && 'grid-cols-2',
                layout === 'rows' && 'grid-rows-2',
                layout === 'grid' && 'grid-cols-2 grid-rows-2',
              )}
            >
              {visiblePanes.map((id, i) => (
                <TermPane
                  key={`${layout}-${i}`}
                  session={sessions.find((s) => s.id === id)}
                  focused={id === activeId}
                  showHeader={layout !== 'single'}
                  onFocus={() => {
                    setFocusPane(i)
                    if (id) setActiveId(id)
                  }}
                  onDropFiles={uploadToSession}
                />
              ))}
            </div>
          )}

          {search.open && active && (
            <div className="absolute top-2 right-3 z-10 flex items-center gap-1 rounded-[10px] border border-border bg-surface/95 p-1 shadow-pop backdrop-blur">
              <MagnifyingGlass size={14} className="ml-1.5 text-muted" />
              <input
                autoFocus
                value={search.q}
                onChange={(event) => setSearch({ open: true, q: event.target.value, hits: [], i: 0 })}
                onKeyDown={(event) => {
                  if (event.key === 'Enter') runSearch(search.q, event.shiftKey ? 1 : -1)
                  if (event.key === 'Escape') {
                    setSearch((s) => ({ ...s, open: false }))
                    terminals.focus(active.id)
                  }
                }}
                placeholder="在输出中搜索…"
                className="h-7 w-52 bg-transparent px-1 text-[12.5px] text-text placeholder:text-faint focus:outline-none"
              />
              <span className="min-w-12 text-center text-[11px] text-muted tabular-nums">{search.hits.length ? `${search.i + 1}/${search.hits.length}` : search.q ? '0' : ''}</span>
              <Button size="sm" variant="ghost" className="h-7 px-2" onClick={() => runSearch(search.q, -1)}>
                上一个
              </Button>
              <Button size="sm" variant="ghost" className="h-7 px-2" onClick={() => runSearch(search.q, 1)}>
                下一个
              </Button>
              <button type="button" onClick={() => setSearch((s) => ({ ...s, open: false }))} className="grid size-7 place-items-center rounded-[7px] text-muted hover:text-text">
                <X size={13} />
              </button>
            </div>
          )}

          {/* 当前会话状态栏 */}
          {active && (
            <div className="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-1 rounded-[10px] border border-border bg-surface px-3 py-1.5 text-[11.5px] text-muted shadow-card">
              <span className="inline-flex items-center gap-1.5 font-medium text-text">
                <span className={cn('size-1.5 rounded-full', STATUS[active.status].pulse && 'animate-pulse')} style={{ background: STATUS[active.status].color }} />
                {sessionName(active)} · {STATUS[active.status].label}
              </span>
              <span className="tabular-nums">时长 {duration(active.connectedAt, active.status === 'connected' ? undefined : active.closedAt)}</span>
              <span className="tabular-nums">
                {active.cols}×{active.rows}
              </span>
              <span className="tabular-nums">↓ {formatSize(active.bytesIn)} · ↑ {formatSize(active.bytesOut)}</span>
              {active.title && <span className="truncate">标题：{active.title}</span>}
              <span className="ml-auto flex items-center gap-0.5">
                {[
                  { label: '复制选中 (Ctrl+Shift+C)', icon: <Copy size={13} />, run: () => terminals.copySelection(active.id) },
                  { label: '粘贴 (Ctrl+Shift+V)', icon: <ClipboardText size={13} />, run: () => void terminals.pasteFromClipboard(active.id) },
                  { label: '清屏', icon: <Eraser size={13} />, run: () => terminals.clear(active.id) },
                  { label: '发送 Ctrl+C', icon: <span className="font-mono text-[10px] font-bold">^C</span>, run: () => terminals.input(active.id, '\x03') },
                  { label: '导出输出为 .log', icon: <DownloadSimple size={13} />, run: exportLog },
                  { label: '在文件管理中打开', icon: <FolderOpen size={13} />, run: () => navigate(`/files?host=${hosts.find((h) => h.name === active.host)?.id ?? ''}${active.cwd ? `&path=${encodeURIComponent(active.cwd)}` : ''}`) },
                  { label: '重命名', icon: <PencilSimple size={13} />, run: () => (setRenaming(active), setRenameValue(active.label ?? '')) },
                  active.status === 'connected'
                    ? { label: '断开', icon: <Plugs size={13} />, run: () => terminals.disconnect(active.id) }
                    : { label: '重新连接', icon: <ArrowClockwise size={13} />, run: () => void terminals.connect(active.id) },
                ].map((b) => (
                  <Tooltip key={b.label} content={b.label}>
                    <button type="button" aria-label={b.label} onClick={b.run} className="grid size-7 place-items-center rounded-[7px] hover:bg-surface-2 hover:text-text">
                      {b.icon}
                    </button>
                  </Tooltip>
                ))}
              </span>
            </div>
          )}
        </div>

        {/* 快捷命令侧栏 */}
        {sidebar && (
          <aside className="hidden w-[250px] shrink-0 flex-col overflow-hidden rounded-[14px] border border-border bg-surface shadow-card lg:flex">
            <div className="flex items-center justify-between border-b border-border px-3 py-2.5">
              <div>
                <p className="text-[13px] font-semibold text-text">快捷命令</p>
                <p className="text-[11px] text-muted">{broadcast ? '将发送到所有会话' : active ? `发送到 ${sessionName(active)}` : '先打开一个会话'}</p>
              </div>
              <Button size="icon" variant="ghost" className="size-7" aria-label="添加" onClick={() => setEditingSnippet({ id: `s${Date.now()}`, name: '', command: '', enter: true })}>
                <Plus size={14} />
              </Button>
            </div>
            <div className="min-h-0 flex-1 space-y-1 overflow-y-auto p-2">
              {snippets.map((sn) => (
                <div key={sn.id} className="group relative">
                  <button
                    type="button"
                    disabled={!active || active.status !== 'connected'}
                    onClick={() => active && terminals.run(active.id, sn.command, sn.enter)}
                    className="w-full rounded-[9px] border border-transparent px-2.5 py-2 text-left transition-colors hover:border-border hover:bg-surface-2 disabled:opacity-45"
                    title={sn.command}
                  >
                    <span className="flex items-center gap-1.5 text-[12.5px] font-medium text-text">
                      <Lightning size={12} weight="fill" className="text-accent" />
                      {sn.name}
                    </span>
                    <span className="mt-0.5 block truncate font-mono text-[10.5px] text-muted">{sn.command}</span>
                  </button>
                  <div className="absolute top-1.5 right-1.5 hidden gap-0.5 group-hover:flex">
                    <button type="button" aria-label="编辑" onClick={() => setEditingSnippet(sn)} className="grid size-6 place-items-center rounded-[6px] bg-surface text-muted shadow-card hover:text-text">
                      <PencilSimple size={11} />
                    </button>
                    <button type="button" aria-label="删除" onClick={() => setSnippets((list) => list.filter((x) => x.id !== sn.id))} className="grid size-6 place-items-center rounded-[6px] bg-surface text-muted shadow-card hover:text-danger">
                      <Trash size={11} />
                    </button>
                  </div>
                </div>
              ))}
            </div>
            <div className="border-t border-border p-2 text-[10.5px] leading-5 text-faint">
              <p>Ctrl+Shift+C / V 复制粘贴 · 右键粘贴</p>
              <p>Alt+1…9 切换会话 · 拖文件到终端上传</p>
              <button type="button" onClick={() => setSnippets(DEFAULT_SNIPPETS)} className="mt-1 text-muted hover:text-text">
                恢复默认命令
              </button>
            </div>
          </aside>
        )}
      </div>

      <Dialog
        open={editingSnippet != null}
        onOpenChange={(o) => !o && setEditingSnippet(null)}
        size="md"
        title={editingSnippet && snippets.some((s) => s.id === editingSnippet.id) ? '编辑快捷命令' : '添加快捷命令'}
        footer={
          <>
            <Button variant="ghost" onClick={() => setEditingSnippet(null)}>
              取消
            </Button>
            <Button
              variant="primary"
              disabled={!editingSnippet?.name.trim() || !editingSnippet?.command.trim()}
              onClick={() => {
                if (!editingSnippet) return
                setSnippets((list) => (list.some((s) => s.id === editingSnippet.id) ? list.map((s) => (s.id === editingSnippet.id ? editingSnippet : s)) : [...list, editingSnippet]))
                setEditingSnippet(null)
              }}
            >
              保存
            </Button>
          </>
        }
      >
        {editingSnippet && (
          <div className="space-y-3">
            <Field label="名称" required>
              {(id) => <Input id={id} autoFocus value={editingSnippet.name} onChange={(e) => setEditingSnippet({ ...editingSnippet, name: e.target.value })} placeholder="例如 重启 nginx" />}
            </Field>
            <Field label="命令" required hint="可以写多行；会原样发送到终端">
              {(id) => <Textarea id={id} rows={4} className="font-mono text-[12.5px]" value={editingSnippet.command} onChange={(e) => setEditingSnippet({ ...editingSnippet, command: e.target.value })} placeholder="systemctl restart nginx && systemctl status nginx --no-pager" />}
            </Field>
            <label className="flex items-center gap-2 text-[13px] text-text">
              <Switch checked={editingSnippet.enter} onCheckedChange={(v) => setEditingSnippet({ ...editingSnippet, enter: v })} />
              发送后自动回车执行
            </label>
          </div>
        )}
      </Dialog>

      <Dialog
        open={renaming != null}
        onOpenChange={(o) => !o && setRenaming(null)}
        size="sm"
        title="重命名会话"
        footer={
          <>
            <Button variant="ghost" onClick={() => setRenaming(null)}>
              取消
            </Button>
            <Button
              variant="primary"
              onClick={() => {
                if (renaming) terminals.rename(renaming.id, renameValue)
                setRenaming(null)
              }}
            >
              保存
            </Button>
          </>
        }
      >
        <Field label="显示名称" hint="留空则显示主机名">
          {(id) => (
            <Input
              id={id}
              autoFocus
              value={renameValue}
              placeholder={renaming?.host}
              onChange={(e) => setRenameValue(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && renaming) {
                  terminals.rename(renaming.id, renameValue)
                  setRenaming(null)
                }
              }}
            />
          )}
        </Field>
      </Dialog>
    </div>
  )
}
