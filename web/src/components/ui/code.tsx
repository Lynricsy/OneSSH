import { Check, Copy } from '@phosphor-icons/react'
import { useState } from 'react'
import { toast } from 'sonner'
import { cn } from '@/lib/cn'

export function Code({
  children,
  copyable,
  className,
  title,
}: {
  children: string
  copyable?: boolean
  className?: string
  title?: string
}) {
  const [copied, setCopied] = useState(false)

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(children)
      setCopied(true)
      toast.success('已复制')
      setTimeout(() => setCopied(false), 1600)
    } catch {
      toast.error('复制失败，请手动选择文本')
    }
  }

  return (
    <span className={cn('inline-flex max-w-full items-center gap-1.5', className)}>
      <code className="truncate font-mono text-[12px] text-muted tabular-nums" title={title ?? children}>
        {children}
      </code>
      {copyable && (
        <button
          type="button"
          onClick={copy}
          aria-label="复制"
          className="shrink-0 rounded-[4px] p-0.5 text-muted transition-colors hover:bg-surface-2 hover:text-text"
        >
          {copied ? <Check size={13} className="text-success" /> : <Copy size={13} />}
        </button>
      )}
    </span>
  )
}
