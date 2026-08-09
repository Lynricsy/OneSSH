import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/cn'

/** badge 是圆角体系里唯一的 4px 例外，锁定在此文件 */
const badge = cva(
  'inline-flex items-center gap-1.5 rounded-[4px] px-2 py-0.5 text-[11px] font-medium whitespace-nowrap',
  {
    variants: {
      variant: {
        default: 'bg-surface-2 text-muted',
        accent: 'bg-accent-soft text-accent',
        success: 'bg-success/12 text-success',
        warning: 'bg-warning/12 text-warning',
        danger: 'bg-danger/12 text-danger',
        outline: 'border border-border text-muted',
      },
    },
    defaultVariants: { variant: 'default' },
  },
)

export type BadgeProps = React.HTMLAttributes<HTMLSpanElement> & VariantProps<typeof badge>

export function Badge({ className, variant, ...props }: BadgeProps) {
  return <span className={cn(badge({ variant }), className)} {...props} />
}

/** 状态点：running 场景加脉冲 */
export function Dot({ className, pulse }: { className?: string; pulse?: boolean }) {
  return (
    <span
      className={cn('inline-block size-1.5 rounded-full bg-current', pulse && 'animate-pulse', className)}
      aria-hidden
    />
  )
}
