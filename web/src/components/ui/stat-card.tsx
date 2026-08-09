import { animate, useMotionValue, useReducedMotion, useTransform, motion } from 'motion/react'
import { useEffect } from 'react'
import { Card } from './card'
import { COUNT_DURATION } from '@/lib/motion'

/**
 * 一排统计合成单张卡 + 竖分隔线。
 * 三张同构卡片是最典型的「模板感」，合成一条数据带既更密，也更像运维仪表盘。
 */
export function StatGroup({ children }: { children: React.ReactNode }) {
  return <Card className="grid grid-cols-3 divide-x divide-border">{children}</Card>
}

export function StatCard({
  title,
  value,
  suffix,
  icon,
}: {
  title: string
  value: number
  suffix?: string
  icon?: React.ReactNode
}) {
  const reduced = useReducedMotion()
  const count = useMotionValue(reduced ? value : 0)
  const rounded = useTransform(count, (v) => Math.round(v).toLocaleString())

  useEffect(() => {
    // 后台标签页里 rAF 不推进，动画会把数字永远钉在 0——这种情况直接落终值
    if (reduced || document.hidden) {
      count.set(value)
      return
    }
    const controls = animate(count, value, { duration: COUNT_DURATION, ease: 'easeOut' })
    return () => controls.stop()
  }, [value, reduced, count])

  return (
    <div className="min-w-0 px-4 py-4 md:px-5 md:py-5">
      <div className="flex items-center justify-between gap-2">
        <p className="truncate text-[11px] font-medium tracking-wide text-muted uppercase">{title}</p>
        {/* 窄屏三栏并排时图标只会挤压标签，直接让位 */}
        {icon && <span className="hidden shrink-0 text-faint sm:block">{icon}</span>}
      </div>
      <p className="mt-2.5 flex items-baseline gap-1">
        <motion.span className="text-[26px] leading-none font-semibold text-text tabular-nums md:text-[30px]">
          {rounded}
        </motion.span>
        {suffix && <span className="text-[12px] text-muted">{suffix}</span>}
      </p>
    </div>
  )
}
