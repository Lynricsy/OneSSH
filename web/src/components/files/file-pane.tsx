import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  ArrowClockwise,
  ArrowLeft,
  ArrowRight,
  ArrowUp,
  Archive,
  CaretDown,
  ClipboardText,
  Copy,
  CopySimple,
  DownloadSimple,
  Eye,
  EyeSlash,
  FilePlus,
  FolderOpen,
  FolderPlus,
  FolderSimpleStar,
  House,
  Info,
  Key,
  MagnifyingGlass,
  PencilSimple,
  PencilSimpleLine,
  Plus,
  Scissors,
  Star,
  Terminal,
  Trash,
  TrayArrowUp,
  UploadSimple,
  X,
  ArrowsLeftRight,
  ArrowSquareOut,
  Folders,
} from '@phosphor-icons/react'
import { useQuery } from '@tanstack/react-query'
import { toast } from 'sonner'
import { baseName, fsApi, joinPath, parentOf, type FsEntry } from '@/api/fs'
import type { ApiError } from '@/api/client'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Spinner } from '@/components/ui/spinner'
import { Tooltip } from '@/components/ui/tooltip'
import { CountryFlag } from '@/components/country-flag'
import { cn } from '@/lib/cn'
import { formatMtime, formatSize, iconOf, isExtractable, kindOf } from '@/lib/file-types'
import { useServerLocations } from '@/lib/use-locations'
import { ContextMenu, type MenuItem, type MenuState } from './context-menu'
import { collectDropped, DRAG_MIME, useFiles } from './files-context'

export type FileTab = {
  id: string
  hostId: number
  path: string
  back: string[]
  forward: string[]
}
export type PaneModel = { tabs: FileTab[]; active: string }

type SortKey = 'name' | 'size' | 'mtime'
let tabSeq = Date.now()
export const newTab = (hostId: number, path = '~'): FileTab => ({
  id: `t${++tabSeq}`,
  hostId,
  path,
  back: [],
  forward: [],
})

const QUICK_PLACES = [
  '/',
  '/etc',
  '/var/log',
  '/tmp',
  '/opt',
  '/srv',
  '/www/wwwroot',
  '/etc/nginx',
  '/usr/local',
]

function isTyping(target: EventTarget | null) {
  const el = target as HTMLElement | null
  return !!el && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.isContentEditable)
}

