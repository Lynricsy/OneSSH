import { useEffect, useMemo, useState } from 'react'
import {
  Check,
  ClockCounterClockwise,
  Desktop,
  DeviceMobile,
  Eye,
  EyeSlash,
  GearSix,
  Info,
  Key,
  LockKey,
  Moon,
  ShieldCheck,
  SignOut,
  Sun,
  UserCircle,
  WarningCircle,
  X,
} from '@phosphor-icons/react'
import { useQueryClient } from '@tanstack/react-query'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { toast } from 'sonner'
import { describeUA, logout, useAccount, useChangePassword, useChangeUsername, useRevokeOthers, useRevokeSession, useSystemInfo } from '@/api/account'
import { ConfirmDialog } from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { PageHeader } from '@/components/ui/page-header'
import { PageTransition } from '@/components/ui/page-transition'
import { Select } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { cn } from '@/lib/cn'
import { formatSize } from '@/lib/file-types'
import { useTheme, type ThemeMode } from '@/lib/theme'
import { getIdleMinutes, setIdleMinutes } from '@/lib/idle-lock'

type Tab = 'security' | 'sessions' | 'logins' | 'prefs' | 'about'
const TABS: Array<{ id: Tab; label: string; icon: React.ReactNode }> = [
  { id: 'security', label: '账户与密码', icon: <LockKey size={16} /> },
  { id: 'sessions', label: '登录设备', icon: <Desktop size={16} /> },
  { id: 'logins', label: '登录记录', icon: <ClockCounterClockwise size={16} /> },
  { id: 'prefs', label: '偏好设置', icon: <GearSix size={16} /> },
  { id: 'about', label: '关于系统', icon: <Info size={16} /> },
]

const timeFmt = new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false })
const fmt = (sec: number) => (sec ? timeFmt.format(sec * 1000) : '—')
function ago(sec: number) {
  const d = Date.now() / 1000 - sec
  if (d < 90) return '刚刚'
  if (d < 3600) return `${Math.round(d / 60)} 分钟前`
  if (d < 86400) return `${Math.round(d / 3600)} 小时前`
  return `${Math.round(d / 86400)} 天前`
}

/** 密码强度：长度 + 字符种类，0–4 档 */
function strength(pw: string) {
  if (!pw) return { score: 0, label: '', color: '' }
  let score = 0
  if (pw.length >= 8) score++
  if (pw.length >= 12) score++
  const kinds = [/[a-z]/, /[A-Z]/, /\d/, /[^A-Za-z0-9]/].filter((r) => r.test(pw)).length
  if (kinds >= 2) score++
  if (kinds >= 3 && pw.length >= 10) score++
  const levels = [
    { label: '太弱', color: '#f43f5e' },
    { label: '弱', color: '#f97316' },
    { label: '一般', color: '#f59e0b' },
    { label: '较强', color: '#22c55e' },
    { label: '很强', color: '#14b8a6' },
  ]
  return { score, ...levels[score] }
}

const REASONS: Record<string, string> = {
  bad_password: '密码错误',
  bad_username: '用户名错误',
  rate_limited: '尝试过多被锁定',
  bad_current_password: '修改密码时当前密码错误',
  password_changed: '修改了密码',
}

function PasswordInput({ id, value, onChange, autoComplete, placeholder }: { id: string; value: string; onChange: (v: string) => void; autoComplete: string; placeholder?: string }) {
  const [show, setShow] = useState(false)
  return (
    <div className="relative">
      <Input id={id} type={show ? 'text' : 'password'} value={value} autoComplete={autoComplete} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} className="pr-9" />
      <button type="button" onClick={() => setShow((s) => !s)} aria-label={show ? '隐藏' : '显示'} className="absolute top-1/2 right-2 -translate-y-1/2 text-faint hover:text-text">
        {show ? <EyeSlash size={15} /> : <Eye size={15} />}
      </button>
    </div>
  )
}

