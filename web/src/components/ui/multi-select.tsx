import * as CheckboxPrimitive from '@radix-ui/react-checkbox'
import * as PopoverPrimitive from '@radix-ui/react-popover'
import { CaretDown, Check, MagnifyingGlass } from '@phosphor-icons/react'
import { useState } from 'react'
import { cn } from '@/lib/cn'
import { Badge } from './badge'

export type MultiSelectOption<T extends string | number> = { value: T; label: string }

/** 触发器里最多平铺几个 chip，其余折成「+N」——多选不该把筛选栏撑高 */
const MAX_VISIBLE_CHIPS = 2

/** 搜索词命中片段高亮，与审计页工具名的等宽语境一致，由调用方决定是否套 mono */
function Highlight({ text, query }: { text: string; query: string }) {
  const i = text.toLowerCase().indexOf(query.toLowerCase())
  if (!query || i < 0) return <>{text}</>
  return (
    <>
      {text.slice(0, i)}
      <mark className="rounded-[2px] bg-accent-soft px-px text-accent">
        {text.slice(i, i + query.length)}
      </mark>
      {text.slice(i + query.length)}
    </>
  )
}

/**
 * 多选 + 搜索的受控封装，外观对齐 Select。
 * 值类型与 Select 一样开放 string | number（主机 id 是 number、工具名是 string）。
 * 优化点：触发器固定高度（超出折成 +N）、已选置顶、回车直接勾选首个匹配项、一键清空。
 */
