import { useRef, useState } from 'react'
import { Controller, useForm } from 'react-hook-form'
import { Check, Copy, Plus, Ticket, Trash, Warning, WarningCircle } from '@phosphor-icons/react'
import { AnimatePresence, motion, useReducedMotion } from 'motion/react'
import { toast } from 'sonner'
import { useCreateToken, useDeleteToken, useHosts, useTokens } from '@/api/queries'
import type { Host, Token, TokenPayload } from '@/api/types'
import { ConfirmDialog } from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { DataTable, type Column } from '@/components/ui/data-table'
import { Dialog } from '@/components/ui/dialog'
import { EmptyState } from '@/components/ui/empty-state'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { MultiSelect } from '@/components/ui/multi-select'
import { PageHeader } from '@/components/ui/page-header'
import { PageTransition } from '@/components/ui/page-transition'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { formatTime } from '@/lib/format'

type TokenFormValues = {
  name: string
  all_hosts: boolean
  host_ids: number[]
}

const defaultValues: TokenFormValues = {
  name: '',
  all_hosts: true,
  host_ids: [],
}

/**
 * 明文只在创建响应里出现一次，复制失败等于令牌作废，所以这里逐级降级：
 * clipboard API（非 https 部署时根本不存在）→ execCommand → 选中文本让用户自己按快捷键。
 */
function copyByExecCommand(text: string) {
  const ta = document.createElement('textarea')
  ta.value = text
  ta.setAttribute('readonly', '')
  ta.style.cssText = 'position:fixed;top:0;left:0;opacity:0'
  document.body.append(ta)
  ta.select()
  let ok = false
  try {
    ok = document.execCommand('copy')
  } catch {
    ok = false
  }
  ta.remove()
  return ok
}

/**
 * 「全部主机」是权限状态，用强调色 badge；具体主机名是数据不是状态，
 * 用文本枚举——十几台主机时比一串 badge 更易读，也不会跟状态色抢注意力。
 */
function TokenPermissions({ token, hosts }: { token: Token; hosts: Host[] | undefined }) {
  if (token.all_hosts) return <Badge variant="accent">全部主机</Badge>

  const names = (token.host_ids ?? []).map(
    (id) => hosts?.find((host) => host.id === id)?.name ?? `#${id}`,
  )
  if (names.length === 0) return <span className="text-[13px] text-muted">无授权主机</span>

  const text = names.join('、')
  return (
    <span className="line-clamp-2 text-[13px] text-muted" title={text}>
      {text}
    </span>
  )
}

