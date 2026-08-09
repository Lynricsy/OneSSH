import { Broadcast, Pulse } from '@phosphor-icons/react'
import { AnimatePresence, motion, useReducedMotion } from 'motion/react'
import { useAudit } from '@/api/queries'
import type { Audit } from '@/api/types'
import { Badge, Dot } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { DataTable, type Column } from '@/components/ui/data-table'
import { EmptyState } from '@/components/ui/empty-state'
import { PageHeader } from '@/components/ui/page-header'
import { PageTransition } from '@/components/ui/page-transition'
import { useEventStream } from '@/hooks/use-event-stream'
import { cn } from '@/lib/cn'
import { formatBytes } from '@/lib/format'

/** 审计与事件流的时间戳是「毫秒」，与 lib/format 的「秒」契约不同，故本页单独格式化 */
const clock = (ms: number) => new Date(ms).toLocaleTimeString('zh-CN', { hour12: false })

/** 列定义与渲染无关联状态，放模块级避免每次渲染重建 */
const auditColumns: Column<Audit>[] = [
  {
    key: 'Ts',
    title: '时间',
    // 审计日志没有时间就失去一半价值，窄屏也保留，靠收紧单元格内边距让出空间
    className: 'w-[84px] font-mono text-[12px] text-muted tabular-nums',
    render: (item) => clock(item.Ts),
  },
  {
    key: 'Tool',
    title: '工具',
    className: 'font-mono text-[13px]',
    render: (item) => item.Tool,
  },
  {
    key: 'Host',
    title: '主机',
    className: 'hidden md:table-cell text-muted',
    render: (item) => (item.Host?.Valid ? item.Host.String : '—'),
  },
  {
    key: 'OK',
    title: '结果',
    className: 'w-[80px]',
    // 一屏几十行里全是绿 badge 会淹没真正需要注意的失败，成功态降级为中性文字 + 状态点
    render: (item) =>
      item.OK ? (
        <span className="inline-flex items-center gap-1.5 text-[12px] text-muted">
          <Dot className="text-success" />
          成功
        </span>
      ) : (
        <Badge variant="danger">失败</Badge>
      ),
  },
  {
    key: 'BytesOut',
    title: '输出',
    className: 'hidden lg:table-cell w-[88px] text-right text-muted tabular-nums',
    render: (item) => (item.BytesOut ? formatBytes(item.BytesOut) : '—'),
  },
  {
    key: 'DurationMS',
    title: '耗时',
    className: 'w-[92px] text-right tabular-nums',
    // 四位数以上的毫秒既难读又会撑宽列，统一在 1s 处进位
    render: (item) =>
      item.DurationMS < 1000 ? `${item.DurationMS} ms` : `${(item.DurationMS / 1000).toFixed(2)} s`,
  },
]

export function ActivityPage() {
  const { events, status } = useEventStream()
  const audit = useAudit()
  const reduceMotion = useReducedMotion()
  /**
   * 后端 SSE 在推出第一条事件前不会 flush 响应头，浏览器的 readyState 会长期停在
   * CONNECTING、onopen 迟迟不触发——对用户来说「已订阅但还没事件」与「已连接」是同一件事，
   * 因此展示层只区分「监听中 / 已断开」。
   */
  const broken = status === 'error'

  return (
    <PageTransition>
      <PageHeader title="活动流" subtitle="实时工具调用、输出与结构化审计" />

      {/* 两栏在 xl 起固定为一屏高：事件与审计都是「持续刷新的流」，各自内部滚动比把页面拉成几千像素更好用 */}
      <div className="grid gap-4 xl:h-[calc(100dvh-11.5rem)] xl:min-h-[520px] xl:grid-cols-[2fr_3fr]">
        <Card className="flex max-h-[60dvh] min-h-0 min-w-0 flex-col xl:max-h-none">
          <CardHeader>
            <CardTitle>实时事件</CardTitle>
            <span
              className={cn(
                'flex items-center gap-1.5 text-[12px]',
                broken ? 'text-danger' : 'text-success',
              )}
            >
              <Dot pulse={!broken} />
              {broken ? '已断开' : '监听中'}
              {events.length > 0 && (
                <span className="text-muted tabular-nums">· {events.length} 条</span>
              )}
            </span>
          </CardHeader>
          <CardContent className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto p-3">
            {events.length === 0 ? (
              <EmptyState
                className="m-auto [&_p]:text-balance"
                icon={<Broadcast size={22} />}
                title="等待事件…"
                description="工具调用与后台任务状态会在发生的瞬间推送到这里。"
              />
            ) : (
              <AnimatePresence initial={false}>
                {events.map((event) => (
                  <motion.article
                    key={event.id}
                    className="shrink-0 rounded-[8px] bg-surface-2 px-3 py-2.5"
                    initial={reduceMotion ? false : { opacity: 0, y: -6 }}
                    animate={{ opacity: 1, y: 0 }}
                    transition={{ duration: 0.18, ease: 'easeOut' }}
                  >
                    <div className="flex items-center justify-between gap-3">
                      <Badge variant="outline" className="font-mono">
                        {event.type}
                      </Badge>
                      <time
                        dateTime={new Date(event.ts).toISOString()}
                        className="text-[12px] text-muted tabular-nums"
                      >
                        {clock(event.ts)}
                      </time>
                    </div>
                    {/*
                      载荷压成单行：这是「流」不是「详情页」，缩进 JSON 会让每条事件占掉
                      六七行，一屏只剩四五条；限高兜住 exec_output 那种整块 stdout。
                    */}
                    <pre className="mt-1.5 max-h-24 overflow-auto font-mono text-[12px] leading-[1.6] break-words whitespace-pre-wrap text-muted">
                      {JSON.stringify(event.data)}
                    </pre>
                  </motion.article>
                ))}
              </AnimatePresence>
            )}
          </CardContent>
        </Card>

        <Card className="flex max-h-[60dvh] min-h-0 min-w-0 flex-col xl:max-h-none">
          <CardHeader>
            <CardTitle>审计记录</CardTitle>
            {audit.data && audit.data.length > 0 && (
              <span className="text-[12px] text-muted tabular-nums">
                最近 {audit.data.length} 条
              </span>
            )}
          </CardHeader>
          <CardContent className="flex min-h-0 flex-1 flex-col p-0">
            <DataTable
              stickyHeader
              // 表格自身即滚动容器（overflow-x-auto 会让 y 轴一并变成 auto）；窄屏收紧单元格内边距，
              // 让「时间」列不用被砍掉也能塞下四列
              className="min-h-0 flex-1 [&_td]:px-3 [&_th]:px-3 sm:[&_td]:px-4 sm:[&_th]:px-4"
              columns={auditColumns}
              rows={audit.data}
              rowKey={(item) => item.ID}
              loading={audit.isLoading}
              empty={
                <EmptyState
                  className="[&_p]:text-balance"
                  icon={<Pulse size={22} />}
                  title="暂无审计记录"
                  description="Agent 通过 MCP 网关调用工具后，每一次调用都会记录在这里。"
                />
              }
            />
          </CardContent>
        </Card>
      </div>
    </PageTransition>
  )
}
