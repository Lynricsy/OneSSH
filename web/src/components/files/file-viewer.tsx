import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import * as DialogPrimitive from '@radix-ui/react-dialog'
import {
  ArrowClockwise,
  ArrowsInSimple,
  ArrowsOutSimple,
  CaretLeft,
  CaretRight,
  DownloadSimple,
  FloppyDisk,
  MagnifyingGlassMinus,
  MagnifyingGlassPlus,
  TextAlignLeft,
  WarningCircle,
  X,
} from '@phosphor-icons/react'
import { toast } from 'sonner'
import { fileUrl, fsApi, triggerDownload, type FsEntry } from '@/api/fs'
import type { ApiError } from '@/api/client'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/alert-dialog'
import { Select } from '@/components/ui/select'
import { Spinner } from '@/components/ui/spinner'
import { cn } from '@/lib/cn'
import { formatMtime, formatSize, iconOf, kindOf, LANGUAGES, languageOf } from '@/lib/file-types'

const CodeEditor = lazy(() => import('./code-editor').then((m) => ({ default: m.CodeEditor })))

function Shell({ open, onClose, children, label }: { open: boolean; onClose: () => void; children: React.ReactNode; label: string }) {
  return (
    <DialogPrimitive.Root open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/55 backdrop-blur-[3px]" />
        <DialogPrimitive.Content
          className="fixed inset-3 z-50 flex flex-col overflow-hidden rounded-[14px] border border-border bg-surface shadow-pop focus:outline-none md:inset-6"
          onEscapeKeyDown={(event) => {
            // 编辑器里的 Esc 留给搜索框等内部组件，关闭交给按钮与未保存确认
            if ((event.target as HTMLElement)?.closest?.('.cm-editor')) event.preventDefault()
          }}
        >
          <DialogPrimitive.Title className="sr-only">{label}</DialogPrimitive.Title>
          <DialogPrimitive.Description className="sr-only">{label}</DialogPrimitive.Description>
          {children}
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}

/* ── 文本编辑器 ── */

export type EditorTarget = { hostId: number; hostName: string; entry: FsEntry } | null

export function FileEditor({ target, onClose, onSaved }: { target: EditorTarget; onClose: () => void; onSaved: (hostId: number) => void }) {
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [binary, setBinary] = useState(false)
  const [content, setContent] = useState('')
  const [original, setOriginal] = useState('')
  const [sha, setSha] = useState('')
  const [meta, setMeta] = useState({ size: 0, mtime: 0, perm: '' })
  const [language, setLanguage] = useState('text')
  const [wrap, setWrap] = useState(() => localStorage.getItem('onessh-editor-wrap') === '1')
  const [saving, setSaving] = useState(false)
  const [cursor, setCursor] = useState({ line: 1, col: 1 })
  const [confirmClose, setConfirmClose] = useState(false)
  const [conflict, setConflict] = useState(false)
  const [forceText, setForceText] = useState(false)
  const dirty = content !== original

  const load = useCallback(async () => {
    if (!target) return
    setLoading(true)
    setError('')
    try {
      const res = await fsApi.read(target.hostId, target.entry.path)
      setBinary(res.binary)
      setContent(res.content ?? '')
      setOriginal(res.content ?? '')
      setSha(res.sha256)
      setMeta({ size: res.size, mtime: res.mtime, perm: res.perm })
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [target])

  useEffect(() => {
    if (!target) return
    setLanguage(languageOf(target.entry.name))
    setForceText(false)
    setConflict(false)
    void load()
  }, [target, load])

  useEffect(() => localStorage.setItem('onessh-editor-wrap', wrap ? '1' : '0'), [wrap])

  useEffect(() => {
    if (!dirty) return
    const guard = (event: BeforeUnloadEvent) => event.preventDefault()
    window.addEventListener('beforeunload', guard)
    return () => window.removeEventListener('beforeunload', guard)
  }, [dirty])

  const save = useCallback(
    async (force = false) => {
      if (!target || saving) return
      setSaving(true)
      try {
        const res = await fsApi.write(target.hostId, target.entry.path, content, force ? undefined : sha)
        setOriginal(content)
        setSha(res.sha256)
        setMeta((m) => ({ ...m, size: res.size, mtime: res.mtime }))
        setConflict(false)
        toast.success(`已保存 ${target.entry.name}`)
        onSaved(target.hostId)
      } catch (e) {
        if ((e as ApiError).status === 409) setConflict(true)
        else toast.error((e as Error).message)
      } finally {
        setSaving(false)
      }
    },
    [target, content, sha, saving, onSaved],
  )

  // Ctrl+S 在窗口层捕获：既不依赖编辑器是否获得焦点，也能拦住浏览器自己的「另存为网页」
  const saveRef = useRef(save)
  saveRef.current = save
  useEffect(() => {
    if (!target) return
    const onKey = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && !event.shiftKey && event.key.toLowerCase() === 's') {
        event.preventDefault()
        event.stopPropagation()
        void saveRef.current()
      }
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [target])

  const requestClose = () => (dirty ? setConfirmClose(true) : onClose())
  const onCursor = useCallback((line: number, col: number) => setCursor({ line, col }), [])
  const { icon: Glyph, color } = target ? iconOf(target.entry.name, false) : { icon: X, color: '' }
  const lines = useMemo(() => (content ? content.split('\n').length : 0), [content])

  return (
    <Shell open={target != null} onClose={requestClose} label="编辑文件">
      {target && (
        <>
          <div className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-2.5">
            <Glyph size={20} weight="duotone" style={{ color }} />
            <div className="min-w-0 flex-1">
              <p className="flex items-center gap-2 truncate text-[14px] font-semibold text-text">
                {target.entry.name}
                {dirty && <span className="size-2 shrink-0 rounded-full bg-warning" title="有未保存的修改" />}
              </p>
              <p className="truncate font-mono text-[11.5px] text-muted">
                {target.hostName}:{target.entry.path}
              </p>
            </div>
            <Select className="h-8 w-36 text-[12px]" value={language} onChange={setLanguage} options={LANGUAGES} />
            <Button size="sm" variant={wrap ? 'secondary' : 'ghost'} onClick={() => setWrap((w) => !w)} title="自动换行">
              <TextAlignLeft size={15} />
              换行
            </Button>
            <Button size="sm" variant="ghost" onClick={() => void load()} title="从服务器重新加载" disabled={loading}>
              <ArrowClockwise size={15} />
            </Button>
            <Button size="sm" variant="ghost" onClick={() => triggerDownload(fileUrl(target.hostId, target.entry.path), target.entry.name)} title="下载">
              <DownloadSimple size={15} />
            </Button>
            <Button size="sm" variant="primary" onClick={() => void save()} loading={saving} disabled={!dirty || binary}>
              <FloppyDisk size={15} />
              保存
              <kbd className="ml-1 rounded bg-black/15 px-1 font-mono text-[10px]">Ctrl S</kbd>
            </Button>
            <Button size="icon" variant="ghost" onClick={requestClose} aria-label="关闭">
              <X size={16} />
            </Button>
          </div>
          {conflict && (
            <div className="flex flex-wrap items-center gap-2 border-b border-warning/30 bg-warning/10 px-4 py-2 text-[12.5px] text-text">
              <WarningCircle size={16} weight="fill" className="text-warning" />
              <span className="flex-1">文件在你打开之后被修改过（可能是 Agent 或其他人）。</span>
              <Button size="sm" variant="outline" onClick={() => void load()}>
                放弃我的修改并重新加载
              </Button>
              <Button size="sm" variant="danger" onClick={() => void save(true)}>
                仍然覆盖保存
              </Button>
            </div>
          )}
          <div className="relative min-h-0 flex-1">
            {loading ? (
              <div className="grid h-full place-items-center">
                <Spinner className="size-6 text-accent" />
              </div>
            ) : error ? (
              <div className="grid h-full place-items-center px-6 text-center">
                <div>
                  <WarningCircle size={28} className="mx-auto text-danger" />
                  <p className="mt-2 text-[14px] text-text">{error}</p>
                  <Button className="mt-4" variant="outline" onClick={() => triggerDownload(fileUrl(target.hostId, target.entry.path), target.entry.name)}>
                    下载文件
                  </Button>
                </div>
              </div>
            ) : binary && !forceText ? (
              <div className="grid h-full place-items-center px-6 text-center">
                <div>
                  <p className="text-[14px] font-medium text-text">这看起来是一个二进制文件</p>
                  <p className="mt-1 text-[13px] text-muted">在线编辑可能会损坏它，建议下载后用专门的工具处理。</p>
                  <div className="mt-4 flex justify-center gap-2">
                    <Button variant="outline" onClick={() => triggerDownload(fileUrl(target.hostId, target.entry.path), target.entry.name)}>
                      下载
                    </Button>
                  </div>
                </div>
              </div>
            ) : (
              <Suspense fallback={<div className="grid h-full place-items-center"><Spinner className="size-6 text-accent" /></div>}>
                <CodeEditor value={content} onChange={setContent} language={language} wrap={wrap} onCursor={onCursor} />
              </Suspense>
            )}
          </div>
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-border bg-surface-2/50 px-4 py-1.5 font-mono text-[11px] text-muted">
            <span>
              行 {cursor.line}，列 {cursor.col}
            </span>
            <span>{lines} 行</span>
            <span>{formatSize(new Blob([content]).size)}</span>
            <span>权限 {meta.perm}</span>
            <span>修改于 {formatMtime(meta.mtime)}</span>
            <span className="ml-auto">UTF-8 · Ctrl+F 搜索 · Ctrl+H 替换</span>
          </div>
          <ConfirmDialog
            open={confirmClose}
            onOpenChange={setConfirmClose}
            title="放弃未保存的修改？"
            description={`${target.entry.name} 有修改还没保存，关闭后会丢失。`}
            confirmText="放弃修改"
            danger
            onConfirm={() => {
              setOriginal(content)
              onClose()
            }}
          />
        </>
      )}
    </Shell>
  )
}

/* ── 媒体预览：图片 / 视频 / 音频 / PDF，左右键切换同目录文件 ── */

export type PreviewTarget = { hostId: number; hostName: string; entry: FsEntry; siblings: FsEntry[] } | null

export const isPreviewable = (entry: FsEntry) => ['image', 'video', 'audio', 'pdf'].includes(kindOf(entry.name, entry.directory))

export function FilePreview({ target, onClose, onEdit }: { target: PreviewTarget; onClose: () => void; onEdit: (entry: FsEntry) => void }) {
  const [index, setIndex] = useState(0)
  const [zoom, setZoom] = useState(1)
  const [fit, setFit] = useState(true)
  const list = useMemo(() => target?.siblings.filter(isPreviewable) ?? [], [target])

  useEffect(() => {
    if (!target) return
    setIndex(Math.max(0, list.findIndex((e) => e.path === target.entry.path)))
    setZoom(1)
    setFit(true)
  }, [target, list])

  const entry = list[index]
  const go = useCallback((delta: number) => {
    if (list.length < 2) return
    setIndex((i) => (i + delta + list.length) % list.length)
    setZoom(1)
  }, [list.length])

  useEffect(() => {
    if (!target) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'ArrowRight') go(1)
      else if (event.key === 'ArrowLeft') go(-1)
      else if (event.key === '+' || event.key === '=') setZoom((z) => Math.min(8, z * 1.25))
      else if (event.key === '-') setZoom((z) => Math.max(0.1, z / 1.25))
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [target, go])

  if (!target || !entry) return <Shell open={false} onClose={onClose} label="预览">{null}</Shell>
  const kind = kindOf(entry.name)
  const src = fileUrl(target.hostId, entry.path, true)

  return (
    <Shell open onClose={onClose} label="预览">
      <div className="flex items-center gap-2 border-b border-border px-4 py-2.5">
        <div className="min-w-0 flex-1">
          <p className="truncate text-[14px] font-semibold text-text">{entry.name}</p>
          <p className="truncate text-[11.5px] text-muted">
            {target.hostName} · {formatSize(entry.size)} · {formatMtime(entry.mtime)}
            {list.length > 1 && ` · ${index + 1} / ${list.length}`}
          </p>
        </div>
        {kind === 'image' && (
          <>
            <Button size="icon" variant="ghost" onClick={() => setZoom((z) => Math.max(0.1, z / 1.25))} aria-label="缩小">
              <MagnifyingGlassMinus size={16} />
            </Button>
            <span className="w-12 text-center text-[12px] text-muted tabular-nums">{Math.round(zoom * 100)}%</span>
            <Button size="icon" variant="ghost" onClick={() => setZoom((z) => Math.min(8, z * 1.25))} aria-label="放大">
              <MagnifyingGlassPlus size={16} />
            </Button>
            <Button size="icon" variant="ghost" onClick={() => setFit((f) => !f)} aria-label="适应窗口" title={fit ? '原始尺寸' : '适应窗口'}>
              {fit ? <ArrowsOutSimple size={16} /> : <ArrowsInSimple size={16} />}
            </Button>
            {entry.name.toLowerCase().endsWith('.svg') && (
              <Button size="sm" variant="ghost" onClick={() => onEdit(entry)}>
                编辑源码
              </Button>
            )}
          </>
        )}
        <Button size="sm" variant="outline" onClick={() => triggerDownload(fileUrl(target.hostId, entry.path), entry.name)}>
          <DownloadSimple size={15} />
          下载
        </Button>
        <Button size="icon" variant="ghost" onClick={onClose} aria-label="关闭">
          <X size={16} />
        </Button>
      </div>
      <div className="relative flex min-h-0 flex-1 items-center justify-center overflow-auto bg-[repeating-conic-gradient(var(--surface-2)_0%_25%,transparent_0%_50%)] bg-[length:22px_22px]">
        {kind === 'image' && (
          <img
            key={entry.path}
            src={src}
            alt={entry.name}
            className={cn('select-none', fit && zoom === 1 && 'max-h-full max-w-full object-contain')}
            style={!fit || zoom !== 1 ? { transform: `scale(${zoom})`, transformOrigin: 'center' } : undefined}
            draggable={false}
          />
        )}
        {kind === 'video' && <video key={entry.path} src={src} controls autoPlay className="max-h-full max-w-full bg-black" />}
        {kind === 'audio' && (
          <div className="rounded-[16px] border border-border bg-surface p-8 text-center shadow-card">
            <p className="mb-4 text-[15px] font-medium text-text">{entry.name}</p>
            <audio key={entry.path} src={src} controls autoPlay className="w-[420px] max-w-full" />
          </div>
        )}
        {kind === 'pdf' && <iframe key={entry.path} src={src} title={entry.name} className="h-full w-full border-0 bg-white" />}
        {list.length > 1 && (
          <>
            <button type="button" onClick={() => go(-1)} aria-label="上一个" className="absolute top-1/2 left-3 grid size-10 -translate-y-1/2 place-items-center rounded-full bg-black/45 text-white backdrop-blur hover:bg-black/65">
              <CaretLeft size={18} weight="bold" />
            </button>
            <button type="button" onClick={() => go(1)} aria-label="下一个" className="absolute top-1/2 right-3 grid size-10 -translate-y-1/2 place-items-center rounded-full bg-black/45 text-white backdrop-blur hover:bg-black/65">
              <CaretRight size={18} weight="bold" />
            </button>
          </>
        )}
      </div>
      {list.length > 1 && kind === 'image' && (
        <div className="flex gap-1.5 overflow-x-auto border-t border-border bg-surface-2/40 p-2">
          {list.map((item, i) =>
            kindOf(item.name) === 'image' ? (
              <button key={item.path} type="button" onClick={() => setIndex(i)} className={cn('size-14 shrink-0 overflow-hidden rounded-[8px] border-2 bg-surface', i === index ? 'border-accent' : 'border-transparent opacity-70 hover:opacity-100')}>
                <img src={fileUrl(target.hostId, item.path, true)} alt="" loading="lazy" className="size-full object-cover" />
              </button>
            ) : null,
          )}
        </div>
      )}
    </Shell>
  )
}
