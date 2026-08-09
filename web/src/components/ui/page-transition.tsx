import { motion } from 'motion/react'
import { pageVariants } from '@/lib/motion'

/** 每个页面的根容器：统一的进场动画，reduce 由根部 MotionConfig 关闭 */
export function PageTransition({ children }: { children: React.ReactNode }) {
  return (
    <motion.div variants={pageVariants} initial="hidden" animate="visible">
      {children}
    </motion.div>
  )
}
