import { useSyncExternalStore } from 'react'
import { FitAddon, Terminal as GhosttyTerminal, init as initGhostty, type ITheme } from 'ghostty-web'
import { toast } from 'sonner'

/**
 * 终端会话管理器（模块级单例）。
 *
 * 会话活在 React 之外：切换页面、切换标签、改分屏布局都不会断开 SSH。每个会话有自己的 DOM 容器，
 * 不显示时停放在屏幕外的「停车场」里，显示时整块移进窗格，canvas 与回滚缓冲原样保留。
 */

export type TermStatus = 'connecting' | 'connected' | 'closed' | 'error'

export type TermSession = {
  id: string
  host: string
  cwd?: string
  label?: string
  title: string
  status: TermStatus
  createdAt: number
  connectedAt?: number
  closedAt?: number
  cols: number
  rows: number
  bytesIn: number
  bytesOut: number
  unread: boolean
  bell: boolean
  element: HTMLDivElement
  term?: GhosttyTerminal
  fit?: FitAddon
  ws?: WebSocket
  attempt: number
}

export type TermSettings = {
  fontSize: number
  theme: string
  cursorStyle: 'block' | 'bar' | 'underline'
  cursorBlink: boolean
  copyOnSelect: boolean
  rightClickPaste: boolean
  scrollback: number
  confirmClose: boolean
}

export const THEMES: Record<string, { label: string; theme: ITheme }> = {
  onessh: {
    label: 'OneSSH 深海',
    theme: {
      background: '#0b0d10', foreground: '#d6e4ff', cursor: '#2dd4bf', selectionBackground: '#2dd4bf40',
      black: '#1c2128', red: '#f87171', green: '#4ade80', yellow: '#fbbf24', blue: '#60a5fa', magenta: '#c084fc', cyan: '#2dd4bf', white: '#d6e4ff',
      brightBlack: '#5c6470', brightRed: '#fca5a5', brightGreen: '#86efac', brightYellow: '#fde68a', brightBlue: '#93c5fd', brightMagenta: '#d8b4fe', brightCyan: '#5eead4', brightWhite: '#ffffff',
    },
  },
  dracula: {
    label: 'Dracula',
    theme: {
      background: '#282a36', foreground: '#f8f8f2', cursor: '#f8f8f2', selectionBackground: '#44475a',
      black: '#21222c', red: '#ff5555', green: '#50fa7b', yellow: '#f1fa8c', blue: '#bd93f9', magenta: '#ff79c6', cyan: '#8be9fd', white: '#f8f8f2',
      brightBlack: '#6272a4', brightRed: '#ff6e6e', brightGreen: '#69ff94', brightYellow: '#ffffa5', brightBlue: '#d6acff', brightMagenta: '#ff92df', brightCyan: '#a4ffff', brightWhite: '#ffffff',
    },
  },
  nord: {
    label: 'Nord',
    theme: {
      background: '#2e3440', foreground: '#d8dee9', cursor: '#d8dee9', selectionBackground: '#434c5e',
      black: '#3b4252', red: '#bf616a', green: '#a3be8c', yellow: '#ebcb8b', blue: '#81a1c1', magenta: '#b48ead', cyan: '#88c0d0', white: '#e5e9f0',
      brightBlack: '#4c566a', brightRed: '#bf616a', brightGreen: '#a3be8c', brightYellow: '#ebcb8b', brightBlue: '#81a1c1', brightMagenta: '#b48ead', brightCyan: '#8fbcbb', brightWhite: '#eceff4',
    },
  },
  monokai: {
    label: 'Monokai',
    theme: {
      background: '#272822', foreground: '#f8f8f2', cursor: '#f8f8f0', selectionBackground: '#49483e',
      black: '#272822', red: '#f92672', green: '#a6e22e', yellow: '#f4bf75', blue: '#66d9ef', magenta: '#ae81ff', cyan: '#a1efe4', white: '#f8f8f2',
      brightBlack: '#75715e', brightRed: '#f92672', brightGreen: '#a6e22e', brightYellow: '#f4bf75', brightBlue: '#66d9ef', brightMagenta: '#ae81ff', brightCyan: '#a1efe4', brightWhite: '#f9f8f5',
    },
  },
  solarized: {
    label: 'Solarized Dark',
    theme: {
      background: '#002b36', foreground: '#93a1a1', cursor: '#93a1a1', selectionBackground: '#073642',
      black: '#073642', red: '#dc322f', green: '#859900', yellow: '#b58900', blue: '#268bd2', magenta: '#d33682', cyan: '#2aa198', white: '#eee8d5',
      brightBlack: '#586e75', brightRed: '#cb4b16', brightGreen: '#586e75', brightYellow: '#657b83', brightBlue: '#839496', brightMagenta: '#6c71c4', brightCyan: '#93a1a1', brightWhite: '#fdf6e3',
    },
  },
  gruvbox: {
    label: 'Gruvbox',
    theme: {
      background: '#282828', foreground: '#ebdbb2', cursor: '#ebdbb2', selectionBackground: '#504945',
      black: '#282828', red: '#cc241d', green: '#98971a', yellow: '#d79921', blue: '#458588', magenta: '#b16286', cyan: '#689d6a', white: '#a89984',
      brightBlack: '#928374', brightRed: '#fb4934', brightGreen: '#b8bb26', brightYellow: '#fabd2f', brightBlue: '#83a598', brightMagenta: '#d3869b', brightCyan: '#8ec07c', brightWhite: '#ebdbb2',
    },
  },
  light: {
    label: '浅色纸张',
    theme: {
      background: '#fbfbf8', foreground: '#24292f', cursor: '#0d9488', selectionBackground: '#0d948830',
      black: '#24292f', red: '#cf222e', green: '#116329', yellow: '#9a6700', blue: '#0969da', magenta: '#8250df', cyan: '#1b7c83', white: '#6e7781',
      brightBlack: '#57606a', brightRed: '#a40e26', brightGreen: '#1a7f37', brightYellow: '#633c01', brightBlue: '#218bff', brightMagenta: '#a475f9', brightCyan: '#3192aa', brightWhite: '#8c959f',
    },
  },
}

