import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { useLocation, useNavigate } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { motion } from 'motion/react'
import { WarningCircle } from '@phosphor-icons/react'
import { post, type ApiError } from '@/api/client'
import { LogoTile } from '@/components/brand/logo'
import { Button } from '@/components/ui/button'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'

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
    <div className="relative flex min-h-dvh items-center justify-center overflow-hidden bg-bg px-4 py-10">
      {/* 单一冷光源：只用来把视线压到卡片上，浅色下靠 accent 混合出可感知但不脏的青灰 */}
      <div
        aria-hidden
        className="pointer-events-none absolute top-1/2 left-1/2 size-[720px] -translate-x-1/2 -translate-y-[62%] rounded-full blur-[100px]"
        style={{
          background:
            'radial-gradient(circle, color-mix(in oklab, var(--accent) 30%, transparent) 0%, transparent 68%)',
          opacity: 0.35,
        }}
      />
      <motion.div
        initial={{ opacity: 0, y: 10 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.28, ease: 'easeOut' }}
        className="relative w-full max-w-[380px] rounded-[12px] border border-border bg-surface p-7 shadow-card sm:p-8"
      >
        <LogoTile className="size-10 rounded-[10px]" />
        <h1 className="mt-5 text-[22px] leading-none font-semibold tracking-tight text-text">OneSSH</h1>
        <p className="mt-2 text-[13px] text-muted">集中式 SSH 网关控制台</p>

        <form onSubmit={submit} className="mt-7 space-y-4">
          <Field label="管理员密码" required error={errors.password?.message}>
            {(id) => (
              <Input
                id={id}
                type="password"
                autoFocus
                autoComplete="current-password"
                placeholder="••••••••"
                invalid={!!errors.password || !!error}
                {...register('password', { required: '请输入管理员密码' })}
              />
            )}
          </Field>

          {/* 登录失败是提交结果而不是字段校验，贴在按钮上方比 toast 更容易被看到 */}
          {error && (
            <p className="flex items-start gap-2 rounded-[8px] border border-danger/25 bg-danger/8 px-3 py-2 text-[12px] leading-5 text-danger">
              <WarningCircle size={15} weight="fill" className="mt-px shrink-0" />
              {error}
            </p>
          )}

          <Button type="submit" variant="primary" size="lg" className="w-full" loading={isSubmitting}>
            进入控制台
          </Button>
        </form>
      </motion.div>
    </div>
  )
}
