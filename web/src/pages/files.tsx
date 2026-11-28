import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Columns, Rows, SquareHalf } from '@phosphor-icons/react'
import { useQueryClient } from '@tanstack/react-query'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { toast } from 'sonner'
import { archiveUrl, baseName, fileUrl, fsApi, joinPath, parentOf, triggerDownload, type Conflict, type FsEntry, type FsListing } from '@/api/fs'
import { useHosts } from '@/api/queries'
import { ConfirmDialog } from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { EmptyState } from '@/components/ui/empty-state'
import { PageHeader } from '@/components/ui/page-header'
import { PageTransition } from '@/components/ui/page-transition'
import { ChmodDialog, ConflictDialog, PromptDialog, PropertiesDialog, TransferDialog, type ChmodState, type ConflictState, type PromptState, type TransferDialogState } from '@/components/files/file-dialogs'
import { FileEditor, FilePreview, isPreviewable, type EditorTarget, type PreviewTarget } from '@/components/files/file-viewer'
import { FilePane, newTab, type PaneModel } from '@/components/files/file-pane'
import { FilesContext, type Clip, type DroppedFile, type FilesActions } from '@/components/files/files-context'
import { TransferPanel, useTransfers, useUploadQueue } from '@/components/files/transfer-queue'
import { FolderOpen } from '@phosphor-icons/react'
import { cn } from '@/lib/cn'
import { kindOf } from '@/lib/file-types'

type Layout = { dual: boolean; vertical: boolean; focus: 0 | 1; panes: [PaneModel, PaneModel] }
const STORAGE = 'onessh-files-v2'

function loadLayout(): Layout | null {
  try {
    const raw = JSON.parse(localStorage.getItem(STORAGE) ?? 'null') as Layout | null
    if (raw && Array.isArray(raw.panes) && raw.panes.length === 2) return raw
  } catch {
    /* 损坏的缓存直接丢弃 */
  }
  return null
}

const readJSON = <T,>(key: string, fallback: T): T => {
  try {
    return (JSON.parse(localStorage.getItem(key) ?? 'null') as T) ?? fallback
  } catch {
    return fallback
  }
}

/** 名称冲突时为上传项生成「name (1).ext」 */
function freeName(name: string, taken: Set<string>) {
  const dot = name.lastIndexOf('.')
  const [stem, ext] = dot > 0 ? [name.slice(0, dot), name.slice(dot)] : [name, '']
  for (let i = 1; ; i++) {
    const candidate = `${stem} (${i})${ext}`
    if (!taken.has(candidate)) return candidate
  }
}

