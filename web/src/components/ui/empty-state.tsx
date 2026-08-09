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
        <div className="flex size-12 items-center justify-center rounded-[12px] bg-surface-2 text-muted">
          {icon}
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
