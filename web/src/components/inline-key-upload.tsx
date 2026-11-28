import { useState } from 'react'
import { ShieldCheck } from '@phosphor-icons/react'
import { useQueryClient } from '@tanstack/react-query'
import { queryKeys, useCreateKey } from '@/api/queries'
import type { SSHKey } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { KeyFileDrop } from './key-file-drop'
import { keyNameFromFile, uniqueKeyName, type LoadedKeyFile } from '@/lib/ssh-key-file'

/**
 * 主机弹窗里的「就地上传私钥」：选文件 →（必要时）填口令 → 导入并自动选中。
 * 它嵌在主机表单内部，不能再套一层 <form>；回车键也要拦下来，免得误提交整张主机表单。
 */
export function InlineKeyUpload({
  existingNames,
  onCreated,
}: {
  existingNames: string[]
  onCreated: (key: SSHKey) => void
}) {
  const createKey = useCreateKey()
  const qc = useQueryClient()
  const [file, setFile] = useState<LoadedKeyFile | null>(null)
  const [name, setName] = useState('')
  const [passphrase, setPassphrase] = useState('')

  const load = (loaded: LoadedKeyFile) => {
    setFile(loaded)
    setPassphrase('')
    setName(uniqueKeyName(keyNameFromFile(loaded.fileName), existingNames))
  }

  const clear = () => {
    setFile(null)
    setName('')
    setPassphrase('')
  }

  const needsPassphrase = Boolean(file?.info.encrypted)
  const ready =
    file != null && !file.info.problem && name.trim() !== '' && (!needsPassphrase || passphrase !== '')

  const submit = async () => {
    if (!file || !ready || createKey.isPending) return
    let key: SSHKey
    try {
      key = await createKey.mutateAsync({
        name: name.trim(),
        mode: 'import',
        private_key: file.text,
        passphrase: needsPassphrase ? passphrase : undefined,
      })
    } catch {
      // 失败提示已由 mutation 的统一 onError 弹出；保留已选文件与名称，改个口令即可重试
      return
    }
    // 先把新密钥写进列表缓存再选中：Radix Select 选中一个尚不存在的选项时会显示空白，
    // 等后台重新拉取完成也不会自动补上文字。
    qc.setQueryData<SSHKey[]>(queryKeys.keys, (old = []) =>
      old.some((item) => item.id === key.id)
        ? old
        : [...old, key].sort((a, b) => a.name.localeCompare(b.name)),
    )
    setTimeout(() => onCreated(key), 0)
    clear()
  }

  const stopEnter = (event: React.KeyboardEvent) => {
    if (event.key !== 'Enter') return
    event.preventDefault()
    void submit()
  }

  return (
    <div className="space-y-3 rounded-[12px] border border-border bg-surface-2/50 p-3">
      <KeyFileDrop file={file} onLoad={load} onClear={clear} />
      {file && !file.info.problem && (
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1.5">
            <Label htmlFor="inline-key-name">密钥名称</Label>
            <Input
              id="inline-key-name"
              value={name}
              maxLength={64}
              onChange={(event) => setName(event.target.value)}
              onKeyDown={stopEnter}
            />
          </div>
          {needsPassphrase ? (
            <div className="space-y-1.5">
              <Label htmlFor="inline-key-passphrase">
                私钥口令<span className="ml-0.5 text-danger">*</span>
              </Label>
              <Input
                id="inline-key-passphrase"
                type="password"
                autoComplete="new-password"
                autoFocus
                value={passphrase}
                onChange={(event) => setPassphrase(event.target.value)}
                onKeyDown={stopEnter}
              />
            </div>
          ) : (
            <p className="flex items-end pb-2 text-[12px] leading-5 text-muted">
              私钥未加口令，可直接导入。
            </p>
          )}
        </div>
      )}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="inline-flex items-center gap-1.5 text-[12px] text-muted">
          <ShieldCheck size={14} weight="duotone" className="text-accent" />
          私钥经主密钥加密保存，口令只用于本次解密
        </p>
        <Button
          type="button"
          size="sm"
          variant="primary"
          disabled={!ready}
          loading={createKey.isPending}
          onClick={() => void submit()}
        >
          上传并使用
        </Button>
      </div>
    </div>
  )
}