function SecurityTab() {
  const account = useAccount()
  const change = useChangePassword()
  const rename = useChangeUsername()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState('')
  const [username, setUsername] = useState('')
  useEffect(() => setUsername(account.data?.username ?? ''), [account.data?.username])
  const s = strength(next)
  const rules = [
    { ok: next.length >= 8, label: '至少 8 个字符' },
    { ok: [/[a-zA-Z]/, /\d/, /[^A-Za-z0-9]/].filter((r) => r.test(next)).length >= 2, label: '包含字母、数字、符号中的两类' },
    { ok: !!next && next !== current, label: '与当前密码不同' },
    { ok: !!next && next === confirm, label: '两次输入一致' },
  ]
  const ready = !!current && rules.every((r) => r.ok)

  const submit = async () => {
    setError('')
    try {
      await change.mutateAsync({ current, new: next })
      setCurrent('')
      setNext('')
      setConfirm('')
    } catch (e) {
      setError((e as Error).message)
    }
  }

  if (account.isLoading) return <Skeleton className="h-80 rounded-[12px]" />
  const info = account.data

  return (
    <div className="grid gap-4 xl:grid-cols-[1fr_340px]">
      <Card className="p-6">
        <h2 className="flex items-center gap-2 text-[15px] font-semibold text-text">
          <Key size={17} className="text-accent" />
          修改登录密码
        </h2>
        <p className="mt-1 text-[12.5px] text-muted">修改后其他设备上的登录会立即失效，只保留当前这个会话。</p>
        <form
          className="mt-5 max-w-md space-y-4"
          onSubmit={(e) => {
            e.preventDefault()
            if (ready) void submit()
          }}
        >
          <Field label="当前密码" required>
            {(id) => <PasswordInput id={id} value={current} onChange={setCurrent} autoComplete="current-password" />}
          </Field>
          <Field label="新密码" required>
            {(id) => <PasswordInput id={id} value={next} onChange={setNext} autoComplete="new-password" />}
          </Field>
          {next && (
            <div>
              <div className="flex gap-1">
                {[0, 1, 2, 3].map((i) => (
                  <span key={i} className="h-1.5 flex-1 rounded-full transition-colors" style={{ background: i < s.score ? s.color : 'var(--surface-2)' }} />
                ))}
              </div>
              <p className="mt-1 text-[11.5px]" style={{ color: s.color }}>
                强度：{s.label}
              </p>
            </div>
          )}
          <Field label="确认新密码" required>
            {(id) => <PasswordInput id={id} value={confirm} onChange={setConfirm} autoComplete="new-password" />}
          </Field>
          <ul className="grid gap-1 sm:grid-cols-2">
            {rules.map((r) => (
              <li key={r.label} className={cn('flex items-center gap-1.5 text-[12px]', r.ok ? 'text-success' : 'text-muted')}>
                {r.ok ? <Check size={13} weight="bold" /> : <span className="size-1.5 rounded-full bg-border-strong" />}
                {r.label}
              </li>
            ))}
          </ul>
          {error && (
            <p className="flex items-center gap-2 rounded-[10px] border border-danger/25 bg-danger/8 px-3 py-2 text-[12.5px] text-danger">
              <WarningCircle size={15} weight="fill" />
              {error}
            </p>
          )}
          <Button type="submit" variant="primary" disabled={!ready} loading={change.isPending}>
            修改密码
          </Button>
        </form>
      </Card>

      <div className="space-y-4">
        <Card className="p-5">
          <h3 className="flex items-center gap-2 text-[14px] font-semibold text-text">
            <UserCircle size={17} className="text-accent" />
            管理员账户
          </h3>
          <div className="mt-4 space-y-3">
            <Field label="用户名" hint="登录时使用，不区分大小写">
              {(id) => <Input id={id} value={username} onChange={(e) => setUsername(e.target.value)} maxLength={32} />}
            </Field>
            <Button size="sm" variant="outline" disabled={!username.trim() || username === info?.username} loading={rename.isPending} onClick={() => rename.mutate(username.trim())}>
              保存用户名
            </Button>
          </div>
        </Card>
        <Card className="p-5">
          <h3 className="flex items-center gap-2 text-[14px] font-semibold text-text">
            <ShieldCheck size={17} className="text-accent" />
            安全状态
          </h3>
          <dl className="mt-3 space-y-2 text-[12.5px]">
            <div className="flex justify-between gap-3">
              <dt className="text-muted">密码来源</dt>
              <dd className="text-right text-text">{info?.password_source === 'custom' ? '控制台设置' : '启动配置 ONESSH_ADMIN_PASSWORD'}</dd>
            </div>
            <div className="flex justify-between gap-3">
              <dt className="text-muted">上次修改</dt>
              <dd className="text-text">{info?.password_changed_at ? fmt(info.password_changed_at) : '从未修改'}</dd>
            </div>
            <div className="flex justify-between gap-3">
              <dt className="text-muted">在线设备</dt>
              <dd className="text-text">{info?.sessions.length ?? 0} 个</dd>
            </div>
            <div className="flex justify-between gap-3">
              <dt className="text-muted">24 小时失败登录</dt>
              <dd className={cn((info?.failed_24h ?? 0) > 0 ? 'font-medium text-warning' : 'text-text')}>{info?.failed_24h ?? 0} 次</dd>
            </div>
          </dl>
          <p className="mt-4 rounded-[10px] bg-surface-2/70 px-3 py-2 text-[11.5px] leading-5 text-muted">
            忘记密码？在 <code className="font-mono">.env</code> 里加一行 <code className="font-mono">ONESSH_ADMIN_RESET=1</code> 重启一次，密码会恢复为 <code className="font-mono">ONESSH_ADMIN_PASSWORD</code> 的值（之后记得删掉这一行）。
          </p>
        </Card>
      </div>
    </div>
  )
}

