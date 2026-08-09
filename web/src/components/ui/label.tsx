import * as LabelPrimitive from '@radix-ui/react-label'
import { forwardRef } from 'react'
import { cn } from '@/lib/cn'

export const Label = forwardRef<
  React.ElementRef<typeof LabelPrimitive.Root>,
  React.ComponentPropsWithoutRef<typeof LabelPrimitive.Root>
>(function Label({ className, ...props }, ref) {
  return (
    <LabelPrimitive.Root
      ref={ref}
      className={cn('text-[13px] font-medium text-text select-none', className)}
      {...props}
    />
  )
})
