import { cn } from '@/lib/cn'
import { Checkbox } from './checkbox'
import { Skeleton } from './skeleton'

export type Column<T> = {
  key: string
  title: string
  /** 同时作用于 th 与 td，用于列宽、对齐与响应式隐藏（如 hidden md:table-cell） */
  className?: string
  render?: (row: T) => React.ReactNode
}

/** 批量选择：传入即在最左渲染勾选列；isRowSelectable 可禁用个别行（如已结束的任务） */
export type TableSelection<T, K extends string | number> = {
  selected: ReadonlySet<K>
  onChange: (next: Set<K>) => void
  isRowSelectable?: (row: T) => boolean
}

export function DataTable<T, K extends string | number = string | number>({
  columns,
  rows,
  rowKey,
  loading,
  empty,
  onRowClick,
  stickyHeader,
  fixedLayout,
  className,
  selection,
}: {
  columns: Column<T>[]
  rows: T[] | undefined
  rowKey: (row: T) => K
  loading?: boolean
  empty?: React.ReactNode
  onRowClick?: (row: T) => void
  /** 表头吸顶：卡片内长表格滚动时列头不丢 */
  stickyHeader?: boolean
  /** 固定布局：列宽只由列定义的宽度决定，内容超长截断而不是挤压其他列（适合持续刷新的表格，列宽不随数据跳动） */
  fixedLayout?: boolean
  className?: string
  selection?: TableSelection<T, K>
}) {
  // 空态下再顶一行列头，下面却什么都没有，看着像表格坏了——直接连表头一起收掉
  const showEmpty = !loading && rows?.length === 0 && !!empty

  const selectableRows = rows?.filter((row) => selection?.isRowSelectable?.(row) ?? true) ?? []
  const selectedInView = selection
    ? selectableRows.filter((row) => selection.selected.has(rowKey(row))).length
    : 0
  const allChecked = selectableRows.length > 0 && selectedInView === selectableRows.length

  const toggleAll = () => {
    if (!selection) return
    const next = new Set(selection.selected)
    if (allChecked) for (const row of selectableRows) next.delete(rowKey(row))
    else for (const row of selectableRows) next.add(rowKey(row))
    selection.onChange(next)
  }

  const toggleRow = (row: T) => {
    if (!selection) return
    const key = rowKey(row)
    const next = new Set(selection.selected)
    if (next.has(key)) next.delete(key)
    else next.add(key)
    selection.onChange(next)
  }

  return (
    <div className={cn('overflow-x-auto', className)}>
      {/* 一律 border-separate：border-collapse 下 Chrome 的 sticky th 会失效，
          分隔线因此挂在单元格而不是 tr 上——两种模式渲染结果一致，不必分叉 */}
      <table
        className={cn(
          'w-full border-separate border-spacing-0 text-sm',
          fixedLayout && 'table-fixed',
          showEmpty && 'hidden',
        )}
      >
        <thead>
          <tr>
            {selection && (
              <th
                scope="col"
                className={cn(
                  'w-10 border-b border-border py-2.5 pr-0 pl-4',
                  stickyHeader && 'sticky top-0 z-10 bg-surface',
                )}
              >
                <Checkbox
                  aria-label="全选"
                  checked={allChecked ? true : selectedInView > 0 ? 'indeterminate' : false}
                  disabled={selectableRows.length === 0}
                  onCheckedChange={toggleAll}
                />
              </th>
            )}
            {columns.map((c) => (
              <th
                key={c.key}
                scope="col"
                className={cn(
                  'border-b border-border px-4 py-2.5 text-left text-[11px] font-medium tracking-wide whitespace-nowrap text-muted uppercase',
                  stickyHeader && 'sticky top-0 z-10 bg-surface',
                  c.className,
                )}
              >
                {c.title}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {loading &&
            Array.from({ length: 4 }, (_, i) => (
              <tr key={`skeleton-${i}`} className="[&:last-child>td]:border-b-0">
                {selection && (
                  <td className="border-b border-border py-3 pr-0 pl-4">
                    <Skeleton className="size-4" />
                  </td>
                )}
                {columns.map((c) => (
                  <td key={c.key} className={cn('border-b border-border px-4 py-3', c.className)}>
                    <Skeleton className="h-4 w-2/3" />
                  </td>
                ))}
              </tr>
            ))}
          {!loading &&
            rows?.map((row) => (
              <tr
                key={rowKey(row)}
                onClick={onRowClick ? () => onRowClick(row) : undefined}
                onKeyDown={
                  onRowClick
                    ? (event) => {
                        // 单元格里的按钮、勾选框自己就消费 Enter/Space，冒泡到行上会再触发一次行点击
                        if (event.target !== event.currentTarget) return
                        if (event.key === 'Enter' || event.key === ' ') {
                          event.preventDefault()
                          onRowClick(row)
                        }
                      }
                    : undefined
                }
                tabIndex={onRowClick ? 0 : undefined}
                className={cn(
                  'transition-colors [&:last-child>td]:border-b-0',
                  onRowClick
                    ? 'cursor-pointer hover:bg-surface-2 focus-visible:bg-surface-2 focus-visible:outline-none'
                    : 'hover:bg-surface-2/60',
                  selection?.selected.has(rowKey(row)) && 'bg-accent/5',
                )}
              >
                {selection && (
                  // 勾选不触发行点击（如文件页的进入目录/下载）
                  <td
                    className="border-b border-border py-3 pr-0 pl-4"
                    onClick={(event) => event.stopPropagation()}
                  >
                    <Checkbox
                      aria-label="选择本行"
                      checked={selection.selected.has(rowKey(row))}
                      disabled={!(selection.isRowSelectable?.(row) ?? true)}
                      onCheckedChange={() => toggleRow(row)}
                    />
                  </td>
                )}
                {columns.map((c) => (
                  <td
                    key={c.key}
                    className={cn('border-b border-border px-4 py-3 align-middle text-text', c.className)}
                  >
                    {c.render ? c.render(row) : String((row as Record<string, unknown>)[c.key] ?? '')}
                  </td>
                ))}
              </tr>
            ))}
        </tbody>
      </table>
      {!loading && rows && rows.length === 0 && empty}
    </div>
  )
}
