import { useState } from 'react'
import { Controller, useForm } from 'react-hook-form'
import { Check, Copy, Key, Plus, Trash } from '@phosphor-icons/react'
import { AnimatePresence, motion, useReducedMotion } from 'motion/react'
import { toast } from 'sonner'
import { useCreateKey, useDeleteKey, useHosts, useKeys } from '@/api/queries'
import type { KeyPayload, SSHKey } from '@/api/types'
import { ConfirmDialog } from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Dialog } from '@/components/ui/dialog'
import { EmptyState } from '@/components/ui/empty-state'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { PageHeader } from '@/components/ui/page-header'
import { PageTransition } from '@/components/ui/page-transition'
import { Select } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Textarea } from '@/components/ui/textarea'
import { formatTime } from '@/lib/format'
import { staggerContainer, staggerItem } from '@/lib/motion'

type KeyFormValues = {
  name: string
  mode: KeyPayload['mode']
  private_key: string
}

const defaultValues: KeyFormValues = {
  name: '',
  mode: 'generate',
  private_key: '',
}

export function KeysPage() {
  const keys = useKeys()
  const hosts = useHosts()
  const createKey = useCreateKey()
  const deleteKey = useDeleteKey()
  const reduceMotion = useReducedMotion()
  const [createOpen, setCreateOpen] = useState(false)
  const [deleting, setDeleting] = useState<SSHKey | null>(null)
  const [copiedId, setCopiedId] = useState<number | null>(null)
  const {
    control,
    handleSubmit,
    register,
    reset,
    watch,
    formState: { errors },
  } = useForm<KeyFormValues>({ defaultValues })
  const mode = watch('mode')

  // 引用数决定删除的破坏力，直接摆在卡片与确认弹层里，比事后翻主机页可靠。
  const usageOf = (keyId: number) =>
    (hosts.data ?? []).filter((host) => host.key_id === keyId).length

  const openCreate = () => {
    reset(defaultValues)
    setCreateOpen(true)
  }

  const changeCreateOpen = (open: boolean) => {
    setCreateOpen(open)
    if (!open) reset(defaultValues)
  }

  const create = async (values: KeyFormValues) => {
    await createKey.mutateAsync({
      name: values.name,
      mode: values.mode,
      private_key: values.mode === 'import' ? values.private_key : undefined,
    })
    setCreateOpen(false)
    reset(defaultValues)
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

  return (
    <PageTransition>
      <PageHeader
        title="密钥"
        subtitle="OpenSSH ed25519 生成与私钥导入"
        actions={
          <Button variant="primary" onClick={openCreate}>
            <Plus size={15} />
            新建密钥
          </Button>
        }
      />

      {keys.isLoading ? (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {Array.from({ length: 3 }, (_, index) => (
            <Skeleton key={index} className="h-[173px] rounded-[12px]" />
          ))}
        </div>
      ) : keys.data?.length ? (
        <motion.div
          className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3"
          variants={staggerContainer}
          initial="hidden"
          animate="visible"
        >
          {keys.data.map((key) => {
            // 公钥首段是算法标识，单独成行后剩下的 base64 才是真正需要核对的材料
            const spaceAt = key.public_key.indexOf(' ')
            const algorithm = spaceAt > 0 ? key.public_key.slice(0, spaceAt) : 'ssh-key'
            const material = spaceAt > 0 ? key.public_key.slice(spaceAt + 1) : key.public_key
            const usage = usageOf(key.id)
            return (
              <motion.div key={key.id} variants={staggerItem}>
                <Card className="flex h-full flex-col p-4">
                  <div className="flex items-start justify-between gap-2">
                    <div className="min-w-0">
                      <p className="truncate leading-5 font-medium text-text">{key.name}</p>
                      <p className="mt-1 truncate text-[12px] text-muted">
                        <span className="tabular-nums">{formatTime(key.created_at)}</span>
                        <span className="px-1.5">·</span>
                        {usage > 0 ? `${usage} 台主机在用` : '暂无主机使用'}
                      </p>
                    </div>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="-mt-1 -mr-1 shrink-0 text-muted hover:bg-danger/10 hover:text-danger"
                      aria-label={`删除密钥 ${key.name}`}
                      title="删除密钥"
                      onClick={() => setDeleting(key)}
                    >
                      <Trash size={16} />
                    </Button>
                  </div>
                  <div className="relative mt-3 grow rounded-[8px] bg-surface-2 p-3 pr-11">
                    <p className="font-mono text-[11px] text-muted">{algorithm}</p>
                    {/* select-all：单击即可整段选中，长 base64 手动拖选很痛苦 */}
                    <p className="mt-1 line-clamp-2 leading-[1.7] font-mono text-[12px] break-all text-text select-all">
                      {material}
                    </p>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="absolute top-1.5 right-1.5 h-8 w-8"
                      aria-label={`复制公钥 ${key.name}`}
                      title="复制公钥"
                      onClick={() => void copyPublicKey(key)}
                    >
                      {copiedId === key.id ? (
                        <Check size={14} className="text-success" />
                      ) : (
                        <Copy size={14} />
                      )}
                    </Button>
                  </div>
                </Card>
              </motion.div>
            )
          })}
        </motion.div>
      ) : (
        <EmptyState
          icon={<Key size={22} />}
          title="还没有密钥"
          description="生成一把 ed25519 密钥，或导入已有的 OpenSSH 私钥。"
          action={
            <Button variant="primary" onClick={openCreate}>
              <Plus size={15} />
              生成第一把密钥
            </Button>
          }
        />
      )}

      <Dialog
        open={createOpen}
        onOpenChange={changeCreateOpen}
        size="md"
        title="新建密钥"
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
              创建
            </Button>
          </>
        }
      >
        <form id="create-key-form" onSubmit={handleSubmit(create)}>
          <div className="space-y-4">
            <Field label="名称" required error={errors.name?.message}>
              {(id) => (
                <Input
                  id={id}
                  autoFocus
                  placeholder="例如 deploy-ed25519"
                  invalid={Boolean(errors.name)}
                  {...register('name', { required: '请输入名称' })}
                />
              )}
            </Field>
            <Field
              label="模式"
              required
              error={errors.mode?.message}
              hint={mode === 'generate' ? '服务端生成私钥并只回传公钥' : undefined}
            >
              {(id) => (
                <Controller
                  name="mode"
                  control={control}
                  rules={{ required: '请选择模式' }}
                  render={({ field }) => (
                    <Select
                      id={id}
                      value={field.value}
                      onChange={field.onChange}
                      invalid={Boolean(errors.mode)}
                      options={[
                        { value: 'generate', label: '生成 ed25519' },
                        { value: 'import', label: '导入 OpenSSH 私钥' },
                      ]}
                    />
                  )}
                />
              )}
            </Field>
          </div>
          {/* 私钥框随模式展开而不是瞬间弹出：弹层高度跟着一起长，切换时不会突然跳一大截 */}
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
                <div className="-m-0.5 p-0.5 pt-[18px]">
                  <Field label="私钥" required error={errors.private_key?.message}>
                    {(id) => (
                      <Textarea
                        id={id}
                        rows={8}
                        placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
                        invalid={Boolean(errors.private_key)}
                        {...register('private_key', {
                          required: mode === 'import' ? '请输入私钥' : false,
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
          if (deleting) await deleteKey.mutateAsync(deleting.id)
        }}
      />
    </PageTransition>
  )
}