export function MultiSelect<T extends string | number>({
  value,
  onChange,
  options,
  placeholder = '选择主机',
  searchPlaceholder = '搜索…',
  emptyText = '无可选项',
  invalid,
  id,
  maxSelected,
}: {
  value: T[]
  onChange: (value: T[]) => void
  options: MultiSelectOption<T>[]
  placeholder?: string
  searchPlaceholder?: string
  emptyText?: string
  invalid?: boolean
  maxSelected?: number
  id?: string
}) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')

  const toggle = (v: T) => {
    if (!value.includes(v) && maxSelected != null && value.length >= maxSelected) return
    onChange(value.includes(v) ? value.filter((x) => x !== v) : [...value, v])
  }
  const labelOf = (v: T) => options.find((o) => o.value === v)?.label ?? String(v)

  const q = query.trim()
  // 已选置顶：长列表里勾选后不用滚回去确认，再次打开也能一眼看到当前选择
  const visible = options
    .filter((o) => !q || o.label.toLowerCase().includes(q.toLowerCase()))
    .sort((a, b) => Number(value.includes(b.value)) - Number(value.includes(a.value)))

  const shown = value.slice(0, MAX_VISIBLE_CHIPS)
  const rest = value.length - shown.length

  return (
    <PopoverPrimitive.Root
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
        if (!next) setQuery('')
      }}
    >
      <PopoverPrimitive.Trigger
        id={id}
        aria-invalid={invalid || undefined}
        // chip 被截断时靠 title 补全，触发器里不再塞第二层控件
        title={value.length > 0 ? value.map(labelOf).join('、') : undefined}
        onKeyDown={(e) => {
          // 关闭态下 Backspace/Delete 摘掉最后一项：chip 上的伪按钮（span[role=button]）
          // 是嵌在触发器里的嵌套控件，键盘根本够不着，改由触发器自身承担快捷移除，
          // 完整的增删仍在弹层里的 checkbox 与「清空」上
          if (!open && (e.key === 'Backspace' || e.key === 'Delete') && value.length > 0) {
            e.preventDefault()
            onChange(value.slice(0, -1))
          }
        }}
        className={cn(
          'flex h-9 w-full items-center justify-between gap-2 overflow-hidden rounded-[8px] border bg-surface px-3 text-sm',
          'transition-colors duration-150 hover:border-border-strong',
          'focus:border-accent focus:ring-2 focus:ring-accent/25 focus:outline-none',
          invalid ? 'border-danger' : 'border-border',
        )}
      >
        {value.length === 0 ? (
          <span className="min-w-0 flex-1 truncate text-muted">{placeholder}</span>
        ) : (
          // flex-1 + overflow-hidden 把 chips 约束在 caret 左侧；chip 允许收缩截断，
          // 否则两个长名字会越过 caret 被 overflow 裁掉（看起来像被下拉图标遮住）
          <span className="flex min-w-0 flex-1 items-center gap-1 overflow-hidden">
            {shown.map((v) => (
              <Badge key={String(v)} variant="accent" className="min-w-0 max-w-36">
                <span className="truncate">{labelOf(v)}</span>
              </Badge>
            ))}
            {rest > 0 && (
              <Badge variant="default" className="shrink-0">
                +{rest}
              </Badge>
            )}
          </span>
        )}
        <CaretDown size={14} className="shrink-0 text-muted" />
      </PopoverPrimitive.Trigger>
      <PopoverPrimitive.Portal>
        <PopoverPrimitive.Content
          align="start"
          sideOffset={6}
          className="z-50 flex max-h-72 w-[var(--radix-popover-trigger-width)] min-w-56 flex-col overflow-hidden rounded-[12px] border border-border bg-surface shadow-pop"
        >
          <div className="flex items-center gap-2 border-b border-border px-3 py-2">
            <MagnifyingGlass size={14} className="shrink-0 text-faint" />
            <input
              autoFocus
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => {
                // 有搜索词时回车直接勾选首个匹配项，全程不需要碰鼠标
                if (e.key === 'Enter' && q && visible.length > 0) {
                  e.preventDefault()
                  toggle(visible[0].value)
                }
              }}
              placeholder={searchPlaceholder}
              className="w-full bg-transparent text-[13px] text-text outline-none placeholder:text-faint"
            />
          </div>
          <div className="flex-1 overflow-y-auto p-1">
            {visible.length === 0 && (
              <p className="px-3 py-2 text-[13px] text-muted">
                {q ? `没有匹配「${q}」的选项` : emptyText}
              </p>
            )}
            {visible.map((o) => {
              const disabled =
                !value.includes(o.value) && maxSelected != null && value.length >= maxSelected
              return (
                <label
                  key={String(o.value)}
                  className={cn(
                    'flex items-center gap-2.5 rounded-[8px] px-2.5 py-1.5 text-sm text-text',
                    disabled
                      ? 'cursor-not-allowed opacity-50'
                      : 'cursor-pointer hover:bg-surface-2',
                  )}
                >
                  <CheckboxPrimitive.Root
                    checked={value.includes(o.value)}
                    disabled={disabled}
                    onCheckedChange={() => toggle(o.value)}
                    className="flex size-4 shrink-0 items-center justify-center rounded-[4px] border border-border-strong data-[state=checked]:border-accent data-[state=checked]:bg-accent"
                  >
                    <CheckboxPrimitive.Indicator>
                      <Check size={11} weight="bold" className="text-accent-fg" />
                    </CheckboxPrimitive.Indicator>
                  </CheckboxPrimitive.Root>
                  <span className="truncate">
                    <Highlight text={o.label} query={q} />
                  </span>
                </label>
              )
            })}
          </div>
          {value.length > 0 && (
            <div className="flex items-center justify-between border-t border-border px-3 py-1.5">
              <span className="text-[12px] text-muted tabular-nums">
                已选 {value.length} 项{maxSelected != null && ` · 最多 ${maxSelected} 项`}
              </span>
              <button
                type="button"
                onClick={() => onChange([])}
                className="cursor-pointer text-[12px] text-muted transition-colors hover:text-danger"
              >
                清空
              </button>
            </div>
          )}
        </PopoverPrimitive.Content>
      </PopoverPrimitive.Portal>
    </PopoverPrimitive.Root>
  )
}
