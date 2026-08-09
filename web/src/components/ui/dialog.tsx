import * as DialogPrimitive from '@radix-ui/react-dialog'
import { X } from '@phosphor-icons/react'
import { AnimatePresence, motion } from 'motion/react'
import { cn } from '@/lib/cn'
import { overlayTransition, popTransition } from '@/lib/motion'

const widths = { sm: 'max-w-sm', md: 'max-w-lg', lg: 'max-w-2xl' } as const

export function Dialog({
  open,
  onOpenChange,
  title,
  description,
  size = 'md',
  footer,
  children,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description?: string
  size?: keyof typeof widths
  footer?: React.ReactNode
  children?: React.ReactNode
}) {
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
                  'fixed top-1/2 left-1/2 z-50 flex max-h-[88dvh] w-[calc(100vw-2rem)] flex-col',
                  'rounded-[12px] border border-border bg-surface shadow-pop focus:outline-none',
                  widths[size],
                )}
                style={{ x: '-50%', y: '-50%' }}
                initial={{ opacity: 0, scale: 0.96 }}
                animate={{ opacity: 1, scale: 1 }}
                exit={{ opacity: 0, scale: 0.96 }}
                transition={popTransition}
              >
                <div className="flex items-start justify-between gap-3 border-b border-border px-5 py-4">
                  <div className="space-y-1">
                    <DialogPrimitive.Title className="text-sm font-semibold text-text">
                      {title}
                    </DialogPrimitive.Title>
                    {description ? (
                      <DialogPrimitive.Description className="text-[13px] text-muted">
                        {description}
                      </DialogPrimitive.Description>
                    ) : (
                      <DialogPrimitive.Description className="sr-only">{title}</DialogPrimitive.Description>
                    )}
                  </div>
                  <DialogPrimitive.Close
                    className="-mr-1 rounded-[8px] p-1 text-muted transition-colors hover:bg-surface-2 hover:text-text"
                    aria-label="关闭"
                  >
                    <X size={16} />
                  </DialogPrimitive.Close>
                </div>
                <div className="overflow-y-auto px-5 py-4">{children}</div>
                {footer && (
                  <div className="flex justify-end gap-2 border-t border-border px-5 py-3">{footer}</div>
                )}
              </motion.div>
            </DialogPrimitive.Content>
          </DialogPrimitive.Portal>
        )}
      </AnimatePresence>
    </DialogPrimitive.Root>
  )
}
