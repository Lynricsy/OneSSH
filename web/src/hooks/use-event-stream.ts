import { useEffect, useRef, useState } from 'react'
import type { StreamEvent } from '@/api/types'

/** 连接状态需要暴露给 UI：SSE 断了页面必须诚实地显示，而不是一直亮着绿点 */
export type StreamStatus = 'connecting' | 'open' | 'error'

/**
 * 服务端事件没有唯一 id，而事件是往列表头部插入的——用数组下标当 key 会让
 * 每来一条新事件后全部旧事件的 key 发生错位，React 复用错节点、入场动画重放。
 * 这里补一个客户端自增序号作为稳定 key。
 */
export type StreamEventItem = StreamEvent & { id: number }

export function useEventStream(limit = 100): {
  events: StreamEventItem[]
  status: StreamStatus
} {
  const [events, setEvents] = useState<StreamEventItem[]>([])
  const [status, setStatus] = useState<StreamStatus>('connecting')
  const seq = useRef(0)

  useEffect(() => {
    const source = new EventSource('/api/v1/events')

    source.onopen = () => setStatus('open')
    // EventSource 自身会重连，onerror 后仍处于 CONNECTING 只是短暂抖动，不该报「已断开」
    source.onerror = () =>
      setStatus(source.readyState === EventSource.CLOSED ? 'error' : 'connecting')
    source.onmessage = (event) => {
      seq.current += 1
      const value: StreamEventItem = {
        ...(JSON.parse(event.data) as StreamEvent),
        id: seq.current,
      }
      setEvents((previous) => [value, ...previous].slice(0, limit))
    }

    // EventSource 已内建断线重连，额外重试会制造重复连接与重复事件。
    return () => source.close()
  }, [limit])

  return { events, status }
}
