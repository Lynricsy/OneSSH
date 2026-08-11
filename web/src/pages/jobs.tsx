import { ArrowClockwise, Broadcast, WarningCircle } from '@phosphor-icons/react'
import { animate, motion, useMotionValue, useReducedMotion } from 'motion/react'
import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useJobs, useKillJob, useKillJobs } from '@/api/queries'
import type { JobStatus } from '@/api/types'
import { ConfirmDialog } from '@/components/ui/alert-dialog'
import { Badge, Dot } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Code } from '@/components/ui/code'
import { DataTable, type Column } from '@/components/ui/data-table'
import { EmptyState } from '@/components/ui/empty-state'
import { PageHeader } from '@/components/ui/page-header'
import { PageTransition } from '@/components/ui/page-transition'
import { SelectionBar } from '@/components/ui/selection-bar'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/cn'
import { formatBytes, formatTime } from '@/lib/format'

/**
 * 后端只会产出 running / exited / killed / lost 四种状态（internal/jobs/manager.go）。
 * 只有「正在跑」和「进程失联」值得上状态色：exited 拿不到 exit code，涂绿等于替它宣布成功。
 * title 保留原始英文，排障时不必翻代码。
 */
function JobBadge({ status }: { status: string }) {
  if (status === 'running')
    return (
      <Badge variant="accent" title={status}>
        <Dot pulse />
        运行中
      </Badge>
    )
  if (status === 'lost')
    return (
      <Badge variant="warning" title={status}>
        已失联
      </Badge>
    )
  const label = status === 'exited' ? '已退出' : status === 'killed' ? '已终止' : status
  return (
    <Badge variant="outline" title={status}>
      {label}
    </Badge>
  )
}