export function FilePane({
  model,
  onChange,
  focused,
  onFocus,
  compact,
  paneLabel,
}: {
  model: PaneModel
  onChange: (next: PaneModel) => void
  focused: boolean
  onFocus: () => void
  compact: boolean
  paneLabel?: string
}) {
  const ctx = useFiles()
  const geo = useServerLocations({ includeArchived: true })
  const tab = model.tabs.find((t) => t.id === model.active) ?? model.tabs[0]
  const host = tab ? ctx.hostById(tab.hostId) : undefined
  const [filter, setFilter] = useState('')
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [anchor, setAnchor] = useState<number | null>(null)
  const [sort, setSort] = useState<{ key: SortKey; desc: boolean }>(() => {
    try {
      return JSON.parse(localStorage.getItem('onessh-files-sort') ?? '') as {
        key: SortKey
        desc: boolean
      }
    } catch {
      return { key: 'name', desc: false }
    }
  })
  const [editingPath, setEditingPath] = useState<string | null>(null)
  const [menu, setMenu] = useState<MenuState>(null)
  const [dropHint, setDropHint] = useState<string | null>(null)
  const [dragTab, setDragTab] = useState<string | null>(null)
  const rootRef = useRef<HTMLDivElement>(null)
  const filterRef = useRef<HTMLInputElement>(null)
  const uploadRef = useRef<HTMLInputElement>(null)
  const folderRef = useRef<HTMLInputElement | null>(null)

  useEffect(() => localStorage.setItem('onessh-files-sort', JSON.stringify(sort)), [sort])

  const query = useQuery({
    queryKey: ['fs', tab?.hostId ?? 0, tab?.path ?? ''],
    queryFn: () => fsApi.ls(tab!.hostId, tab!.path),
    enabled: !!tab,
    retry: false,
    staleTime: 15_000,
  })
  const listing = query.data
  const dir = listing?.path ?? tab?.path ?? '~'

  const patchTab = useCallback(
    (id: string, update: Partial<FileTab>) =>
      onChange({
        ...model,
        tabs: model.tabs.map((t) => (t.id === id ? { ...t, ...update } : t)),
      }),
    [model, onChange],
  )

  // ~ 与相对路径解析成绝对路径后写回标签页，历史与书签里记录的就都是真实路径
  useEffect(() => {
    if (tab && listing && listing.path !== tab.path) patchTab(tab.id, { path: listing.path })
    if (tab && listing) ctx.rememberDir(tab.hostId, listing.path)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [listing?.path])

  useEffect(() => {
    setSelected(new Set())
    setAnchor(null)
    setFilter('')
  }, [tab?.id, dir])

  const navigate = useCallback(
    (path: string) => {
      if (!tab || path === dir) return
      patchTab(tab.id, {
        path,
        back: [...tab.back, dir].slice(-50),
        forward: [],
      })
    },
    [tab, dir, patchTab],
  )
  const goBack = () =>
    tab &&
    tab.back.length &&
    patchTab(tab.id, {
      path: tab.back[tab.back.length - 1],
      back: tab.back.slice(0, -1),
      forward: [dir, ...tab.forward],
    })
  const goForward = () =>
    tab &&
    tab.forward.length &&
    patchTab(tab.id, {
      path: tab.forward[0],
      forward: tab.forward.slice(1),
      back: [...tab.back, dir],
    })
  const goUp = () => dir !== '/' && navigate(parentOf(dir))

  const entries = useMemo(() => {
    const list = (listing?.entries ?? []).filter(
      (e) =>
        (ctx.showHidden || !e.name.startsWith('.')) &&
        (!filter || e.name.toLowerCase().includes(filter.toLowerCase())),
    )
    const dirFirst = (a: FsEntry, b: FsEntry) => Number(b.directory) - Number(a.directory)
    const cmp: Record<SortKey, (a: FsEntry, b: FsEntry) => number> = {
      name: (a, b) =>
        a.name.localeCompare(b.name, 'zh-CN', {
          numeric: true,
          sensitivity: 'base',
        }),
      size: (a, b) => a.size - b.size,
      mtime: (a, b) => a.mtime - b.mtime,
    }
    return [...list].sort(
      (a, b) => dirFirst(a, b) || (sort.desc ? -1 : 1) * cmp[sort.key](a, b) || cmp.name(a, b),
    )
  }, [listing, ctx.showHidden, filter, sort])

  const selectedEntries = entries.filter((e) => selected.has(e.path))
  const selectionSize = selectedEntries.reduce((sum, e) => sum + (e.directory ? 0 : e.size), 0)
  const existingNames = (listing?.entries ?? []).map((e) => e.name)

  if (!tab) return null

  const clickRow = (event: React.MouseEvent, entry: FsEntry, index: number) => {
    onFocus()
    if (event.shiftKey && anchor != null) {
      const [a, b] = [Math.min(anchor, index), Math.max(anchor, index)]
      setSelected(new Set(entries.slice(a, b + 1).map((e) => e.path)))
      return
    }
    if (event.ctrlKey || event.metaKey) {
      setSelected((prev) => {
        const next = new Set(prev)
        if (next.has(entry.path)) next.delete(entry.path)
        else next.add(entry.path)
        return next
      })
    } else {
      setSelected(new Set([entry.path]))
    }
    setAnchor(index)
  }

  const open = (entry: FsEntry) => {
    if (entry.directory) navigate(entry.path)
    else ctx.openEntry(tab.hostId, entry, entries)
  }

  const copySel = (op: 'copy' | 'cut', list = selectedEntries) => {
    if (!list.length) return
    ctx.setClipboard({ op, hostId: tab.hostId, entries: list, from: dir })
    toast.success(
      `${op === 'copy' ? '已复制' : '已剪切'} ${list.length} 项，切到任意服务器的目录按 Ctrl+V 粘贴`,
    )
  }

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (isTyping(event.target)) return
    const mod = event.ctrlKey || event.metaKey
    const key = event.key.toLowerCase()
    const current = anchor != null ? entries[anchor] : selectedEntries[0]
    if (mod && key === 'a') {
      event.preventDefault()
      setSelected(new Set(entries.map((e) => e.path)))
    } else if (mod && key === 'c') {
      event.preventDefault()
      copySel('copy')
    } else if (mod && key === 'x') {
      event.preventDefault()
      copySel('cut')
    } else if (mod && key === 'v') {
      event.preventDefault()
      ctx.paste(tab.hostId, dir)
    } else if (mod && key === 'f') {
      event.preventDefault()
      filterRef.current?.focus()
    } else if (event.key === 'Delete' && selectedEntries.length) {
      event.preventDefault()
      ctx.remove(tab.hostId, selectedEntries)
    } else if (event.key === 'F2' && current) {
      event.preventDefault()
      ctx.rename(tab.hostId, current)
    } else if (event.key === 'F5' || (mod && key === 'r')) {
      event.preventDefault()
      void query.refetch()
    } else if (event.key === 'Enter' && current) {
      event.preventDefault()
      open(current)
    } else if (event.key === 'Backspace' || (event.altKey && event.key === 'ArrowUp')) {
      event.preventDefault()
      goUp()
    } else if (event.altKey && event.key === 'ArrowLeft') {
      event.preventDefault()
      goBack()
    } else if (event.altKey && event.key === 'ArrowRight') {
      event.preventDefault()
      goForward()
    } else if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      const i = Math.min(
        entries.length - 1,
        Math.max(0, (anchor ?? -1) + (event.key === 'ArrowDown' ? 1 : -1)),
      )
      if (entries[i]) {
        setAnchor(i)
        setSelected(
          event.shiftKey ? new Set([...selected, entries[i].path]) : new Set([entries[i].path]),
        )
        rootRef.current?.querySelector(`[data-row="${i}"]`)?.scrollIntoView({ block: 'nearest' })
      }
    } else if (event.key === 'Escape') {
      setSelected(new Set())
    }
  }

  /* ── 拖拽：行可拖出，目录行 / 列表空白 / 其他标签页都可以接收 ── */
  const dragStart = (event: React.DragEvent, entry: FsEntry) => {
    const list = selected.has(entry.path) ? selectedEntries : [entry]
    if (!selected.has(entry.path)) setSelected(new Set([entry.path]))
    event.dataTransfer.setData(DRAG_MIME, JSON.stringify({ hostId: tab.hostId, entries: list }))
    event.dataTransfer.setData('text/plain', list.map((e) => e.path).join('\n'))
    event.dataTransfer.effectAllowed = 'copyMove'
  }

  const dropOp = (event: React.DragEvent, srcHostId: number, dstHostId: number): 'copy' | 'move' =>
    srcHostId === dstHostId
      ? event.ctrlKey || event.altKey
        ? 'copy'
        : 'move'
      : event.shiftKey
        ? 'move'
        : 'copy'

  const allowDrop = (event: React.DragEvent, key: string, dstHostId: number) => {
    const types = Array.from(event.dataTransfer.types)
    if (!types.includes(DRAG_MIME) && !types.includes('Files')) return
    event.preventDefault()
    event.stopPropagation()
    // dragover 阶段读不到拖拽数据，无法判断是否跨主机：光标提示只区分「上传 / 按住 Ctrl 复制 / 默认」
    event.dataTransfer.dropEffect =
      types.includes('Files') || event.ctrlKey || event.altKey ? 'copy' : 'move'
    void dstHostId
    setDropHint(key)
  }

  const handleDrop = (
    event: React.DragEvent,
    dstHostId: number,
    dstDir: string,
    existing: string[],
  ) => {
    event.preventDefault()
    event.stopPropagation()
    setDropHint(null)
    const raw = event.dataTransfer.getData(DRAG_MIME)
    if (raw) {
      const payload = JSON.parse(raw) as { hostId: number; entries: FsEntry[] }
      const items = payload.entries.filter(
        (e) =>
          !(payload.hostId === dstHostId && (e.path === dstDir || parentOf(e.path) === dstDir)),
      )
      if (!items.length) return
      ctx.dropTransfer(
        payload.hostId,
        items,
        dstHostId,
        dstDir,
        dropOp(event, payload.hostId, dstHostId),
      )
      return
    }
    if (event.dataTransfer.files.length || event.dataTransfer.items.length) {
      void collectDropped(event.dataTransfer).then(
        (files) => files.length && ctx.upload(dstHostId, dstDir, files, existing),
      )
    }
  }

  /* ── 右键菜单 ── */
  const entryMenu = (event: React.MouseEvent, entry: FsEntry, index: number) => {
    event.preventDefault()
    onFocus()
    let list = selectedEntries
    if (!selected.has(entry.path)) {
      setSelected(new Set([entry.path]))
      setAnchor(index)
      list = [entry]
    }
    const single = list.length === 1 ? list[0] : null
    const kind = single ? kindOf(single.name, single.directory) : null
    const items: MenuItem[] = []
    if (single?.directory) {
      items.push({
        label: '打开',
        icon: <FolderOpen size={15} />,
        shortcut: 'Enter',
        onSelect: () => navigate(single.path),
      })
      items.push({
        label: '在新标签页打开',
        icon: <ArrowSquareOut size={15} />,
        onSelect: () => openInTab(tab.hostId, single.path),
      })
    } else if (single) {
      if (kind === 'image' || kind === 'video' || kind === 'audio' || kind === 'pdf')
        items.push({
          label: '预览',
          icon: <Eye size={15} />,
          shortcut: 'Enter',
          onSelect: () => ctx.openEntry(tab.hostId, single, entries),
        })
      if (kind !== 'video' && kind !== 'audio' && kind !== 'archive')
        items.push({
          label: '编辑',
          icon: <PencilSimple size={15} />,
          shortcut: kind === 'text' ? 'Enter' : undefined,
          onSelect: () => ctx.editEntry(tab.hostId, single),
        })
    }
    items.push({
      label: list.length > 1 || single?.directory ? '打包下载 (.tar.gz)' : '下载',
      icon: <DownloadSimple size={15} />,
      onSelect: () => ctx.download(tab.hostId, list),
    })
    items.push('separator')
    items.push({
      label: '复制',
      icon: <Copy size={15} />,
      shortcut: 'Ctrl C',
      onSelect: () => copySel('copy', list),
    })
    items.push({
      label: '剪切',
      icon: <Scissors size={15} />,
      shortcut: 'Ctrl X',
      onSelect: () => copySel('cut', list),
    })
    if (single?.directory && ctx.clipboard)
      items.push({
        label: '粘贴到此文件夹',
        icon: <ClipboardText size={15} />,
        onSelect: () => ctx.paste(tab.hostId, single.path),
      })
    items.push({
      label: '复制到其他服务器…',
      icon: <ArrowsLeftRight size={15} />,
      onSelect: () => ctx.transferTo('copy', tab.hostId, list, dir),
    })
    items.push({
      label: '移动到…',
      icon: <ArrowRight size={15} />,
      onSelect: () => ctx.transferTo('move', tab.hostId, list, dir),
    })
    items.push('separator')
    if (single)
      items.push({
        label: '重命名',
        icon: <PencilSimpleLine size={15} />,
        shortcut: 'F2',
        onSelect: () => ctx.rename(tab.hostId, single),
      })
    items.push({
      label: '修改权限',
      icon: <Key size={15} />,
      onSelect: () => ctx.chmod(tab.hostId, list),
    })
    items.push({
      label: '压缩…',
      icon: <Archive size={15} />,
      onSelect: () => ctx.compress(tab.hostId, dir, list),
    })
    if (single && !single.directory && isExtractable(single.name))
      items.push({
        label: '解压到当前目录',
        icon: <TrayArrowUp size={15} />,
        onSelect: () => ctx.extract(tab.hostId, single),
      })
    if (single) {
      items.push({
        label: '复制路径',
        icon: <CopySimple size={15} />,
        onSelect: () => {
          void navigator.clipboard.writeText(single.path)
          toast.success('路径已复制')
        },
      })
      items.push({
        label: '在终端中打开',
        icon: <Terminal size={15} />,
        onSelect: () => ctx.openTerminal(tab.hostId, single.directory ? single.path : dir),
      })
      items.push({
        label: '属性',
        icon: <Info size={15} />,
        onSelect: () => ctx.properties(tab.hostId, single),
      })
    }
    items.push('separator')
    items.push({
      label: `删除${list.length > 1 ? ` ${list.length} 项` : ''}`,
      icon: <Trash size={15} />,
      shortcut: 'Del',
      danger: true,
      onSelect: () => ctx.remove(tab.hostId, list),
    })
    setMenu({ x: event.clientX, y: event.clientY, items })
  }

  const backgroundMenu = (event: React.MouseEvent) => {
    if ((event.target as HTMLElement).closest('[data-row]')) return
    event.preventDefault()
    onFocus()
    setSelected(new Set())
    setMenu({
      x: event.clientX,
      y: event.clientY,
      items: [
        {
          label: '新建文件夹',
          icon: <FolderPlus size={15} />,
          onSelect: () => ctx.newItem(tab.hostId, dir, 'dir'),
        },
        {
          label: '新建文件',
          icon: <FilePlus size={15} />,
          onSelect: () => ctx.newItem(tab.hostId, dir, 'file'),
        },
        {
          label: '上传文件',
          icon: <UploadSimple size={15} />,
          onSelect: () => uploadRef.current?.click(),
        },
        {
          label: '上传文件夹',
          icon: <Folders size={15} />,
          onSelect: () => folderRef.current?.click(),
        },
        'separator',
        {
          label: ctx.clipboard ? `粘贴 ${ctx.clipboard.entries.length} 项` : '粘贴',
          icon: <ClipboardText size={15} />,
          shortcut: 'Ctrl V',
          disabled: !ctx.clipboard,
          onSelect: () => ctx.paste(tab.hostId, dir),
        },
        {
          label: '全选',
          shortcut: 'Ctrl A',
          onSelect: () => setSelected(new Set(entries.map((e) => e.path))),
        },
        {
          label: '刷新',
          icon: <ArrowClockwise size={15} />,
          shortcut: 'F5',
          onSelect: () => void query.refetch(),
        },
        'separator',
        {
          label: '在终端中打开',
          icon: <Terminal size={15} />,
          onSelect: () => ctx.openTerminal(tab.hostId, dir),
        },
        {
          label: ctx.showHidden ? '隐藏隐藏文件' : '显示隐藏文件',
          icon: ctx.showHidden ? <EyeSlash size={15} /> : <Eye size={15} />,
          onSelect: () => ctx.setShowHidden(!ctx.showHidden),
        },
      ],
    })
  }

  /* ── 标签页 ── */
  const activate = (id: string) => onChange({ ...model, active: id })
  const openInTab = (hostId: number, path = '~') => {
    const t = newTab(hostId, path)
    onChange({ tabs: [...model.tabs, t], active: t.id })
  }
  const closeTab = (id: string) => {
    if (model.tabs.length === 1) return
    const i = model.tabs.findIndex((t) => t.id === id)
    const tabs = model.tabs.filter((t) => t.id !== id)
    onChange({
      tabs,
      active: model.active === id ? tabs[Math.max(0, i - 1)].id : model.active,
    })
  }
  const switchHost = (hostId: number) =>
    patchTab(tab.id, { hostId, path: '~', back: [], forward: [] })

  const segments = dir === '/' ? [] : dir.split('/').filter(Boolean)
  const bookmarks = ctx.bookmarks(tab.hostId)
  const starred = bookmarks.includes(dir)
  const error = query.error as ApiError | null
  const activeHosts = ctx.hosts.filter((h) => !h.archived)
  const sortBy = (key: SortKey) =>
    setSort((s) => (s.key === key ? { key, desc: !s.desc } : { key, desc: key !== 'name' }))
  const sortMark = (key: SortKey) => (sort.key === key ? (sort.desc ? ' ↓' : ' ↑') : '')

  const pickUploads = (input: HTMLInputElement | null, folder: boolean) => {
    const files = Array.from(input?.files ?? [])
    if (!files.length) return
    ctx.upload(
      tab.hostId,
      dir,
      files.map((file) => ({
        file,
        rel: folder
          ? (file as File & { webkitRelativePath?: string }).webkitRelativePath || file.name
          : file.name,
      })),
      existingNames,
    )
    if (input) input.value = ''
  }

  return (
    <div
      ref={rootRef}
      tabIndex={0}
      onKeyDown={onKeyDown}
      onMouseDown={onFocus}
      className={cn(
        'flex min-h-0 min-w-0 flex-col overflow-hidden rounded-[14px] border bg-surface shadow-card outline-none transition-shadow',
        focused ? 'border-accent/45 shadow-[0_0_0_3px_var(--accent-soft)]' : 'border-border',
      )}
    >
      {/* 标签栏 */}
      <div className="flex items-center gap-1 overflow-x-auto border-b border-border bg-surface-2/50 px-1.5 pt-1.5">
        {paneLabel && (
          <span className="mr-1 shrink-0 rounded-[5px] bg-accent-soft px-1.5 py-0.5 text-[10.5px] font-bold text-accent">
            {paneLabel}
          </span>
        )}
        {model.tabs.map((t) => {
          const h = ctx.hostById(t.hostId)
          const active = t.id === tab.id
          return (
            <div
              key={t.id}
              role="tab"
              aria-selected={active}
              onClick={() => activate(t.id)}
              onAuxClick={(event) => event.button === 1 && closeTab(t.id)}
              onDragOver={(event) => {
                allowDrop(event, `tab:${t.id}`, t.hostId)
                if (dragTab !== t.id) {
                  setDragTab(t.id)
                  // 拖着文件悬停在别的标签上一会儿就切过去，像浏览器一样
                  window.setTimeout(
                    () => setDragTab((cur) => (cur === t.id ? (activate(t.id), null) : cur)),
                    700,
                  )
                }
              }}
              onDragLeave={() => {
                setDropHint(null)
                setDragTab(null)
              }}
              onDrop={(event) => {
                setDragTab(null)
                handleDrop(event, t.hostId, t.path, [])
              }}
              className={cn(
                'group relative flex max-w-[220px] min-w-[120px] shrink-0 cursor-pointer items-center gap-1.5 rounded-t-[9px] border border-b-0 px-2.5 py-1.5 text-[12px] transition-colors',
                active
                  ? 'border-border bg-surface text-text'
                  : 'border-transparent text-muted hover:bg-surface/60 hover:text-text',
                dropHint === `tab:${t.id}` && 'bg-accent-soft ring-1 ring-accent/50',
              )}
              title={`${h?.name ?? '未知主机'}:${t.path}`}
            >
              <CountryFlag code={geo.geoById.get(t.hostId)?.country_code} />
              <span className="min-w-0 flex-1 truncate">
                <b className="font-semibold">{h?.name ?? '已删除'}</b>
                <span className="text-faint"> · {baseName(t.path)}</span>
              </span>
              {model.tabs.length > 1 && (
                <button
                  type="button"
                  aria-label="关闭标签页"
                  onClick={(event) => {
                    event.stopPropagation()
                    closeTab(t.id)
                  }}
                  className="rounded-[4px] p-0.5 text-faint opacity-0 group-hover:opacity-100 hover:bg-surface-2 hover:text-text"
                >
                  <X size={11} weight="bold" />
                </button>
              )}
            </div>
          )
        })}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              type="button"
              aria-label="新建标签页"
              className="mb-1 ml-0.5 grid size-7 shrink-0 place-items-center rounded-[7px] text-muted hover:bg-surface hover:text-text"
            >
              <Plus size={14} weight="bold" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start">
            <DropdownMenuLabel>在新标签页打开服务器</DropdownMenuLabel>
            {activeHosts.map((h) => (
              <DropdownMenuItem key={h.id} onSelect={() => openInTab(h.id)}>
                <CountryFlag code={geo.geoById.get(h.id)?.country_code} />
                {h.name}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      {/* 工具栏 */}
      <div className="flex flex-wrap items-center gap-1 border-b border-border px-2 py-1.5">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              type="button"
              className="mr-1 inline-flex h-8 items-center gap-1.5 rounded-[8px] border border-border bg-surface px-2 text-[12.5px] font-medium text-text hover:border-border-strong"
            >
              <CountryFlag code={geo.geoById.get(tab.hostId)?.country_code} />
              <span className="max-w-[120px] truncate">{host?.name ?? '选择主机'}</span>
              <CaretDown size={11} className="text-muted" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start">
            <DropdownMenuLabel>切换此标签页的服务器</DropdownMenuLabel>
            {activeHosts.map((h) => (
              <DropdownMenuItem key={h.id} onSelect={() => switchHost(h.id)}>
                <CountryFlag code={geo.geoById.get(h.id)?.country_code} />
                <span className="flex-1">{h.name}</span>
                {h.id === tab.hostId && <span className="size-1.5 rounded-full bg-accent" />}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
        {[
          {
            label: '后退 (Alt+←)',
            icon: <ArrowLeft size={15} />,
            run: goBack,
            disabled: !tab.back.length,
          },
          {
            label: '前进 (Alt+→)',
            icon: <ArrowRight size={15} />,
            run: goForward,
            disabled: !tab.forward.length,
          },
          {
            label: '上一级 (Backspace)',
            icon: <ArrowUp size={15} />,
            run: goUp,
            disabled: dir === '/',
          },
          {
            label: '家目录',
            icon: <House size={15} />,
            run: () => navigate(listing?.home ?? '~'),
            disabled: false,
          },
          {
            label: '刷新 (F5)',
            icon: <ArrowClockwise size={15} className={cn(query.isFetching && 'animate-spin')} />,
            run: () => void query.refetch(),
            disabled: false,
          },
        ].map((b) => (
          <Tooltip key={b.label} content={b.label}>
            <button
              type="button"
              aria-label={b.label}
              disabled={b.disabled}
              onClick={b.run}
              className="grid size-8 place-items-center rounded-[8px] text-muted transition-colors hover:bg-surface-2 hover:text-text disabled:opacity-35"
            >
              {b.icon}
            </button>
          </Tooltip>
        ))}

        {/* 路径：点击段落跳转，点击空白处直接输入 */}
        <div className="mx-1 flex h-8 min-w-[160px] flex-1 items-center overflow-hidden rounded-[8px] border border-border bg-surface-2/50">
          {editingPath != null ? (
            <input
              autoFocus
              value={editingPath}
              spellCheck={false}
              onChange={(event) => setEditingPath(event.target.value)}
              onBlur={() => setEditingPath(null)}
              onKeyDown={(event) => {
                if (event.key === 'Enter') {
                  navigate(editingPath.trim() || '/')
                  setEditingPath(null)
                } else if (event.key === 'Escape') setEditingPath(null)
              }}
              className="h-full w-full bg-transparent px-2.5 font-mono text-[12.5px] text-text focus:outline-none"
            />
          ) : (
            <div
              className="flex h-full min-w-0 flex-1 cursor-text items-center overflow-x-auto px-1"
              onClick={(event) => event.target === event.currentTarget && setEditingPath(dir)}
            >
              <button
                type="button"
                onClick={() => navigate('/')}
                className="shrink-0 rounded-[5px] px-1.5 py-0.5 font-mono text-[12.5px] text-muted hover:bg-surface hover:text-text"
              >
                /
              </button>
              {segments.map((seg, i) => {
                const target = '/' + segments.slice(0, i + 1).join('/')
                return (
                  <span key={target} className="flex shrink-0 items-center">
                    {i > 0 && <span className="text-faint">/</span>}
                    <button
                      type="button"
                      onClick={() => navigate(target)}
                      onDragOver={(event) => allowDrop(event, `crumb:${target}`, tab.hostId)}
                      onDragLeave={() => setDropHint(null)}
                      onDrop={(event) => handleDrop(event, tab.hostId, target, [])}
                      className={cn(
                        'rounded-[5px] px-1.5 py-0.5 font-mono text-[12.5px] hover:bg-surface hover:text-text',
                        i === segments.length - 1 ? 'font-semibold text-text' : 'text-muted',
                        dropHint === `crumb:${target}` && 'bg-accent-soft text-accent',
                      )}
                    >
                      {seg}
                    </button>
                  </span>
                )
              })}
              <span className="min-w-6 flex-1 self-stretch" onClick={() => setEditingPath(dir)} />
            </div>
          )}
          <Tooltip content={starred ? '取消收藏' : '收藏此目录'}>
            <button
              type="button"
              onClick={() => ctx.toggleBookmark(tab.hostId, dir)}
              className="grid h-full w-8 shrink-0 place-items-center border-l border-border text-muted hover:text-warning"
            >
              <Star
                size={14}
                weight={starred ? 'fill' : 'regular'}
                className={cn(starred && 'text-warning')}
              />
            </button>
          </Tooltip>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button
                type="button"
                aria-label="快速跳转"
                className="grid h-full w-8 shrink-0 place-items-center border-l border-border text-muted hover:text-text"
              >
                <FolderSimpleStar size={15} />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="max-h-[60vh] overflow-y-auto">
              {bookmarks.length > 0 && (
                <>
                  <DropdownMenuLabel>收藏</DropdownMenuLabel>
                  {bookmarks.map((b) => (
                    <DropdownMenuItem key={b} onSelect={() => navigate(b)}>
                      <Star size={13} weight="fill" className="text-warning" />
                      <span className="font-mono text-[12px]">{b}</span>
                    </DropdownMenuItem>
                  ))}
                  <DropdownMenuSeparator />
                </>
              )}
              <DropdownMenuLabel>常用位置</DropdownMenuLabel>
              <DropdownMenuItem onSelect={() => navigate(listing?.home ?? '~')}>
                <House size={13} />
                <span className="font-mono text-[12px]">{listing?.home ?? '~'}</span>
              </DropdownMenuItem>
              {QUICK_PLACES.map((p) => (
                <DropdownMenuItem key={p} onSelect={() => navigate(p)}>
                  <FolderOpen size={13} />
                  <span className="font-mono text-[12px]">{p}</span>
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        </div>

        <div className="relative w-[150px] max-w-full">
          <MagnifyingGlass
            size={13}
            className="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-faint"
          />
          <input
            ref={filterRef}
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
            onKeyDown={(event) =>
              event.key === 'Escape' && (setFilter(''), rootRef.current?.focus())
            }
            placeholder="筛选 (Ctrl F)"
            className="h-8 w-full rounded-[8px] border border-border bg-surface pr-2 pl-7 text-[12.5px] text-text placeholder:text-faint focus:border-accent focus:outline-none"
          />
        </div>
        <Tooltip content={ctx.showHidden ? '隐藏以 . 开头的文件' : '显示隐藏文件'}>
          <button
            type="button"
            onClick={() => ctx.setShowHidden(!ctx.showHidden)}
            className={cn(
              'grid size-8 place-items-center rounded-[8px] hover:bg-surface-2',
              ctx.showHidden ? 'text-accent' : 'text-muted',
            )}
          >
            {ctx.showHidden ? <Eye size={15} /> : <EyeSlash size={15} />}
          </button>
        </Tooltip>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button size="sm" variant="primary" className="h-8">
              <Plus size={14} weight="bold" />
              {!compact && '新建'}
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onSelect={() => ctx.newItem(tab.hostId, dir, 'dir')}>
              <FolderPlus size={14} />
              新建文件夹
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => ctx.newItem(tab.hostId, dir, 'file')}>
              <FilePlus size={14} />
              新建文件
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={() => uploadRef.current?.click()}>
              <UploadSimple size={14} />
              上传文件
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => folderRef.current?.click()}>
              <Folders size={14} />
              上传文件夹
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        <input
          ref={uploadRef}
          type="file"
          multiple
          className="hidden"
          onChange={(event) => pickUploads(event.currentTarget, false)}
        />
        <input
          ref={(el) => {
            folderRef.current = el
            el?.setAttribute('webkitdirectory', '')
          }}
          type="file"
          multiple
          className="hidden"
          onChange={(event) => pickUploads(event.currentTarget, true)}
        />
      </div>

      <div className="relative flex min-h-0 flex-1 flex-col">
        {/* 列表 */}
        <div
          className={cn(
            'relative min-h-0 flex-1 overflow-auto',
            dropHint === 'pane' && 'bg-accent/[0.04]',
            selectedEntries.length > 0 && 'pb-16',
          )}
          onContextMenu={backgroundMenu}
          onClick={(event) => event.target === event.currentTarget && setSelected(new Set())}
          onDragOver={(event) => allowDrop(event, 'pane', tab.hostId)}
          onDragLeave={(event) =>
            !event.currentTarget.contains(event.relatedTarget as Node) && setDropHint(null)
          }
          onDrop={(event) => handleDrop(event, tab.hostId, dir, existingNames)}
        >
          {dropHint === 'pane' && (
            <div className="pointer-events-none absolute inset-2 z-10 grid place-items-center rounded-[12px] border-2 border-dashed border-accent/60 bg-accent/[0.06]">
              <p className="rounded-full bg-surface px-4 py-1.5 text-[13px] font-medium text-accent shadow-card">
                放到 {host?.name}:{dir}
              </p>
            </div>
          )}
          {query.isLoading ? (
            <div className="grid h-40 place-items-center">
              <Spinner className="size-5 text-accent" />
            </div>
          ) : error ? (
            <div className="grid place-items-center px-6 py-14 text-center">
              <p className="text-[14px] font-medium text-text">
                {error.status === 404
                  ? '目录不存在'
                  : error.status === 403
                    ? '没有权限访问这个目录'
                    : '无法打开这个目录'}
              </p>
              <p className="mt-1 max-w-md text-[12.5px] text-muted">{error.message}</p>
              <div className="mt-4 flex gap-2">
                {tab.back.length > 0 && (
                  <Button size="sm" variant="outline" onClick={goBack}>
                    返回上一个目录
                  </Button>
                )}
                <Button size="sm" variant="outline" onClick={() => navigate('~')}>
                  回到家目录
                </Button>
                <Button size="sm" variant="primary" onClick={() => void query.refetch()}>
                  重试
                </Button>
              </div>
            </div>
          ) : (
            <table className="w-full border-separate border-spacing-0 text-[13px]">
              <thead className="sticky top-0 z-[5]">
                <tr className="bg-surface-2 text-[11px] font-semibold tracking-wide text-muted uppercase">
                  <th className="w-9 border-b border-border py-2 pl-3">
                    <Checkbox
                      aria-label="全选"
                      checked={
                        entries.length > 0 && selectedEntries.length === entries.length
                          ? true
                          : selectedEntries.length
                            ? 'indeterminate'
                            : false
                      }
                      onCheckedChange={() =>
                        setSelected(
                          selectedEntries.length === entries.length
                            ? new Set()
                            : new Set(entries.map((e) => e.path)),
                        )
                      }
                    />
                  </th>
                  <th
                    className="cursor-pointer border-b border-border px-2 py-2 text-left select-none hover:text-text"
                    onClick={() => sortBy('name')}
                  >
                    名称{sortMark('name')}
                  </th>
                  <th
                    className="w-[96px] cursor-pointer border-b border-border px-2 py-2 text-right select-none hover:text-text"
                    onClick={() => sortBy('size')}
                  >
                    大小{sortMark('size')}
                  </th>
                  <th
                    className="hidden w-[150px] cursor-pointer border-b border-border px-2 py-2 text-left select-none hover:text-text md:table-cell"
                    onClick={() => sortBy('mtime')}
                  >
                    修改时间{sortMark('mtime')}
                  </th>
                  {!compact && (
                    <th className="hidden w-[110px] border-b border-border px-2 py-2 text-left xl:table-cell">
                      权限
                    </th>
                  )}
                  {!compact && (
                    <th className="hidden w-[80px] border-b border-border px-2 py-2 text-left 2xl:table-cell">
                      属主
                    </th>
                  )}
                </tr>
              </thead>
              <tbody>
                {dir !== '/' && (
                  <tr
                    className="cursor-pointer text-muted hover:bg-accent/[0.04]"
                    onDoubleClick={goUp}
                    onDragOver={(event) => allowDrop(event, 'up', tab.hostId)}
                    onDragLeave={() => setDropHint(null)}
                    onDrop={(event) => handleDrop(event, tab.hostId, parentOf(dir), [])}
                  >
                    <td className="border-b border-border/60 py-1.5 pl-3" />
                    <td
                      colSpan={5}
                      className={cn(
                        'border-b border-border/60 px-2 py-1.5',
                        dropHint === 'up' && 'bg-accent-soft',
                      )}
                    >
                      <span className="inline-flex items-center gap-2">
                        <ArrowUp size={14} />
                        ..
                      </span>
                    </td>
                  </tr>
                )}
                {entries.map((entry, index) => {
                  const { icon: Glyph, color } = iconOf(entry.name, entry.directory, entry.symlink)
                  const isSel = selected.has(entry.path)
                  const isCut =
                    ctx.clipboard?.op === 'cut' &&
                    ctx.clipboard.hostId === tab.hostId &&
                    ctx.clipboard.entries.some((e) => e.path === entry.path)
                  const hint = `row:${entry.path}`
                  return (
                    <tr
                      key={entry.path}
                      data-row={index}
                      draggable
                      onDragStart={(event) => dragStart(event, entry)}
                      onDragEnd={() => setDropHint(null)}
                      onDragOver={
                        entry.directory ? (event) => allowDrop(event, hint, tab.hostId) : undefined
                      }
                      onDragLeave={entry.directory ? () => setDropHint(null) : undefined}
                      onDrop={
                        entry.directory
                          ? (event) => handleDrop(event, tab.hostId, entry.path, [])
                          : undefined
                      }
                      onClick={(event) => clickRow(event, entry, index)}
                      onDoubleClick={() => open(entry)}
                      onContextMenu={(event) => entryMenu(event, entry, index)}
                      className={cn(
                        'cursor-default transition-colors select-none',
                        isSel ? 'bg-accent/[0.10]' : 'hover:bg-accent/[0.04]',
                        dropHint === hint && 'bg-accent-soft outline outline-1 outline-accent/50',
                        isCut && 'opacity-50',
                      )}
                    >
                      <td
                        className="border-b border-border/60 py-1.5 pl-3"
                        onClick={(event) => event.stopPropagation()}
                      >
                        <Checkbox
                          aria-label={`选择 ${entry.name}`}
                          checked={isSel}
                          onCheckedChange={() =>
                            setSelected((prev) => {
                              const next = new Set(prev)
                              if (next.has(entry.path)) next.delete(entry.path)
                              else next.add(entry.path)
                              return next
                            })
                          }
                        />
                      </td>
                      <td className="max-w-0 border-b border-border/60 px-2 py-1.5">
                        <span className="flex min-w-0 items-center gap-2">
                          <Glyph
                            size={17}
                            weight={entry.directory ? 'fill' : 'duotone'}
                            style={{ color }}
                            className="shrink-0"
                          />
                          <span
                            className={cn(
                              'truncate',
                              entry.directory ? 'font-medium text-text' : 'text-text',
                              entry.name.startsWith('.') && 'opacity-70',
                            )}
                            title={entry.name}
                          >
                            {entry.name}
                          </span>
                          {entry.symlink && entry.symlink_target && (
                            <span
                              className="truncate font-mono text-[11px] text-faint"
                              title={entry.symlink_target}
                            >
                              → {entry.symlink_target}
                            </span>
                          )}
                        </span>
                      </td>
                      <td className="border-b border-border/60 px-2 py-1.5 text-right text-[12px] text-muted tabular-nums">
                        {entry.directory ? '—' : formatSize(entry.size)}
                      </td>
                      <td className="hidden border-b border-border/60 px-2 py-1.5 text-[12px] text-muted tabular-nums md:table-cell">
                        {formatMtime(entry.mtime)}
                      </td>
                      {!compact && (
                        <td
                          className="hidden border-b border-border/60 px-2 py-1.5 font-mono text-[11.5px] text-muted xl:table-cell"
                          title={entry.mode}
                        >
                          {entry.perm} <span className="text-faint">{entry.mode.slice(-9)}</span>
                        </td>
                      )}
                      {!compact && (
                        <td className="hidden border-b border-border/60 px-2 py-1.5 font-mono text-[11.5px] text-muted 2xl:table-cell">
                          {entry.uid === 0 ? 'root' : entry.uid}
                        </td>
                      )}
                    </tr>
                  )
                })}
              </tbody>
            </table>
          )}
          {!query.isLoading && !error && entries.length === 0 && (
            <div className="grid place-items-center px-6 py-14 text-center text-[13px] text-muted">
              {filter
                ? `没有名称包含「${filter}」的项目`
                : '这个目录是空的 —— 可以把文件拖到这里上传'}
            </div>
          )}
        </div>

        {/* 多选操作条：浮在列表底部，不挤占行位置（否则双击第二下会落到别的行上） */}
        {selectedEntries.length > 0 && (
          <div className="absolute bottom-3 left-1/2 z-20 flex max-w-[calc(100%-1.5rem)] -translate-x-1/2 flex-wrap items-center gap-1 rounded-[12px] border border-accent/25 bg-surface/95 px-2 py-1.5 text-[12px] shadow-pop backdrop-blur">
            <span className="mr-1 pl-1.5 font-medium text-text">
              已选 {selectedEntries.length} 项
              {selectionSize > 0 && ` · ${formatSize(selectionSize)}`}
            </span>
            {[
              {
                label: '下载',
                icon: <DownloadSimple size={13} />,
                run: () => ctx.download(tab.hostId, selectedEntries),
              },
              {
                label: '复制',
                icon: <Copy size={13} />,
                run: () => copySel('copy'),
              },
              {
                label: '剪切',
                icon: <Scissors size={13} />,
                run: () => copySel('cut'),
              },
              {
                label: '传到其他服务器',
                icon: <ArrowsLeftRight size={13} />,
                run: () => ctx.transferTo('copy', tab.hostId, selectedEntries, dir),
              },
              {
                label: '压缩',
                icon: <Archive size={13} />,
                run: () => ctx.compress(tab.hostId, dir, selectedEntries),
              },
              {
                label: '权限',
                icon: <Key size={13} />,
                run: () => ctx.chmod(tab.hostId, selectedEntries),
              },
            ].map((b) => (
              <button
                key={b.label}
                type="button"
                onClick={b.run}
                className="inline-flex items-center gap-1 rounded-[6px] px-2 py-1 text-muted hover:bg-surface hover:text-text"
              >
                {b.icon}
                {b.label}
              </button>
            ))}
            <button
              type="button"
              onClick={() => ctx.remove(tab.hostId, selectedEntries)}
              className="inline-flex items-center gap-1 rounded-[6px] px-2 py-1 text-danger hover:bg-danger/10"
            >
              <Trash size={13} />
              删除
            </button>
            <button
              type="button"
              onClick={() => setSelected(new Set())}
              className="ml-auto rounded-[6px] px-2 py-1 text-muted hover:text-text"
            >
              取消选择
            </button>
          </div>
        )}
      </div>

      {/* 状态栏 */}
      <div className="flex items-center gap-3 border-t border-border bg-surface-2/40 px-3 py-1.5 text-[11.5px] text-muted">
        <span className="tabular-nums">
          {entries.filter((e) => e.directory).length} 个文件夹 ·{' '}
          {entries.filter((e) => !e.directory).length} 个文件
          {listing?.truncated && ' · 仅显示前 5000 项'}
        </span>
        {ctx.clipboard && (
          <button
            type="button"
            onClick={() => ctx.paste(tab.hostId, dir)}
            className="inline-flex min-w-0 items-center gap-1.5 rounded-full bg-accent-soft px-2.5 py-0.5 font-medium text-accent hover:bg-accent/20"
            title={`来自 ${ctx.hostById(ctx.clipboard.hostId)?.name}:${ctx.clipboard.from}`}
          >
            <ClipboardText size={12} />
            <span className="truncate">
              粘贴 {ctx.clipboard.entries.length} 项（
              {ctx.clipboard.op === 'cut' ? '移动' : '复制'}自{' '}
              {ctx.hostById(ctx.clipboard.hostId)?.name}）
            </span>
          </button>
        )}
        <span className="ml-auto hidden lg:inline">
          拖拽：同服务器移动 · 跨服务器复制 · 按住 Ctrl 复制 / Shift 移动
        </span>
      </div>
      <ContextMenu menu={menu} onClose={() => setMenu(null)} />
    </div>
  )
}
