import { Terminal as TerminalIcon } from '@phosphor-icons/react'
import { useState } from 'react'
import { useHosts } from '@/api/queries'
import { Dot } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { EmptyState } from '@/components/ui/empty-state'
import { PageHeader } from '@/components/ui/page-header'
import { PageTransition } from '@/components/ui/page-transition'
import { Select } from '@/components/ui/select'
import { useTerminal, type TerminalStatus } from '@/hooks/use-terminal'
import { cn } from '@/lib/cn'

const statusPresentation: Record<
  TerminalStatus,
  { label: string; className: string; pulse?: boolean }
> = {
  idle: { label: '未连接', className: 'text-faint' },
  connecting: { label: '正在握手', className: 'text-warning', pulse: true },
  connected: { label: '已连接', className: 'text-success', pulse: true },
  closed: { label: '已断开', className: 'text-muted' },
  error: { label: '连接失败', className: 'text-danger' },
}

export function TerminalPage() {
  const { data: hosts = [], isLoading } = useHosts()
  const [host, setHost] = useState<string>()
  const { mountRef, status, connect, disconnect } = useTerminal()
  const current = statusPresentation[status]

  return (
    // 终端页锁定视口高度：工具条固定、终端吃满剩余空间，整页不产生外层滚动。
    // PageTransition 的 motion.div 挡在中间，只能用子选择器把高度传下去。
    <div className="h-[calc(100dvh-3.5rem)] overflow-hidden [&>div]:flex [&>div]:h-full [&>div]:flex-col">
      <PageTransition>
        <div className="flex min-h-0 flex-1 flex-col gap-3 px-4 py-4 md:px-8">
          <div className="shrink-0 [&>div]:mb-0">
            <PageHeader
              title="交互终端"
              subtitle="Ghostty WASM · xterm-256color WebSocket PTY"
            />
          </div>

          <Card className="flex shrink-0 flex-wrap items-center gap-2 p-2.5">
            <Select
              value={host}
              onChange={setHost}
              options={hosts.map((item) => ({ value: item.name, label: item.name }))}
              placeholder="选择主机"
              disabled={isLoading}
              className="w-full sm:w-56"
            />
            <Button
              variant="primary"
              onClick={() => {
                if (host) void connect(host)
              }}
              disabled={!host || status === 'connecting'}
              loading={status === 'connecting'}
            >
              {status === 'connected' ? '重新连接' : '连接'}
            </Button>
            {(status === 'connecting' || status === 'connected') && (
              <Button variant="ghost" onClick={disconnect}>
                {status === 'connecting' ? '取消' : '断开'}
              </Button>
            )}
            <span
              className={cn('ml-auto inline-flex items-center gap-2 text-[13px]', current.className)}
              aria-live="polite"
            >
              <Dot pulse={current.pulse} />
              {current.label}
            </span>
          </Card>

          {/*
            终端恒为深底（终端惯例，不跟随主题）。深底由内层的 .dark 提供而不是写死 #0b0d10：
            令牌整体切到深色档，底色即 --bg(#0b0d10)，内部文字/图标也一并拿到深底上
            可读的颜色——浅色主题下空态文字才不会糊成一片。
            外层 section 留在页面自身的主题里，用与其它卡片同一套边框和阴影收边，
            这块黑色才是版面里的一块「屏幕」，而不是一个突兀的黑洞。
          */}
          <section
            className="relative min-h-0 flex-1 overflow-hidden rounded-[12px] border border-border shadow-card"
            aria-label="终端会话"
          >
            <div className="dark relative h-full w-full bg-bg p-2">
              <div ref={mountRef} className="h-full w-full" />
              {status === 'idle' && (
                // 不吃指针事件；bg-bg 与终端同色，取消连接回到 idle 时能盖住上一次会话的残留输出
                <div className="pointer-events-none absolute inset-0 flex items-center justify-center bg-bg">
                  <EmptyState
                    icon={<TerminalIcon size={22} />}
                    title={host ? `准备连接 ${host}` : '选择一台主机开始会话'}
                    description={
                      host
                        ? '点击「连接」建立 PTY 会话，输入内容会直接送往远端 shell。'
                        : '在上方选择主机后即可建立交互式 SSH 会话。'
                    }
                  />
                </div>
              )}
            </div>
          </section>
        </div>
      </PageTransition>
    </div>
  )
}