export function FilesPage() {
  const qc = useQueryClient()
  const navigateTo = useNavigate()
  const [params, setParams] = useSearchParams()
  const { data: hosts = [], isLoading } = useHosts()
  const activeHosts = useMemo(() => hosts.filter((h) => !h.archived), [hosts])
  const [layout, setLayout] = useState<Layout | null>(() => loadLayout())
  const [showHidden, setShowHidden] = useState(() => localStorage.getItem('onessh-files-hidden') === '1')
  const [clipboard, setClipboard] = useState<Clip | null>(null)
  const [bookmarks, setBookmarks] = useState<Record<number, string[]>>(() => readJSON('onessh-files-bookmarks', {}))
  const recent = useRef<Record<number, string[]>>(readJSON('onessh-files-recent', {}))

  const [prompt, setPrompt] = useState<PromptState>(null)
  const [chmod, setChmod] = useState<ChmodState>(null)
  const [transferDialog, setTransferDialog] = useState<TransferDialogState>(null)
  const [conflict, setConflict] = useState<ConflictState>(null)
  const [deleting, setDeleting] = useState<{ hostId: number; entries: FsEntry[] } | null>(null)
  const [props, setProps] = useState<{ hostId: number; entry: FsEntry } | null>(null)
  const [editor, setEditor] = useState<EditorTarget>(null)
  const [preview, setPreview] = useState<PreviewTarget>(null)

  const uploads = useUploadQueue()
  const transfers = useTransfers()
  const hostById = useCallback((id: number) => hosts.find((h) => h.id === id), [hosts])

  // 首次进入或缓存里的主机已被删除：用第一台在役主机建一个标签页
  useEffect(() => {
    if (!activeHosts.length) return
    setLayout((prev) => {
      const valid = (p: PaneModel): PaneModel | null => {
        const tabs = p.tabs.filter((t) => hosts.some((h) => h.id === t.hostId))
        return tabs.length ? { tabs, active: tabs.some((t) => t.id === p.active) ? p.active : tabs[0].id } : null
      }
      const fallback = (hostId: number): PaneModel => {
        const t = newTab(hostId)
        return { tabs: [t], active: t.id }
      }
      if (!prev) return { dual: false, vertical: false, focus: 0, panes: [fallback(activeHosts[0].id), fallback(activeHosts[1]?.id ?? activeHosts[0].id)] }
      const a = valid(prev.panes[0]) ?? fallback(activeHosts[0].id)
      const b = valid(prev.panes[1]) ?? fallback(activeHosts[1]?.id ?? activeHosts[0].id)
      return a === prev.panes[0] && b === prev.panes[1] ? prev : { ...prev, panes: [a, b] }
    })
  }, [activeHosts, hosts])

  // ?host=ID[&path=] 直达：激活或新开对应主机的标签页
  useEffect(() => {
    const hostId = Number(params.get('host'))
    if (!hostId || !layout || !hostById(hostId)) return
    const path = params.get('path') || undefined
    setLayout((prev) => {
      if (!prev) return prev
      const pane = prev.panes[prev.focus]
      const existing = pane.tabs.find((t) => t.hostId === hostId && (!path || t.path === path))
      const nextPane = existing ? { ...pane, active: existing.id } : (() => {
        const t = newTab(hostId, path ?? '~')
        return { tabs: [...pane.tabs, t], active: t.id }
      })()
      const panes = [...prev.panes] as [PaneModel, PaneModel]
      panes[prev.focus] = nextPane
      return { ...prev, panes }
    })
    params.delete('host')
    params.delete('path')
    setParams(params, { replace: true })
  }, [params, setParams, layout, hostById])

  useEffect(() => {
    if (layout) localStorage.setItem(STORAGE, JSON.stringify(layout))
  }, [layout])
  useEffect(() => localStorage.setItem('onessh-files-hidden', showHidden ? '1' : '0'), [showHidden])
  useEffect(() => localStorage.setItem('onessh-files-bookmarks', JSON.stringify(bookmarks)), [bookmarks])

  const refreshHost = useCallback((hostId: number) => void qc.invalidateQueries({ queryKey: ['fs', hostId] }), [qc])

  /** 目标目录里的同名项目：优先用缓存，没有缓存再现查一次 */
  const namesIn = useCallback(
    async (hostId: number, dir: string) => {
      const cached = qc.getQueryData<FsListing>(['fs', hostId, dir])
      if (cached) return cached.entries.map((e) => e.name)
      try {
        return (await qc.fetchQuery({ queryKey: ['fs', hostId, dir], queryFn: () => fsApi.ls(hostId, dir), staleTime: 5000 })).entries.map((e) => e.name)
      } catch {
        return []
      }
    },
    [qc],
  )

  const askConflict = (names: string[]) => new Promise<Conflict | null>((resolve) => setConflict({ names, resolve: (c) => (setConflict(null), resolve(c)) }))

  const runTransfer = useCallback(
    async (srcHostId: number, entries: FsEntry[], dstHostId: number, dstDir: string, op: 'copy' | 'move', conflictChoice?: Conflict) => {
      let choice = conflictChoice
      if (!choice) {
        const existing = new Set(await namesIn(dstHostId, dstDir))
        const clashes = entries.filter((e) => existing.has(e.name) && !(srcHostId === dstHostId && parentOf(e.path) === dstDir))
        if (clashes.length) {
          const picked = await askConflict(clashes.map((e) => e.name))
          if (!picked) return false
          choice = picked
        } else choice = 'rename'
      }
      const job = await transfers.start({ src_host_id: srcHostId, src_paths: entries.map((e) => e.path), dst_host_id: dstHostId, dst_dir: dstDir, op, conflict: choice })
      if (job) toast.message(`${op === 'move' ? '正在移动' : '正在复制'} ${entries.length} 项到 ${hostById(dstHostId)?.name}:${dstDir}`)
      return !!job
    },
    [namesIn, transfers, hostById],
  )

  const actions: FilesActions = {
    hosts,
    hostById,
    showHidden,
    setShowHidden,
    clipboard,
    setClipboard,
    paste: (dstHostId, dstDir) => {
      if (!clipboard) return toast.info('剪贴板是空的：先选中文件按 Ctrl+C 或 Ctrl+X')
      const op = clipboard.op === 'cut' ? 'move' : 'copy'
      if (op === 'move' && clipboard.hostId === dstHostId && clipboard.from === dstDir) return toast.info('已经在这个目录里了')
      void runTransfer(clipboard.hostId, clipboard.entries, dstHostId, dstDir, op).then((ok) => ok && op === 'move' && setClipboard(null))
    },
    dropTransfer: (srcHostId, entries, dstHostId, dstDir, op) => void runTransfer(srcHostId, entries, dstHostId, dstDir, op),
    upload: (hostId, dir, files: DroppedFile[], existing) => {
      const host = hostById(hostId)
      if (!host) return
      void (async () => {
        const taken = new Set(existing.length ? existing : await namesIn(hostId, dir))
        const tops = [...new Set(files.map((f) => f.rel.split('/')[0]))]
        const clashes = tops.filter((t) => taken.has(t))
        let choice: Conflict = 'overwrite'
        if (clashes.length) {
          const picked = await askConflict(clashes)
          if (!picked) return
          choice = picked
        }
        const renames = new Map<string, string>()
        if (choice === 'rename') for (const top of clashes) renames.set(top, freeName(top, taken))
        const items = files
          .filter((f) => !(choice === 'skip' && clashes.includes(f.rel.split('/')[0])))
          .map((f) => {
            const [top, ...rest] = f.rel.split('/')
            const rel = [renames.get(top) ?? top, ...rest].join('/')
            return { hostId, hostName: host.name, dest: joinPath(dir, rel), file: f.file, overwrite: true }
          })
        if (items.length) uploads.enqueue(items)
      })()
    },
    openEntry: (hostId, entry, siblings) => {
      const kind = kindOf(entry.name, entry.directory)
      const hostName = hostById(hostId)?.name ?? ''
      if (isPreviewable(entry)) setPreview({ hostId, hostName, entry, siblings })
      else if (kind === 'archive') {
        setPrompt({
          title: `解压 ${entry.name}`,
          label: '解压到目录',
          initial: parentOf(entry.path),
          confirmText: '解压',
          onSubmit: async (dest) => {
            await fsApi.extract(hostId, entry.path, dest)
            toast.success(`已解压到 ${dest}`)
            refreshHost(hostId)
          },
        })
      } else setEditor({ hostId, hostName, entry })
    },
    editEntry: (hostId, entry) => setEditor({ hostId, hostName: hostById(hostId)?.name ?? '', entry }),
    remove: (hostId, entries) => entries.length && setDeleting({ hostId, entries }),
    rename: (hostId, entry) =>
      setPrompt({
        title: '重命名',
        label: '新名称',
        initial: entry.name,
        selectStem: !entry.directory,
        confirmText: '重命名',
        onSubmit: async (name) => {
          if (name === entry.name) return
          await fsApi.rename(hostId, entry.path, joinPath(parentOf(entry.path), name))
          refreshHost(hostId)
          setClipboard((c) => (c && c.entries.some((e) => e.path === entry.path) ? null : c))
        },
      }),
    newItem: (hostId, dir, kind) =>
      setPrompt({
        title: kind === 'dir' ? '新建文件夹' : '新建文件',
        label: '名称',
        initial: kind === 'dir' ? '新建文件夹' : 'untitled.txt',
        selectStem: kind === 'file',
        confirmText: '创建',
        onSubmit: async (name) => {
          const path = joinPath(dir, name)
          if (kind === 'dir') await fsApi.mkdir(hostId, path)
          else await fsApi.create(hostId, path)
          refreshHost(hostId)
          if (kind === 'file') {
            const now = Math.floor(Date.now() / 1000)
            setEditor({ hostId, hostName: hostById(hostId)?.name ?? '', entry: { name, path, size: 0, mode: '-rw-r--r--', perm: '0644', mtime: now, directory: false, symlink: false, uid: 0, gid: 0 } })
          }
        },
      }),
    chmod: (hostId, entries) =>
      setChmod({
        entries,
        onSubmit: async (mode, recursive) => {
          const res = await fsApi.chmod(hostId, entries.map((e) => e.path), mode, recursive)
          if (res.failed) toast.warning(`${res.failed} 项修改失败：${res.results.find((r) => !r.ok)?.error ?? ''}`)
          else toast.success(`权限已改为 ${mode}`)
          refreshHost(hostId)
        },
      }),
    compress: (hostId, dir, entries) =>
      setPrompt({
        title: `压缩 ${entries.length} 项`,
        description: `压缩包保存在 ${dir}`,
        label: '压缩包名称',
        initial: `${entries.length === 1 ? entries[0].name : baseName(dir) || 'archive'}.tar.gz`,
        confirmText: '压缩',
        extra: 'archive-format',
        selectStem: true,
        onSubmit: async (name, format) => {
          const res = await fsApi.compress(hostId, entries.map((e) => e.path), name, format ?? 'tar.gz')
          toast.success(`已生成 ${baseName(res.path)}`)
          refreshHost(hostId)
        },
      }),
    extract: (hostId, entry) => {
      const id = toast.loading(`正在解压 ${entry.name}…`)
      fsApi
        .extract(hostId, entry.path)
        .then((res) => {
          toast.success(`已解压到 ${res.dest}`, { id })
          refreshHost(hostId)
        })
        .catch((e: Error) => toast.error(e.message, { id }))
    },
    transferTo: (op, hostId, entries, dir) => {
      const other = layout?.panes[layout.focus === 0 ? 1 : 0]
      const otherTab = other?.tabs.find((t) => t.id === other.active)
      const useOther = layout?.dual && otherTab
      setTransferDialog({
        op,
        srcHostId: hostId,
        entries,
        defaultHostId: useOther ? otherTab.hostId : (activeHosts.find((h) => h.id !== hostId)?.id ?? hostId),
        defaultDir: useOther ? otherTab.path : dir,
        onSubmit: (dstHostId, dstDir, realOp, c) => void runTransfer(hostId, entries, dstHostId, dstDir, realOp, c),
      })
    },
    download: (hostId, entries) => {
      if (entries.length === 1 && !entries[0].directory) triggerDownload(fileUrl(hostId, entries[0].path), entries[0].name)
      else {
        const parents = new Set(entries.map((e) => parentOf(e.path)))
        if (parents.size > 1) return toast.error('只能打包同一目录下的项目')
        triggerDownload(archiveUrl(hostId, entries.map((e) => e.path)))
        toast.message('正在服务器上实时打包，下载进度见浏览器下载栏')
      }
    },
    properties: (hostId, entry) => setProps({ hostId, entry }),
    openTerminal: (hostId, dir) => {
      const host = hostById(hostId)
      if (host) navigateTo(`/terminal?host=${encodeURIComponent(host.name)}&cwd=${encodeURIComponent(dir)}`)
    },
    bookmarks: (hostId) => bookmarks[hostId] ?? [],
    toggleBookmark: (hostId, dir) =>
      setBookmarks((b) => {
        const list = b[hostId] ?? []
        return { ...b, [hostId]: list.includes(dir) ? list.filter((d) => d !== dir) : [...list, dir].slice(-30) }
      }),
    rememberDir: (hostId, dir) => {
      const list = [dir, ...(recent.current[hostId] ?? []).filter((d) => d !== dir)].slice(0, 8)
      recent.current = { ...recent.current, [hostId]: list }
      localStorage.setItem('onessh-files-recent', JSON.stringify(recent.current))
    },
  }

  const recentDirs = useCallback((hostId: number) => [...new Set([...(bookmarks[hostId] ?? []), ...(recent.current[hostId] ?? [])])].slice(0, 10), [bookmarks])

  if (isLoading) return <div className="h-[60vh] animate-pulse rounded-[16px] bg-surface-2/60" />
  if (!activeHosts.length || !layout) {
    return (
      <PageTransition>
        <PageHeader title="文件管理" subtitle="多服务器 SFTP 文件管理" />
        <Card>
          <EmptyState icon={<FolderOpen size={22} />} title="还没有可用的主机" description="添加主机后即可在这里浏览和管理服务器文件。" action={<Button variant="primary" onClick={() => navigateTo('/hosts?new=1')}>添加主机</Button>} />
        </Card>
      </PageTransition>
    )
  }

  const setPane = (i: 0 | 1) => (next: PaneModel) =>
    setLayout((prev) => {
      if (!prev) return prev
      const panes = [...prev.panes] as [PaneModel, PaneModel]
      panes[i] = next
      return { ...prev, panes }
    })

  return (
    <FilesContext.Provider value={actions}>
      <div className="flex h-[calc(100dvh-7rem)] min-h-[520px] flex-col">
        <PageHeader
          title="文件管理"
          subtitle="多服务器标签页 · 拖拽 / 复制粘贴跨服务器传输 · 在线预览与编辑"
          actions={
            <div className="inline-flex rounded-[10px] border border-border bg-surface-2 p-0.5">
              {[
                { label: '单栏', icon: <SquareHalf size={15} />, active: !layout.dual, run: () => setLayout({ ...layout, dual: false, focus: 0 }) },
                { label: '左右双栏', icon: <Columns size={15} />, active: layout.dual && !layout.vertical, run: () => setLayout({ ...layout, dual: true, vertical: false }) },
                { label: '上下双栏', icon: <Rows size={15} />, active: layout.dual && layout.vertical, run: () => setLayout({ ...layout, dual: true, vertical: true }) },
              ].map((b) => (
                <button
                  key={b.label}
                  type="button"
                  onClick={b.run}
                  className={cn('inline-flex items-center gap-1.5 rounded-[8px] px-2.5 py-1 text-[12px] font-medium transition-all', b.active ? 'bg-surface text-text shadow-card' : 'text-muted hover:text-text')}
                >
                  {b.icon}
                  {b.label}
                </button>
              ))}
            </div>
          }
        />
        <div className={cn('grid min-h-0 flex-1 gap-3', layout.dual && (layout.vertical ? 'grid-rows-2' : 'lg:grid-cols-2 max-lg:grid-rows-2'))}>
          {([0, 1] as const).slice(0, layout.dual ? 2 : 1).map((i) => (
            <FilePane
              key={i}
              model={layout.panes[i]}
              onChange={setPane(i)}
              focused={layout.dual && layout.focus === i}
              onFocus={() => layout.focus !== i && setLayout((prev) => (prev ? { ...prev, focus: i } : prev))}
              compact={layout.dual && !layout.vertical}
              paneLabel={layout.dual ? (i === 0 ? (layout.vertical ? '上' : '左') : layout.vertical ? '下' : '右') : undefined}
            />
          ))}
        </div>
      </div>

      <PromptDialog state={prompt} onClose={() => setPrompt(null)} />
      <ChmodDialog state={chmod} onClose={() => setChmod(null)} />
      <TransferDialog state={transferDialog} hosts={hosts} recentDirs={recentDirs} onClose={() => setTransferDialog(null)} />
      <ConflictDialog state={conflict} />
      <PropertiesDialog entry={props?.entry ?? null} host={props ? hostById(props.hostId) : undefined} onClose={() => setProps(null)} />
      <ConfirmDialog
        open={deleting != null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={deleting && deleting.entries.length === 1 ? `删除 ${deleting.entries[0].name}？` : `删除 ${deleting?.entries.length ?? 0} 个项目？`}
        description={`将从 ${deleting ? hostById(deleting.hostId)?.name : ''} 上永久删除${deleting?.entries.some((e) => e.directory) ? '（包括文件夹里的全部内容）' : ''}，无法恢复：${deleting?.entries.slice(0, 5).map((e) => e.name).join('、') ?? ''}${(deleting?.entries.length ?? 0) > 5 ? ' 等' : ''}`}
        confirmText="永久删除"
        danger
        onConfirm={async () => {
          if (!deleting) return
          const res = await fsApi.remove(deleting.hostId, deleting.entries.map((e) => e.path))
          if (res.failed) toast.error(`${res.failed} 项删除失败：${res.results.find((r) => !r.ok)?.error ?? ''}`)
          else toast.success(`已删除 ${deleting.entries.length} 项`)
          refreshHost(deleting.hostId)
          setClipboard((c) => (c && c.hostId === deleting.hostId && c.entries.some((e) => deleting.entries.some((d) => d.path === e.path)) ? null : c))
        }}
      />
      <FileEditor target={editor} onClose={() => setEditor(null)} onSaved={refreshHost} />
      <FilePreview
        target={preview}
        onClose={() => setPreview(null)}
        onEdit={(entry) => {
          if (!preview) return
          setPreview(null)
          setEditor({ hostId: preview.hostId, hostName: preview.hostName, entry })
        }}
      />
      <TransferPanel uploads={uploads.tasks} transfers={transfers.transfers} onCancelUpload={uploads.cancel} onClearUploads={uploads.clearFinished} onCancelTransfer={(id, remove) => void transfers.cancel(id, remove)} />
    </FilesContext.Provider>
  )
}
