import { useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useForm } from 'react-hook-form'
import {
  Check,
  ClipboardText,
  Copy,
  Fingerprint,
  HardDrives,
  Key,
  LockKey,
  Plus,
  ShieldCheck,
  Sparkle,
  Trash,
  UploadSimple,
} from '@phosphor-icons/react'
import { AnimatePresence, motion, useReducedMotion } from 'motion/react'
import { toast } from 'sonner'
import { useCreateKey, useDeleteKey, useDeleteKeys, useHosts, useKeys } from '@/api/queries'
import type { KeyPayload, SSHKey } from '@/api/types'
import { ConfirmDialog } from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog } from '@/components/ui/dialog'
import { EmptyState } from '@/components/ui/empty-state'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { PageHeader } from '@/components/ui/page-header'
import { PageTransition } from '@/components/ui/page-transition'
import { SelectionBar } from '@/components/ui/selection-bar'
import { Skeleton } from '@/components/ui/skeleton'
import { Textarea } from '@/components/ui/textarea'
import { KeyFileDrop } from '@/components/key-file-drop'
import { cn } from '@/lib/cn'
import { formatTime } from '@/lib/format'
import { staggerContainer, staggerItem } from '@/lib/motion'
import {
  algorithmLabel,
  fingerprintOf,
  inspectKeyText,
  keyNameFromFile,
  uniqueKeyName,
  type LoadedKeyFile,
} from '@/lib/ssh-key-file'

type KeyFormValues = {
  name: string
  mode: KeyPayload['mode']
  private_key: string
  passphrase: string
}

const defaultValues: KeyFormValues = {
  name: '',
  mode: 'generate',
  private_key: '',
  passphrase: '',
}

/** 算法决定卡片的色调：一眼区分 ed25519 / RSA / ECDSA，而不用逐张读公钥前缀 */
const algorithmTone = (algorithm: string) => {
  if (algorithm.includes('ed25519')) return 'from-teal-500/80 to-emerald-400/70 text-teal-600 dark:text-teal-300'
  if (algorithm.includes('rsa')) return 'from-indigo-500/80 to-violet-400/70 text-indigo-600 dark:text-indigo-300'
  if (algorithm.includes('ecdsa')) return 'from-amber-500/80 to-orange-400/70 text-amber-600 dark:text-amber-300'
  return 'from-slate-500/70 to-slate-400/60 text-slate-600 dark:text-slate-300'
}

function useFingerprint(publicKey: string) {
  const [value, setValue] = useState<string | null>(null)
  useEffect(() => {
    let alive = true
    void fingerprintOf(publicKey).then((fp) => alive && setValue(fp))
    return () => {
      alive = false
    }
  }, [publicKey])
  return value
}

function ModeCard({
  active,
  icon,
  title,
  description,
  onClick,
}: {
  active: boolean
  icon: React.ReactNode
  title: string
  description: string
  onClick: () => void
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={active}
      onClick={onClick}
      className={cn(
        'flex items-start gap-3 rounded-[12px] border p-3 text-left transition-all duration-150',
        active
          ? 'border-accent bg-accent-soft shadow-[0_0_0_3px_var(--accent-soft)]'
          : 'border-border bg-surface hover:border-border-strong hover:bg-surface-2',
      )}
    >
      <span
        className={cn(
          'grid size-9 shrink-0 place-items-center rounded-[10px] transition-colors',
          active ? 'bg-accent text-accent-fg' : 'bg-surface-2 text-muted',
        )}
      >
        {icon}
      </span>
      <span className="min-w-0">
        <span className="block text-[13px] font-semibold text-text">{title}</span>
        <span className="mt-0.5 block text-[12px] leading-5 text-muted">{description}</span>
      </span>
    </button>
  )
}

