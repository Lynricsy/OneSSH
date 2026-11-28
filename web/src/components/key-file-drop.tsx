import { useRef, useState } from 'react'
import { CloudArrowUp, FileLock, LockKey, WarningCircle, X } from '@phosphor-icons/react'
import { toast } from 'sonner'
import { cn } from '@/lib/cn'
import {
  algorithmLabel,
  formatFileSize,
  readKeyFile,
  type LoadedKeyFile,
} from '@/lib/ssh-key-file'

/**
 * 私钥文件投放区：点击选择或直接拖入。只在浏览器内读取文本做预检，
 * 不会自行上传——调用方决定什么时候把内容交给 /keys 接口。
 */
export function KeyFileDrop({
  file,
  onLoad,
  onClear,
  className,
}: {
  file: LoadedKeyFile | null
  onLoad: (file: LoadedKeyFile) => void
  onClear?: () => void
  className?: string
}) {
  const inputRef = useRef<HTMLInputElement>(null)
  const [dragging, setDragging] = useState(false)

  const accept = async (picked: File | undefined) => {
    if (!picked) return
    try {
      onLoad(await readKeyFile(picked))
    } catch (error) {
      toast.error((error as Error).message)
    }
  }

  const picker = (
    <input
      ref={inputRef}
      type="file"
      className="hidden"
      // 私钥文件通常没有扩展名（id_ed25519），accept 一旦收窄反而选不到
      onChange={(event) => {
        void accept(event.target.files?.[0])
        event.target.value = ''
      }}
    />
  )

  const dropHandlers = {
    onDragOver: (event: React.DragEvent) => {
      event.preventDefault()
      setDragging(true)
    },
    onDragLeave: () => setDragging(false),
    onDrop: (event: React.DragEvent) => {
      event.preventDefault()
      setDragging(false)
      void accept(event.dataTransfer.files?.[0])
    },
  }

  if (file) {
    const { info } = file
    const algorithm = algorithmLabel(info.algorithm)
    return (
      <div
        {...dropHandlers}
        className={cn(
          'flex items-center gap-3 rounded-[10px] border px-3 py-2.5 transition-colors',
          info.problem ? 'border-danger/40 bg-danger/5' : 'border-accent/40 bg-accent-soft',
          dragging && 'border-accent',
          className,
        )}
      >
        {picker}
        <span
          className={cn(
            'grid size-9 shrink-0 place-items-center rounded-[8px]',
            info.problem ? 'bg-danger/12 text-danger' : 'bg-accent/15 text-accent',
          )}
        >
          {info.problem ? <WarningCircle size={18} weight="fill" /> : <FileLock size={18} weight="duotone" />}
        </span>
        <div className="min-w-0 flex-1">
          <p className="truncate font-mono text-[13px] font-medium text-text">{file.fileName}</p>
          {info.problem ? (
            <p className="mt-0.5 text-[12px] leading-5 text-danger">{info.problem}</p>
          ) : (
            <p className="mt-0.5 flex flex-wrap items-center gap-x-2 text-[12px] text-muted">
              <span className="tabular-nums">{formatFileSize(file.size)}</span>
              {algorithm && (
                <>
                  <span className="text-faint">·</span>
                  <span>{algorithm}</span>
                </>
              )}
              <span className="text-faint">·</span>
              {info.encrypted ? (
                <span className="inline-flex items-center gap-1 text-warning">
                  <LockKey size={12} weight="fill" />
                  受口令保护
                </span>
              ) : (
                <span className="text-success">无口令</span>
              )}
            </p>
          )}
        </div>
        <button
          type="button"
          onClick={() => inputRef.current?.click()}
          className="shrink-0 rounded-[6px] px-2 py-1 text-[12px] font-medium text-accent transition-colors hover:bg-accent/10"
        >
          更换
        </button>
        {onClear && (
          <button
            type="button"
            aria-label="移除文件"
            onClick={onClear}
            className="shrink-0 rounded-[6px] p-1 text-muted transition-colors hover:bg-surface-2 hover:text-text"
          >
            <X size={14} />
          </button>
        )}
      </div>
    )
  }

  return (
    <button
      type="button"
      {...dropHandlers}
      onClick={() => inputRef.current?.click()}
      className={cn(
        'group relative flex w-full flex-col items-center justify-center gap-2 overflow-hidden rounded-[12px] border border-dashed px-4 py-6 text-center transition-all duration-200',
        dragging
          ? 'scale-[1.01] border-accent bg-accent-soft'
          : 'border-border-strong bg-surface-2/60 hover:border-accent hover:bg-accent-soft',
        className,
      )}
    >
      {picker}
      <span className="grid size-11 place-items-center rounded-full bg-accent/12 text-accent transition-transform duration-200 group-hover:-translate-y-0.5">
        <CloudArrowUp size={22} weight="duotone" />
      </span>
      <span className="text-[13px] font-medium text-text">
        拖入私钥文件，或 <span className="text-accent">点击选择</span>
      </span>
      <span className="text-[12px] leading-5 text-muted">
        支持 OpenSSH / PEM（id_ed25519、id_rsa、*.pem），不超过 64 KiB
      </span>
    </button>
  )
}
