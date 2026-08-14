import { useEffect } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MotionConfig } from 'motion/react'
import { BrowserRouter, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { Toaster } from 'sonner'
import { UNAUTHORIZED_EVENT } from '@/api/client'
import { AppShell } from '@/components/layout/app-shell'
import { TooltipProvider } from '@/components/ui/tooltip'
import { ThemeProvider, useTheme } from '@/lib/theme'
import { ActivityPage } from '@/pages/activity'
import { DashboardPage } from '@/pages/dashboard'
import { FilesPage } from '@/pages/files'
import { HostsPage } from '@/pages/hosts'
import { JobsPage } from '@/pages/jobs'
import { KeysPage } from '@/pages/keys'
import { LoginPage } from '@/pages/login'
import { MemoriesPage } from '@/pages/memories'
import { MetricsPage } from '@/pages/metrics'
import { NotFoundPage } from '@/pages/not-found'
import { OAuthAuthorizePage } from '@/pages/oauth-authorize'
import { TerminalPage } from '@/pages/terminal'
import { TokensPage } from '@/pages/tokens'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 15_000, retry: 1, refetchOnWindowFocus: false },
  },
})

/** 任何请求拿到 401 都会广播事件，这里统一跳登录并记住来路 */
function AuthGuard() {
  const navigate = useNavigate()
  const location = useLocation()

  useEffect(() => {
    const onUnauthorized = () => {
      if (location.pathname === '/login') return
      // 必须连查询串一起记：OAuth 授权入口的全部参数都在 search 里，丢了就只能让用户从客户端重来
      navigate('/login', {
        replace: true,
        state: { from: location.pathname + location.search },
      })
    }
    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
  }, [navigate, location.pathname, location.search])

  return null
}

/** Toaster 需要读主题，因此必须在 ThemeProvider 内部 */
function ThemedToaster() {
  const { resolved } = useTheme()
  // 68 = 顶栏 56px + 12px 间距：窄屏下 toast 默认贴顶会整块盖住顶栏，
  // 汉堡钮与主题菜单在 toast 存续期间既看不见也点不到。
  return (
    <Toaster
      richColors
      position="top-right"
      theme={resolved}
      offset={{ top: 68 }}
      mobileOffset={{ top: 68, left: 16, right: 16 }}
    />
  )
}

export default function App() {
  return (
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <MotionConfig reducedMotion="user">
          <TooltipProvider delayDuration={200}>
            <BrowserRouter>
              <AuthGuard />
              <Routes>
                <Route path="/login" element={<LoginPage />} />
                {/* 授权页是给第三方客户端看的独立全页，不套 AppShell 的导航与顶栏 */}
                <Route path="/oauth/authorize" element={<OAuthAuthorizePage />} />
                <Route element={<AppShell />}>
                  <Route index element={<DashboardPage />} />
                  <Route path="hosts" element={<HostsPage />} />
                  <Route path="keys" element={<KeysPage />} />
                  <Route path="tokens" element={<TokensPage />} />
                  <Route path="terminal" element={<TerminalPage />} />
                  <Route path="files" element={<FilesPage />} />
                  <Route path="jobs" element={<JobsPage />} />
                  <Route path="activity" element={<ActivityPage />} />
                  <Route path="metrics" element={<MetricsPage />} />
                  <Route path="memories" element={<MemoriesPage />} />
                  {/* 未知路径仍留在 AppShell 内，用户可直接从侧边栏跳走 */}
                  <Route path="*" element={<NotFoundPage />} />
                </Route>
              </Routes>
            </BrowserRouter>
            <ThemedToaster />
          </TooltipProvider>
        </MotionConfig>
      </QueryClientProvider>
    </ThemeProvider>
  )
}
