import { ArrowClockwise, ArrowDown, Broadcast, FileText, WarningCircle } from '@phosphor-icons/react'
import { animate, motion, useMotionValue, useReducedMotion } from 'motion/react'
import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useJobLogs, useJobs, useKillJob, useKillJobs } from '@/api/queries'
import type { JobStatus } from '@/api/types'
import { ConfirmDialog } from '@/components/ui/alert-dialog'
import { Badge, Dot } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Code } from '@/components/ui/code'
import { DataTable, type Column } from '@/components/ui/data-table'
import { Dialog } from '@/components/ui/dialog'
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

function JobLogs({ job }: { job: JobStatus }) {
  const logs = useJobLogs(job.job.id, job.job.status === 'running')
  const content = logs.data?.pages.map((page) => page.content).join('') ?? ''
  const total = logs.data?.pages[0]?.total_bytes ?? job.log_bytes
  if (logs.isError && !content) {
    return (
      <EmptyState
        icon={<WarningCircle size={22} />}
        title="日志读取失败"
        description={(logs.error as Error).message}
      />
    )
  }
  return (
    <div>
      <div className="mb-2 flex items-center justify-between gap-3 text-[12px] text-muted">
        <span>合并日志 · {formatBytes(total)}</span>
        {job.job.exit_code != null && (
          <span className="font-mono tabular-nums">退出码 {job.job.exit_code}</span>
        )}
      </div>
      <pre className="max-h-[55dvh] min-h-40 overflow-auto rounded-[8px] bg-[#0d1117] px-3 py-2.5 font-mono text-[12px] leading-[1.6] break-words whitespace-pre-wrap text-[#d8dee9]">
        {content || (logs.isLoading ? '正在读取…' : '（暂无输出）')}
      </pre>
      {logs.hasNextPage && (
        <Button
          className="mt-2"
          size="sm"
          variant="outline"
          loading={logs.isFetchingNextPage}
          onClick={() => void logs.fetchNextPage()}
        >
          <ArrowDown size={14} />
          继续读取
        </Button>
      )}
    </div>
  )
}

export function JobsPage() {
  const jobs = useJobs()
  const killJob = useKillJob()
  const killJobs = useKillJobs()
  const navigate = useNavigate()
  const [confirmJobId, setConfirmJobId] = useState<string | null>(null)
  const [logsJobId, setLogsJobId] = useState<string | null>(null)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [batchConfirm, setBatchConfirm] = useState(false)
  const rotate = useMotionValue(0)
  const reduce = useReducedMotion()
  const fetching = useRef(false)
  const spinning = useRef(false)

  // 列表每 4 秒轮询：已退出/被终止的任务留在选中态只会误导下一次批量终止，随数据自动剔除
  useEffect(() => {
    const rows = jobs.data
    if (!rows) return
    setSelected((prev) => {
      if (prev.size === 0) return prev
      // 只剔除「响应里确实存在且已不再 running」的任务。反过来按 running 白名单过滤的话，
      // 响应里暂时缺席的 id（批量终止失败后的重试目标、竞态中的旧快照）会被静默清掉，
      // 用户就再也点不到重试了
      const settled = new Set(
        rows.filter((item) => item.job.status !== 'running').map((item) => item.job.id),
      )
      const next = new Set([...prev].filter((id) => !settled.has(id)))
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
      className: 'w-[148px] text-right',
      render: (item) => (
        <div className="flex justify-end gap-1.5">
          <Button size="sm" variant="outline" onClick={() => setLogsJobId(item.job.id)}>
            <FileText size={14} />
            日志
          </Button>
          {item.job.status === 'running' && (
            <Button size="sm" variant="danger" onClick={() => setConfirmJobId(item.job.id)}>
              终止
            </Button>
          )}
        </div>
      ),
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
                      <div className="flex gap-1.5">
                        <Button size="sm" variant="outline" onClick={() => setLogsJobId(item.job.id)}>
                          日志
                        </Button>
                        {item.job.status === 'running' && (
                          <Button size="sm" variant="danger" onClick={() => setConfirmJobId(item.job.id)}>
                            终止
                          </Button>
                        )}
                      </div>
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

      <Dialog
        open={logsJobId != null}
        onOpenChange={(open) => {
          if (!open) setLogsJobId(null)
        }}
        title="后台任务日志"
        description={logsJobId ? `job_id · ${logsJobId}` : undefined}
        size="lg"
      >
        {logsJobId && jobs.data ? (
          <JobLogs
            job={
              // 列表可能已刷新掉这条任务，用只带 id 的占位记录兜底：日志按 job_id 拉取，与它是否还在列表里无关
              jobs.data.find((item) => item.job.id === logsJobId) ?? {
                job: {
                  id: logsJobId,
                  host_id: 0,
                  command: '',
                  cwd: '',
                  status: 'exited',
                  exit_code: null,
                  started_at: 0,
                  finished_at: null,
                },
                log_bytes: 0,
              }
            }
          />
        ) : null}
      </Dialog>

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
