export type ApiError = Error & { status: number }

/** 401 广播：AuthGuard 监听后跳转登录页 */
export const UNAUTHORIZED_EVENT = 'onessh:unauthorized'

async function responseError(response: Response): Promise<ApiError> {
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
  return error
}

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch('/api/v1' + path, {
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', ...(init?.headers || {}) },
    ...init,
  })
  if (!response.ok) throw await responseError(response)
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
 * 在交给浏览器原生下载前验证权限与远端文件可读性。HEAD 不传输正文，避免把接近
 * 100 MiB 上限的文件完整缓冲进 JS 堆；后续 GET 由浏览器下载栈流式处理。
 */
export async function checkDownload(hostId: number, path: string): Promise<void> {
  const response = await fetch(downloadUrl(hostId, path), {
    method: 'HEAD',
    credentials: 'same-origin',
  })
  if (!response.ok) throw await responseError(response)
}
