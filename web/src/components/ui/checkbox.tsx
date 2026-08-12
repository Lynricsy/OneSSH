import * as CheckboxPrimitive from '@radix-ui/react-checkbox'
import { Check, Minus } from '@phosphor-icons/react'
import { cn } from '@/lib/cn'

/** 样式与 multi-select 内嵌勾选框同出一源；'indeterminate' 半选态用于表头「全选」 */
export function Checkbox({
  checked,
  onCheckedChange,
  disabled,
  className,
  'aria-label': ariaLabel,
}: {
  checked: boolean | 'indeterminate'
  onCheckedChange?: (checked: boolean | 'indeterminate') => void
  disabled?: boolean
  className?: string
  'aria-label'?: string
}) {
  return (
    <CheckboxPrimitive.Root
      checked={checked}
      onCheckedChange={onCheckedChange}
      disabled={disabled}
      aria-label={ariaLabel}
      className={cn(
        'flex size-4 shrink-0 items-center justify-center rounded-[4px] border border-border-strong transition-colors',
        'focus-visible:ring-2 focus-visible:ring-accent/25 focus-visible:outline-none',
        'data-[state=checked]:border-accent data-[state=checked]:bg-accent',
        'data-[state=indeterminate]:border-accent data-[state=indeterminate]:bg-accent',
        'disabled:cursor-not-allowed disabled:opacity-40',
        className,
      )}
    >
      <CheckboxPrimitive.Indicator>
        {checked === 'indeterminate' ? (
          <Minus size={11} weight="bold" className="text-accent-fg" />
        ) : (
          <Check size={11} weight="bold" className="text-accent-fg" />
        )}
      </CheckboxPrimitive.Indicator>
    </CheckboxPrimitive.Root>
  )
}
