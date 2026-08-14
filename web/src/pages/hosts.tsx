import { DotsThree, HardDrives, MagnifyingGlass, Plus } from '@phosphor-icons/react'
import { Controller, useForm } from 'react-hook-form'
import { useId, useMemo, useState } from 'react'
import { toast } from 'sonner'
import {
  useDeleteHost,
  useDeleteHosts,
  useHosts,
  useKeys,
  useResetFingerprint,
  useSaveHost,
  useTestHost,
} from '@/api/queries'
import type { Host, HostPayload } from '@/api/types'
import { HostConnection } from '@/components/host-connection'
import { ConfirmDialog } from '@/components/ui/alert-dialog'
import { Badge, Dot } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Code } from '@/components/ui/code'
import { DataTable, type Column } from '@/components/ui/data-table'
import { Dialog } from '@/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { EmptyState } from '@/components/ui/empty-state'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { MultiSelect } from '@/components/ui/multi-select'
import { PageHeader } from '@/components/ui/page-header'
import { PageTransition } from '@/components/ui/page-transition'
import { Select } from '@/components/ui/select'
import { SelectionBar } from '@/components/ui/selection-bar'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { cn } from '@/lib/cn'

type ConfirmState = { kind: 'reset' | 'delete'; host: Host } | { kind: 'batch-delete' } | null

const emptyForm: HostPayload = {
  name: '',
  username: '',
  addr: '',
  port: 22,
  auth_type: 'password',
  password: undefined,
  key_id: undefined,
  jump_host: undefined,
  monitor_enabled: true,
  tags: [],
}

/**
 * TOFU 未完成是全新主机的正常初始状态，不是故障。
 * 用一个警告色小圆点点到为止，避免整列黄色 badge 把视线从主机名上抢走。
 */
const FingerprintCell = ({ host }: { host: Host }) =>
  host.hostkey_fp ? (
    <Code className="max-w-full" copyable title={host.hostkey_fp}>
      {host.hostkey_fp}
    </Code>
  ) : (
    <span className="inline-flex items-center gap-2 text-[12px] text-muted">
      <Dot className="bg-warning" />
      待首次信任
    </span>
  )

/** 监控开关是状态而非属性，与指纹共用「圆点 + 文字」这一套状态语汇 */
const MonitorCell = ({ host }: { host: Host }) => (
  <span className="inline-flex items-center gap-2 text-[12px] text-muted">
    <Dot className={host.monitor_enabled ? 'bg-accent' : 'bg-border-strong'} />
    {host.monitor_enabled ? '启用' : '关闭'}
  </span>
)

/** 标签是分组属性而非状态：平铺徽章、允许换行，没有标签时用破折号留白 */
const TagsCell = ({ host }: { host: Host }) =>
  host.tags.length > 0 ? (
    <span className="flex flex-wrap items-center gap-1">
      {host.tags.map((tag) => (
        <Badge key={tag} variant="accent">
          {tag}
        </Badge>
      ))}
    </span>
  ) : (
    <span className="text-muted">—</span>
  )


