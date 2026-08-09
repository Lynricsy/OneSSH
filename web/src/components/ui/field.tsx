import { useId } from 'react'
import { cn } from '@/lib/cn'
import { Label } from './label'

/**
 * 表单字段外壳：把 label / 必填标记 / 行内错误三件事收敛在一处。
 * 表单错误一律行内红字，不走 toast——toast 只用于服务端结果。
 */
export function Field({
  label,
  error,
  required,
  hint,
  className,
  children,
}: {
  label: string
  error?: string
  required?: boolean
  hint?: string
  className?: string
  /** 接收字段 id，绑定到具体控件上以保证 label 可点 */
  children: (id: string) => React.ReactNode
}) {
  const id = useId()
  return (
    <div className={cn('space-y-1.5', className)}>
      <Label htmlFor={id}>
        {label}
        {required && <span className="ml-0.5 text-danger">*</span>}
      </Label>
      {children(id)}
      {error ? (
        <p className="text-[12px] text-danger">{error}</p>
      ) : hint ? (
        <p className="text-[12px] text-muted">{hint}</p>
      ) : null}
    </div>
  )
}
