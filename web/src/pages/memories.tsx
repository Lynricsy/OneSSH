import { Brain, Database, MagnifyingGlass, Sparkle, Trash, WarningCircle } from '@phosphor-icons/react'
import { useEffect, useId, useMemo, useState } from 'react'
import { useDeleteMemory, useHosts, useMemories, useMemoryStats } from '@/api/queries'
import type { MemoryRow } from '@/api/types'
import { ConfirmDialog } from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { DataTable, type Column } from '@/components/ui/data-table'
import { EmptyState } from '@/components/ui/empty-state'
import { Input } from '@/components/ui/input'
import { PageHeader } from '@/components/ui/page-header'
import { PageTransition } from '@/components/ui/page-transition'
import { Select } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { StatCard, StatGroup } from '@/components/ui/stat-card'
import { cn } from '@/lib/cn'
import { formatTime } from '@/lib/format'

const PAGE_SIZE = 50
/** 筛选值全部是字符串：Radix Select 只接受 string，'all' 与主机 id 混在一个下拉里 */
const ALL_BANKS = 'all'
/** 后端约定 host_id=0 表示全局库 */
const GLOBAL_BANK = '0'

/** 后端 CHECK 约束的四个取值；未知值原样透出，不静默吞掉 */
const VERACITY_LABEL: Record<string, string> = {
  stated: '断言',
  inferred: '推断',
  tool: '工具',
  unknown: '未知',
}

/** 记忆库是这一页唯一需要一眼分辨的维度，因此全页只给它上色：全局用强调色，主机库用描边 */
function BankBadge({ memory }: { memory: MemoryRow }) {
  if (memory.host_id == null) return <Badge variant="accent">全局</Badge>
  return (
    <Badge variant="outline" title={`主机 #${memory.host_id}`}>
      {memory.host_name ?? `#${memory.host_id}`}
    </Badge>
  )
}

/** 重要度是 0–1 连续值：数字给精度，色条给跨行可比的量级，二者缺一都要靠脑补 */
function Importance({ value }: { value: number }) {
  const ratio = Math.min(Math.max(value, 0), 1)
  return (
    <span className="inline-flex items-center gap-2" title={`重要度 ${value.toFixed(2)}`}>
      <span className="h-1 w-10 shrink-0 overflow-hidden rounded-full bg-surface-2">
        <span className="block h-full rounded-full bg-accent" style={{ width: `${ratio * 100}%` }} />
      </span>
      <span className="text-[12px] text-muted tabular-nums">{value.toFixed(2)}</span>
    </span>
  )
}

