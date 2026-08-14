import {
  Brain,
  Broadcast,
  ChartLine,
  FolderOpen,
  HardDrives,
  Key,
  Pulse,
  SquaresFour,
  Terminal,
  Ticket,
  type Icon,
} from '@phosphor-icons/react'

export type NavItem = { to: string; label: string; icon: Icon }
export type NavGroup = { title?: string; items: NavItem[] }

export const navGroups: NavGroup[] = [
  { items: [{ to: '/', label: '概览', icon: SquaresFour }] },
  {
    title: '资源',
    items: [
      { to: '/hosts', label: '主机', icon: HardDrives },
      { to: '/keys', label: '密钥', icon: Key },
      { to: '/tokens', label: '令牌', icon: Ticket },
    ],
  },
  {
    title: '操作',
    items: [
      { to: '/terminal', label: '终端', icon: Terminal },
      { to: '/files', label: '文件', icon: FolderOpen },
      { to: '/jobs', label: '任务', icon: Broadcast },
    ],
  },
  {
    title: '观测',
    items: [
      { to: '/activity', label: '活动', icon: Pulse },
      { to: '/metrics', label: '指标', icon: ChartLine },
      { to: '/memories', label: '记忆', icon: Brain },
    ],
  },
]

const flat = navGroups.flatMap((g) => g.items)

/** 顶栏标题取自同一份导航定义，避免路由与标题两处维护 */
export const titleForPath = (pathname: string) =>
  flat.find((i) => (i.to === '/' ? pathname === '/' : pathname.startsWith(i.to)))?.label ?? '未找到'
