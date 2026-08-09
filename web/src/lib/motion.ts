import type { Transition, Variants } from 'motion/react'

/** 页面进场：轻微上浮淡入 */
export const pageVariants: Variants = {
  hidden: { opacity: 0, y: 6 },
  visible: { opacity: 1, y: 0, transition: { duration: 0.2, ease: 'easeOut' } },
}

/** 列表/网格容器：子项 30ms 依次入场，仅首次挂载 */
export const staggerContainer: Variants = {
  hidden: {},
  visible: { transition: { staggerChildren: 0.03 } },
}

export const staggerItem: Variants = {
  hidden: { opacity: 0, y: 6 },
  visible: { opacity: 1, y: 0, transition: { duration: 0.2, ease: 'easeOut' } },
}

/** 弹层：spring 弹入，overlay 单独 fade */
export const overlayTransition: Transition = { duration: 0.15 }
export const popTransition: Transition = { type: 'spring', stiffness: 400, damping: 32 }

/** 统计数字滚动时长（秒） */
export const COUNT_DURATION = 0.6
