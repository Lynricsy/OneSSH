import { useEffect, useMemo, useRef, useState } from 'react'
import { ArrowRight, Copy as CopyIcon } from '@phosphor-icons/react'
import { toast } from 'sonner'
import type { Host } from '@/api/types'
import type { Conflict, FsEntry } from '@/api/fs'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog } from '@/components/ui/dialog'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { cn } from '@/lib/cn'
import { formatMtime, formatSize, iconOf } from '@/lib/file-types'

/* ── 单行输入：重命名 / 新建 / 压缩包名 / 解压目录 ── */

export type PromptState = {
  title: string
  description?: string
  label: string
  initial: string
  confirmText?: string
  /** 打开时选中文件名主体（不含扩展名），方便直接改名 */
  selectStem?: boolean
  extra?: 'archive-format'
  onSubmit: (value: string, format?: 'tar.gz' | 'zip') => Promise<unknown> | void
} | null

/** 每次打开都按 state 重新挂载内容：输入框的初值、选区与忙碌状态都从干净的状态开始 */
let promptSeq = 0
export function PromptDialog({ state, onClose }: { state: PromptState; onClose: () => void }) {
  const last = useRef<PromptState>(null)
  if (state) last.current = state
  // state 一变就换 key：同一次渲染里完成重新挂载，不会多出一帧空白
  const key = useMemo(() => (state ? ++promptSeq : -promptSeq), [state])
  return <PromptDialogBody key={key} state={state} lastState={last.current} onClose={onClose} />
}

function PromptDialogBody({ state, lastState, onClose }: { state: PromptState; lastState: PromptState; onClose: () => void }) {
  const [value, setValue] = useState(() => state?.initial ?? '')
  const [format, setFormat] = useState<'tar.gz' | 'zip'>('tar.gz')
  const [busy, setBusy] = useState(false)
  const ref = useRef<HTMLInputElement>(null)
  // 关闭动画期间沿用最后一次的标题，避免内容闪成空白
  const view = state ?? lastState

  useEffect(() => {
    if (!state) return
    // 等 Radix 完成自己的初始聚焦后再设置选区，否则会被它覆盖
    const timer = window.setTimeout(() => {
      const input = ref.current
      if (!input) return
      input.focus()
      const dot = state.initial.lastIndexOf('.')
      if (state.selectStem && dot > 0) input.setSelectionRange(0, state.initial.toLowerCase().endsWith('.tar.gz') ? dot - 4 : dot)
      else input.select()
    }, 60)
    return () => window.clearTimeout(timer)
  }, [state])

  const pickFormat = (f: 'tar.gz' | 'zip') => {
    setFormat(f)
    setValue((v) => v.replace(/\.(tar\.gz|zip)$/i, '') + (f === 'zip' ? '.zip' : '.tar.gz'))
  }

  const submit = async () => {
    if (!state || !value.trim() || busy) return
    setBusy(true)
    try {
      await state.onSubmit(value.trim(), format)
      onClose()
    } catch (error) {
      toast.error((error as Error).message)
      setBusy(false)
    }
  }

  return (
    <Dialog
      open={state != null}
      onOpenChange={(open) => !open && onClose()}
      size="sm"
      title={view?.title ?? ''}
      description={view?.description}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            取消
          </Button>
          <Button variant="primary" loading={busy} disabled={!value.trim()} onClick={() => void submit()}>
            {view?.confirmText ?? '确定'}
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        {view?.extra === 'archive-format' && (
          <div className="grid grid-cols-2 gap-2">
            {(['tar.gz', 'zip'] as const).map((f) => (
              <button
                key={f}
                type="button"
                onClick={() => pickFormat(f)}
                className={cn(
                  'rounded-[10px] border px-3 py-2 text-left text-[13px] transition-colors',
                  format === f ? 'border-accent bg-accent-soft text-text' : 'border-border text-muted hover:bg-surface-2',
                )}
              >
                <b className="font-semibold">.{f}</b>
                <span className="block text-[11px] text-muted">{f === 'zip' ? '需要目标主机安装 zip' : 'Linux 自带，推荐'}</span>
              </button>
            ))}
          </div>
        )}
        <Field label={view?.label ?? ''}>
          {(id) => (
            <Input
              id={id}
              ref={ref}
              autoFocus
              value={value}
              spellCheck={false}
              className="font-mono"
              onChange={(event) => setValue(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter') {
                  event.preventDefault()
                  void submit()
                }
              }}
            />
          )}
        </Field>
      </div>
    </Dialog>
  )
}