function SessionsTab() {
  const account = useAccount()
  const revoke = useRevokeSession()
  const revokeOthers = useRevokeOthers()
  const [confirmAll, setConfirmAll] = useState(false)
  if (account.isLoading) return <Skeleton className="h-60 rounded-[12px]" />
  const sessions = account.data?.sessions ?? []
  return (
    <Card className="overflow-hidden p-0">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-5 py-3.5">
        <div>
          <h2 className="text-[14px] font-semibold text-text">已登录的设备</h2>
          <p className="text-[12px] text-muted">不认识的设备请立即下线并修改密码</p>
        </div>
        <Button size="sm" variant="outline" disabled={sessions.length < 2} onClick={() => setConfirmAll(true)}>
          <SignOut size={14} />
          下线其他所有设备
        </Button>
      </div>
      <ul className="divide-y divide-border">
        {sessions.map((s) => {
          const ua = describeUA(s.user_agent)
          return (
            <li key={s.id} className="flex flex-wrap items-center gap-4 px-5 py-3.5">
              <span className={cn('grid size-10 shrink-0 place-items-center rounded-[11px]', s.current ? 'bg-accent text-accent-fg' : 'bg-surface-2 text-muted')}>
                {ua.mobile ? <DeviceMobile size={19} /> : <Desktop size={19} />}
              </span>
              <div className="min-w-0 flex-1">
                <p className="flex flex-wrap items-center gap-2 text-[13.5px] font-medium text-text">
                  {ua.browser}
                  {ua.os && ` · ${ua.os}`}
                  {s.current && <Badge variant="accent">当前设备</Badge>}
                  {s.remember && <Badge variant="outline">记住 30 天</Badge>}
                </p>
                <p className="mt-0.5 text-[12px] text-muted">
                  <span className="font-mono">{s.ip}</span> · 登录于 {fmt(s.created_at)} · 最近活动 {ago(s.last_seen)} · {fmt(s.expires_at)} 过期
                </p>
              </div>
              {!s.current && (
                <Button size="sm" variant="ghost" className="text-danger hover:bg-danger/10" loading={revoke.isPending && revoke.variables === s.id} onClick={() => revoke.mutate(s.id)}>
                  <X size={13} />
                  下线
                </Button>
              )}
            </li>
          )
        })}
      </ul>
      <ConfirmDialog
        open={confirmAll}
        onOpenChange={setConfirmAll}
        title="下线其他所有设备？"
        description="除了你正在用的这个浏览器，其他地方的登录会立即失效。"
        confirmText="全部下线"
        danger
        onConfirm={() => revokeOthers.mutateAsync()}
      />
    </Card>
  )
}