export function MemoriesPage() {
  const [bank, setBank] = useState<string>(ALL_BANKS)
  const [search, setSearch] = useState('')
  const [query, setQuery] = useState('')
  const [offset, setOffset] = useState(0)
  const [deleting, setDeleting] = useState<MemoryRow | null>(null)
  const bankSelectId = useId()

  useEffect(() => {
    // 搜索走后端 LIKE：逐字符发请求既压网关也让列表反复重排，敲停 300ms 再查
    const timer = window.setTimeout(() => {
      setQuery(search.trim())
      setOffset(0)
    }, 300)
    return () => window.clearTimeout(timer)
  }, [search])

  const hosts = useHosts()
  const stats = useMemoryStats()
  const hostId = bank === ALL_BANKS ? undefined : Number(bank)
  const memories = useMemories({ hostId, q: query, limit: PAGE_SIZE, offset })
  const deleteMemory = useDeleteMemory()

  // 统计按 host_id 索引，全局库归到 0——与筛选值的编码保持同一套
  const statByBank = useMemo(
    () => new Map((stats.data ?? []).map((item) => [item.host_id ?? 0, item] as const)),
    [stats.data],
  )

  const bankOptions = useMemo(() => {
    const withCount = (label: string, id: number) => {
      const count = statByBank.get(id)?.count
      return count ? `${label} · ${count}` : label
    }
    return [
      { value: ALL_BANKS, label: '全部记忆库' },
      { value: GLOBAL_BANK, label: withCount('全局', 0) },
      ...(hosts.data ?? []).map((host) => ({
        value: String(host.id),
        label: withCount(host.name, host.id),
      })),
    ]
  }, [hosts.data, statByBank])

  const totals = useMemo(
    () =>
      (stats.data ?? []).reduce(
        (acc, item) => ({ count: acc.count + item.count, embedded: acc.embedded + item.embedded }),
        { count: 0, embedded: 0 },
      ),
    [stats.data],
  )

  const rows = memories.data
  const selectedStat = hostId == null ? undefined : statByBank.get(hostId)
  const narrowed = bank !== ALL_BANKS || query !== '' || offset > 0
  const isEmpty = !memories.isLoading && rows?.length === 0
  const hasNext = (rows?.length ?? 0) === PAGE_SIZE

  const resetFilters = () => {
    setBank(ALL_BANKS)
    setSearch('')
    setQuery('')
    setOffset(0)
  }

  const columns: Column<MemoryRow>[] = [
    {
      key: 'content',
      title: '记忆',
      render: (memory) => (
        <p
          className="line-clamp-2 max-w-[380px] text-[13px] leading-relaxed text-text xl:max-w-[620px]"
          title={memory.content}
        >
          {memory.content}
        </p>
      ),
    },
    {
      key: 'bank',
      title: '记忆库',
      className: 'w-[1%] whitespace-nowrap',
      render: (memory) => <BankBadge memory={memory} />,
    },
    {
      key: 'source',
      title: '来源',
      className: 'hidden w-[1%] whitespace-nowrap lg:table-cell',
      render: (memory) => <span className="font-mono text-[12px] text-muted">{memory.source}</span>,
    },
    {
      key: 'importance',
      title: '重要度',
      className: 'w-[1%] whitespace-nowrap',
      render: (memory) => <Importance value={memory.importance} />,
    },
    {
      key: 'veracity',
      title: '可信度',
      className: 'hidden w-[1%] whitespace-nowrap xl:table-cell',
      // title 保留后端原值，排障时不必回头翻映射表
      render: (memory) => (
        <span className="text-[12px] text-muted" title={memory.veracity}>
          {VERACITY_LABEL[memory.veracity] ?? memory.veracity}
        </span>
      ),
    },
    {
      key: 'recall_count',
      title: '召回',
      className: 'hidden w-[1%] whitespace-nowrap text-muted tabular-nums lg:table-cell',
      render: (memory) => memory.recall_count,
    },
    {
      key: 'created_at',
      title: '写入时间',
      className: 'hidden w-[1%] whitespace-nowrap text-muted tabular-nums lg:table-cell',
      render: (memory) => (
        <>
          <span>{formatTime(memory.created_at)}</span>
          {memory.updated_at > memory.created_at && (
            <span className="block text-[11px] text-faint">更新 {formatTime(memory.updated_at)}</span>
          )}
        </>
      ),
    },
    {
      key: 'actions',
      title: '操作',
      className: 'w-16 text-right',
      render: (memory) => (
        <Button
          variant="ghost"
          size="icon"
          aria-label={`删除记忆 #${memory.id}`}
          title="删除记忆"
          onClick={() => setDeleting(memory)}
        >
          <Trash size={16} />
        </Button>
      ),
    },
  ]

  return (
    <PageTransition>
      <PageHeader
        title="记忆"
        subtitle="Agent 跨会话记住的运维事实 · 每台主机一个记忆库，另有一个全局库"
      />

      {stats.isError ? (
        // 统计失败不该连累列表：缩成一条可重试的提示，页面其余部分照常可用
        <Card className="flex flex-wrap items-center justify-between gap-3 px-5 py-4">
          <p className="text-[13px] text-muted">
            记忆库统计加载失败：{(stats.error as Error).message}
          </p>
          <Button variant="outline" size="sm" onClick={() => void stats.refetch()}>
            重试
          </Button>
        </Card>
      ) : stats.isLoading ? (
        <Skeleton className="h-[92px] rounded-[12px] md:h-[104px]" />
      ) : (
        <StatGroup>
          <StatCard title="记忆总数" value={totals.count} suffix="条" icon={<Brain size={16} />} />
          <StatCard
            title="记忆库"
            value={stats.data?.length ?? 0}
            suffix="个"
            icon={<Database size={16} />}
          />
          <StatCard
            title="已向量化"
            value={totals.embedded}
            suffix="条"
            icon={<Sparkle size={16} />}
          />
        </StatGroup>
      )}

      <Card className="mt-4 flex flex-wrap items-center gap-2 p-2.5">
        <label htmlFor={bankSelectId} className="sr-only">
          记忆库
        </label>
        <Select
          id={bankSelectId}
          className="w-full sm:w-56"
          value={bank}
          onChange={(next) => {
            setBank(next)
            setOffset(0)
          }}
          options={bankOptions}
        />

        <div className="min-w-0 flex-1 sm:min-w-[200px]">
          <Input
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="搜索记忆正文"
            aria-label="搜索记忆正文"
            spellCheck={false}
            prefix={<MagnifyingGlass size={14} />}
          />
        </div>

        {/* 选中单个库时，把该库的统计摊在筛选条右侧：省得为一行数字再起一块区域 */}
        {selectedStat && (
          <p className="px-1 text-[12px] text-muted tabular-nums">
            本库 {selectedStat.count} 条
            {selectedStat.last_written != null && ` · 最近写入 ${formatTime(selectedStat.last_written)}`}
          </p>
        )}
      </Card>

      <div className="mt-4">
        {memories.isError ? (
          <Card className="flex min-h-[320px] items-center justify-center">
            <EmptyState
              icon={<WarningCircle size={24} className="text-danger" />}
              title="记忆列表加载失败"
              description={(memories.error as Error).message}
              action={
                <Button variant="outline" onClick={() => void memories.refetch()}>
                  重试
                </Button>
              }
            />
          </Card>
        ) : isEmpty ? (
          <Card className="flex min-h-[320px] items-center justify-center">
            {narrowed ? (
              <EmptyState
                icon={<MagnifyingGlass size={24} />}
                title="没有匹配的记忆"
                description="当前记忆库与关键词下没有结果，换个库或换个词试试。"
                action={
                  <Button variant="outline" onClick={resetFilters}>
                    清除筛选
                  </Button>
                }
              />
            ) : (
              <EmptyState
                icon={<Brain size={24} />}
                title="还没有记忆"
                description="Agent 调用 memory_remember 写下的运维事实会出现在这里，之后可被 memory_recall 召回。"
              />
            )}
          </Card>
        ) : (
          <div
            className={cn(
              'transition-opacity duration-150',
              // 去抖后的新一页在飞：旧数据留在原位淡一档，比整块骨架屏更不打断阅读
              memories.isPlaceholderData && 'opacity-60',
            )}
          >
            <Card className="hidden overflow-hidden md:block">
              <DataTable
                columns={columns}
                rows={rows}
                rowKey={(memory) => memory.id}
                loading={memories.isLoading}
              />
            </Card>

            <div className="space-y-3 md:hidden">
              {memories.isLoading
                ? Array.from({ length: 3 }, (_, index) => (
                    <Skeleton key={index} className="h-[132px]" />
                  ))
                : rows?.map((memory) => (
                    <Card key={memory.id} className="p-4">
                      <div className="flex items-start justify-between gap-3">
                        <p className="min-w-0 flex-1 text-[13px] leading-relaxed text-text">
                          {memory.content}
                        </p>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="-mt-1 shrink-0"
                          aria-label={`删除记忆 #${memory.id}`}
                          title="删除记忆"
                          onClick={() => setDeleting(memory)}
                        >
                          <Trash size={16} />
                        </Button>
                      </div>
                      <div className="mt-3 flex flex-wrap items-center gap-2">
                        <BankBadge memory={memory} />
                        <span className="font-mono text-[12px] text-muted">{memory.source}</span>
                        <span className="text-[12px] text-muted" title={memory.veracity}>
                          {VERACITY_LABEL[memory.veracity] ?? memory.veracity}
                        </span>
                        <Importance value={memory.importance} />
                      </div>
                      <p className="mt-2.5 text-[12px] text-muted tabular-nums">
                        召回 {memory.recall_count} 次 · {formatTime(memory.created_at)}
                        {memory.updated_at > memory.created_at &&
                          ` · 更新 ${formatTime(memory.updated_at)}`}
                      </p>
                    </Card>
                  ))}
            </div>

            {(offset > 0 || hasNext) && (
              <div className="mt-4 flex flex-wrap items-center justify-between gap-3">
                <p className="text-[12px] text-muted tabular-nums">
                  第 {offset + 1}–{offset + (rows?.length ?? 0)} 条
                </p>
                <div className="flex gap-2">
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={offset === 0}
                    onClick={() => setOffset((prev) => Math.max(0, prev - PAGE_SIZE))}
                  >
                    上一页
                  </Button>
                  {/* 后端不返回总数，只能用「本页取满」判断还有下一页 */}
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={!hasNext}
                    onClick={() => setOffset((prev) => prev + PAGE_SIZE)}
                  >
                    下一页
                  </Button>
                </div>
              </div>
            )}
          </div>
        )}
      </div>

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => {
          if (!open) setDeleting(null)
        }}
        danger
        title="删除这条记忆？"
        description={
          deleting
            ? `删除后 Agent 将无法再召回它：${deleting.content.slice(0, 60)}${deleting.content.length > 60 ? '…' : ''}`
            : undefined
        }
        confirmText="删除"
        onConfirm={async () => {
          if (!deleting) return
          // 失败提示由 mutation 的统一 onError 弹出，这里吞掉异常以免弹层卡在 pending
          await deleteMemory.mutateAsync(deleting.id).catch(() => undefined)
        }}
      />
    </PageTransition>
  )
}
