import * as SwitchPrimitive from '@radix-ui/react-switch'
import { cn } from '@/lib/cn'

export function Switch({
  checked,
  onCheckedChange,
  id,
  disabled,
  className,
}: {
  checked: boolean
  onCheckedChange: (checked: boolean) => void
  id?: string
  disabled?: boolean
  className?: string
}) {
  return (
    <SwitchPrimitive.Root
      id={id}
      checked={checked}
      onCheckedChange={onCheckedChange}
      disabled={disabled}
      className={cn(
        'inline-flex h-5 w-9 shrink-0 items-center rounded-full border border-transparent p-0.5 transition-colors duration-150',
        'data-[state=checked]:bg-accent data-[state=unchecked]:bg-border-strong',
        'disabled:cursor-not-allowed disabled:opacity-60',
        className,
      )}
    >
      <SwitchPrimitive.Thumb className="size-4 rounded-full bg-white shadow-sm transition-transform duration-150 data-[state=checked]:translate-x-4" />
    </SwitchPrimitive.Root>
  )
}