export function HostsPage() {
  const hosts = useHosts()
  const keys = useKeys()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<Host>()
  const [testingId, setTestingId] = useState<number>()
  const [confirm, setConfirm] = useState<ConfirmState>(null)
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const monitorId = useId()
  const save = useSaveHost(editing?.id)
  const deleteHost = useDeleteHost()
  const deleteHosts = useDeleteHosts()
  const resetFingerprint = useResetFingerprint()
  const testHost = useTestHost()
  const {
    register,
    control,
    watch,
    reset,
    handleSubmit,
    formState: { errors },
  } = useForm<HostPayload>({ defaultValues: emptyForm, shouldUnregister: true })
  const authType = watch('auth_type')

  // 列表一次拉全量且无服务端分页，过滤全部在前端本地做，改动即生效、不发请求
  const [query, setQuery] = useState('')
  const [filterTags, setFilterTags] = useState<string[]>([])
  const [filterAuth, setFilterAuth] = useState<'all' | 'password' | 'key'>('all')
  const [filterMonitor, setFilterMonitor] = useState<'all' | 'on' | 'off'>('all')
  const tagFilterId = useId()
  const authFilterId = useId()
  const monitorFilterId = useId()

  /** 全部主机已有标签的去重集合，同时喂给过滤条与表单的可选项 */
  const allTags = useMemo(
    () => [...new Set((hosts.data ?? []).flatMap((host) => host.tags))].sort(),
    [hosts.data],
  )

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    return (hosts.data ?? []).filter((host) => {
      if (
        q &&
        ![host.name, host.addr, host.username].some((text) => text.toLowerCase().includes(q))
      )
        return false
      // 标签取「任一交集即命中」：多选是扩面而不是收窄
      if (filterTags.length > 0 && !host.tags.some((tag) => filterTags.includes(tag))) return false
      if (filterAuth !== 'all' && host.auth_type !== filterAuth) return false
      if (filterMonitor !== 'all' && host.monitor_enabled !== (filterMonitor === 'on')) return false
      return true
    })
  }, [hosts.data, query, filterTags, filterAuth, filterMonitor])

  /** 列表本身非空、只是被过滤条件清空时，换一套空态文案引导清除条件 */
  const narrowedToNothing = (hosts.data?.length ?? 0) > 0 && filtered.length === 0

  const openCreate = () => {
    setEditing(undefined)
    reset(emptyForm)
    setDialogOpen(true)
  }

  const openEdit = (host: Host) => {
    setEditing(host)
    // 私密凭据不能由列表响应恢复；显式清空可避免误把旧值再次提交。
    // 跳板在视图里是 id、在载荷里是 name，回显时换算一次。
    reset({
      ...host,
      password: undefined,
      jump_host: hosts.data?.find((item) => item.id === host.jump_host_id)?.name,
    })
    setDialogOpen(true)
  }

  const submit = handleSubmit(async (values) => {
    // 空字符串是「直连」选项的值，归一成 undefined 才符合后端「省略即清除」的整体替换语义。
    const base = { ...values, jump_host: values.jump_host || undefined }
    const payload: HostPayload =
      values.auth_type === 'key'
        ? { ...base, password: undefined }
        : { ...base, key_id: undefined, password: values.password || undefined }
    await save.mutateAsync(payload)
    setDialogOpen(false)
  })

  const test = async (host: Host) => {
    setTestingId(host.id)
    const request = testHost.mutateAsync(host.id)
    toast.promise(request, {
      loading: `正在连接 ${host.name}`,
      success: (result) => result.output || '连接成功',
      error: (error) => (error as Error).message,
    })
    try {
      await request
    } catch {
      // 错误已由 toast.promise 呈现，这里只负责收敛 loading 状态。
    } finally {
      setTestingId(undefined)
    }
  }

  const hostMenu = (host: Host) => (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" aria-label={`${host.name} 操作`}>
          <DotsThree size={18} weight="bold" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onSelect={() => openEdit(host)}>编辑</DropdownMenuItem>
        <DropdownMenuItem onSelect={() => setConfirm({ kind: 'reset', host })}>
          重置指纹
        </DropdownMenuItem>
        <DropdownMenuItem danger onSelect={() => setConfirm({ kind: 'delete', host })}>
          删除
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )

  // 显式列宽：属性列定宽、余量全部给主机名列，避免最后一列前出现上百像素空档。
  const columns: Column<Host>[] = [
    {
      key: 'name',
      title: '主机',
      className: 'min-w-[220px]',
      render: (host) => (
        <div className="min-w-0">
          <div className="truncate leading-5 font-medium">{host.name}</div>
          <HostConnection host={host} />
        </div>
      ),
    },
    {
      key: 'auth_type',
      title: '认证',
      className: 'w-[112px]',
      render: (host) => (
        <span className="text-[13px] text-muted">
          {host.auth_type === 'key' ? 'SSH 密钥' : '密码'}
        </span>
      ),
    },
    {
      key: 'tags',
      title: '标签',
      className: 'w-[168px]',
      render: (host) => <TagsCell host={host} />,
    },
    {
      key: 'hostkey_fp',
      title: '指纹',
      className: 'w-[320px]',
      render: (host) => <FingerprintCell host={host} />,
    },
    {
      key: 'monitor_enabled',
      title: '监控',
      className: 'w-[104px]',
      render: (host) => <MonitorCell host={host} />,
    },
    {
      key: 'actions',
      title: '操作',
      className: 'w-[136px] text-right',
      render: (host) => (
        <div className="flex items-center justify-end gap-1">
          <Button
            size="sm"
            variant="outline"
            // 定宽：spinner 出现时按钮不再变宽，避免整行操作区在测试过程中抖动
            className="min-w-[76px]"
            loading={testingId === host.id}
            onClick={() => void test(host)}
          >
            测试
          </Button>
          {hostMenu(host)}
        </div>
      ),
    },
  ]

  const emptyState = (
    <EmptyState
      icon={<HardDrives size={22} />}
      title="还没有 SSH 主机"
      description="添加主机后即可使用终端、文件与指标。"
      action={
        <Button variant="primary" onClick={openCreate}>
          <Plus size={15} />
          添加主机
        </Button>
      }
    />
  )

  const noMatchState = (
    <EmptyState
      icon={<MagnifyingGlass size={22} />}
      title="没有匹配过滤条件的主机"
      description="换个关键词，或放宽标签、认证与监控筛选。"
      action={
        <Button
          variant="outline"
          onClick={() => {
            setQuery('')
            setFilterTags([])
            setFilterAuth('all')
            setFilterMonitor('all')
          }}
        >
          清除筛选
        </Button>
      }
    />
  )

  return (
    <PageTransition>
      <PageHeader
        title="主机"
        subtitle="SSH 目标、认证方式与 TOFU 指纹"
        actions={
          <Button variant="primary" onClick={openCreate}>
            <Plus size={15} />
            添加主机
          </Button>
        }
      />

      {/* 一份过滤条同时驱动桌面表格与移动端卡片；窄屏下各项整行堆叠 */}
      <Card className="mb-4 flex flex-wrap items-center gap-2 p-2.5">
        <div className="min-w-0 flex-1 sm:min-w-[200px]">
          <Input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="搜索名称、地址或用户名"
            aria-label="搜索名称、地址或用户名"
            spellCheck={false}
            prefix={<MagnifyingGlass size={14} />}
          />
        </div>
        <label htmlFor={tagFilterId} className="sr-only">
          按标签筛选
        </label>
        <div className="w-full sm:w-44">
          <MultiSelect
            id={tagFilterId}
            value={filterTags}
            onChange={setFilterTags}
            placeholder="全部标签"
            searchPlaceholder="搜索标签…"
            options={allTags.map((tag) => ({ value: tag, label: tag }))}
          />
        </div>
        <label htmlFor={authFilterId} className="sr-only">
          按认证方式筛选
        </label>
        <Select
          id={authFilterId}
          className="w-full sm:w-36"
          value={filterAuth}
          onChange={setFilterAuth}
          options={[
            { value: 'all', label: '全部认证' },
            { value: 'password', label: '密码' },
            { value: 'key', label: 'SSH 密钥' },
          ]}
        />
        <label htmlFor={monitorFilterId} className="sr-only">
          按监控状态筛选
        </label>
        <Select
          id={monitorFilterId}
          className="w-full sm:w-36"
          value={filterMonitor}
          onChange={setFilterMonitor}
          options={[
            { value: 'all', label: '全部监控' },
            { value: 'on', label: '启用' },
            { value: 'off', label: '关闭' },
          ]}
        />
      </Card>

      <Card className="hidden md:block">
        <DataTable
          columns={columns}
          rows={filtered}
          rowKey={(host) => host.id}
          loading={hosts.isLoading}
          empty={narrowedToNothing ? noMatchState : emptyState}
          selection={{ selected, onChange: setSelected }}
        />
      </Card>

      <div className="space-y-3 md:hidden">
        {hosts.isLoading ? (
          Array.from({ length: 3 }, (_, index) => (
            <Card key={index} className="space-y-3 p-4">
              <Skeleton className="h-5 w-2/5" />
              <Skeleton className="h-4 w-3/5" />
              <Skeleton className="h-8 w-full" />
            </Card>
          ))
        ) : filtered.length ? (
          filtered.map((host) => (
            <Card
              key={host.id}
              className={cn('min-w-0 p-4', selected.has(host.id) && 'border-accent')}
            >
              <div className="flex min-w-0 items-start justify-between gap-2">
                <div className="flex min-w-0 items-start gap-2.5">
                  <Checkbox
                    className="mt-1"
                    checked={selected.has(host.id)}
                    onCheckedChange={() =>
                      setSelected((prev) => {
                        const next = new Set(prev)
                        if (next.has(host.id)) next.delete(host.id)
                        else next.add(host.id)
                        return next
                      })
                    }
                    aria-label={`选择主机 ${host.name}`}
                  />
                  <div className="min-w-0 pt-0.5">
                    <div className="truncate leading-5 font-medium text-text">{host.name}</div>
                    <HostConnection host={host} />
                  </div>
                </div>
                {hostMenu(host)}
              </div>
              {/* 窄屏没有表头，用定值标签列把属性对齐成一张迷你规格表 */}
              <dl className="mt-3 grid grid-cols-[52px_minmax(0,1fr)] items-center gap-y-2 text-[12px]">
                <dt className="text-muted">认证</dt>
                <dd className="text-text">{host.auth_type === 'key' ? 'SSH 密钥' : '密码'}</dd>
                <dt className="text-muted">标签</dt>
                <dd className="min-w-0">
                  <TagsCell host={host} />
                </dd>
                <dt className="text-muted">跳板</dt>
                <dd className="text-text">
                  {hosts.data?.find((item) => item.id === host.jump_host_id)?.name ?? '直连'}
                </dd>
                <dt className="text-muted">指纹</dt>
                <dd className="min-w-0">
                  <FingerprintCell host={host} />
                </dd>
                <dt className="text-muted">监控</dt>
                <dd>
                  <MonitorCell host={host} />
                </dd>
              </dl>
              <Button
                className="mt-4 w-full"
                variant="outline"
                loading={testingId === host.id}
                onClick={() => void test(host)}
              >
                测试连接
              </Button>
            </Card>
          ))
        ) : (
          <Card className="p-4">{narrowedToNothing ? noMatchState : emptyState}</Card>
        )}
      </div>

      <Dialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        size="md"
        title={editing ? '编辑主机' : '添加主机'}
        footer={
          <>
            <Button type="button" variant="ghost" onClick={() => setDialogOpen(false)}>
              取消
            </Button>
            <Button type="submit" form="host-form" variant="primary" loading={save.isPending}>
              保存
            </Button>
          </>
        }
      >
        {/* 6 列栅格：端口只占 2 列，凭据与认证方式并排——切换认证方式时不再挤动下方行 */}
        <form id="host-form" className="grid gap-4 sm:grid-cols-6" onSubmit={submit}>
          <Field label="名称" required error={errors.name?.message} className="sm:col-span-3">
            {(id) => (
              <Input
                id={id}
                autoFocus
                invalid={Boolean(errors.name)}
                {...register('name', { required: '请输入名称' })}
              />
            )}
          </Field>
          <Field label="用户名" required error={errors.username?.message} className="sm:col-span-3">
            {(id) => (
              <Input
                id={id}
                invalid={Boolean(errors.username)}
                {...register('username', { required: '请输入用户名' })}
              />
            )}
          </Field>
          <Field label="地址" required error={errors.addr?.message} className="sm:col-span-4">
            {(id) => (
              <Input
                id={id}
                invalid={Boolean(errors.addr)}
                placeholder="主机名或 IP 地址"
                {...register('addr', { required: '请输入地址' })}
              />
            )}
          </Field>
          <Field label="端口" required error={errors.port?.message} className="sm:col-span-2">
            {(id) => (
              <Input
                id={id}
                type="number"
                min={1}
                max={65535}
                // 端口不适合用步进箭头微调，隐藏原生 spinner 免得悬停时冒出与体系无关的控件
                className="tabular-nums [appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                invalid={Boolean(errors.port)}
                {...register('port', {
                  required: '请输入端口',
                  valueAsNumber: true,
                  min: { value: 1, message: '端口不能小于 1' },
                  max: { value: 65535, message: '端口不能大于 65535' },
                })}
              />
            )}
          </Field>
          <Field
            label="认证方式"
            required
            error={errors.auth_type?.message}
            className="sm:col-span-3"
          >
            {(id) => (
              <Controller
                name="auth_type"
                control={control}
                rules={{ required: '请选择认证方式' }}
                render={({ field }) => (
                  <Select
                    id={id}
                    value={field.value}
                    onChange={field.onChange}
                    invalid={Boolean(errors.auth_type)}
                    options={[
                      { value: 'password', label: '密码' },
                      { value: 'key', label: 'SSH 密钥' },
                    ]}
                  />
                )}
              />
            )}
          </Field>

          {authType === 'key' ? (
            <Field
              label="密钥"
              required
              error={errors.key_id?.message}
              hint={keys.data?.length === 0 ? '密钥页尚未创建任何密钥' : undefined}
              className="sm:col-span-3"
            >
              {(id) => (
                <Controller
                  name="key_id"
                  control={control}
                  rules={{ required: '请选择密钥' }}
                  render={({ field }) => (
                    <Select
                      id={id}
                      value={field.value}
                      onChange={field.onChange}
                      options={(keys.data ?? []).map((key) => ({ value: key.id, label: key.name }))}
                      placeholder="选择密钥"
                      invalid={Boolean(errors.key_id)}
                    />
                  )}
                />
              )}
            </Field>
          ) : (
            <Field
              label={editing ? '新密码' : '密码'}
              required={!editing}
              error={errors.password?.message}
              hint={editing ? '留空则不修改' : undefined}
              className="sm:col-span-3"
            >
              {(id) => (
                <Input
                  id={id}
                  type="password"
                  autoComplete="new-password"
                  invalid={Boolean(errors.password)}
                  {...register('password', { required: editing ? false : '请输入密码' })}
                />
              )}
            </Field>
          )}

          <Field label="跳板主机" hint="选中后先经该主机建立隧道再连目标" className="sm:col-span-6">
            {(id) => (
              <Controller
                name="jump_host"
                control={control}
                render={({ field }) => (
                  <Select
                    id={id}
                    // 选项取主机 id 而非主机名：后端只校验名称非空、不限字符集，任何字符串哨兵
                    // 都可能与真实主机名撞车；id 恒为正整数，0 可以安全表示「直连」。
                    // 载荷仍按名称提交，两侧在这里换算。
                    value={hosts.data?.find((item) => item.name === field.value)?.id ?? 0}
                    onChange={(value) =>
                      field.onChange(hosts.data?.find((item) => item.id === value)?.name)
                    }
                    placeholder="直连（不使用跳板）"
                    options={[
                      { value: 0, label: '直连（不使用跳板）' },
                      ...(hosts.data ?? [])
                        .filter((item) => item.name !== editing?.name)
                        .map((item) => ({ value: item.id, label: item.name })),
                    ]}
                  />
                )}
              />
            )}
          </Field>

          <Field
            label="标签"
            hint="用于按用途或环境分组，可输入新标签回车创建"
            className="sm:col-span-6"
          >
            {(id) => (
              <Controller
                name="tags"
                control={control}
                render={({ field }) => (
                  <MultiSelect
                    id={id}
                    value={field.value ?? []}
                    onChange={field.onChange}
                    placeholder="选择或创建标签"
                    searchPlaceholder="搜索或输入新标签…"
                    // 候选 = 已有标签 ∪ 当前值：刚创建、还没保存到任何主机的标签也要能回显 label
                    options={[...new Set([...allTags, ...(field.value ?? [])])].map((tag) => ({
                      value: tag,
                      label: tag,
                    }))}
                    onCreateOption={(label) => {
                      const tag = label.trim()
                      if (tag && !(field.value ?? []).includes(tag)) {
                        field.onChange([...(field.value ?? []), tag])
                      }
                    }}
                  />
                )}
              />
            )}
          </Field>

          {/* 开关不是会报错的字段，改用一块 surface-2 行：说明与控件同一行，也给表单收了个尾 */}
          <Controller
            name="monitor_enabled"
            control={control}
            render={({ field }) => (
              <div className="flex items-center justify-between gap-4 rounded-[8px] bg-surface-2 px-3 py-2.5 sm:col-span-6">
                <div className="space-y-0.5">
                  <Label htmlFor={monitorId}>资源监控</Label>
                  <p className="text-[12px] text-muted">定期采集 CPU、内存、负载与磁盘</p>
                </div>
                <Switch id={monitorId} checked={field.value} onCheckedChange={field.onChange} />
              </div>
            )}
          />
        </form>
      </Dialog>

      <SelectionBar count={selected.size} onClear={() => setSelected(new Set())}>
        <Button variant="danger" size="sm" onClick={() => setConfirm({ kind: 'batch-delete' })}>
          删除
        </Button>
      </SelectionBar>

      <ConfirmDialog
        open={confirm !== null}
        onOpenChange={(open) => {
          if (!open) setConfirm(null)
        }}
        title={
          confirm?.kind === 'reset'
            ? '重置主机指纹？'
            : confirm?.kind === 'batch-delete'
              ? `批量删除 ${selected.size} 台主机？`
              : '删除主机？'
        }
        description={
          confirm?.kind === 'reset'
            ? `${confirm.host.name} 下次连接将重新执行 TOFU 首次信任。`
            : confirm?.kind === 'batch-delete'
              ? '所选主机及其令牌授权会一并失效，此操作不可撤销。'
              : `${confirm?.host.name ?? ''} 及其令牌授权会一并失效，此操作不可撤销。`
        }
        confirmText={confirm?.kind === 'reset' ? '重置' : '删除'}
        danger={confirm?.kind !== 'reset'}
        onConfirm={async () => {
          if (!confirm) return
          if (confirm.kind === 'reset') return resetFingerprint.mutateAsync(confirm.host.id)
          if (confirm.kind === 'delete') {
            await deleteHost.mutateAsync(confirm.host.id)
            // 删成功才摘掉选中项：失败时保留，批量条上还能看到它并重试
            setSelected((prev) => {
              if (!prev.has(confirm.host.id)) return prev
              const next = new Set(prev)
              next.delete(confirm.host.id)
              return next
            })
            return
          }
          // 失败的留在选中态：列表刷新后用户能直接看到哪些没删掉，可立即重试
          const { failedIds } = await deleteHosts.mutateAsync([...selected])
          setSelected(new Set(failedIds))
        }}
      />
    </PageTransition>
  )
}
