import { forwardRef } from 'react'
import { cn } from '@/lib/cn'

export type TextareaProps = React.TextareaHTMLAttributes<HTMLTextAreaElement> & {
  invalid?: boolean
}

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(function Textarea(
  { className, invalid, ...props },
  ref,
) {
  return (
    <textarea
      ref={ref}
      aria-invalid={invalid || undefined}
      className={cn(
        'w-full resize-y rounded-[8px] border bg-surface px-3 py-2 font-mono text-[13px] leading-relaxed text-text',
        'placeholder:text-faint transition-colors duration-150',
        'hover:border-border-strong focus:border-accent focus:ring-2 focus:ring-accent/25 focus:outline-none',
        invalid ? 'border-danger' : 'border-border',
        className,
      )}
      {...props}
    />
  )
})
