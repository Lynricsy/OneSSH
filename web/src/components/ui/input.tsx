import { forwardRef } from 'react'
import { cn } from '@/lib/cn'

export type InputProps = Omit<React.InputHTMLAttributes<HTMLInputElement>, 'prefix'> & {
  /** 左侧图标槽 */
  prefix?: React.ReactNode
  invalid?: boolean
}

export const Input = forwardRef<HTMLInputElement, InputProps>(function Input(
  { className, prefix, invalid, ...props },
  ref,
) {
  const field = (
    <input
      ref={ref}
      aria-invalid={invalid || undefined}
      className={cn(
        'h-9 w-full rounded-[8px] border bg-surface px-3 text-sm text-text',
        'placeholder:text-faint transition-colors duration-150',
        'hover:border-border-strong focus:border-accent focus:ring-2 focus:ring-accent/25 focus:outline-none',
        'disabled:cursor-not-allowed disabled:opacity-60',
        invalid ? 'border-danger' : 'border-border',
        prefix && 'pl-9',
        className,
      )}
      {...props}
    />
  )
  if (!prefix) return field
  return (
    <div className="relative">
      <span className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-faint">
        {prefix}
      </span>
      {field}
    </div>
  )
})
