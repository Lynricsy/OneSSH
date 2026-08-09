import * as DialogPrimitive from '@radix-ui/react-dialog'
import { X } from '@phosphor-icons/react'
import { AnimatePresence, motion } from 'motion/react'
import { cn } from '@/lib/cn'
import { overlayTransition, popTransition } from '@/lib/motion'

/** Radix Dialog 的侧滑变体：移动端导航抽屉与图片预览共用 */
export function Sheet({
  open,
  onOpenChange,
  side = 'right',
  title,
  header,
  width = 'min(720px, 92vw)',
  className,
  children,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  side?: 'left' | 'right'
  title: string
  /** 自定义头部内容（如品牌块）；给出时 title 退为 sr-only，避免标题与内容重复 */
  header?: React.ReactNode
  width?: string
  className?: string
  children?: React.ReactNode
}) {
  const offset = side === 'left' ? '-100%' : '100%'
  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <AnimatePresence>
        {open && (
          <DialogPrimitive.Portal forceMount>
            <DialogPrimitive.Overlay asChild forceMount>
              <motion.div
                className="fixed inset-0 z-50 bg-black/45 backdrop-blur-[2px]"
                initial={{ opacity: 0 }}
                animate={{ opacity: 1 }}
                exit={{ opacity: 0 }}
                transition={overlayTransition}
              />
            </DialogPrimitive.Overlay>
            <DialogPrimitive.Content asChild forceMount>
              <motion.div
                className={cn(
                  'fixed inset-y-0 z-50 flex flex-col border-border bg-surface shadow-pop focus:outline-none',
                  side === 'left' ? 'left-0 border-r' : 'right-0 border-l',
                  className,
                )}
                style={{ width }}
                initial={{ x: offset }}
                animate={{ x: 0 }}
                exit={{ x: offset }}
                transition={popTransition}
              >
                <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-3">
                  {header ? (
                    <>
                      <DialogPrimitive.Title className="sr-only">{title}</DialogPrimitive.Title>
                      <div className="min-w-0 flex-1">{header}</div>
                    </>
                  ) : (
                    <DialogPrimitive.Title className="text-sm font-semibold text-text">
                      {title}
                    </DialogPrimitive.Title>
                  )}
                  <DialogPrimitive.Description className="sr-only">{title}</DialogPrimitive.Description>
                  <DialogPrimitive.Close
                    className="-mr-1 shrink-0 rounded-[8px] p-1 text-muted transition-colors hover:bg-surface-2 hover:text-text"
                    aria-label="关闭"
                  >
                    <X size={16} />
                  </DialogPrimitive.Close>
                </div>
                <div className="flex-1 overflow-y-auto">{children}</div>
              </motion.div>
            </DialogPrimitive.Content>
          </DialogPrimitive.Portal>
        )}
      </AnimatePresence>
    </DialogPrimitive.Root>
  )
}
