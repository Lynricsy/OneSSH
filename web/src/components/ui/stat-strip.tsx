import { motion } from 'motion/react'
import { cn } from '@/lib/cn'
import { staggerContainer, staggerItem } from '@/lib/motion'

export type StatItem = {
  label: string
  value: React.ReactNode
  hint?: React.ReactNode
  icon: React.ReactNode
  /** 图标底色，任意 CSS 颜色 */
  tone?: string
  onClick?: () => void
}

/** 页面顶部的一排小统计卡：各页面共用同一套视觉，保证整个控制台的节奏一致 */
export function StatStrip({ items, className }: { items: StatItem[]; className?: string }) {
  return (
    <motion.div
      className={cn('mb-5 grid grid-cols-2 gap-3 md:grid-cols-[repeat(auto-fit,minmax(176px,1fr))]', className)}
      variants={staggerContainer}
      initial="hidden"
      animate="visible"
    >
      {items.map((item) => {
        const tone = item.tone ?? 'var(--accent)'
        const Tag = item.onClick ? 'button' : 'div'
        return (
          <motion.div key={item.label} variants={staggerItem} className="min-w-0">
            <Tag
              type={item.onClick ? 'button' : undefined}
              onClick={item.onClick}
              className={cn(
                'group relative flex h-full w-full items-center gap-3 overflow-hidden rounded-[12px] border border-border bg-surface px-3.5 py-3 text-left shadow-card transition-all duration-200',
                item.onClick && 'hover:-translate-y-0.5 hover:border-border-strong hover:shadow-pop',
              )}
            >
              <span
                className="pointer-events-none absolute -right-6 -bottom-8 size-20 rounded-full opacity-[0.10] blur-xl transition-opacity group-hover:opacity-20"
                style={{ background: tone }}
              />
              <span
                className="grid size-9 shrink-0 place-items-center rounded-[10px]"
                style={{ background: `color-mix(in oklab, ${tone} 14%, transparent)`, color: tone }}
              >
                {item.icon}
              </span>
              <span className="min-w-0">
                <span className="block truncate text-[11.5px] font-medium text-muted">{item.label}</span>
                <span className="mt-0.5 block truncate text-[19px] leading-6 font-semibold tracking-tight text-text tabular-nums">
                  {item.value}
                </span>
                {item.hint && <span className="block truncate text-[11px] text-faint">{item.hint}</span>}
              </span>
            </Tag>
          </motion.div>
        )
      })}
    </motion.div>
  )
}
