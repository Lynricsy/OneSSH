import { useState } from 'react'
import { Controller, useForm } from 'react-hook-form'
import { useLocation } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { AnimatePresence, motion, useReducedMotion } from 'motion/react'
import {
  ArrowSquareOut,
  Check,
  Desktop,
  Moon,
  ShieldCheck,
  Sun,
  WarningCircle,
} from '@phosphor-icons/react'
import { api, post, type ApiError } from '@/api/client'
import type { OAuthAuthorizationInfo, OAuthDecisionPayload, OAuthDecisionResult } from '@/api/types'
import { LogoTile } from '@/components/brand/logo'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { EmptyState } from '@/components/ui/empty-state'
import { Field } from '@/components/ui/field'
import { Label } from '@/components/ui/label'
import { MultiSelect } from '@/components/ui/multi-select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { useTheme, type ThemeMode } from '@/lib/theme'

type ConsentFormValues = {
  all_hosts: boolean
  manage_hosts: boolean
  host_ids: number[]
}

/** 与令牌创建保持同一套默认值：先给最宽的执行范围，管理权限必须显式打开 */
const defaultValues: ConsentFormValues = { all_hosts: true, manage_hosts: false, host_ids: [] }

/** 后端目前只签发 mcp 一种范围；未知范围原样展示，不编造语义 */
const SCOPE_HINTS: Record<string, string> = {
  mcp: '使用 OneSSH MCP 工具',
}

const themeModes: { value: ThemeMode; label: string; icon: React.ReactNode }[] = [
  { value: 'light', label: '浅色', icon: <Sun size={15} /> },
  { value: 'dark', label: '深色', icon: <Moon size={15} /> },
  { value: 'system', label: '跟随系统', icon: <Desktop size={15} /> },
]