export function TokensPage() {
  const tokens = useTokens()
  const hosts = useHosts()
  const createToken = useCreateToken()
  const deleteToken = useDeleteToken()
  const [createOpen, setCreateOpen] = useState(false)
  const [plainToken, setPlainToken] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)
  const [deleting, setDeleting] = useState<Token | null>(null)
  const plainRef = useRef<HTMLElement>(null)
  const reduce = useReducedMotion()
  const {
    control,
    handleSubmit,
    register,
    reset,
    watch,
    formState: { errors },
  } = useForm<TokenFormValues>({ defaultValues })
  const allHosts = watch('all_hosts')

  const openCreate = () => {
    reset(defaultValues)
    setCreateOpen(true)
  }

  const changeCreateOpen = (open: boolean) => {
    setCreateOpen(open)
    if (!open) reset(defaultValues)
  }

  const create = async (values: TokenFormValues) => {
    const payload: TokenPayload = {
      name: values.name,
      all_hosts: values.all_hosts,
      host_ids: values.all_hosts ? undefined : values.host_ids,
    }
    const created = await createToken.mutateAsync(payload)
    setCreateOpen(false)
    reset(defaultValues)
    setCopied(false)
    setPlainToken(created.token ?? '')
  }

  const selectPlainToken = () => {
    const el = plainRef.current
    if (!el) return
    const selection = window.getSelection()
    const range = document.createRange()
    range.selectNodeContents(el)
    selection?.removeAllRanges()
    selection?.addRange(range)
  }

  const copyPlainToken = async () => {
    if (!plainToken) return
    try {
      await navigator.clipboard.writeText(plainToken)
    } catch {
      if (!copyByExecCommand(plainToken)) {
        selectPlainToken()
        toast.error('无法访问剪贴板，已选中明文，请按 Ctrl/⌘ + C 复制')
        return
      }
    }
    setCopied(true)
    toast.success('令牌已复制')
    window.setTimeout(() => setCopied(false), 1800)
  }

  const columns: Column<Token>[] = [
    {
      key: 'name',
      title: '名称',
      className: 'w-[26%] min-w-[160px]',
      render: (token) => <span className="font-medium">{token.name}</span>,
    },
    {
      key: 'permissions',
      title: '权限',
      render: (token) => <TokenPermissions token={token} hosts={hosts.data} />,
    },
    {
      key: 'created_at',
      // 时间是第三层信息，压到 muted，避免跟名称抢视线
      title: '创建时间',
      className: 'w-[1%] whitespace-nowrap tabular-nums text-muted',
      render: (token) => formatTime(token.created_at),
    },
    {
      key: 'actions',
      title: '操作',
      className: 'w-16 text-right',
      render: (token) => (
        <Button
          variant="ghost"
          size="icon"
          aria-label={`删除令牌 ${token.name}`}
          title="删除令牌"
          onClick={() => setDeleting(token)}
        >
          <Trash size={16} />
        </Button>
      ),
    },
  ]

  const isEmpty = !tokens.isLoading && tokens.data?.length === 0

  return (
    <PageTransition>
      <PageHeader
        title="Agent 令牌"
        subtitle="按主机最小授权；明文仅展示一次"
        actions={
          <Button variant="primary" onClick={openCreate}>
            <Plus size={16} />
            创建令牌
          </Button>
        }
      />

      {tokens.isError ? (
        // 拉取失败原本是静默的（query 没有全局 error toast），只剩一具空表头，这里给出原因和重试
        <Card className="flex min-h-[320px] items-center justify-center">
          <EmptyState
            icon={<WarningCircle size={24} className="text-danger" />}
            title="令牌列表加载失败"
            description={(tokens.error as Error).message}
            action={
              <Button variant="outline" onClick={() => void tokens.refetch()}>
                重试
              </Button>
            }
          />
        </Card>
      ) : isEmpty ? (
        // 空态提到卡片层而不是塞进 DataTable 的 empty 槽：桌面表格与移动卡片列表共用同一份空态
        <Card className="flex min-h-[320px] items-center justify-center">
          <EmptyState
            icon={<Ticket size={24} />}
            title="还没有令牌"
            description="令牌是 Agent 接入网关的唯一凭据，可限定它只能访问指定主机。"
            action={
              <Button variant="primary" onClick={openCreate}>
                <Plus size={16} />
                创建令牌
              </Button>
            }
          />
        </Card>
      ) : (
        <>
          <Card className="hidden overflow-hidden md:block">
            <DataTable
              columns={columns}
              rows={tokens.data}
              rowKey={(token) => token.id}
              loading={tokens.isLoading}
            />
          </Card>

          <div className="space-y-3 md:hidden">
            {tokens.isLoading
              ? Array.from({ length: 3 }, (_, index) => <Skeleton key={index} className="h-[124px]" />)
              : tokens.data?.map((token) => (
                  <Card key={token.id} className="p-4">
                    <div className="flex items-start justify-between gap-3">
                      <p className="min-w-0 truncate font-medium text-text">{token.name}</p>
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label={`删除令牌 ${token.name}`}
                        title="删除令牌"
                        onClick={() => setDeleting(token)}
                      >
                        <Trash size={16} />
                      </Button>
                    </div>
                    {/* 移动端不复刻表格的「标签 + 值」两列：权限和时间本身自解释，去掉标签更安静 */}
                    <div className="mt-2.5">
                      <TokenPermissions token={token} hosts={hosts.data} />
                    </div>
                    <p className="mt-2.5 text-[12px] tabular-nums text-muted">
                      创建于 {formatTime(token.created_at)}
                    </p>
                  </Card>
                ))}
          </div>
        </>
      )}

      <Dialog
        open={createOpen}
        onOpenChange={changeCreateOpen}
        size="md"
        title="创建令牌"
        footer={
          <>
            <Button variant="ghost" type="button" onClick={() => changeCreateOpen(false)}>
              取消
            </Button>
            <Button
              variant="primary"
              type="submit"
              form="create-token-form"
              loading={createToken.isPending}
            >
              创建
            </Button>
          </>
        }
      >
        <form id="create-token-form" className="space-y-4" onSubmit={handleSubmit(create)}>
          {/* placeholder 已经在示范命名，再加 hint 只是同一句话说两遍 */}
          <Field label="名称" required error={errors.name?.message}>
            {(id) => (
              <Input
                id={id}
                autoFocus
                placeholder="ci-runner"
                invalid={Boolean(errors.name)}
                {...register('name', { required: '请输入名称' })}
              />
            )}
          </Field>

          {/* 开关自带语义，横排成一条设置行比「标签在上、开关在下」更紧凑也更好点 */}
          <div className="flex items-center justify-between gap-4 rounded-[8px] border border-border bg-surface-2 px-3 py-2.5">
            <div className="min-w-0">
              <Label htmlFor="token-all-hosts">允许全部主机</Label>
              <p className="mt-0.5 text-[12px] text-muted">关闭后只授权选定的主机</p>
            </div>
            <Controller
              name="all_hosts"
              control={control}
              render={({ field }) => (
                <Switch
                  id="token-all-hosts"
                  checked={field.value}
                  onCheckedChange={field.onChange}
                />
              )}
            />
          </div>

          {/* 主机选择器是条件字段，直接挂载会让弹层高度硬跳 86px；连 margin 一起动画消掉跳动 */}
          <AnimatePresence initial={false}>
            {!allHosts && (
              <motion.div
                key="host-scope"
                className="overflow-hidden"
                initial={reduce ? false : { height: 0, opacity: 0, marginTop: 0 }}
                animate={reduce ? {} : { height: 'auto', opacity: 1, marginTop: 16 }}
                exit={reduce ? {} : { height: 0, opacity: 0, marginTop: 0 }}
                transition={{ duration: 0.22, ease: [0.22, 1, 0.36, 1] }}
              >
                <Field label="允许主机" required error={errors.host_ids?.message}>
                  {(id) => (
                    <Controller
                      name="host_ids"
                      control={control}
                      rules={{
                        validate: (value) => value.length > 0 || '请至少选择一台主机',
                      }}
                      render={({ field }) => (
                        <MultiSelect
                          id={id}
                          value={field.value}
                          onChange={field.onChange}
                          invalid={Boolean(errors.host_ids)}
                          options={(hosts.data ?? []).map((host) => ({
                            value: host.id,
                            label: host.name,
                          }))}
                        />
                      )}
                    />
                  )}
                </Field>
              </motion.div>
            )}
          </AnimatePresence>
        </form>
      </Dialog>

      <Dialog
        open={plainToken !== null}
        onOpenChange={(open) => !open && setPlainToken(null)}
        size="md"
        title="令牌已创建"
        footer={
          <Button variant="outline" onClick={() => setPlainToken(null)}>
            我已保存
          </Button>
        }
      >
        <div className="space-y-4">
          <div className="flex gap-2.5 rounded-[8px] border border-warning/30 bg-warning/10 px-3 py-2.5 text-warning">
            <Warning size={16} weight="fill" className="mt-0.5 shrink-0" />
            <div className="space-y-0.5">
              <p className="text-[13px] font-medium">关闭后无法再次查看</p>
              <p className="text-[12px] text-warning/80">网关只存哈希，丢失后只能删除重建。</p>
            </div>
          </div>

          <div className="space-y-1.5">
            <p className="text-[12px] text-muted">令牌明文</p>
            <code
              ref={plainRef}
              onClick={selectPlainToken}
              className="block cursor-text rounded-[8px] bg-surface-2 px-3 py-2.5 font-mono text-[13px] leading-relaxed break-all text-text select-all"
            >
              {plainToken}
            </code>
          </div>

          {/* 这一步唯一要做的事就是复制：按钮做成弹层里最重的一块，并抢到初始焦点（回车即复制），「我已保存」退成 outline */}
          <Button
            autoFocus
            variant="primary"
            size="lg"
            className="w-full"
            onClick={() => void copyPlainToken()}
          >
            {copied ? (
              <>
                <Check size={16} weight="bold" />
                已复制到剪贴板
              </>
            ) : (
              <>
                <Copy size={16} />
                复制令牌
              </>
            )}
          </Button>
        </div>
      </Dialog>

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title="删除令牌？"
        description="使用该令牌的 Agent 将立即失去访问权限。"
        confirmText="删除"
        danger
        onConfirm={async () => {
          if (deleting) await deleteToken.mutateAsync(deleting.id)
        }}
      />
    </PageTransition>
  )
}
