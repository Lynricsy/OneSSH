import { Check, Copy } from '@phosphor-icons/react'
import { useState } from 'react'
import { toast } from 'sonner'
import { cn } from '@/lib/cn'

/**
 * 带复制按钮的原文块：命令、调用摘要这类需要原样带走的内容统一走这里。
 * clamp 用于详情场景——下方还有更长的输出块时，命令块限高滚动，免得一条超长命令把整页撑开。
 */
export function CopyableBlock({
  label,
  value,
  clamp,
}: {
  label: string
  value: string
  clamp?: boolean
}) {
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(true)
      toast.success('已复制')
      window.setTimeout(() => setCopied(false), 1600)
    } catch {
      toast.error('复制失败，请手动选择文本')
    }
  }
  return (
    <div>
      <div className="mb-1.5 flex items-center justify-between gap-2">
        <p className="text-[11px] tracking-wide text-muted uppercase">{label}</p>
        <button
          type="button"
          onClick={() => void copy()}
          aria-label={`复制${label}`}
          className="rounded-[4px] p-0.5 text-muted transition-colors hover:bg-surface-2 hover:text-text"
        >
          {copied ? <Check size={13} className="text-success" /> : <Copy size={13} />}
        </button>
      </div>
      <pre
        className={cn(
          'rounded-[8px] bg-surface-2 px-3 py-2.5 font-mono text-[12px] leading-[1.6] break-words whitespace-pre-wrap text-text',
          clamp && 'max-h-56 overflow-auto',
        )}
      >
        {value}
      </pre>
    </div>
  )
}
