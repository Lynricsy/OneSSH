import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { api, post, put } from './client'

export type AccountSession = {
  id: string
  current: boolean
  created_at: number
  last_seen: number
  expires_at: number
  ip: string
  user_agent: string
  remember: boolean
}

export type AccountLogin = { ts: number; ip: string; user_agent: string; username: string; ok: boolean; reason: string }

export type AccountInfo = {
  username: string
  password_source: 'env' | 'custom'
  password_changed_at: number
  store_enabled: boolean
  sessions: AccountSession[]
  logins: AccountLogin[]
  failed_24h: number
}

export type SystemInfo = {
  version: string
  started_at: number
  listen: string
  data_dir: string
  master_key_source: string
  go_version: string
  platform: string
  pid: number
  db_bytes: number
  hosts: number
  keys: number
  tokens: number
  geoip: boolean
}

export const useAccount = () => useQuery({ queryKey: ['account'], queryFn: () => api<AccountInfo>('/account'), staleTime: 30_000 })
export const useSystemInfo = () => useQuery({ queryKey: ['system'], queryFn: () => api<SystemInfo>('/system'), staleTime: 60_000 })

export function useChangePassword() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: { current: string; new: string }) => put<{ revoked_sessions: number }>('/account/password', body),
    onSuccess: (res) => {
      toast.success(res.revoked_sessions ? `密码已修改，其他 ${res.revoked_sessions} 个登录会话已下线` : '密码已修改')
      void qc.invalidateQueries({ queryKey: ['account'] })
    },
  })
}

export function useChangeUsername() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (username: string) => put<{ username: string }>('/account/profile', { username }),
    onSuccess: () => {
      toast.success('用户名已修改，下次登录请使用新用户名')
      void qc.invalidateQueries({ queryKey: ['account'] })
    },
    onError: (e: Error) => toast.error(e.message),
  })
}

export function useRevokeSession() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api<void>(`/account/sessions/${id}`, { method: 'DELETE' }),
    onSuccess: () => {
      toast.success('该会话已下线')
      void qc.invalidateQueries({ queryKey: ['account'] })
    },
    onError: (e: Error) => toast.error(e.message),
  })
}

export function useRevokeOthers() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => post<{ revoked_sessions: number }>('/account/sessions/revoke-others', {}),
    onSuccess: (res) => {
      toast.success(`已下线 ${res.revoked_sessions} 个其他会话`)
      void qc.invalidateQueries({ queryKey: ['account'] })
    },
    onError: (e: Error) => toast.error(e.message),
  })
}

export async function logout() {
  await fetch('/api/v1/logout', { method: 'POST', credentials: 'same-origin' }).catch(() => undefined)
}

/** 从 User-Agent 粗略识别浏览器与系统，只用于会话列表展示 */
export function describeUA(ua: string) {
  const browser = /Edg\//.test(ua) ? 'Edge' : /OPR\//.test(ua) ? 'Opera' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : /curl|python|Go-http/i.test(ua) ? '脚本 / API' : '未知浏览器'
  const os = /Windows NT 10/.test(ua) ? 'Windows' : /Windows/.test(ua) ? 'Windows' : /Mac OS X/.test(ua) ? 'macOS' : /Android/.test(ua) ? 'Android' : /iPhone|iPad/.test(ua) ? 'iOS' : /Linux/.test(ua) ? 'Linux' : ''
  const mobile = /Mobile|Android|iPhone/.test(ua)
  return { browser, os, mobile }
}
