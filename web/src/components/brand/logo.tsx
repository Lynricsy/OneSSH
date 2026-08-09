import { cn } from '@/lib/cn'

/**
 * OneSSH 品牌符号：左侧终端提示符 `>` 是唯一入口，右侧三条链路是被统一纳管的多台主机。
 *
 * 坐标基于 32×32 网格，与 `web/public/logo.svg`（favicon）保持同一份几何，改一处必须改另一处。
 * 线宽 3.2 / 2.7 与两组元素之间 2.6 的间隙是 16px favicon 下仍能分辨的下限，调整前先在小尺寸下验证。
 * 描边一律 currentColor，因此嵌进任何容器都跟随文字色，不需要为浅色/深色主题各留一份。
 */
export function LogoMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" fill="none" aria-hidden className={className}>
      <path
        d="M7.8 9.2 13.6 16 7.8 22.8"
        stroke="currentColor"
        strokeWidth="3.2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <g stroke="currentColor" strokeWidth="2.7" strokeLinecap="round">
        <path d="M19.2 10.6h5.2" />
        <path d="M19.2 16h5.2" />
        <path d="M19.2 21.4h5.2" />
      </g>
    </svg>
  )
}

/**
 * 品牌图标块：accent 渐变底 + accent-fg 符号，侧栏、登录页、启动占位和授权页共用同一个。
 * 尺寸和圆角由调用方通过 className 给（`size-8 rounded-[8px]` 之类），符号自身按 100% 填满，
 * 留白已经画进 viewBox，不要再额外加 padding。
 */
export function LogoTile({ className }: { className?: string }) {
  return (
    <span
      className={cn(
        'flex shrink-0 items-center justify-center rounded-[8px] bg-gradient-to-br from-accent to-accent-hover text-accent-fg',
        className,
      )}
    >
      <LogoMark className="size-full" />
    </span>
  )
}