const SETTINGS_KEY = 'onessh-term-settings'
const DEFAULT_SETTINGS: TermSettings = {
  fontSize: 14,
  theme: 'onessh',
  cursorStyle: 'block',
  cursorBlink: true,
  copyOnSelect: true,
  rightClickPaste: true,
  scrollback: 10000,
  confirmClose: true,
}

const HANDSHAKE_TIMEOUT = 15_000
const FONT = '"JetBrainsMono Nerd Font Mono", "PingFang SC", "Microsoft YaHei", monospace'
let ghosttyReady: Promise<void> | undefined
let fontsReady: Promise<void> | undefined
let seq = 0

function loadSettings(): TermSettings {
  try {
    return { ...DEFAULT_SETTINGS, ...(JSON.parse(localStorage.getItem(SETTINGS_KEY) ?? '{}') as Partial<TermSettings>) }
  } catch {
    return DEFAULT_SETTINGS
  }
}

type Snapshot = { version: number; sessions: TermSession[]; settings: TermSettings; broadcast: boolean }

class TerminalManager {
  private sessions = new Map<string, TermSession>()
  private order: string[] = []
  private listeners = new Set<() => void>()
  private snapshot: Snapshot
  private parking: HTMLDivElement | null = null
  settings = loadSettings()
  broadcast = false

  constructor() {
    this.snapshot = { version: 0, sessions: [], settings: this.settings, broadcast: false }
    window.addEventListener('beforeunload', (event) => {
      if (this.list().some((s) => s.status === 'connected')) event.preventDefault()
    })
  }

  subscribe = (fn: () => void) => {
    this.listeners.add(fn)
    return () => this.listeners.delete(fn)
  }

  getSnapshot = () => this.snapshot

  private emit() {
    this.snapshot = { version: this.snapshot.version + 1, sessions: this.list().map((s) => ({ ...s })), settings: this.settings, broadcast: this.broadcast }
    for (const fn of this.listeners) fn()
  }

  list() {
    return this.order.map((id) => this.sessions.get(id)!).filter(Boolean)
  }

  get(id: string) {
    return this.sessions.get(id)
  }

  private park(): HTMLDivElement {
    if (!this.parking) {
      this.parking = document.createElement('div')
      Object.assign(this.parking.style, { position: 'fixed', left: '-10000px', top: '0', width: '960px', height: '540px', overflow: 'hidden', pointerEvents: 'none' })
      this.parking.setAttribute('aria-hidden', 'true')
      document.body.appendChild(this.parking)
    }
    return this.parking
  }

