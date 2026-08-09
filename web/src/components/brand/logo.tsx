import { useId } from 'react'
import { cn } from '@/lib/cn'

/**
 * 外框是超椭圆 |x/16|³ + |y/16|³ = 1（n = 3，即小米 2021 标识采用的那一条），
 * 与圆角矩形的区别在于四角没有「直线接圆弧」的曲率突变，边缘更饱满、转角更收。
 *
 * 路径由最小二乘拟合生成：每象限 4 段三次贝塞尔，端点切向取解析值、切向量长度按
 * 最小二乘定，最大径向误差 0.0065（32 网格，约 0.02%）；四个象限由第一象限镜像而来，
 * 保证严格对称。手改这串数字只会破坏对称性和拟合精度——要调形状请重新生成整条路径。
 */
const SUPERELLIPSE =
  'M32 16C32 18.246 32.006 20.509 31.594 22.723C31.183 24.935 30.31 27.088 28.699 28.699C27.087 30.311 24.933 31.183 22.723 31.594C20.508 32.006 18.242 32 16 32C13.758 32 11.492 32.006 9.277 31.594C7.067 31.183 4.913 30.311 3.301 28.699C1.69 27.088 0.817 24.935 0.406 22.723C-0.006 20.509 0 18.246 0 16C0 13.754 -0.006 11.491 0.406 9.277C0.817 7.065 1.69 4.912 3.301 3.301C4.913 1.689 7.067 0.817 9.277 0.406C11.492 -0.006 13.758 0 16 0C18.242 0 20.508 -0.006 22.723 0.406C24.933 0.817 27.087 1.689 28.699 3.301C30.31 4.912 31.183 7.065 31.594 9.277C32.006 11.491 32 13.754 32 16Z'

/**
 * 品牌图标：左侧终端提示符 `>` 是唯一入口，右侧三条链路是被统一纳管的多台主机。
 *
 * 几何与 `web/public/logo.svg`（favicon）共用同一份 32×32 网格，改一处必须改另一处。
 * 线宽 3.2 / 2.7 与两组元素间 2.6 的间隙是 16px favicon 下仍能分辨的下限，调整前先在
 * 小尺寸下验证。渐变与描边都走 accent 令牌，浅色 / 深色主题自动适配。
 *
 * 尺寸由调用方通过 className 给（`size-8` 之类）。留白已经画进 viewBox，不要再加 padding；
 * 外框不是 border-radius，传圆角类名不会有任何效果。
 */
export function LogoTile({ className }: { className?: string }) {
  // 同一页面可能同时出现多个实例，渐变 id 必须唯一，否则后挂载的会抢走前面的引用
  const gradient = useId()
  return (
    <svg viewBox="0 0 32 32" aria-hidden className={cn('shrink-0 text-accent-fg', className)}>
      <defs>
        <linearGradient id={gradient} x1="0" y1="0" x2="32" y2="32" gradientUnits="userSpaceOnUse">
          <stop offset="0" style={{ stopColor: 'var(--accent)' }} />
          <stop offset="1" style={{ stopColor: 'var(--accent-hover)' }} />
        </linearGradient>
      </defs>
      <path d={SUPERELLIPSE} fill={`url(#${gradient})`} />
      <path
        d="M7.8 9.2 13.6 16 7.8 22.8"
        fill="none"
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
