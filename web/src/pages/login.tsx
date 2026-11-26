import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { useLocation, useNavigate } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { motion } from 'motion/react'
import {
  ChartLineUp,
  ClockCounterClockwise,
  GlobeHemisphereEast,
  LockKey,
  ShieldCheck,
  WarningCircle,
} from '@phosphor-icons/react'
import { post, type ApiError } from '@/api/client'
import { LogoTile } from '@/components/brand/logo'
import { Button } from '@/components/ui/button'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'

const FEATURES = [
  { icon: ShieldCheck, title: '凭据不出网关', text: '私钥与密码经 AES-256-GCM 加密，Agent 永远拿不到明文' },
  { icon: ClockCounterClockwise, title: '全程留痕', text: '每次工具调用、每条命令的退出码与输出都可回看' },
  { icon: ChartLineUp, title: '资源一目了然', text: 'CPU、内存、磁盘趋势与 Agent 活动集中在一张概览上' },
  { icon: GlobeHemisphereEast, title: '全球分布', text: '按公网 IP 把服务器标在世界地图上，延迟实时可见' },
]

export function LoginPage() {
  const navigate = useNavigate()
  const location = useLocation()
  const queryClient = useQueryClient()
  const [error, setError] = useState('')
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<{ password: string }>()

  const submit = handleSubmit(async ({ password }) => {
    setError('')
    try {
      await post('/login', { password })
      // 换会话必须丢弃旧缓存，否则上一个身份的数据会闪现
      queryClient.clear()
      // from 已带查询串（OAuth 授权链接全靠它）；只认站内相对路径，"//host" 会被浏览器当协议相对 URL 跳走
      const from = (location.state as { from?: string } | null)?.from
      const back = from && from.startsWith('/') && !from.startsWith('//') ? from : '/'
      navigate(back, { replace: true })
    } catch (e) {
      // 后端 401 只会回 "Unauthorized"，直接透出对中文界面既突兀也没信息量
      const err = e as ApiError
      setError(err.status === 401 ? '密码不正确，请重新输入' : err.message)
    }
  })

  return (
    <div className="relative grid min-h-dvh overflow-hidden bg-bg lg:grid-cols-[1.1fr_1fr]">
      {/* ── 左侧品牌区 ── */}
      <aside className="login-brand relative hidden overflow-hidden lg:flex lg:flex-col lg:justify-between lg:p-12">
        <div className="login-grid pointer-events-none absolute inset-0" />
        <div className="pointer-events-none absolute -top-40 -left-32 size-[520px] rounded-full bg-[var(--accent)] opacity-25 blur-[120px]" />
        <div className="pointer-events-none absolute -right-24 -bottom-40 size-[480px] rounded-full bg-[var(--accent-2)] opacity-25 blur-[120px]" />

        <div className="relative flex items-center gap-3">
          <LogoTile className="size-10" />
          <div>
            <p className="text-[17px] font-semibold tracking-tight text-white">OneSSH</p>
            <p className="text-[11px] tracking-[0.14em] text-white/60 uppercase">SSH Gateway for AI Agents</p>
          </div>
        </div>

        <div className="relative max-w-[520px]">
          <motion.h2
            initial={{ opacity: 0, y: 12 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.4, ease: 'easeOut' }}
            className="text-[34px] leading-[1.25] font-semibold tracking-tight text-white"
          >
            一个网关，
            <br />
            安全地交给 Agent 管理所有服务器
          </motion.h2>
          <p className="mt-4 text-[14px] leading-7 text-white/70">
            集中纳管 SSH 主机、密钥与令牌，按主机最小授权，随时撤销；每一次读写都有据可查。
          </p>

          <div className="mt-8 grid grid-cols-2 gap-3">
            {FEATURES.map((feature, index) => (
              <motion.div
                key={feature.title}
                initial={{ opacity: 0, y: 10 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ duration: 0.35, delay: 0.1 + index * 0.06 }}
                className="rounded-[14px] border border-white/10 bg-white/[0.06] p-4 backdrop-blur-sm"
              >
                <feature.icon size={20} weight="duotone" className="text-[#5eead4]" />
                <p className="mt-2.5 text-[13px] font-semibold text-white">{feature.title}</p>
                <p className="mt-1 text-[12px] leading-5 text-white/60">{feature.text}</p>
              </motion.div>
            ))}
          </div>
        </div>

        {/* 一段假的终端输出，交代「这是 SSH 网关」 */}
        <motion.div
          initial={{ opacity: 0, y: 10 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.4, delay: 0.35 }}
          className="relative w-full max-w-[520px] overflow-hidden rounded-[14px] border border-white/10 bg-black/40 font-mono text-[12px] shadow-2xl backdrop-blur"
        >
          <div className="flex items-center gap-1.5 border-b border-white/10 px-3.5 py-2.5">
            <span className="size-2.5 rounded-full bg-[#ff5f57]" />
            <span className="size-2.5 rounded-full bg-[#febc2e]" />
            <span className="size-2.5 rounded-full bg-[#28c840]" />
            <span className="ml-2 text-[11px] text-white/40">agent@onessh — mcp</span>
          </div>
          <div className="space-y-1 px-4 py-3 leading-6">
            <p className="text-white/50">
              <span className="text-[#5eead4]">›</span> hosts_list
            </p>
            <p className="text-white/80">
              <span className="text-[#a5b4fc]">web-01</span> 10.0.1.11 <span className="text-[#4ade80]">● online</span>
            </p>
            <p className="text-white/50">
              <span className="text-[#5eead4]">›</span> exec <span className="text-white/80">web-01 &quot;systemctl status nginx&quot;</span>
            </p>
            <p className="text-white/80">
              active (running) <span className="text-white/40">· exit 0 · 182 ms · 已记录审计</span>
              <span className="login-caret ml-1 inline-block h-3.5 w-1.5 translate-y-0.5 bg-[#5eead4]" />
            </p>
          </div>
        </motion.div>
      </aside>

      {/* ── 右侧登录表单 ── */}
      <main className="relative flex items-center justify-center px-4 py-10">
        <div
          aria-hidden
          className="pointer-events-none absolute top-1/2 left-1/2 size-[620px] -translate-x-1/2 -translate-y-[60%] rounded-full blur-[110px] lg:hidden"
          style={{ background: 'radial-gradient(circle, color-mix(in oklab, var(--accent) 30%, transparent) 0%, transparent 68%)', opacity: 0.4 }}
        />
        <motion.div
          initial={{ opacity: 0, y: 10 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.28, ease: 'easeOut' }}
          className="relative w-full max-w-[400px]"
        >
          <div className="rounded-[18px] border border-border bg-surface/90 p-7 shadow-pop backdrop-blur sm:p-9">
            <LogoTile className="size-11 lg:hidden" />
            <p className="inline-flex items-center gap-1.5 rounded-full border border-accent/30 bg-accent-soft px-2.5 py-0.5 text-[11px] font-medium text-accent max-lg:mt-5">
              <LockKey size={12} weight="fill" />
              管理员登录
            </p>
            <h1 className="mt-4 text-[24px] leading-tight font-semibold tracking-tight text-text">欢迎回来</h1>
            <p className="mt-1.5 text-[13px] text-muted">输入管理员密码进入 OneSSH 控制台</p>

            <form onSubmit={submit} className="mt-7 space-y-4">
              <Field label="管理员密码" required error={errors.password?.message}>
                {(id) => (
                  <Input
                    id={id}
                    type="password"
                    autoFocus
                    autoComplete="current-password"
                    placeholder="••••••••"
                    prefix={<LockKey size={15} />}
                    className="h-11"
                    invalid={!!errors.password || !!error}
                    {...register('password', { required: '请输入管理员密码' })}
                  />
                )}
              </Field>

              {/* 登录失败是提交结果而不是字段校验，贴在按钮上方比 toast 更容易被看到 */}
              {error && (
                <p className="flex items-start gap-2 rounded-[10px] border border-danger/25 bg-danger/8 px-3 py-2 text-[12px] leading-5 text-danger">
                  <WarningCircle size={15} weight="fill" className="mt-px shrink-0" />
                  {error}
                </p>
              )}

              <Button type="submit" variant="primary" size="lg" className="w-full" loading={isSubmitting}>
                进入控制台
              </Button>
            </form>
          </div>
          <p className="mt-5 text-center text-[12px] text-faint">
            密码来自启动配置 <code className="rounded bg-surface-2 px-1 font-mono text-[11px] text-muted">ONESSH_ADMIN_PASSWORD</code>
          </p>
        </motion.div>
      </main>
    </div>
  )
}
