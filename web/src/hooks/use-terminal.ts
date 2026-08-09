import { useCallback, useEffect, useRef, useState } from 'react'
import { FitAddon, Terminal as GhosttyTerminal, init as initGhostty } from 'ghostty-web'
import { toast } from 'sonner'

export type TerminalStatus = 'idle' | 'connecting' | 'connected' | 'closed' | 'error'

let ghosttyReady: Promise<void> | undefined

/**
 * 握手看门狗：目标主机不可达时后端会长时间挂在 SSH 拨号上，WebSocket 既不 open 也不 close，
 * 状态就会永远停在「连接中」。超过这个时限一律判失败，保证状态机必然收敛。
 */
const HANDSHAKE_TIMEOUT = 15_000

export function useTerminal() {
  const mountRef = useRef<HTMLDivElement>(null)
  const terminal = useRef<GhosttyTerminal>()
  const socket = useRef<WebSocket>()
  const watchdog = useRef<number>()
  // 每次 connect 递增：异步回调用它判断自己是否已被后来的连接取代
  const attempt = useRef(0)
  const [status, setStatus] = useState<TerminalStatus>('idle')

  /** 拆掉旧连接的回调再关闭，否则它的 onclose 会覆盖新连接的状态 */
  const detach = useCallback(() => {
    window.clearTimeout(watchdog.current)
    const ws = socket.current
    if (!ws) return
    ws.onopen = ws.onclose = ws.onerror = ws.onmessage = null
    ws.close()
    socket.current = undefined
  }, [])

  useEffect(
    () => () => {
      attempt.current += 1
      detach()
      terminal.current?.dispose()
    },
    [detach],
  )

  const disconnect = useCallback(() => {
    // 主动断开也要作废进行中的尝试，避免看门狗事后再把状态改成 error
    attempt.current += 1
    detach()
    setStatus((prev) => (prev === 'connected' ? 'closed' : 'idle'))
  }, [detach])

  const connect = useCallback(
    async (hostName: string) => {
      const mount = mountRef.current
      if (!mount) return

      const token = (attempt.current += 1)
      detach()
      setStatus('connecting')

      try {
        ghosttyReady ??= initGhostty()
        await ghosttyReady
      } catch (error) {
        // 失败的 promise 会被永久缓存，清掉才允许下次重试
        ghosttyReady = undefined
        if (attempt.current !== token) return
        toast.error(
          `Ghostty WASM 初始化失败：${error instanceof Error ? error.message : String(error)}`,
        )
        setStatus('error')
        return
      }
      if (attempt.current !== token) return

      terminal.current?.dispose()
      mount.innerHTML = ''

      // WASM canvas 若在字体加载前测量字宽会永久使用错误的单元格尺寸，因此先等待字体可用
      await document.fonts.load('14px "JetBrainsMono Nerd Font Mono"').catch(() => {})
      if (attempt.current !== token) return

      const term = new GhosttyTerminal({
        cursorBlink: true,
        fontFamily: '"JetBrainsMono Nerd Font Mono", "PingFang SC", monospace',
        fontSize: 14,
        theme: { background: '#0b0d10', foreground: '#d6e4ff' },
      })
      const fit = new FitAddon()
      term.loadAddon(fit)
      term.open(mount)
      fit.fit()
      fit.observeResize()
      terminal.current = term

      const proto = location.protocol === 'https:' ? 'wss' : 'ws'
      const ws = new WebSocket(
        `${proto}://${location.host}/api/v1/ws/terminal?host=${encodeURIComponent(hostName)}&cols=${term.cols}&rows=${term.rows}`,
      )
      ws.binaryType = 'arraybuffer'
      socket.current = ws

      let opened = false
      ws.onmessage = (event) => term.write(new Uint8Array(event.data))
      ws.onopen = () => {
        opened = true
        window.clearTimeout(watchdog.current)
        if (attempt.current !== token) return
        term.focus()
        setStatus('connected')
      }
      // 不挂 onerror：握手失败时它只是 onclose 的前奏，提示集中在 onclose 才不会弹两次
      ws.onclose = () => {
        window.clearTimeout(watchdog.current)
        if (attempt.current !== token) return
        if (opened) {
          term.write('\r\n\x1b[33m连接已关闭\x1b[0m')
          setStatus('closed')
        } else {
          term.write(`\r\n\x1b[31m无法连接到 ${hostName}\x1b[0m`)
          toast.error(`无法连接到 ${hostName}`)
          setStatus('error')
        }
      }
      watchdog.current = window.setTimeout(() => {
        if (attempt.current !== token || opened) return
        detach()
        term.write('\r\n\x1b[31m连接超时：后端未在 15 秒内完成握手\x1b[0m')
        toast.error(`连接 ${hostName} 超时`)
        setStatus('error')
      }, HANDSHAKE_TIMEOUT)

      term.onData(
        (data) =>
          ws.readyState === WebSocket.OPEN && ws.send(JSON.stringify({ type: 'input', data })),
      )
      term.onResize(
        ({ cols, rows }) =>
          ws.readyState === WebSocket.OPEN &&
          ws.send(JSON.stringify({ type: 'resize', cols, rows })),
      )
    },
    [detach],
  )

  return { mountRef, status, connect, disconnect }
}