  create(host: string, opts: { cwd?: string; label?: string } = {}) {
    const id = `term-${++seq}`
    const element = document.createElement('div')
    element.className = 'onessh-term'
    Object.assign(element.style, { width: '100%', height: '100%' })
    this.park().appendChild(element)
    const session: TermSession = {
      id, host, cwd: opts.cwd, label: opts.label, title: '', status: 'connecting', createdAt: Date.now(),
      cols: 0, rows: 0, bytesIn: 0, bytesOut: 0, unread: false, bell: false, element, attempt: 0,
    }
    this.sessions.set(id, session)
    this.order.push(id)
    this.emit()
    void this.connect(id)
    return id
  }

  private patch(id: string, update: Partial<TermSession>) {
    const s = this.sessions.get(id)
    if (!s) return
    Object.assign(s, update)
    this.emit()
  }

  private makeTerminal(session: TermSession) {
    session.term?.dispose()
    session.element.innerHTML = ''
    const term = new GhosttyTerminal({
      cursorBlink: this.settings.cursorBlink,
      cursorStyle: this.settings.cursorStyle,
      fontFamily: FONT,
      fontSize: this.settings.fontSize,
      scrollback: this.settings.scrollback,
      theme: (THEMES[this.settings.theme] ?? THEMES.onessh).theme,
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(session.element)
    fit.fit()
    fit.observeResize()
    session.term = term
    session.fit = fit

    term.onData((data) => this.input(session.id, data))
    term.onResize(({ cols, rows }) => {
      session.cols = cols
      session.rows = rows
      const ws = session.ws
      if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'resize', cols, rows }))
      this.emit()
    })
    term.onTitleChange((title) => this.patch(session.id, { title }))
    term.onBell(() => this.patch(session.id, { bell: true }))
    term.onSelectionChange(() => {
      if (!this.settings.copyOnSelect || !term.hasSelection()) return
      const text = term.getSelection()
      if (text) void navigator.clipboard?.writeText(text).catch(() => undefined)
    })
    term.attachCustomKeyEventHandler((event) => {
      if (event.type !== 'keydown') return false
      const mod = event.ctrlKey || event.metaKey
      if (mod && event.shiftKey && event.code === 'KeyC') {
        const text = term.getSelection()
        if (text) void navigator.clipboard?.writeText(text)
        return true
      }
      if (mod && event.shiftKey && event.code === 'KeyV') {
        void this.pasteFromClipboard(session.id)
        return true
      }
      return false
    })
    session.element.oncontextmenu = (event) => {
      if (!this.settings.rightClickPaste) return
      event.preventDefault()
      if (term.hasSelection()) {
        void navigator.clipboard?.writeText(term.getSelection())
        term.clearSelection()
        toast.success('已复制选中内容')
      } else void this.pasteFromClipboard(session.id)
    }
    return term
  }

  async connect(id: string) {
    const session = this.sessions.get(id)
    if (!session) return
    const token = ++session.attempt
    this.detachSocket(session)
    this.patch(id, { status: 'connecting', closedAt: undefined })
    try {
      ghosttyReady ??= initGhostty()
      await ghosttyReady
      // Canvas 不会在字体晚到后自动重绘；常规与粗体都必须先就绪，否则 ANSI Bold 会用非等宽字体画进单元格
      fontsReady ??= Promise.all([
        document.fonts.load('400 14px "JetBrainsMono Nerd Font Mono"'),
        document.fonts.load('700 14px "JetBrainsMono Nerd Font Mono"'),
      ]).then(() => undefined)
      await fontsReady
    } catch (error) {
      ghosttyReady = undefined
      fontsReady = undefined
      if (session.attempt !== token) return
      toast.error(`终端初始化失败：${error instanceof Error ? error.message : String(error)}`)
      this.patch(id, { status: 'error' })
      return
    }
    if (session.attempt !== token || !this.sessions.has(id)) return
    // 重连沿用同一个终端实例：回滚缓冲区里之前的输出都还在
    const reconnecting = !!session.term
    const term = session.term ?? this.makeTerminal(session)
    if (reconnecting) term.write('\r\n\x1b[2m── 正在重新连接… ──\x1b[0m\r\n')

    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    const qs = new URLSearchParams({ host: session.host, cols: String(term.cols), rows: String(term.rows) })
    if (session.cwd) qs.set('cwd', session.cwd)
    const ws = new WebSocket(`${proto}://${location.host}/api/v1/ws/terminal?${qs}`)
    ws.binaryType = 'arraybuffer'
    session.ws = ws
    let opened = false
    const watchdog = window.setTimeout(() => {
      if (opened || session.attempt !== token) return
      this.detachSocket(session)
      term.write('\r\n\x1b[31m连接超时：15 秒内没有完成握手\x1b[0m\r\n')
      this.patch(id, { status: 'error', closedAt: Date.now() })
    }, HANDSHAKE_TIMEOUT)
    ws.onmessage = (event) => {
      const data = new Uint8Array(event.data as ArrayBuffer)
      session.bytesIn += data.length
      term.write(data)
      if (!session.element.isConnected || session.element.parentElement === this.parking) {
        if (!session.unread) this.patch(id, { unread: true })
      }
    }
    ws.onopen = () => {
      opened = true
      window.clearTimeout(watchdog)
      if (session.attempt !== token) return
      this.patch(id, { status: 'connected', connectedAt: Date.now(), cols: term.cols, rows: term.rows })
      term.focus()
    }
    ws.onclose = (event) => {
      window.clearTimeout(watchdog)
      if (session.attempt !== token) return
      if (opened) {
        term.write('\r\n\x1b[33m── 连接已关闭 ──\x1b[0m\r\n')
        this.patch(id, { status: 'closed', closedAt: Date.now() })
      } else {
        term.write(`\r\n\x1b[31m无法连接到 ${session.host}${event.reason ? `：${event.reason}` : ''}\x1b[0m\r\n`)
        this.patch(id, { status: 'error', closedAt: Date.now() })
      }
    }
  }

  private detachSocket(session: TermSession) {
    const ws = session.ws
    if (!ws) return
    ws.onopen = ws.onclose = ws.onerror = ws.onmessage = null
    ws.close()
    session.ws = undefined
  }

  /** 键盘输入：开启同步输入时广播到所有已连接的会话 */
  input(id: string, data: string) {
    const targets = this.broadcast ? this.list().filter((s) => s.status === 'connected') : [this.sessions.get(id)].filter(Boolean)
    for (const s of targets as TermSession[]) this.sendRaw(s, data)
  }

  private sendRaw(s: TermSession, data: string) {
    if (s.ws?.readyState !== WebSocket.OPEN) return
    s.ws.send(JSON.stringify({ type: 'input', data }))
    s.bytesOut += data.length
  }

  /** 快捷命令：发送到当前会话（或广播） */
  run(id: string, command: string, enter = true) {
    this.input(id, enter ? `${command}\r` : command)
    this.sessions.get(id)?.term?.focus()
  }

  async pasteFromClipboard(id: string) {
    const term = this.sessions.get(id)?.term
    if (!term) return
    try {
      const text = await navigator.clipboard.readText()
      if (text) term.paste(text)
    } catch {
      toast.error('浏览器不允许读取剪贴板，请用 Ctrl+Shift+V 或在地址栏旁授予剪贴板权限')
    }
  }

  copySelection(id: string) {
    const term = this.sessions.get(id)?.term
    const text = term?.getSelection()
    if (!text) return toast.info('先用鼠标选中要复制的内容')
    void navigator.clipboard.writeText(text).then(() => toast.success('已复制'))
  }

  attach(id: string, mount: HTMLElement) {
    const s = this.sessions.get(id)
    if (!s) return
    if (s.element.parentElement !== mount) mount.appendChild(s.element)
    requestAnimationFrame(() => {
      s.fit?.fit()
      if (s.unread || s.bell) this.patch(id, { unread: false, bell: false })
    })
  }

  detach(id: string, mount: HTMLElement) {
    const s = this.sessions.get(id)
    if (s && s.element.parentElement === mount) this.park().appendChild(s.element)
  }

  fit(id: string) {
    this.sessions.get(id)?.fit?.fit()
  }

  focus(id: string) {
    this.sessions.get(id)?.term?.focus()
  }

  close(id: string) {
    const s = this.sessions.get(id)
    if (!s) return
    s.attempt++
    this.detachSocket(s)
    s.term?.dispose()
    s.element.remove()
    this.sessions.delete(id)
    this.order = this.order.filter((x) => x !== id)
    this.emit()
  }

  disconnect(id: string) {
    const s = this.sessions.get(id)
    if (!s) return
    s.attempt++
    this.detachSocket(s)
    s.term?.write('\r\n\x1b[33m── 已手动断开 ──\x1b[0m\r\n')
    this.patch(id, { status: 'closed', closedAt: Date.now() })
  }

  rename(id: string, label: string) {
    this.patch(id, { label: label.trim() || undefined })
  }

  move(id: string, toIndex: number) {
    const from = this.order.indexOf(id)
    if (from < 0) return
    const next = [...this.order]
    next.splice(from, 1)
    next.splice(Math.max(0, Math.min(toIndex, next.length)), 0, id)
    this.order = next
    this.emit()
  }

  clear(id: string) {
    this.sessions.get(id)?.term?.clear()
  }

  setBroadcast(on: boolean) {
    this.broadcast = on
    this.emit()
  }

  updateSettings(update: Partial<TermSettings>) {
    this.settings = { ...this.settings, ...update }
    localStorage.setItem(SETTINGS_KEY, JSON.stringify(this.settings))
    for (const s of this.list()) {
      const term = s.term
      if (!term) continue
      if (update.cursorStyle) term.options.cursorStyle = update.cursorStyle
      if (update.cursorBlink !== undefined) term.options.cursorBlink = update.cursorBlink
      if (update.theme) term.renderer?.setTheme((THEMES[update.theme] ?? THEMES.onessh).theme)
      // 字号变化会触发整屏重绘；换主题时借它刷新一次画面
      if (update.fontSize || update.theme) term.options.fontSize = this.settings.fontSize
      requestAnimationFrame(() => s.fit?.fit())
    }
    this.emit()
  }

  /** 导出回滚缓冲区为纯文本 */
  exportText(id: string) {
    const term = this.sessions.get(id)?.term
    if (!term) return ''
    const buffer = term.buffer.active
    const lines: string[] = []
    for (let i = 0; i < buffer.length; i++) lines.push(buffer.getLine(i)?.translateToString(true) ?? '')
    while (lines.length && !lines[lines.length - 1]) lines.pop()
    return lines.join('\n')
  }

  /** 在回滚缓冲区里查找文本，返回所有命中位置 */
  search(id: string, needle: string, caseSensitive = false) {
    const term = this.sessions.get(id)?.term
    if (!term || !needle) return []
    const buffer = term.buffer.active
    const hits: Array<{ row: number; col: number }> = []
    const n = caseSensitive ? needle : needle.toLowerCase()
    for (let row = 0; row < buffer.length && hits.length < 2000; row++) {
      let line = buffer.getLine(row)?.translateToString(true) ?? ''
      if (!caseSensitive) line = line.toLowerCase()
      let col = line.indexOf(n)
      while (col >= 0) {
        hits.push({ row, col })
        col = line.indexOf(n, col + Math.max(1, n.length))
      }
    }
    return hits
  }

  reveal(id: string, hit: { row: number; col: number }, length: number) {
    const term = this.sessions.get(id)?.term
    if (!term) return
    // viewportY 表示「从底部往上滚了多少行」：0 即最底部
    const sb = term.getScrollbackLength()
    const viewportY = Math.max(0, Math.min(sb, sb - (hit.row - Math.floor(term.rows / 2))))
    term.scrollToLine(viewportY)
    const viewRow = hit.row - (sb - viewportY)
    if (viewRow >= 0 && viewRow < term.rows) term.select(hit.col, viewRow, length)
  }
}

