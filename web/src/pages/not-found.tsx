import { useLocation, useNavigate } from 'react-router-dom'
import { Button } from '@/components/ui/button'
import { Code } from '@/components/ui/code'
import { PageTransition } from '@/components/ui/page-transition'

export function NotFoundPage() {
  const navigate = useNavigate()
  const { pathname } = useLocation()

  return (
    <PageTransition>
      <div className="flex min-h-[58vh] flex-col items-center justify-center gap-5 py-16 text-center">
        {/* 404 只是氛围字，真正要读的信息在下面两行，所以压到最弱的中性色 */}
        <p className="text-[64px] leading-none font-semibold tracking-tight text-border-strong tabular-nums">
          404
        </p>
        <div className="space-y-1.5">
          <p className="text-sm font-medium text-text">页面不存在</p>
          <p className="flex flex-wrap items-center justify-center gap-1 text-[13px] text-muted">
            <Code>{pathname}</Code>
            不在控制台的路由表里。
          </p>
        </div>
        <Button variant="primary" onClick={() => navigate('/')}>
          返回概览
        </Button>
      </div>
    </PageTransition>
  )
}
