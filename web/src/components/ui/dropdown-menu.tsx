import * as DropdownMenuPrimitive from '@radix-ui/react-dropdown-menu'
import { forwardRef } from 'react'
import { cn } from '@/lib/cn'

export const DropdownMenu = DropdownMenuPrimitive.Root
export const DropdownMenuTrigger = DropdownMenuPrimitive.Trigger

export const DropdownMenuContent = forwardRef<
  React.ElementRef<typeof DropdownMenuPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof DropdownMenuPrimitive.Content>
>(function DropdownMenuContent({ className, sideOffset = 6, ...props }, ref) {
  return (
    <DropdownMenuPrimitive.Portal>
      <DropdownMenuPrimitive.Content
        ref={ref}
        sideOffset={sideOffset}
        className={cn(
          'z-50 min-w-40 rounded-[12px] border border-border bg-surface p-1 shadow-pop',
          className,
        )}
        {...props}
      />
    </DropdownMenuPrimitive.Portal>
  )
})

export const DropdownMenuItem = forwardRef<
  React.ElementRef<typeof DropdownMenuPrimitive.Item>,
  React.ComponentPropsWithoutRef<typeof DropdownMenuPrimitive.Item> & { danger?: boolean }
>(function DropdownMenuItem({ className, danger, ...props }, ref) {
  return (
    <DropdownMenuPrimitive.Item
      ref={ref}
      className={cn(
        'flex cursor-pointer items-center gap-2 rounded-[8px] px-2.5 py-1.5 text-[13px] select-none',
        'data-[highlighted]:bg-surface-2 data-[highlighted]:outline-none',
        danger ? 'text-danger' : 'text-text',
        className,
      )}
      {...props}
    />
  )
})

export const DropdownMenuSeparator = () => (
  <DropdownMenuPrimitive.Separator className="my-1 h-px bg-border" />
)

export const DropdownMenuLabel = ({ children }: { children: React.ReactNode }) => (
  <DropdownMenuPrimitive.Label className="px-2.5 py-1.5 text-[11px] font-medium tracking-wide text-muted uppercase">
    {children}
  </DropdownMenuPrimitive.Label>
)