function KeyCard({
  sshKey,
  usedBy,
  selected,
  copied,
  onToggle,
  onCopy,
  onDelete,
}: {
  sshKey: SSHKey
  usedBy: string[]
  selected: boolean
  copied: boolean
  onToggle: () => void
  onCopy: () => void
  onDelete: () => void
}) {
  // 公钥首段是算法标识，单独成行后剩下的 base64 才是真正需要核对的材料
  const spaceAt = sshKey.public_key.indexOf(' ')
  const algorithm = spaceAt > 0 ? sshKey.public_key.slice(0, spaceAt) : 'ssh-key'
  const material = (spaceAt > 0 ? sshKey.public_key.slice(spaceAt + 1) : sshKey.public_key).trim()
  const fingerprint = useFingerprint(sshKey.public_key)
  const tone = algorithmTone(algorithm)

  return (
    <Card
      className={cn(
        'group relative flex h-full flex-col overflow-hidden p-0 transition-all duration-200 hover:-translate-y-0.5 hover:shadow-pop',
        selected && 'border-accent ring-2 ring-accent/20',
      )}
    >
      <div className={cn('h-1 w-full bg-linear-to-r', tone)} />
      <div className="flex flex-1 flex-col p-4">
        <div className="flex items-start justify-between gap-2">
          <div className="flex min-w-0 items-start gap-3">
            <Checkbox
              className="mt-2.5"
              checked={selected}
              onCheckedChange={onToggle}
              aria-label={`选择密钥 ${sshKey.name}`}
            />
            <span
              className={cn(
                'grid size-10 shrink-0 place-items-center rounded-[10px] bg-linear-to-br text-white shadow-sm',
                tone.split(' ').filter((c) => c.startsWith('from-') || c.startsWith('to-')).join(' '),
              )}
            >
              <Key size={18} weight="fill" />
            </span>
            <div className="min-w-0">
              <p className="truncate leading-5 font-semibold text-text">{sshKey.name}</p>
              <p className="mt-0.5 flex items-center gap-1.5 text-[12px] text-muted">
                <span className={cn('font-medium', tone.split(' ').filter((c) => c.startsWith('text-') || c.startsWith('dark:')).join(' '))}>
                  {algorithmLabel(algorithm) ?? algorithm}
                </span>
                <span className="text-faint">·</span>
                <span className="tabular-nums">{formatTime(sshKey.created_at)}</span>
              </p>
            </div>
          </div>
          <Button
            variant="ghost"
            size="icon"
            className="-mt-1 -mr-1 shrink-0 text-muted opacity-70 group-hover:opacity-100 hover:bg-danger/10 hover:text-danger"
            aria-label={`删除密钥 ${sshKey.name}`}
            title="删除密钥"
            onClick={onDelete}
          >
            <Trash size={16} />
          </Button>
        </div>

        <div className="mt-3 flex min-h-[26px] flex-wrap items-center gap-1.5">
          {usedBy.length ? (
            <>
              <HardDrives size={13} className="text-faint" />
              {usedBy.slice(0, 4).map((name) => (
                <span
                  key={name}
                  className="rounded-full bg-surface-2 px-2 py-0.5 font-mono text-[11px] text-text"
                >
                  {name}
                </span>
              ))}
              {usedBy.length > 4 && (
                <span className="text-[11px] text-muted">+{usedBy.length - 4}</span>
              )}
            </>
          ) : (
            <span className="rounded-full border border-dashed border-border-strong px-2 py-0.5 text-[11px] text-muted">
              暂无主机使用
            </span>
          )}
        </div>

        <div className="relative mt-3 grow rounded-[10px] bg-surface-2 p-3 pr-11">
          {/* select-all：单击即可整段选中，长 base64 手动拖选很痛苦 */}
          <p className="line-clamp-2 leading-[1.7] font-mono text-[12px] break-all text-text select-all">
            {material}
          </p>
          <Button
            variant="ghost"
            size="icon"
            className="absolute top-1.5 right-1.5 h-8 w-8"
            aria-label={`复制公钥 ${sshKey.name}`}
            title="复制公钥"
            onClick={onCopy}
          >
            {copied ? <Check size={14} className="text-success" /> : <Copy size={14} />}
          </Button>
        </div>
        {fingerprint && (
          <p
            className="mt-2.5 flex items-center gap-1.5 truncate font-mono text-[11px] text-muted"
            title={fingerprint}
          >
            <Fingerprint size={13} className="shrink-0 text-faint" />
            <span className="truncate">{fingerprint}</span>
          </p>
        )}
      </div>
    </Card>
  )
}

