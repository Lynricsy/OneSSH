import { useEffect } from 'react'
import { logout } from '@/api/account'

const KEY = 'onessh-idle-minutes'
const LAST = 'onessh-last-activity'

export const getIdleMinutes = () => Number(localStorage.getItem(KEY) || 0)
export const setIdleMinutes = (m: number) => localStorage.setItem(KEY, String(m))

/** 空闲自动退出：多个标签页共享同一个「最后活动时间」，任意一个页面有操作都算 */
export function useIdleLock(onLock: () => void) {
  useEffect(() => {
    let lastWrite = 0
    const touch = () => {
      const now = Date.now()
      if (now - lastWrite > 5000) {
        lastWrite = now
        localStorage.setItem(LAST, String(now))
      }
    }
    touch()
    const events = ['mousemove', 'keydown', 'mousedown', 'wheel', 'touchstart'] as const
    for (const e of events) window.addEventListener(e, touch, { passive: true })
    const timer = window.setInterval(() => {
      const minutes = getIdleMinutes()
      if (!minutes) return
      const last = Number(localStorage.getItem(LAST) || Date.now())
      if (Date.now() - last > minutes * 60_000) {
        void logout().then(onLock)
      }
    }, 20_000)
    return () => {
      for (const e of events) window.removeEventListener(e, touch)
      window.clearInterval(timer)
    }
  }, [onLock])
}
