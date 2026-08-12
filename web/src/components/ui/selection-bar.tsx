import { X } from '@phosphor-icons/react'
import { AnimatePresence, motion } from 'motion/react'
import { popTransition } from '@/lib/motion'
import { Button } from './button'

/**
 * 批量操作条：有选中项时浮在内容底部中央，与列表布局解耦（表格与卡片列表共用）。
 * 具体动作（删除/终止/下载……）由页面以 children 传入。
 */
export function SelectionBar({
  count,
  onClear,
  children,
}: {
  count: number
  onClear: () => void
  children?: React.ReactNode
}) {
  return (
    <>
      {/*
        计数播报交给常驻的 sr-only 区域：整条 bar 随选择出现/消失地挂载，
        与内容同时插入 DOM 的 live region 不会被朗读，可见文案因此改为 aria-hidden。
      */}
      <span aria-live="polite" aria-atomic="true" className="sr-only">
        {count > 0 ? `已选 ${count} 项` : ''}
      </span>
      <AnimatePresence>
        {count > 0 && (
          <motion.div
            className="fixed bottom-6 left-1/2 z-40"
            // x 走 style 而不是 -translate-x-1/2：motion 会覆写 transform 属性
            style={{ x: '-50%' }}
            initial={{ opacity: 0, y: 12 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, y: 12 }}
            transition={popTransition}
          >
            <div className="flex items-center gap-2 rounded-full border border-border bg-surface py-1.5 pr-1.5 pl-4 shadow-pop">
              <span
                aria-hidden="true"
                className="text-[13px] whitespace-nowrap text-muted tabular-nums"
              >
                已选 {count} 项
              </span>
              {children}
              <Button
                variant="ghost"
                size="icon"
                className="size-7"
                aria-label="清除选择"
                title="清除选择"
                onClick={onClear}
              >
                <X size={14} />
              </Button>
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </>
  )
}
