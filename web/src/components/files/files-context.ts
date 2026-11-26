import { createContext, useContext } from 'react'
import type { Host } from '@/api/types'
import type { FsEntry } from '@/api/fs'

export type Clip = { op: 'copy' | 'cut'; hostId: number; entries: FsEntry[]; from: string }

export type DroppedFile = { file: File; rel: string }

export type FilesActions = {
  hosts: Host[]
  hostById: (id: number) => Host | undefined
  showHidden: boolean
  setShowHidden: (v: boolean) => void
  clipboard: Clip | null
  setClipboard: (clip: Clip | null) => void
  paste: (dstHostId: number, dstDir: string) => void
  dropTransfer: (srcHostId: number, entries: FsEntry[], dstHostId: number, dstDir: string, op: 'copy' | 'move') => void
  upload: (hostId: number, dir: string, files: DroppedFile[], existing: string[]) => void
  openEntry: (hostId: number, entry: FsEntry, siblings: FsEntry[]) => void
  editEntry: (hostId: number, entry: FsEntry) => void
  remove: (hostId: number, entries: FsEntry[]) => void
  rename: (hostId: number, entry: FsEntry) => void
  newItem: (hostId: number, dir: string, kind: 'dir' | 'file') => void
  chmod: (hostId: number, entries: FsEntry[]) => void
  compress: (hostId: number, dir: string, entries: FsEntry[]) => void
  extract: (hostId: number, entry: FsEntry) => void
  transferTo: (op: 'copy' | 'move', hostId: number, entries: FsEntry[], dir: string) => void
  download: (hostId: number, entries: FsEntry[]) => void
  properties: (hostId: number, entry: FsEntry) => void
  openTerminal: (hostId: number, dir: string) => void
  bookmarks: (hostId: number) => string[]
  toggleBookmark: (hostId: number, dir: string) => void
  rememberDir: (hostId: number, dir: string) => void
}

export const FilesContext = createContext<FilesActions | null>(null)

export function useFiles() {
  const ctx = useContext(FilesContext)
  if (!ctx) throw new Error('FilesContext missing')
  return ctx
}

export const DRAG_MIME = 'application/x-onessh-files'

/** 拖进来的本地文件 / 文件夹展开成 {file, 相对路径}；必须在 drop 事件同步阶段取出 entry */
export async function collectDropped(dt: DataTransfer): Promise<DroppedFile[]> {
  const roots: FileSystemEntry[] = []
  for (const item of Array.from(dt.items)) {
    const entry = item.kind === 'file' ? item.webkitGetAsEntry() : null
    if (entry) roots.push(entry)
  }
  if (roots.length === 0) return Array.from(dt.files).map((file) => ({ file, rel: file.name }))
  const out: DroppedFile[] = []
  const walk = async (entry: FileSystemEntry, prefix: string): Promise<void> => {
    if (entry.isFile) {
      const file = await new Promise<File>((ok, fail) => (entry as FileSystemFileEntry).file(ok, fail))
      out.push({ file, rel: prefix + entry.name })
      return
    }
    if (!entry.isDirectory) return
    const reader = (entry as FileSystemDirectoryEntry).createReader()
    // readEntries 每次最多返回 100 条，要循环到空为止
    for (;;) {
      const batch = await new Promise<FileSystemEntry[]>((ok, fail) => reader.readEntries(ok, fail))
      if (batch.length === 0) break
      for (const child of batch) await walk(child, `${prefix}${entry.name}/`)
    }
  }
  for (const root of roots) await walk(root, '')
  return out
}
