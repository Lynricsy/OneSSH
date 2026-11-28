import { cn } from '@/lib/cn'

export function EmptyState({
  icon,
  title,
  description,
  action,
  className,
}: {
  icon?: React.ReactNode
  title: string
  description?: string
  action?: React.ReactNode
  className?: string
}) {
  return (
    <div className={cn('flex flex-col items-center justify-center gap-3 px-6 py-14 text-center', className)}>
      {icon && (
        <div className="relative grid size-16 place-items-center">
          {/* 两圈虚线光环把空态图标托起来，比一个灰方块更有「等待被填满」的意思 */}
          <span className="absolute inset-0 rounded-full border border-dashed border-border-strong" />
          <span className="absolute inset-2 rounded-full bg-linear-to-br from-accent/14 to-[var(--accent-2)]/10" />
          <span className="relative grid size-10 place-items-center rounded-[12px] bg-surface text-accent shadow-card">{icon}</span>
        </div>
      )}
      <div className="space-y-1">
        <p className="text-sm font-medium text-balance text-text">{title}</p>
        {description && (
          <p className="max-w-sm text-[13px] text-balance text-muted">{description}</p>
        )}
      </div>
      {action}
    </div>
  )
}
