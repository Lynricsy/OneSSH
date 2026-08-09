import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

/** 合并 className：clsx 处理条件，tailwind-merge 消解冲突 utility */
export const cn = (...inputs: ClassValue[]) => twMerge(clsx(inputs))