function LoginsTab() {
  const account = useAccount()
  if (account.isLoading) return <Skeleton className="h-60 rounded-[12px]" />
  const logins = account.data?.logins ?? []
  return (
    <Card className="overflow-hidden p-0">
      <div className="border-b border-border px-5 py-3.5">
        <h2 className="text-[14px] font-semibold text-text">最近的登录与安全事件</h2>
        <p className="text-[12px] text-muted">保留最近 300 条；同一 IP 连续失败 10 次会锁定 15 分钟</p>
      </div>
      <table className="w-full text-[13px]">
        <thead>
          <tr className="bg-surface-2/50 text-left text-[11px] font-semibold tracking-wide text-muted uppercase">
            <th className="px-5 py-2">结果</th>
            <th className="px-3 py-2">时间</th>
            <th className="px-3 py-2">IP</th>
            <th className="hidden px-3 py-2 md:table-cell">设备</th>
            <th className="hidden px-3 py-2 lg:table-cell">用户名</th>
          </tr>
        </thead>
        <tbody>
          {logins.map((l, i) => {
            const ua = describeUA(l.user_agent)
            return (
              <tr key={`${l.ts}-${i}`} className="border-t border-border">
                <td className="px-5 py-2">
                  {l.ok ? (
                    <span className="inline-flex items-center gap-1.5 text-success">
                      <span className="size-1.5 rounded-full bg-success" />
                      {l.reason ? REASONS[l.reason] ?? l.reason : '登录成功'}
                    </span>
                  ) : (
                    <span className="inline-flex items-center gap-1.5 text-danger">
                      <span className="size-1.5 rounded-full bg-danger" />
                      {REASONS[l.reason] ?? '登录失败'}
                    </span>
                  )}
                </td>
                <td className="px-3 py-2 text-muted tabular-nums">{fmt(l.ts)}</td>
                <td className="px-3 py-2 font-mono text-[12px] text-text">{l.ip}</td>
                <td className="hidden px-3 py-2 text-muted md:table-cell">
                  {ua.browser}
                  {ua.os && ` · ${ua.os}`}
                </td>
                <td className="hidden px-3 py-2 font-mono text-[12px] text-muted lg:table-cell">{l.username || '—'}</td>
              </tr>
            )
          })}
          {logins.length === 0 && (
            <tr>
              <td colSpan={5} className="px-5 py-10 text-center text-muted">
                暂无记录
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </Card>
  )
}

function PrefsTab() {
  const { mode, setMode } = useTheme()
  const [idle, setIdle] = useState(getIdleMinutes)
  const [remember, setRemember] = useState(() => localStorage.getItem('onessh-login-remember') === '1')
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <Card className="p-5">
        <h3 className="text-[14px] font-semibold text-text">外观主题</h3>
        <div className="mt-3 grid grid-cols-3 gap-2">
          {(
            [
              ['light', '浅色', <Sun size={18} />],
              ['dark', '深色', <Moon size={18} />],
              ['system', '跟随系统', <Desktop size={18} />],
            ] as Array<[ThemeMode, string, React.ReactNode]>
          ).map(([m, label, icon]) => (
            <button key={m} type="button" onClick={() => setMode(m)} className={cn('flex flex-col items-center gap-1.5 rounded-[12px] border px-3 py-3 text-[12.5px] transition-colors', mode === m ? 'border-accent bg-accent-soft text-text' : 'border-border text-muted hover:bg-surface-2')}>
              {icon}
              {label}
            </button>
          ))}
        </div>
      </Card>
      <Card className="p-5">
        <h3 className="text-[14px] font-semibold text-text">自动锁定</h3>
        <p className="mt-1 text-[12.5px] text-muted">一段时间没有操作就自动退出登录（只对本浏览器生效）。终端会话不受影响直到页面刷新。</p>
        <Select
          className="mt-3 w-56"
          value={idle}
          onChange={(v) => {
            setIdle(v)
            setIdleMinutes(v)
            toast.success(v ? `已设置：${v} 分钟无操作自动退出` : '已关闭自动锁定')
          }}
          options={[
            { value: 0, label: '从不' },
            { value: 15, label: '15 分钟' },
            { value: 30, label: '30 分钟' },
            { value: 60, label: '1 小时' },
            { value: 240, label: '4 小时' },
          ]}
        />
      </Card>
      <Card className="p-5">
        <h3 className="text-[14px] font-semibold text-text">登录</h3>
        <label className="mt-3 flex items-center justify-between gap-3 text-[13px] text-text">
          <span>
            登录页默认勾选「记住我 30 天」
            <span className="block text-[12px] text-muted">不勾选时登录 24 小时后过期</span>
          </span>
          <Switch
            checked={remember}
            onCheckedChange={(v) => {
              setRemember(v)
              localStorage.setItem('onessh-login-remember', v ? '1' : '0')
            }}
          />
        </label>
      </Card>
      <Card className="p-5">
        <h3 className="text-[14px] font-semibold text-text">本地缓存</h3>
        <p className="mt-1 text-[12.5px] text-muted">文件管理的标签页、收藏目录、终端快捷命令与配色都保存在浏览器里。</p>
        <Button
          size="sm"
          variant="outline"
          className="mt-3"
          onClick={() => {
            for (const key of Object.keys(localStorage)) if (key.startsWith('onessh-files') || key.startsWith('onessh-term')) localStorage.removeItem(key)
            toast.success('已清除，刷新页面后生效')
          }}
        >
          清除文件与终端的本地设置
        </Button>
      </Card>
    </div>
  )
}

function AboutTab() {
  const sys = useSystemInfo()
  if (sys.isLoading) return <Skeleton className="h-60 rounded-[12px]" />
  const s = sys.data
  if (!s) return null
  const rows: Array<[string, React.ReactNode]> = [
    ['版本', s.version],
    ['运行时长', ago(s.started_at).replace('前', '')],
    ['启动时间', fmt(s.started_at)],
    ['监听地址', <span className="font-mono">{s.listen}</span>],
    ['数据目录', <span className="font-mono">{s.data_dir}</span>],
    ['数据库大小', formatSize(s.db_bytes)],
    ['主密钥来源', s.master_key_source],
    ['自动定位', s.geoip ? '开启（ip-api.com）' : '关闭'],
    ['运行平台', `${s.platform} · ${s.go_version} · PID ${s.pid}`],
    ['纳管资源', `${s.hosts} 台主机 · ${s.keys} 把密钥 · ${s.tokens} 个令牌`],
  ]
  return (
    <Card className="overflow-hidden p-0">
      <dl className="divide-y divide-border">
        {rows.map(([label, value]) => (
          <div key={label} className="grid grid-cols-[120px_1fr] gap-4 px-5 py-2.5 text-[13px]">
            <dt className="text-muted">{label}</dt>
            <dd className="min-w-0 break-all text-text">{value}</dd>
          </div>
        ))}
      </dl>
    </Card>
  )
}

export function SettingsPage() {
  const [params, setParams] = useSearchParams()
  const tab = (params.get('tab') as Tab) || 'security'
  const navigate = useNavigate()
  const qc = useQueryClient()
  const account = useAccount()
  const content = useMemo(
    () => ({ security: <SecurityTab />, sessions: <SessionsTab />, logins: <LoginsTab />, prefs: <PrefsTab />, about: <AboutTab /> })[tab] ?? <SecurityTab />,
    [tab],
  )
  return (
    <PageTransition>
      <PageHeader
        title="系统设置"
        subtitle={`以 ${account.data?.username ?? 'admin'} 身份登录 · 账户安全、登录设备与偏好`}
        actions={
          <Button
            variant="outline"
            onClick={async () => {
              await logout()
              qc.clear()
              navigate('/login', { replace: true })
            }}
          >
            <SignOut size={15} />
            退出登录
          </Button>
        }
      />
      <div className="grid gap-5 lg:grid-cols-[210px_1fr]">
        <nav className="flex gap-1 overflow-x-auto lg:flex-col">
          {TABS.map((t) => (
            <button
              key={t.id}
              type="button"
              onClick={() => setParams({ tab: t.id }, { replace: true })}
              className={cn('flex shrink-0 items-center gap-2.5 rounded-[10px] px-3 py-2 text-left text-[13px] font-medium transition-colors', tab === t.id ? 'bg-accent-soft text-accent' : 'text-muted hover:bg-surface-2 hover:text-text')}
            >
              {t.icon}
              {t.label}
              {t.id === 'logins' && (account.data?.failed_24h ?? 0) > 0 && <span className="ml-auto rounded-full bg-warning/15 px-1.5 text-[10.5px] text-warning">{account.data?.failed_24h}</span>}
              {t.id === 'sessions' && <span className="ml-auto text-[11px] text-faint">{account.data?.sessions.length ?? ''}</span>}
            </button>
          ))}
        </nav>
        <div className="min-w-0">{content}</div>
      </div>
    </PageTransition>
  )
}