/** 授权页在 AppShell 之外，主题入口得自己带一个，否则用户改不了配色 */
function ThemeMenu() {
  const { mode, resolved, setMode } = useTheme()
  const current =
    mode === 'system' ? <Desktop size={16} /> : resolved === 'dark' ? <Moon size={16} /> : <Sun size={16} />

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" aria-label="切换主题">
          {current}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {themeModes.map((m) => (
          <DropdownMenuItem key={m.value} onSelect={() => setMode(m.value)}>
            {m.icon}
            <span className="flex-1">{m.label}</span>
            {mode === m.value && <Check size={14} className="text-accent" />}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

type CallbackTrust = {
  /** 展示用地址：http(s) 只留 host + path，自定义协议保持原样 */
  display: string
  badge: string
  tone: 'success' | 'warning' | 'default'
  detail: string
}

/**
 * 回调地址是这个页面唯一能验证「授权码会去哪」的线索，所以按可信度分级说明，
 * 而不是笼统地把 redirect_uri 丢给用户自己判断。
 */
function describeCallback(raw: string): CallbackTrust {
  let url: URL
  try {
    url = new URL(raw)
  } catch {
    return {
      display: raw,
      badge: '无法解析',
      tone: 'warning',
      detail: '这不是一个合法的回调 URL，除非你确认客户端来源，否则不要授权。',
    }
  }

  if (url.protocol !== 'http:' && url.protocol !== 'https:') {
    return {
      display: raw,
      badge: '本机应用',
      tone: 'success',
      detail: `授权码交给本机注册了 ${url.protocol.replace(':', '')}: 协议的应用，不经过网络。`,
    }
  }

  const hostname = url.hostname.replace(/^\[|\]$/g, '')
  const isLoopback =
    hostname === 'localhost' ||
    hostname.endsWith('.localhost') ||
    hostname === '::1' ||
    /^127\./.test(hostname)
  const display = url.host + (url.pathname === '/' ? '' : url.pathname)

  if (isLoopback) {
    return {
      display,
      badge: '本机回调',
      tone: 'success',
      detail: '回调指向你这台机器上的本地端口，授权码不会离开本机。',
    }
  }
  if (url.protocol === 'http:') {
    return {
      display,
      badge: '明文外部地址',
      tone: 'warning',
      detail: '回调走未加密的 HTTP 发往外部主机，授权码可能在链路中被读取。',
    }
  }
  return {
    display,
    badge: '外部地址',
    tone: 'default',
    detail: '授权码会被发往这个外部地址，确认你信任它再继续。',
  }
}

/** 页面骨架与真实内容同高同分区，避免加载完成时整卡跳动 */
function ConsentSkeleton() {
  return (
    <Card aria-busy className="overflow-hidden">
      <div className="px-5 py-6 sm:px-6">
        <Skeleton className="h-3 w-24" />
        <Skeleton className="mt-3 h-6 w-52" />
        <Skeleton className="mt-3 h-3 w-full max-w-[320px]" />
      </div>
      <div className="border-t border-border px-5 py-5 sm:px-6">
        <Skeleton className="h-3 w-16" />
        <Skeleton className="mt-3 h-[62px] w-full" />
      </div>
      <div className="border-t border-border px-5 py-5 sm:px-6">
        <Skeleton className="h-3 w-16" />
        <Skeleton className="mt-3 h-4 w-40" />
      </div>
      <div className="space-y-3 border-t border-border px-5 py-5 sm:px-6">
        <Skeleton className="h-3 w-20" />
        <Skeleton className="h-[58px] w-full" />
        <Skeleton className="h-[58px] w-full" />
      </div>
      <div className="border-t border-border px-5 py-4 sm:px-6">
        <Skeleton className="h-11 w-full sm:ml-auto sm:w-40" />
      </div>
    </Card>
  )
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="border-t border-border px-5 py-5 sm:px-6">
      <h2 className="text-[11px] font-medium tracking-wide text-muted uppercase">{title}</h2>
      <div className="mt-3">{children}</div>
    </section>
  )
}

export function OAuthAuthorizePage() {
  const { search } = useLocation()
  const reduce = useReducedMotion()
  // 两个按钮共用一次提交，用 pending 记住是谁在提交：另一个按钮同时禁用，spinner 也只转在被点的那个上
  const [pending, setPending] = useState<'approve' | 'deny' | null>(null)
  const [submitError, setSubmitError] = useState('')

  const authorization = useQuery({
    queryKey: ['oauth-authorization', search],
    queryFn: () => api<OAuthAuthorizationInfo>(`/oauth/authorization${search}`),
    // 授权请求是一次性的：重试只会让用户对着 spinner 多等一轮，失败原因也不会变
    retry: false,
    staleTime: Infinity,
    enabled: search.length > 0,
  })

  const {
    clearErrors,
    control,
    getValues,
    handleSubmit,
    watch,
    formState: { errors },
  } = useForm<ConsentFormValues>({ defaultValues })
  const allHosts = watch('all_hosts')
  const manageHosts = watch('manage_hosts')
  const hostIds = watch('host_ids')

  const info = authorization.data
  const hosts = info?.hosts ?? []

  const submit = async (decision: 'approve' | 'deny', values: ConsentFormValues) => {
    setPending(decision)
    setSubmitError('')
    const payload: OAuthDecisionPayload = {
      query: search,
      decision,
      all_hosts: values.all_hosts,
      manage_hosts: values.manage_hosts,
      host_ids: values.all_hosts ? undefined : values.host_ids,
    }
    try {
      const result = await post<OAuthDecisionResult>('/oauth/authorization', payload)
      // 成功后不复位 pending：页面正在离开，按钮保持禁用比闪回可点状态更诚实
      window.location.assign(result.redirect_uri)
    } catch (e) {
      setSubmitError((e as ApiError).message)
      setPending(null)
    }
  }

  const approve = handleSubmit((values) => submit('approve', values))

  const header = (
    <header className="mb-5 flex items-center justify-between gap-3">
      <div className="flex min-w-0 items-center gap-2.5">
        <LogoTile className="size-8" />
        <span className="truncate text-[13px] font-medium text-text">OneSSH</span>
        <span className="hidden text-[13px] text-faint sm:inline">授权请求</span>
      </div>
      <ThemeMenu />
    </header>
  )

  if (!search) {
    return (
      <div className="min-h-dvh bg-bg px-4 py-10 sm:py-16">
        <div className="mx-auto w-full max-w-[560px]">
          {header}
          <Card>
            <EmptyState
              icon={<WarningCircle size={24} className="text-danger" />}
              title="授权链接不完整"
              description="地址里缺少 OAuth 参数，请回到客户端重新发起授权。"
            />
          </Card>
        </div>
      </div>
    )
  }

  const callback = info ? describeCallback(info.redirect_uri) : null

  return (
    <div className="min-h-dvh bg-bg px-4 py-10 sm:py-16">
      <div className="mx-auto w-full max-w-[560px]">
        {header}

        {authorization.isPending ? (
          <ConsentSkeleton />
        ) : authorization.isError ? (
          <Card>
            <EmptyState
              icon={<WarningCircle size={24} className="text-danger" />}
              title="无法读取授权请求"
              description={(authorization.error as Error).message}
              action={
                <Button variant="outline" onClick={() => void authorization.refetch()}>
                  重试
                </Button>
              }
            />
          </Card>
        ) : (
          info &&
          callback && (
            <motion.div
              initial={reduce ? false : { opacity: 0, y: 10 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.28, ease: 'easeOut' }}
            >
              <Card className="overflow-hidden">
                <form onSubmit={approve}>
                  <div className="px-5 py-6 sm:px-6">
                    <p className="text-[12px] text-muted">MCP 客户端请求接入</p>
                    <h1 className="mt-1.5 text-[20px] leading-tight font-semibold tracking-tight break-words text-text sm:text-[22px]">
                      {info.client_name}
                    </h1>
                    {/* client_uri 由客户端自报，只放行 http(s)，其余一律降级成纯文本 */}
                    {info.client_uri &&
                      (/^https?:\/\//i.test(info.client_uri) ? (
                        <a
                          href={info.client_uri}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="mt-2 inline-flex max-w-full items-center gap-1 rounded-[4px] text-[13px] text-accent underline-offset-2 hover:underline"
                        >
                          <span className="truncate">{info.client_uri}</span>
                          <ArrowSquareOut size={13} className="shrink-0" />
                        </a>
                      ) : (
                        <p className="mt-2 truncate font-mono text-[12px] text-muted" title={info.client_uri}>
                          {info.client_uri}
                        </p>
                      ))}
                    <p className="mt-3.5 text-[13px] leading-6 text-muted">
                      它将以你的身份调用 OneSSH，能做什么完全由下面这几项决定。
                    </p>
                  </div>

                  <Section title="回调地址">
                    <div className="rounded-[8px] border border-border bg-surface-2 px-3 py-2.5">
                      {/* 窄屏把徽章挑到上一行：回调地址是这里最该被逐字读完的东西，不能被挤成两截 */}
                      <div className="flex flex-col gap-1.5 sm:flex-row-reverse sm:items-start sm:justify-between sm:gap-3">
                        <Badge
                          variant={
                            callback.tone === 'success'
                              ? 'success'
                              : callback.tone === 'warning'
                                ? 'warning'
                                : 'outline'
                          }
                          className="self-start"
                        >
                          {callback.tone === 'success' ? (
                            <ShieldCheck size={11} weight="fill" />
                          ) : callback.tone === 'warning' ? (
                            <WarningCircle size={11} weight="fill" />
                          ) : null}
                          {callback.badge}
                        </Badge>
                        <code
                          className="min-w-0 font-mono text-[12px] break-all text-text"
                          title={info.redirect_uri}
                        >
                          {callback.display}
                        </code>
                      </div>
                      <p className="mt-1.5 text-[12px] leading-5 text-muted">{callback.detail}</p>
                    </div>
                  </Section>

                  <Section title="请求的范围">
                    {info.requested_scopes.length === 0 ? (
                      <p className="text-[13px] text-muted">
                        客户端没有声明额外范围，权限完全由下面的主机设置决定。
                      </p>
                    ) : (
                      <ul className="space-y-2">
                        {info.requested_scopes.map((scope) => (
                          <li key={scope} className="flex gap-2.5">
                            <Check size={14} weight="bold" className="mt-1 shrink-0 text-accent" />
                            {/* 认识的范围给中文语义、原文降为脚注；不认识的只敢原样照抄，不替后端编语义 */}
                            {SCOPE_HINTS[scope] ? (
                              <div className="min-w-0">
                                <p className="text-[13px] text-text">{SCOPE_HINTS[scope]}</p>
                                <code className="font-mono text-[11px] break-all text-faint">{scope}</code>
                              </div>
                            ) : (
                              <code className="min-w-0 font-mono text-[12px] break-all text-text">
                                {scope}
                              </code>
                            )}
                          </li>
                        ))}
                      </ul>
                    )}
                  </Section>

                  <Section title="主机权限">
                    <div className="space-y-3">
                      {/* 开关横排成设置行，与令牌创建表单完全同构，避免同一套权限出现两种界面 */}
                      <div className="flex items-center justify-between gap-4 rounded-[8px] border border-border bg-surface-2 px-3 py-2.5">
                        <div className="min-w-0">
                          <Label htmlFor="oauth-all-hosts">允许全部主机</Label>
                          <p className="mt-0.5 text-[12px] leading-5 text-muted">
                            {allHosts
                              ? `可访问当前全部 ${hosts.length} 台主机，以及之后新增的主机`
                              : '只授权下面选定的主机'}
                          </p>
                        </div>
                        <Controller
                          name="all_hosts"
                          control={control}
                          render={({ field }) => (
                            <Switch
                              id="oauth-all-hosts"
                              checked={field.value}
                              onCheckedChange={(checked) => {
                                field.onChange(checked)
                                clearErrors('host_ids')
                              }}
                              disabled={pending !== null}
                            />
                          )}
                        />
                      </div>

                      <div className="flex items-center justify-between gap-4 rounded-[8px] border border-border bg-surface-2 px-3 py-2.5">
                        <div className="min-w-0">
                          <div className="flex flex-wrap items-center gap-2">
                            <Label htmlFor="oauth-manage-hosts">允许管理主机</Label>
                            <Badge variant="warning">高权限</Badge>
                          </div>
                          <p className="mt-0.5 text-[12px] leading-5 text-muted">
                            可新增、编辑、测试和删除全部 SSH 主机；不会扩大命令执行范围
                          </p>
                        </div>
                        <Controller
                          name="manage_hosts"
                          control={control}
                          render={({ field }) => (
                            <Switch
                              id="oauth-manage-hosts"
                              checked={field.value}
                              // 打开管理权限后主机不再必填，旧的红字校验必须跟着撤掉
                              onCheckedChange={(checked) => {
                                field.onChange(checked)
                                if (checked) clearErrors('host_ids')
                              }}
                              disabled={pending !== null}
                            />
                          )}
                        />
                      </div>

                      {/* 条件字段：直接挂载会让卡片高度硬跳，连 margin 一起动画消掉跳动 */}
                      <AnimatePresence initial={false}>
                        {!allHosts && (
                          <motion.div
                            key="host-scope"
                            className="overflow-hidden"
                            initial={reduce ? false : { height: 0, opacity: 0, marginTop: 0 }}
                            animate={reduce ? {} : { height: 'auto', opacity: 1, marginTop: 12 }}
                            exit={reduce ? {} : { height: 0, opacity: 0, marginTop: 0 }}
                            transition={{ duration: 0.22, ease: [0.22, 1, 0.36, 1] }}
                          >
                            <Field
                              label="允许主机"
                              required={!manageHosts}
                              error={errors.host_ids?.message}
                              hint={
                                hosts.length === 0
                                  ? '当前没有可授权的主机'
                                  : `已选 ${hostIds.length} 台，共 ${hosts.length} 台`
                              }
                            >
                              {(id) => (
                                <Controller
                                  name="host_ids"
                                  control={control}
                                  rules={{
                                    validate: (value) =>
                                      manageHosts || value.length > 0 || '请至少选择一台主机',
                                  }}
                                  render={({ field }) => (
                                    <MultiSelect
                                      id={id}
                                      value={field.value}
                                      onChange={field.onChange}
                                      invalid={Boolean(errors.host_ids)}
                                      options={hosts.map((host) => ({
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
                    </div>
                  </Section>

                  <div className="border-t border-border px-5 py-4 sm:px-6">
                    {/* 提交失败是服务端结果而不是字段校验，贴在按钮上方比 toast 更容易被看到 */}
                    {submitError && (
                      <p className="mb-3 flex items-start gap-2 rounded-[8px] border border-danger/25 bg-danger/8 px-3 py-2 text-[12px] leading-5 text-danger">
                        <WarningCircle size={15} weight="fill" className="mt-px shrink-0" />
                        {submitError}
                      </p>
                    )}
                    <div className="flex flex-col-reverse gap-2.5 sm:flex-row sm:justify-end">
                      <Button
                        type="button"
                        variant="outline"
                        size="lg"
                        className="w-full sm:w-auto"
                        onClick={() => void submit('deny', getValues())}
                        loading={pending === 'deny'}
                        disabled={pending !== null}
                      >
                        拒绝授权
                      </Button>
                      <Button
                        type="submit"
                        variant="primary"
                        size="lg"
                        className="w-full sm:w-auto"
                        loading={pending === 'approve'}
                        disabled={pending !== null}
                      >
                        允许访问
                      </Button>
                    </div>
                  </div>
                </form>
              </Card>
            </motion.div>
          )
        )}

        <p className="mt-4 px-1 text-[12px] leading-5 text-muted">
          授权后客户端会拿到一枚 Agent 令牌，你可以随时在控制台的「Agent 令牌」页面撤销它。
        </p>
      </div>
    </div>
  )
}
