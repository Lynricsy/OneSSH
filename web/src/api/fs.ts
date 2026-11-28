import { api, post, put, UNAUTHORIZED_EVENT } from './client'

/** 文件管理器接口（/api/v1/sftp/*），与后端 sftp_ops.go / transfers.go 一一对应 */

export type FsEntry = {
  name: string
  path: string
  size: number
  mode: string
  perm: string
  mtime: number
  directory: boolean
  symlink: boolean
  symlink_target?: string
  uid: number
  gid: number
}

export type FsListing = {
  path: string
  parent: string
  home: string
  entries: FsEntry[]
  truncated: boolean
}

export type FsReadResult = {
  path: string
  size: number
  mtime: number
  perm: string
  sha256: string
  binary: boolean
  content?: string
}

export type ItemResult = { path: string; ok: boolean; error?: string }
export type BatchResult = { results: ItemResult[]; failed: number }

export type TransferStatus = 'running' | 'done' | 'partial' | 'failed' | 'cancelled'

export type Transfer = {
  id: string
  op: 'copy' | 'move'
  src_host_id: number
  src_host: string
  dst_host_id: number
  dst_host: string
  src_paths: string[]
  dst_dir: string
  conflict: string
  status: TransferStatus
  total_bytes: number
  done_bytes: number
  total_files: number
  done_files: number
  skipped: number
  current: string
  errors: string[]
  started_at: number
  finished_at?: number
}

export type Conflict = 'error' | 'overwrite' | 'rename' | 'skip'

const q = (path: string) => encodeURIComponent(path)

export const fsApi = {
  ls: (hostId: number, path: string) => api<FsListing>(`/sftp/${hostId}/ls?path=${q(path)}`),
  stat: (hostId: number, path: string) =>
    api<{ exists: boolean; path: string; entry?: FsEntry }>(`/sftp/${hostId}/stat?path=${q(path)}`),
  read: (hostId: number, path: string) => api<FsReadResult>(`/sftp/${hostId}/read?path=${q(path)}`),
  write: (hostId: number, path: string, content: string, expected_sha256?: string) =>
    put<{ path: string; sha256: string; size: number; mtime: number }>(`/sftp/${hostId}/write`, {
      path,
      content,
      expected_sha256,
    }),
  mkdir: (hostId: number, path: string) => post<{ path: string }>(`/sftp/${hostId}/mkdir`, { path }),
  create: (hostId: number, path: string) => post<{ path: string }>(`/sftp/${hostId}/create`, { path }),
  rename: (hostId: number, from: string, to: string, overwrite = false) =>
    post<{ path: string }>(`/sftp/${hostId}/rename`, { from, to, overwrite }),
  remove: (hostId: number, paths: string[]) => post<BatchResult>(`/sftp/${hostId}/delete`, { paths }),
  chmod: (hostId: number, paths: string[], mode: string, recursive: boolean) =>
    post<BatchResult>(`/sftp/${hostId}/chmod`, { paths, mode, recursive }),
  compress: (hostId: number, paths: string[], archive: string, format: 'tar.gz' | 'zip') =>
    post<{ path: string }>(`/sftp/${hostId}/compress`, { paths, archive, format }),
  extract: (hostId: number, path: string, dest?: string) =>
    post<{ dest: string }>(`/sftp/${hostId}/extract`, { path, dest }),
  transfers: () => api<Transfer[]>('/sftp/transfers'),
  startTransfer: (body: {
    src_host_id: number
    src_paths: string[]
    dst_host_id: number
    dst_dir: string
    op: 'copy' | 'move'
    conflict: Conflict
  }) => post<Transfer>('/sftp/transfers', body),
  cancelTransfer: (id: string, remove = false) =>
    api<void>(`/sftp/transfers/${id}${remove ? '?remove=1' : ''}`, { method: 'DELETE' }),
}

export const fileUrl = (hostId: number, path: string, inline = false) =>
  `/api/v1/sftp/${hostId}/download?path=${q(path)}${inline ? '&inline=1' : ''}`

export const archiveUrl = (hostId: number, paths: string[]) =>
  `/api/v1/sftp/${hostId}/archive?${paths.map((p) => `path=${q(p)}`).join('&')}`

/** 用隐藏 a 标签触发浏览器原生下载：大文件不会进 JS 内存，进度显示在浏览器下载栏 */
export function triggerDownload(url: string, name?: string) {
  const a = document.createElement('a')
  a.href = url
  if (name) a.download = name
  a.rel = 'noopener'
  document.body.appendChild(a)
  a.click()
  a.remove()
}

export type UploadHandle = { promise: Promise<void>; abort: () => void }

/** XHR 上传：fetch 拿不到上传进度。dest 为完整目标路径 */
export function uploadWithProgress(
  hostId: number,
  dest: string,
  file: File,
  onProgress: (loaded: number, total: number) => void,
  overwrite = true,
): UploadHandle {
  const xhr = new XMLHttpRequest()
  const promise = new Promise<void>((resolve, reject) => {
    const body = new FormData()
    // 顺序重要：后端流式解析，path / overwrite 必须先于 file
    body.append('path', dest)
    body.append('overwrite', overwrite ? '1' : '0')
    body.append('file', file)
    xhr.open('POST', `/api/v1/sftp/${hostId}/upload`)
    xhr.withCredentials = true
    xhr.upload.onprogress = (event) => onProgress(event.loaded, event.total || file.size)
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) return resolve()
      if (xhr.status === 401) window.dispatchEvent(new CustomEvent(UNAUTHORIZED_EVENT))
      let message = xhr.statusText || `HTTP ${xhr.status}`
      try {
        message = JSON.parse(xhr.responseText).error || message
      } catch {
        if (xhr.responseText) message = xhr.responseText
      }
      reject(new Error(message))
    }
    xhr.onerror = () => reject(new Error('网络错误，上传中断'))
    xhr.onabort = () => reject(new Error('已取消'))
    xhr.send(body)
  })
  return { promise, abort: () => xhr.abort() }
}

export const joinPath = (dir: string, name: string) => (dir === '/' ? `/${name}` : `${dir.replace(/\/+$/, '')}/${name}`)
export const parentOf = (path: string) => {
  const trimmed = path.replace(/\/+$/, '')
  const i = trimmed.lastIndexOf('/')
  return i <= 0 ? '/' : trimmed.slice(0, i)
}
export const baseName = (path: string) => path.replace(/\/+$/, '').split('/').pop() || '/'
