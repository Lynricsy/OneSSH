export type ApiError = Error & { status: number }

/** 401 广播：AuthGuard 监听后跳转登录页 */
export const UNAUTHORIZED_EVENT = 'onessh:unauthorized'

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch('/api/v1' + path, {
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', ...(init?.headers || {}) },
    ...init,
  })
  if (!response.ok) {
    let message = response.statusText
    try {
      const body = await response.json()
      message = body.error || message
    } catch {
      /* 非 JSON 错误体，保留 statusText */
    }
    const error = new Error(message) as ApiError
    error.status = response.status
    if (response.status === 401) window.dispatchEvent(new CustomEvent(UNAUTHORIZED_EVENT))
    throw error
  }
  if (response.status === 204) return undefined as T
  return response.json()
}

export const post = <T>(path: string, body: unknown) =>
  api<T>(path, { method: 'POST', body: JSON.stringify(body) })

export const put = <T>(path: string, body: unknown) =>
  api<T>(path, { method: 'PUT', body: JSON.stringify(body) })

export const del = (path: string) => api<void>(path, { method: 'DELETE' })

/** 上传走 multipart，不能复用 JSON wrapper（会覆盖 boundary） */
export async function uploadFile(hostId: number, path: string, file: File) {
  const body = new FormData()
  body.append('file', file)
  body.append('path', path)
  const response = await fetch(`/api/v1/sftp/${hostId}/upload`, {
    method: 'POST',
    credentials: 'same-origin',
    body,
  })
  if (!response.ok) {
    if (response.status === 401) window.dispatchEvent(new CustomEvent(UNAUTHORIZED_EVENT))
    throw new Error((await response.text()) || response.statusText)
  }
}

/** SFTP 下载/预览直链 */
export const downloadUrl = (hostId: number, path: string) =>
  `/api/v1/sftp/${hostId}/download?path=${encodeURIComponent(path)}`

/**
 * 下载文件正文。直链 <a download> 拿不到状态码——会话过期时浏览器会把 401 的错误页
 * 当作文件存下来，所以批量下载走 fetch，错误语义（含 401 广播）与 api() 保持一致。
 */
export async function fetchDownload(hostId: number, path: string): Promise<Blob> {
  const response = await fetch(downloadUrl(hostId, path), { credentials: 'same-origin' })
  if (!response.ok) {
    let message = response.statusText
    try {
      const body = await response.json()
      message = body.error || message
    } catch {
      /* 非 JSON 错误体，保留 statusText */
    }
    const error = new Error(message) as ApiError
    error.status = response.status
    if (response.status === 401) window.dispatchEvent(new CustomEvent(UNAUTHORIZED_EVENT))
    throw error
  }
  return response.blob()
}