/* ── 权限 ── */

const BITS = [
  { who: '所有者', shift: 6 },
  { who: '同组', shift: 3 },
  { who: '其他人', shift: 0 },
]
const PERMS = [
  { label: '读', bit: 4 },
  { label: '写', bit: 2 },
  { label: '执行', bit: 1 },
]

export type ChmodState = { entries: FsEntry[]; onSubmit: (mode: string, recursive: boolean) => Promise<unknown> } | null

export function ChmodDialog({ state, onClose }: { state: ChmodState; onClose: () => void }) {
  const [mode, setMode] = useState(0o644)
  const [recursive, setRecursive] = useState(false)
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (!state) return
    setMode(parseInt(state.entries[0]?.perm ?? '0644', 8) & 0o777)
    setRecursive(false)
    setBusy(false)
  }, [state])
  const hasDir = state?.entries.some((e) => e.directory)
  const octal = mode.toString(8).padStart(3, '0')
  const symbolic = BITS.map(({ shift }) => PERMS.map(({ label, bit }) => ((mode >> shift) & bit ? 'rwx'[PERMS.findIndex((p) => p.label === label)] : '-')).join('')).join('')

  return (
    <Dialog
      open={state != null}
      onOpenChange={(open) => !open && onClose()}
      size="sm"
      title={state && state.entries.length === 1 ? `修改权限 · ${state.entries[0].name}` : `修改 ${state?.entries.length ?? 0} 项的权限`}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            取消
          </Button>
          <Button
            variant="primary"
            loading={busy}
            onClick={async () => {
              if (!state) return
              setBusy(true)
              try {
                await state.onSubmit(octal, recursive)
                onClose()
              } catch (e) {
                toast.error((e as Error).message)
                setBusy(false)
              }
            }}
          >
            应用
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <div className="overflow-hidden rounded-[10px] border border-border">
          <table className="w-full text-[13px]">
            <thead>
              <tr className="bg-surface-2/60 text-[11px] text-muted">
                <th className="px-3 py-2 text-left font-medium" />
                {PERMS.map((p) => (
                  <th key={p.label} className="px-3 py-2 font-medium">
                    {p.label}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {BITS.map(({ who, shift }) => (
                <tr key={who} className="border-t border-border">
                  <td className="px-3 py-2 text-muted">{who}</td>
                  {PERMS.map(({ label, bit }) => {
                    const mask = bit << shift
                    return (
                      <td key={label} className="px-3 py-2 text-center">
                        <span className="inline-flex">
                          <Checkbox
                            aria-label={`${who}${label}`}
                            checked={(mode & mask) !== 0}
                            onCheckedChange={(checked) => setMode((m) => (checked ? m | mask : m & ~mask))}
                          />
                        </span>
                      </td>
                    )
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <div className="flex items-center gap-3">
          <Field label="八进制" className="w-28">
            {(id) => (
              <Input
                id={id}
                value={octal}
                maxLength={4}
                className="font-mono tabular-nums"
                onChange={(event) => {
                  const v = event.target.value.replace(/[^0-7]/g, '')
                  if (v) setMode(parseInt(v, 8) & 0o777)
                }}
              />
            )}
          </Field>
          <p className="mt-6 font-mono text-[13px] text-muted">{symbolic}</p>
        </div>
        {hasDir && (
          <label className="flex items-center gap-2 text-[13px] text-text">
            <Checkbox checked={recursive} onCheckedChange={(c) => setRecursive(c === true)} aria-label="递归" />
            同时应用到目录里的所有文件与子目录（chmod -R）
          </label>
        )}
      </div>
    </Dialog>
  )
}

/* ── 复制 / 移动到其他位置（可跨主机） ── */

export type TransferDialogState = {
  op: 'copy' | 'move'
  srcHostId: number
  entries: FsEntry[]
  defaultHostId: number
  defaultDir: string
  onSubmit: (dstHostId: number, dstDir: string, op: 'copy' | 'move', conflict: Conflict) => Promise<unknown> | void
} | null

const CONFLICTS: Array<{ value: Conflict; label: string; hint: string }> = [
  { value: 'rename', label: '保留两者', hint: '自动命名为 name (1)' },
  { value: 'overwrite', label: '覆盖', hint: '替换目标里的同名项目' },
  { value: 'skip', label: '跳过', hint: '同名项目不处理' },
]

export function TransferDialog({
  state,
  hosts,
  recentDirs,
  onClose,
}: {
  state: TransferDialogState
  hosts: Host[]
  recentDirs: (hostId: number) => string[]
  onClose: () => void
}) {
  const [hostId, setHostId] = useState<number>(0)
  const [dir, setDir] = useState('')
  const [op, setOp] = useState<'copy' | 'move'>('copy')
  const [conflict, setConflict] = useState<Conflict>('rename')
  useEffect(() => {
    if (!state) return
    setHostId(state.defaultHostId)
    setDir(state.defaultDir)
    setOp(state.op)
    setConflict('rename')
  }, [state])
  const suggestions = useMemo(() => (hostId ? recentDirs(hostId) : []), [hostId, recentDirs])
  const src = hosts.find((h) => h.id === state?.srcHostId)
  const dst = hosts.find((h) => h.id === hostId)

  return (
    <Dialog
      open={state != null}
      onOpenChange={(open) => !open && onClose()}
      size="md"
      title={op === 'copy' ? '复制到…' : '移动到…'}
      description={`${state?.entries.length ?? 0} 个项目，来自 ${src?.name ?? ''}`}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            取消
          </Button>
          <Button
            variant="primary"
            disabled={!hostId || !dir.trim()}
            onClick={async () => {
              if (!state) return
              await state.onSubmit(hostId, dir.trim(), op, conflict)
              onClose()
            }}
          >
            开始{op === 'copy' ? '复制' : '移动'}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <div className="flex flex-wrap gap-1.5">
          {state?.entries.slice(0, 8).map((e) => {
            const { icon: Glyph, color } = iconOf(e.name, e.directory, e.symlink)
            return (
              <span key={e.path} className="inline-flex max-w-[200px] items-center gap-1.5 rounded-full bg-surface-2 px-2.5 py-1 text-[12px] text-text">
                <Glyph size={13} weight="fill" style={{ color }} />
                <span className="truncate">{e.name}</span>
              </span>
            )
          })}
          {(state?.entries.length ?? 0) > 8 && <span className="px-1 py-1 text-[12px] text-muted">等 {state!.entries.length} 项</span>}
        </div>
        <div className="grid grid-cols-2 gap-2">
          {(['copy', 'move'] as const).map((o) => (
            <button
              key={o}
              type="button"
              onClick={() => setOp(o)}
              className={cn('rounded-[10px] border px-3 py-2 text-left text-[13px] transition-colors', op === o ? 'border-accent bg-accent-soft' : 'border-border hover:bg-surface-2')}
            >
              <b className="font-semibold text-text">{o === 'copy' ? '复制' : '移动'}</b>
              <span className="block text-[11px] text-muted">{o === 'copy' ? '保留原文件' : '完成后删除原文件'}</span>
            </button>
          ))}
        </div>
        <div className="grid gap-3 sm:grid-cols-[180px_1fr]">
          <Field label="目标主机">
            {(id) => (
              <Select
                id={id}
                value={hostId || undefined}
                onChange={(v) => setHostId(v)}
                options={hosts.filter((h) => !h.archived).map((h) => ({ value: h.id, label: h.id === state?.srcHostId ? `${h.name}（当前）` : h.name }))}
              />
            )}
          </Field>
          <Field label="目标目录" hint="不存在会自动创建">
            {(id) => <Input id={id} value={dir} className="font-mono" spellCheck={false} onChange={(e) => setDir(e.target.value)} />}
          </Field>
        </div>
        {suggestions.length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {suggestions.map((s) => (
              <button key={s} type="button" onClick={() => setDir(s)} className="rounded-full border border-border px-2.5 py-0.5 font-mono text-[11.5px] text-muted hover:border-accent/40 hover:text-text">
                {s}
              </button>
            ))}
          </div>
        )}
        <div>
          <p className="mb-1.5 text-[12px] font-medium text-text">目标已有同名项目时</p>
          <div className="grid grid-cols-3 gap-2">
            {CONFLICTS.map((c) => (
              <button
                key={c.value}
                type="button"
                onClick={() => setConflict(c.value)}
                className={cn('rounded-[10px] border px-2.5 py-1.5 text-left text-[12px] transition-colors', conflict === c.value ? 'border-accent bg-accent-soft' : 'border-border hover:bg-surface-2')}
              >
                <b className="font-semibold text-text">{c.label}</b>
                <span className="block text-[10.5px] text-muted">{c.hint}</span>
              </button>
            ))}
          </div>
        </div>
        {state && hostId && hostId !== state.srcHostId && (
          <p className="flex items-center gap-2 rounded-[10px] bg-surface-2/70 px-3 py-2 text-[12px] text-muted">
            <span className="font-medium text-text">{src?.name}</span>
            <ArrowRight size={12} />
            <span className="font-medium text-text">{dst?.name}</span>
            跨主机经网关流式中转，两台服务器之间无需互通
          </p>
        )}
      </div>
    </Dialog>
  )
}

/* ── 同名冲突确认（拖拽 / 粘贴时） ── */

export type ConflictState = { names: string[]; resolve: (choice: Conflict | null) => void } | null

export function ConflictDialog({ state }: { state: ConflictState }) {
  return (
    <Dialog
      open={state != null}
      onOpenChange={(open) => !open && state?.resolve(null)}
      size="sm"
      title="目标目录里已有同名项目"
      description={`${state?.names.length ?? 0} 个同名：${state?.names.slice(0, 4).join('、') ?? ''}${(state?.names.length ?? 0) > 4 ? ' 等' : ''}`}
      footer={
        <>
          <Button variant="ghost" onClick={() => state?.resolve(null)}>
            取消
          </Button>
          <Button variant="outline" onClick={() => state?.resolve('skip')}>
            跳过
          </Button>
          <Button variant="outline" onClick={() => state?.resolve('rename')}>
            保留两者
          </Button>
          <Button variant="danger" onClick={() => state?.resolve('overwrite')}>
            覆盖
          </Button>
        </>
      }
    />
  )
}

/* ── 属性 ── */

export function PropertiesDialog({ entry, host, onClose }: { entry: FsEntry | null; host?: Host; onClose: () => void }) {
  const rows: Array<[string, React.ReactNode]> = entry
    ? [
        ['名称', entry.name],
        ['完整路径', <span className="font-mono">{entry.path}</span>],
        ['主机', host?.name ?? '—'],
        ['类型', entry.directory ? '目录' : entry.symlink ? '符号链接' : '文件'],
        ...(entry.symlink_target ? ([['链接目标', <span className="font-mono">{entry.symlink_target}</span>]] as Array<[string, React.ReactNode]>) : []),
        ['大小', entry.directory ? '—' : `${formatSize(entry.size)}（${entry.size.toLocaleString()} 字节）`],
        ['权限', <span className="font-mono">{entry.perm} · {entry.mode}</span>],
        ['所有者', <span className="font-mono">uid {entry.uid} · gid {entry.gid}</span>],
        ['修改时间', formatMtime(entry.mtime)],
      ]
    : []
  return (
    <Dialog open={entry != null} onOpenChange={(open) => !open && onClose()} size="md" title="属性">
      <dl className="divide-y divide-border rounded-[10px] border border-border">
        {rows.map(([label, value]) => (
          <div key={label} className="grid grid-cols-[96px_1fr] gap-3 px-3 py-2 text-[13px]">
            <dt className="text-muted">{label}</dt>
            <dd className="min-w-0 break-all text-text">{value}</dd>
          </div>
        ))}
      </dl>
      {entry && (
        <Button
          variant="ghost"
          size="sm"
          className="mt-3"
          onClick={() => {
            void navigator.clipboard.writeText(entry.path)
            toast.success('路径已复制')
          }}
        >
          <CopyIcon size={14} />
          复制路径
        </Button>
      )}
    </Dialog>
  )
}