export const terminals = new TerminalManager()

export function useTerminals() {
  return useSyncExternalStore(terminals.subscribe, terminals.getSnapshot)
}

export const sessionName = (s: Pick<TermSession, 'label' | 'host'>) => s.label || s.host

export type Snippet = { id: string; name: string; command: string; enter: boolean }

export const DEFAULT_SNIPPETS: Snippet[] = [
  { id: 's1', name: '系统概况', command: 'uname -a; cat /etc/os-release | head -3; uptime', enter: true },
  { id: 's2', name: '磁盘', command: 'df -hT -x tmpfs -x devtmpfs', enter: true },
  { id: 's3', name: '内存', command: 'free -h', enter: true },
  { id: 's4', name: 'CPU 占用前十', command: 'ps -eo pid,user,pcpu,pmem,comm --sort=-pcpu | head -11', enter: true },
  { id: 's5', name: '监听端口', command: 'ss -tlnp', enter: true },
  { id: 's6', name: '实时进程', command: 'top', enter: true },
  { id: 's7', name: 'Docker 容器', command: 'docker ps --format "table {{.Names}}\\t{{.Image}}\\t{{.Status}}\\t{{.Ports}}"', enter: true },
  { id: 's8', name: '系统日志', command: 'journalctl -n 100 --no-pager', enter: true },
  { id: 's9', name: '登录记录', command: 'last -n 15', enter: true },
  { id: 's10', name: '大文件 Top 10', command: 'du -ah . 2>/dev/null | sort -rh | head -10', enter: true },
]
