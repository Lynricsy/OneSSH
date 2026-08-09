import { cn } from '@/lib/cn'
import { Skeleton } from './skeleton'

export type Column<T> = {
  key: string
  title: string
  /** 同时作用于 th 与 td，用于列宽、对齐与响应式隐藏（如 hidden md:table-cell） */
  className?: string
  render?: (row: T) => React.ReactNode
}

export function DataTable<T>({
  columns,
  rows,
  rowKey,
  loading,
  empty,
  onRowClick,
  stickyHeader,
  className,
}: {
  columns: Column<T>[]
  rows: T[] | undefined
  rowKey: (row: T) => string | number
  loading?: boolean
  empty?: React.ReactNode
  onRowClick?: (row: T) => void
  /** 表头吸顶：卡片内长表格滚动时列头不丢 */
  stickyHeader?: boolean
  className?: string
}) {
  // 空态下再顶一行列头，下面却什么都没有，看着像表格坏了——直接连表头一起收掉
  const showEmpty = !loading && rows?.length === 0 && !!empty

  return (
    <div className={cn('overflow-x-auto', className)}>
      {/* 一律 border-separate：border-collapse 下 Chrome 的 sticky th 会失效，
          分隔线因此挂在单元格而不是 tr 上——两种模式渲染结果一致，不必分叉 */}
      <table className={cn('w-full border-separate border-spacing-0 text-sm', showEmpty && 'hidden')}>
        <thead>
          <tr>
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
                className={cn(
                  'transition-colors [&:last-child>td]:border-b-0',
                  onRowClick ? 'cursor-pointer hover:bg-surface-2' : 'hover:bg-surface-2/60',
                )}
              >
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