export function KeysPage() {
  const keys = useKeys()
  const hosts = useHosts()
  const createKey = useCreateKey()
  const deleteKey = useDeleteKey()
  const deleteKeys = useDeleteKeys()
  const reduceMotion = useReducedMotion()
  const [createOpen, setCreateOpen] = useState(false)
  const [deleting, setDeleting] = useState<SSHKey | null>(null)
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [batchDeleting, setBatchDeleting] = useState(false)
  const [copiedId, setCopiedId] = useState<number | null>(null)
  const [file, setFile] = useState<LoadedKeyFile | null>(null)
  const [pasteOpen, setPasteOpen] = useState(false)
  const {
    handleSubmit,
    register,
    reset,
    watch,
    setValue,
    getValues,
    formState: { errors },
  } = useForm<KeyFormValues>({ defaultValues })
  const mode = watch('mode')
  const privateKey = watch('private_key')
  const pasted = useMemo(() => (privateKey.trim() ? inspectKeyText(privateKey) : null), [privateKey])

  /** 主机名按密钥聚合：引用关系决定删除的破坏力，卡片与确认弹层都要用 */
  const usage = useMemo(() => {
    const map = new Map<number, string[]>()
    for (const host of hosts.data ?? []) {
      if (host.auth_type !== 'key' || host.key_id == null) continue
      map.set(host.key_id, [...(map.get(host.key_id) ?? []), host.name])
    }
    return map
  }, [hosts.data])
  const usageOf = (keyId: number) => usage.get(keyId)?.length ?? 0

  const total = keys.data?.length ?? 0
  const inUse = (keys.data ?? []).filter((key) => usageOf(key.id) > 0).length

  const openCreate = (initialMode: KeyPayload['mode']) => {
    reset({ ...defaultValues, mode: initialMode })
    setFile(null)
    setPasteOpen(false)
    setCreateOpen(true)
  }

  const changeCreateOpen = (open: boolean) => {
    setCreateOpen(open)
    if (!open) {
      reset(defaultValues)
      setFile(null)
    }
  }

  const loadFile = (loaded: LoadedKeyFile) => {
    setFile(loaded)
    setValue('private_key', loaded.text, { shouldValidate: true })
    setValue('passphrase', '')
    if (!getValues('name').trim()) {
      setValue(
        'name',
        uniqueKeyName(
          keyNameFromFile(loaded.fileName),
          (keys.data ?? []).map((key) => key.name),
        ),
        { shouldValidate: true },
      )
    }
  }

  const clearFile = () => {
    setFile(null)
    setValue('private_key', '')
  }

  const create = async (values: KeyFormValues) => {
    await createKey.mutateAsync({
      name: values.name.trim(),
      mode: values.mode,
      private_key: values.mode === 'import' ? values.private_key : undefined,
      passphrase: values.mode === 'import' && values.passphrase ? values.passphrase : undefined,
    })
    changeCreateOpen(false)
  }

  const copyPublicKey = async (key: SSHKey) => {
    try {
      await navigator.clipboard.writeText(key.public_key)
      // 图标就地变勾比只弹 toast 更贴近操作发生的位置
      setCopiedId(key.id)
      setTimeout(() => setCopiedId((current) => (current === key.id ? null : current)), 1600)
      toast.success('公钥已复制')
    } catch {
      toast.error('复制失败，请手动选择文本')
    }
  }

  const toggle = (id: number) =>
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

  const needsPassphrase = Boolean(pasted?.encrypted)

  // ?upload=1 / ?new=1 直达上传或生成弹窗（命令面板使用）
  const [searchParams, setSearchParams] = useSearchParams()
  useEffect(() => {
    const intent = searchParams.get('upload') === '1' ? 'import' : searchParams.get('new') === '1' ? 'generate' : null
    if (!intent) return
    openCreate(intent)
    searchParams.delete('upload')
    searchParams.delete('new')
    setSearchParams(searchParams, { replace: true })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchParams])

  return (
    <PageTransition>
      <PageHeader
        title="密钥"
        subtitle="生成 ed25519 密钥，或上传已有的 OpenSSH / PEM 私钥文件"
        actions={
          <>
            <Button variant="outline" onClick={() => openCreate('import')}>
              <UploadSimple size={15} />
              上传私钥
            </Button>
            <Button variant="primary" onClick={() => openCreate('generate')}>
              <Plus size={15} />
              新建密钥
            </Button>
          </>
        }
      />

      {total > 0 && (
        <div className="mb-5 flex flex-wrap items-center gap-2 text-[12px]">
          <span className="inline-flex items-center gap-1.5 rounded-full border border-border bg-surface px-3 py-1 text-muted">
            <Key size={13} className="text-accent" />共 <b className="text-text tabular-nums">{total}</b> 把
          </span>
          <span className="inline-flex items-center gap-1.5 rounded-full border border-border bg-surface px-3 py-1 text-muted">
            <span className="size-1.5 rounded-full bg-success" />在用 <b className="text-text tabular-nums">{inUse}</b>
          </span>
          <span className="inline-flex items-center gap-1.5 rounded-full border border-border bg-surface px-3 py-1 text-muted">
            <span className="size-1.5 rounded-full bg-border-strong" />闲置 <b className="text-text tabular-nums">{total - inUse}</b>
          </span>
          <span className="ml-auto inline-flex items-center gap-1.5 text-muted">
            <ShieldCheck size={14} weight="duotone" className="text-accent" />
            私钥经 AES-256-GCM 加密保存，界面与 MCP 都不会返回私钥
          </span>
        </div>
      )}

      {keys.isLoading ? (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {Array.from({ length: 3 }, (_, index) => (
            <Skeleton key={index} className="h-[220px] rounded-[12px]" />
          ))}
        </div>
      ) : keys.data?.length ? (
        <motion.div
          className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3"
          variants={staggerContainer}
          initial="hidden"
          animate="visible"
        >
          {keys.data.map((key) => (
            <motion.div key={key.id} variants={staggerItem}>
              <KeyCard
                sshKey={key}
                usedBy={usage.get(key.id) ?? []}
                selected={selected.has(key.id)}
                copied={copiedId === key.id}
                onToggle={() => toggle(key.id)}
                onCopy={() => void copyPublicKey(key)}
                onDelete={() => setDeleting(key)}
              />
            </motion.div>
          ))}
        </motion.div>
      ) : (
        <EmptyState
          icon={<Key size={22} />}
          title="还没有密钥"
          description="生成一把 ed25519 密钥，或直接拖入已有的私钥文件。"
          action={
            <div className="flex flex-wrap justify-center gap-2">
              <Button variant="outline" onClick={() => openCreate('import')}>
                <UploadSimple size={15} />
                上传私钥文件
              </Button>
              <Button variant="primary" onClick={() => openCreate('generate')}>
                <Plus size={15} />
                生成第一把密钥
              </Button>
            </div>
          }
        />
      )}

      <Dialog
        open={createOpen}
        onOpenChange={changeCreateOpen}
        size="md"
        title={mode === 'import' ? '上传私钥' : '新建密钥'}
        footer={
          <>
            <Button variant="ghost" type="button" onClick={() => changeCreateOpen(false)}>
              取消
            </Button>
            <Button
              variant="primary"
              type="submit"
              form="create-key-form"
              loading={createKey.isPending}
            >
              {mode === 'import' ? '上传' : '生成'}
            </Button>
          </>
        }
      >
        <form id="create-key-form" onSubmit={handleSubmit(create)}>
          <div className="space-y-4">
            <div role="radiogroup" aria-label="模式" className="grid gap-2 sm:grid-cols-2">
              <ModeCard
                active={mode === 'generate'}
                icon={<Sparkle size={17} weight="fill" />}
                title="生成 ed25519"
                description="网关内生成，只回传公钥"
                onClick={() => setValue('mode', 'generate')}
              />
              <ModeCard
                active={mode === 'import'}
                icon={<UploadSimple size={17} weight="bold" />}
                title="上传私钥"
                description="选择文件或粘贴已有私钥"
                onClick={() => setValue('mode', 'import')}
              />
            </div>
            <Field label="名称" required error={errors.name?.message}>
              {(id) => (
                <Input
                  id={id}
                  placeholder="例如 deploy-ed25519"
                  maxLength={64}
                  invalid={Boolean(errors.name)}
                  {...register('name', {
                    validate: (value) => value.trim() !== '' || '请输入名称',
                  })}
                />
              )}
            </Field>
          </div>
          {/* 私钥区随模式展开而不是瞬间弹出：弹层高度跟着一起长，切换时不会突然跳一大截 */}
          <AnimatePresence initial={false}>
            {mode === 'import' && (
              <motion.div
                key="private-key"
                className="overflow-hidden"
                initial={reduceMotion ? false : { height: 0, opacity: 0 }}
                animate={{ height: 'auto', opacity: 1 }}
                exit={reduceMotion ? { opacity: 0 } : { height: 0, opacity: 0 }}
                transition={{ duration: reduceMotion ? 0 : 0.22, ease: 'easeOut' }}
              >
                {/* 负外边距补回 overflow-hidden 裁掉的焦点环空间，同时保持与上方字段左右对齐 */}
                <div className="-m-0.5 space-y-4 p-0.5 pt-[18px]">
                  <div className="space-y-2">
                    <KeyFileDrop file={file} onLoad={loadFile} onClear={clearFile} />
                    {!file && (
                      <button
                        type="button"
                        onClick={() => setPasteOpen((open) => !open)}
                        className="inline-flex items-center gap-1.5 text-[12px] font-medium text-accent hover:text-accent-hover"
                      >
                        <ClipboardText size={13} />
                        {pasteOpen ? '收起粘贴框' : '或者直接粘贴私钥内容'}
                      </button>
                    )}
                  </div>
                  {/* 文件模式下 textarea 隐藏但仍注册，提交值来自同一个字段 */}
                  <div className={cn(!file && pasteOpen ? 'block' : 'hidden')}>
                    <Field
                      label="私钥内容"
                      error={errors.private_key?.message}
                    >
                      {(id) => (
                        <Textarea
                          id={id}
                          rows={7}
                          className="font-mono text-[12px]"
                          placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
                          invalid={Boolean(errors.private_key)}
                          {...register('private_key', {
                            validate: (value) => {
                              if (getValues('mode') !== 'import') return true
                              if (!value.trim()) return '请选择私钥文件或粘贴私钥内容'
                              return inspectKeyText(value).problem ?? true
                            },
                          })}
                        />
                      )}
                    </Field>
                  </div>
                  {(file || !pasteOpen) && errors.private_key?.message && (
                    <p className="-mt-2 text-[12px] text-danger">{errors.private_key.message}</p>
                  )}
                  <Field
                    label={needsPassphrase ? '私钥口令（检测到口令保护）' : '私钥口令'}
                    required={needsPassphrase}
                    error={errors.passphrase?.message}
                    hint="私钥受口令保护时填写；仅用于本次解密，不会保存"
                  >
                    {(id) => (
                      <Input
                        id={id}
                        type="password"
                        autoComplete="new-password"
                        prefix={<LockKey size={14} />}
                        invalid={Boolean(errors.passphrase)}
                        {...register('passphrase', {
                          validate: (value) =>
                            getValues('mode') !== 'import' ||
                            !inspectKeyText(getValues('private_key')).encrypted ||
                            value !== '' ||
                            '该私钥受口令保护，请填写口令',
                        })}
                      />
                    )}
                  </Field>
                </div>
              </motion.div>
            )}
          </AnimatePresence>
        </form>
      </Dialog>

      <SelectionBar count={selected.size} onClear={() => setSelected(new Set())}>
        <Button variant="danger" size="sm" onClick={() => setBatchDeleting(true)}>
          删除
        </Button>
      </SelectionBar>

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title="删除密钥？"
        description={
          deleting && usageOf(deleting.id) > 0
            ? `${deleting.name} 正被 ${usageOf(deleting.id)} 台主机使用，删除后这些主机将无法连接。`
            : `${deleting?.name ?? ''} 删除后无法恢复，私钥不会留有备份。`
        }
        confirmText="删除"
        danger
        onConfirm={async () => {
          if (!deleting) return
          await deleteKey.mutateAsync(deleting.id)
          // 删成功才摘掉选中项：失败时保留，批量条上还能看到它并重试
          setSelected((prev) => {
            if (!prev.has(deleting.id)) return prev
            const next = new Set(prev)
            next.delete(deleting.id)
            return next
          })
        }}
      />

      <ConfirmDialog
        open={batchDeleting}
        onOpenChange={setBatchDeleting}
        title={`批量删除 ${selected.size} 把密钥？`}
        description="所选密钥删除后无法恢复，正使用它们的主机将无法连接。"
        confirmText="删除"
        danger
        onConfirm={async () => {
          // 失败的留在选中态，方便对照着重试
          const { failedIds } = await deleteKeys.mutateAsync([...selected])
          setSelected(new Set(failedIds))
        }}
      />
    </PageTransition>
  )
}