export function JobsPage() {
  const jobs = useJobs()
  const killJob = useKillJob()
  const killJobs = useKillJobs()
  const navigate = useNavigate()
  const [confirmJobId, setConfirmJobId] = useState<string | null>(null)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [batchConfirm, setBatchConfirm] = useState(false)
  const rotate = useMotionValue(0)
  const reduce = useReducedMotion()
  const fetching = useRef(false)
  const spinning = useRef(false)

  // 列表每 4 秒轮询：已退出/被终止的任务留在选中态只会误导下一次批量终止，随数据自动剔除
  useEffect(() => {
    setSelected((prev) => {
      if (prev.size === 0) return prev
      const running = new Set(
        (jobs.data ?? [])
          .filter((item) => item.job.status === 'running')
          .map((item) => item.job.id),
      )
      const next = new Set([...prev].filter((id) => running.has(id)))
      return next.size === prev.size ? prev : next
    })
  }, [jobs.data])

  /**
   * 本地网关常在几十毫秒内返回：把 isFetching 直接绑到 animate-spin 只会让图标抖一下，
   * 而 4 秒轮询还会让它无故自转。所以旋转只由点击驱动，一圈转满才停；请求还在飞就接着转下一圈，
   * 节奏完全由动画自己决定，不用定时器假造 loading。
   * 用 MotionValue + 命令式 animate 而不是 CSS 动画：每一圈都能 await，才能判断「要不要再补一圈」。
   */
  const refresh = async () => {
    if (!fetching.current) {
      fetching.current = true
      void jobs.refetch().finally(() => {
        fetching.current = false
      })
    }
    if (reduce || spinning.current) return
    spinning.current = true
    do {
      await animate(rotate, rotate.get() + 360, { duration: 0.55, ease: 'easeInOut' })
    } while (fetching.current)
    spinning.current = false
  }

  const columns: Column<JobStatus>[] = [
    {
      key: 'job',
      title: '任务',
      render: (item) => (
        <div className="min-w-0">
          <Code>{item.job.id.slice(0, 8)}</Code>
          <p
            className="mt-1 max-w-[220px] truncate text-[12px] text-muted lg:max-w-[420px]"
            title={item.job.command}
          >
            {item.job.command}
          </p>
        </div>
      ),
    },
    {
      key: 'status',
      title: '状态',
      className: 'w-[1%] whitespace-nowrap',
      render: (item) => <JobBadge status={item.job.status} />,
    },
    {
      key: 'log_bytes',
      title: '日志',
      className: 'w-[1%] whitespace-nowrap tabular-nums text-muted',
      render: (item) => formatBytes(item.log_bytes),
    },
    {
      key: 'started_at',
      title: '启动时间',
      className: 'hidden w-[1%] whitespace-nowrap tabular-nums text-muted lg:table-cell',
      render: (item) => formatTime(item.job.started_at),
    },
    {
      key: 'actions',
      title: '操作',
      className: 'w-16 text-right',
      render: (item) =>
        item.job.status === 'running' ? (
          <Button size="sm" variant="danger" onClick={() => setConfirmJobId(item.job.id)}>
            终止
          </Button>
        ) : null,
    },
  ]

  const isEmpty = !jobs.isLoading && jobs.data?.length === 0

  return (
    <PageTransition>
      <PageHeader
        title="后台任务"
        subtitle="远程无驻留进程生命周期 · 每 4 秒自动刷新"
        actions={
          <Button variant="outline" onClick={() => void refresh()}>
            <motion.span className="inline-flex" style={{ rotate }}>
              <ArrowClockwise size={15} />
            </motion.span>
            刷新
          </Button>
        }
      />

      {jobs.isError ? (
        // 拉取失败原本是静默的（query 没有全局 error toast），只剩一具空表头，这里给出原因和重试
        <Card className="flex min-h-[320px] items-center justify-center">
          <EmptyState
            icon={<WarningCircle size={24} className="text-danger" />}
            title="任务列表加载失败"
            description={(jobs.error as Error).message}
            action={
              <Button variant="outline" onClick={() => void refresh()}>
                重试
              </Button>
            }
          />
        </Card>
      ) : isEmpty ? (
        // 空态提到卡片层而不是塞进 DataTable 的 empty 槽：桌面表格与移动卡片列表共用同一份空态，
        // 顺便让空卡片有 320px 的存在感，居中呈现
        <Card className="flex min-h-[320px] items-center justify-center">
          <EmptyState
            icon={<Broadcast size={24} />}
            title="暂无后台任务"
            description="Agent 用 job_start 启动的远程进程会出现在这里。"
            action={
              <Button variant="outline" onClick={() => navigate('/activity')}>
                查看活动记录
              </Button>
            }
          />
        </Card>
      ) : (
        <>
          <Card className="hidden overflow-hidden md:block">
            <DataTable
              columns={columns}
              rows={jobs.data}
              rowKey={(item) => item.job.id}
              loading={jobs.isLoading}
              selection={{
                selected,
                onChange: setSelected,
                // 只有运行中的任务能终止，其余行禁用勾选
                isRowSelectable: (item) => item.job.status === 'running',
              }}
            />
          </Card>

          <div className="space-y-3 md:hidden">
            {jobs.isLoading
              ? Array.from({ length: 3 }, (_, index) => <Skeleton key={index} className="h-[116px]" />)
              : jobs.data?.map((item) => (
                  <Card
                    key={item.job.id}
                    className={cn('p-4', selected.has(item.job.id) && 'border-accent')}
                  >
                    <div className="flex items-center justify-between gap-3">
                      <div className="flex min-w-0 items-center gap-2.5">
                        <Checkbox
                          checked={selected.has(item.job.id)}
                          disabled={item.job.status !== 'running'}
                          onCheckedChange={() =>
                            setSelected((prev) => {
                              const next = new Set(prev)
                              if (next.has(item.job.id)) next.delete(item.job.id)
                              else next.add(item.job.id)
                              return next
                            })
                          }
                          aria-label={`选择任务 ${item.job.id.slice(0, 8)}`}
                        />
                        <Code>{item.job.id.slice(0, 8)}</Code>
                      </div>
                      <JobBadge status={item.job.status} />
                    </div>
                    <p className="mt-2 line-clamp-2 font-mono text-[12px] break-all text-muted">
                      {item.job.command}
                    </p>
                    <div className="mt-3 flex items-center justify-between gap-3 text-[12px] tabular-nums text-muted">
                      <span>
                        日志 {formatBytes(item.log_bytes)} · {formatTime(item.job.started_at)}
                      </span>
                      {item.job.status === 'running' && (
                        <Button size="sm" variant="danger" onClick={() => setConfirmJobId(item.job.id)}>
                          终止
                        </Button>
                      )}
                    </div>
                  </Card>
                ))}
          </div>
        </>
      )}

      <SelectionBar count={selected.size} onClear={() => setSelected(new Set())}>
        <Button variant="danger" size="sm" onClick={() => setBatchConfirm(true)}>
          终止
        </Button>
      </SelectionBar>

      <ConfirmDialog
        open={confirmJobId !== null}
        onOpenChange={(open) => {
          if (!open) setConfirmJobId(null)
        }}
        danger
        title="终止任务？"
        description="远程进程会收到终止信号，未落盘的输出可能丢失。"
        confirmText="终止"
        onConfirm={async () => {
          if (confirmJobId !== null) await killJob.mutateAsync(confirmJobId)
        }}
      />

      <ConfirmDialog
        open={batchConfirm}
        onOpenChange={setBatchConfirm}
        danger
        title={`批量终止 ${selected.size} 个任务？`}
        description="这些远程进程会收到终止信号，未落盘的输出可能丢失。"
        confirmText="终止"
        onConfirm={async () => {
          // 失败的留在选中态，方便对照着重试
          const { failedIds } = await killJobs.mutateAsync([...selected])
          setSelected(new Set(failedIds))
        }}
      />
    </PageTransition>
  )
}
