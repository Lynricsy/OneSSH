import * as PopoverPrimitive from '@radix-ui/react-popover'
import * as CheckboxPrimitive from '@radix-ui/react-checkbox'
import { CaretDown, Check } from '@phosphor-icons/react'
import { cn } from '@/lib/cn'
import { Badge } from './badge'

export function MultiSelect({
  value,
  onChange,
  options,
  placeholder = '选择主机',
  invalid,
  id,
}: {
  value: number[]
  onChange: (value: number[]) => void
  options: { value: number; label: string }[]
  placeholder?: string
  invalid?: boolean
  id?: string
}) {
  const toggle = (v: number) =>
    onChange(value.includes(v) ? value.filter((x) => x !== v) : [...value, v])

  return (
    <PopoverPrimitive.Root>
      <PopoverPrimitive.Trigger
        id={id}
        aria-invalid={invalid || undefined}
        className={cn(
          'flex min-h-9 w-full items-center justify-between gap-2 rounded-[8px] border bg-surface px-3 py-1.5 text-sm',
          'transition-colors duration-150 hover:border-border-strong',
          'focus:border-accent focus:ring-2 focus:ring-accent/25 focus:outline-none',
          invalid ? 'border-danger' : 'border-border',
        )}
      >
        {value.length === 0 ? (
          <span className="text-muted">{placeholder}</span>
        ) : (
          <span className="flex flex-wrap gap-1">
            {value.map((v) => (
              <Badge key={v} variant="accent">
                {options.find((o) => o.value === v)?.label ?? v}
              </Badge>
            ))}
          </span>
        )}
        <CaretDown size={14} className="shrink-0 text-muted" />
      </PopoverPrimitive.Trigger>
      <PopoverPrimitive.Portal>
        <PopoverPrimitive.Content
          align="start"
          sideOffset={6}
          className="z-50 max-h-64 w-[var(--radix-popover-trigger-width)] overflow-y-auto rounded-[12px] border border-border bg-surface p-1 shadow-pop"
        >
          {options.length === 0 && <p className="px-3 py-2 text-[13px] text-muted">无可选主机</p>}
          {options.map((o) => (
            <label
              key={o.value}
              className="flex cursor-pointer items-center gap-2.5 rounded-[8px] px-2.5 py-1.5 text-sm text-text hover:bg-surface-2"
            >
              <CheckboxPrimitive.Root
                checked={value.includes(o.value)}
                onCheckedChange={() => toggle(o.value)}
                className="flex size-4 shrink-0 items-center justify-center rounded-[4px] border border-border-strong data-[state=checked]:border-accent data-[state=checked]:bg-accent"
              >
                <CheckboxPrimitive.Indicator>
                  <Check size={11} weight="bold" className="text-accent-fg" />
                </CheckboxPrimitive.Indicator>
              </CheckboxPrimitive.Root>
              {o.label}
            </label>
          ))}
        </PopoverPrimitive.Content>
      </PopoverPrimitive.Portal>
    </PopoverPrimitive.Root>
  )
}
