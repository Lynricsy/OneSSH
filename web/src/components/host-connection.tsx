import { useState } from 'react'
import type { Host } from '@/api/types'

/** 地址默认只做视觉脱敏；用户名与端口保留，便于在多台主机间快速辨认。 */
export function HostConnection({ host }: { host: Host }) {
  const [revealed, setRevealed] = useState(false)
  const connection = `${host.username}@${host.addr}:${host.port}`

  return (
    <button
      type="button"
      onClick={() => setRevealed((value) => !value)}
      aria-label={
        revealed ? `${connection}，点击隐藏服务器地址` : `${host.name} 的服务器地址已隐藏，点击显示`
      }
      aria-pressed={revealed}
      className="mt-0.5 inline-flex max-w-full min-w-0 items-center rounded-[4px] font-mono text-[12px] text-muted tabular-nums transition-colors hover:text-text"
    >
      <span className="truncate">
        {host.username}@
        <span
          className={`inline-block transition-[filter] duration-200 ${
            revealed ? '' : 'select-none blur-[4px]'
          }`}
        >
          {host.addr}
        </span>
        :{host.port}
      </span>
    </button>
  )
}
