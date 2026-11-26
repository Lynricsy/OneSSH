import {
  ArrowClockwise,
  ArrowsClockwise,
  Clock,
  Cpu,
  Cube,
  HardDrive,
  Hash,
  Memory,
  Network,
  Package,
  Plugs,
  Stack,
  User,
  Users,
  WarningCircle,
} from '@phosphor-icons/react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { motion } from 'motion/react'
import { api } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/cn'
import { formatSize } from '@/lib/file-types'

export type SysInfo = {
  host_id: number
  hostname: string
  fqdn: string
  os_name: string
  os_id: string
  os_version: string
  os_codename: string
  kernel: string
  kernel_build: string
  arch: string
  cpu_model: string
  cpu_cores: number
  cpu_sockets: number
  cpu_mhz: number
  mem_total_kb: number
  mem_available_kb: number
  swap_total_kb: number
  swap_free_kb: number
  uptime_seconds: number
  boot_time: string
  load: number[] | null
  virtualization: string
  vendor: string
  product: string
  timezone: string
  remote_time: string
  users: number
  processes: number
  shell: string
  identity: string
  disks: Array<{ source: string; fstype: string; total_kb: number; used_kb: number; mount: string }>
  addrs: Array<{ iface: string; addr: string; v6: boolean }>
  gateway: string
  dns: string[]
  ports: Array<{ proto: string; addr: string; port: string; process: string }>
  top: Array<{ pid: string; user: string; cpu: number; mem: number; rss_kb: number; comm: string }>
  services: number
  failed_units: string[]
  containers: Array<{ name: string; image: string; status: string }>
  packages: number
  updates: number
  reboot_required: boolean
  collected_at: number
  collect_ms: number
}

const DISTROS: Record<string, { color: string; label: string }> = {
  ubuntu: { color: '#e95420', label: 'U' },
  debian: { color: '#d70a53', label: 'D' },
  centos: { color: '#932279', label: 'C' },
  rocky: { color: '#10b981', label: 'R' },
  almalinux: { color: '#0f4266', label: 'A' },
  rhel: { color: '#ee0000', label: 'RH' },
  fedora: { color: '#51a2da', label: 'F' },
  alpine: { color: '#0d597f', label: 'A' },
  arch: { color: '#1793d1', label: 'A' },
  opensuse: { color: '#73ba25', label: 'S' },
  'opensuse-leap': { color: '#73ba25', label: 'S' },
  amzn: { color: '#ff9900', label: 'AL' },
  openwrt: { color: '#00b5e2', label: 'W' },
  kali: { color: '#367bf0', label: 'K' },
  anolis: { color: '#ff6a00', label: 'An' },
  openEuler: { color: '#002fa7', label: 'E' },
  tencentos: { color: '#00a3ff', label: 'T' },
}

function uptimeText(sec: number) {
  if (!sec) return '—'
  const d = Math.floor(sec / 86400)
  const h = Math.floor((sec % 86400) / 3600)
  const m = Math.floor((sec % 3600) / 60)
  return d ? `${d} 天 ${h} 小时` : h ? `${h} 小时 ${m} 分` : `${m} 分钟`
}

function Bar({ pct, className }: { pct: number; className?: string }) {
  const color = pct >= 90 ? '#f43f5e' : pct >= 75 ? '#f59e0b' : 'var(--accent)'
  return (
    <div className={cn('h-1.5 overflow-hidden rounded-full bg-surface-2', className)}>
      <motion.div className="h-full rounded-full" style={{ background: color }} initial={{ width: 0 }} animate={{ width: `${Math.min(100, pct)}%` }} transition={{ duration: 0.6 }} />
    </div>
  )
}

function Fact({ icon, label, value, mono }: { icon: React.ReactNode; label: string; value: React.ReactNode; mono?: boolean }) {
  return (
    <div className="flex min-w-0 items-start gap-2.5 rounded-[10px] bg-surface-2/55 px-3 py-2">
      <span className="mt-0.5 shrink-0 text-muted">{icon}</span>
      <span className="min-w-0">
        <span className="block text-[11px] text-muted">{label}</span>
        <span className={cn('block truncate text-[13px] font-medium text-text', mono && 'font-mono text-[12px]')} title={typeof value === 'string' ? value : undefined}>
          {value || '—'}
        </span>
      </span>
    </div>
  )
}

function Section({ icon, title, extra, children, className }: { icon: React.ReactNode; title: string; extra?: React.ReactNode; children: React.ReactNode; className?: string }) {
  return (
    <Card className={cn('flex flex-col p-4', className)}>
      <div className="mb-3 flex items-center gap-2">
        <span className="grid size-7 place-items-center rounded-[8px] bg-accent-soft text-accent">{icon}</span>
        <h3 className="flex-1 text-[13.5px] font-semibold text-text">{title}</h3>
        {extra}
      </div>
      {children}
    </Card>
  )
}

/** 指标页顶部的「服务器档案」：操作系统、内核、硬件、网络、端口与进程 */
export function HostProfile({ hostId }: { hostId: number }) {
  const qc = useQueryClient()
  const query = useQuery({
    queryKey: ['sysinfo', hostId],
    queryFn: () => api<SysInfo>(`/hosts/${hostId}/sysinfo`),
    staleTime: 5 * 60_000,
    retry: false,
  })
  const refresh = async () => {
    const fresh = await api<SysInfo>(`/hosts/${hostId}/sysinfo?refresh=1`)
    qc.setQueryData(['sysinfo', hostId], fresh)
  }
  const info = query.data

  if (query.isLoading) {
    return (
      <div className="mb-4 grid gap-4 lg:grid-cols-3">
        <Skeleton className="h-[220px] rounded-[12px] lg:col-span-2" />
        <Skeleton className="h-[220px] rounded-[12px]" />
      </div>
    )
  }
  if (query.isError || !info) {
    return (
      <Card className="mb-4 flex items-center gap-3 p-4 text-[13px]">
        <WarningCircle size={18} className="text-danger" />
        <span className="flex-1 text-muted">读取服务器信息失败：{(query.error as Error)?.message}</span>
        <Button size="sm" variant="outline" onClick={() => void query.refetch()}>
          重试
        </Button>
      </Card>
    )
  }

  const distro = DISTROS[info.os_id] ?? { color: '#64748b', label: (info.os_id || 'L').slice(0, 1).toUpperCase() }
  const memUsed = info.mem_total_kb - info.mem_available_kb
  const memPct = info.mem_total_kb ? (memUsed / info.mem_total_kb) * 100 : 0
  const swapUsed = info.swap_total_kb - info.swap_free_kb
  const cores = Math.max(1, info.cpu_cores)
  const realDisks = info.disks.filter((d) => d.total_kb > 0)

  return (
    <motion.div className="mb-4 space-y-4" initial={{ opacity: 0, y: 6 }} animate={{ opacity: 1, y: 0 }}>
      {/* 系统档案 */}
      <Card className="relative overflow-hidden p-5">
        <div className="pointer-events-none absolute -top-16 -right-16 size-56 rounded-full opacity-[0.10] blur-3xl" style={{ background: distro.color }} />
        <div className="relative flex flex-wrap items-start gap-4">
          <span className="grid size-14 shrink-0 place-items-center rounded-[16px] text-[20px] font-bold text-white shadow-pop" style={{ background: `linear-gradient(135deg, ${distro.color}, color-mix(in oklab, ${distro.color} 60%, #000))` }}>
            {distro.label}
          </span>
          <div className="min-w-0 flex-1">
            <h2 className="text-[19px] font-semibold tracking-tight text-text">{info.os_name || '未知系统'}</h2>
            <p className="mt-0.5 font-mono text-[12.5px] text-muted">
              {info.hostname}
              {info.fqdn && info.fqdn !== info.hostname && ` (${info.fqdn})`} · Linux {info.kernel} · {info.arch}
            </p>
            <div className="mt-2.5 flex flex-wrap gap-1.5">
              {info.virtualization && <span className="rounded-full bg-accent-soft px-2.5 py-0.5 text-[11.5px] font-medium text-accent">{info.virtualization === '物理机' ? '物理机' : `虚拟化 · ${info.virtualization}`}</span>}
              {(info.vendor || info.product) && <span className="rounded-full bg-surface-2 px-2.5 py-0.5 text-[11.5px] text-muted">{[info.vendor, info.product].filter(Boolean).join(' ')}</span>}
              {info.os_codename && <span className="rounded-full bg-surface-2 px-2.5 py-0.5 text-[11.5px] text-muted">代号 {info.os_codename}</span>}
              {info.reboot_required && <span className="rounded-full bg-warning/15 px-2.5 py-0.5 text-[11.5px] font-medium text-warning">需要重启以完成更新</span>}
              {info.updates > 0 && <span className="rounded-full bg-[var(--accent-2)]/15 px-2.5 py-0.5 text-[11.5px] font-medium text-[var(--accent-2)]">{info.updates} 个可更新的软件包</span>}
              {info.failed_units.length > 0 && <span className="rounded-full bg-danger/12 px-2.5 py-0.5 text-[11.5px] font-medium text-danger" title={info.failed_units.join('\n')}>{info.failed_units.length} 个失败的服务</span>}
            </div>
          </div>
          <div className="text-right text-[11.5px] text-muted">
            <Button size="sm" variant="outline" onClick={() => void refresh()}>
              <ArrowClockwise size={14} />
              刷新信息
            </Button>
            <p className="mt-1.5">采集于 {new Date(info.collected_at * 1000).toLocaleTimeString('zh-CN', { hour12: false })} · 用时 {info.collect_ms} ms</p>
          </div>
        </div>
        <div className="relative mt-4 grid grid-cols-2 gap-2 md:grid-cols-3 xl:grid-cols-6">
          <Fact icon={<Clock size={15} />} label="已运行" value={uptimeText(info.uptime_seconds)} />
          <Fact icon={<ArrowsClockwise size={15} />} label="启动时间" value={info.boot_time} />
          <Fact icon={<Hash size={15} />} label="时区 / 服务器时间" value={`${info.timezone || '—'} · ${info.remote_time.slice(11, 16) || '—'}`} />
          <Fact icon={<Users size={15} />} label="在线用户 / 进程" value={`${info.users} 人 · ${info.processes} 个进程`} />
          <Fact icon={<Package size={15} />} label="已装软件包 / 运行服务" value={`${info.packages || '—'} · ${info.services || '—'} 个服务`} />
          <Fact icon={<User size={15} />} label="登录身份 / Shell" value={`${info.identity.split(' ')[0]?.replace(/^uid=\d+\(|\)$/g, '') || '—'} · ${info.shell.split('/').pop() || '—'}`} mono />
        </div>
        {info.kernel_build && <p className="relative mt-3 truncate font-mono text-[11px] text-faint" title={info.kernel_build}>内核构建：{info.kernel_build}</p>}
      </Card>

      <div className="grid gap-4 lg:grid-cols-3">
        <Section icon={<Cpu size={15} weight="bold" />} title="处理器">
          <p className="text-[13px] font-medium text-text">{info.cpu_model || '未知型号'}</p>
          <p className="mt-0.5 text-[12px] text-muted">
            {info.cpu_cores} 核{info.cpu_sockets > 1 ? ` · ${info.cpu_sockets} 路` : ''}
            {info.cpu_mhz ? ` · ${(info.cpu_mhz / 1000).toFixed(2)} GHz` : ''}
          </p>
          <div className="mt-3 space-y-2">
            {(info.load ?? []).map((l, i) => (
              <div key={i}>
                <div className="flex justify-between text-[11.5px] text-muted">
                  <span>{['1 分钟', '5 分钟', '15 分钟'][i]}负载</span>
                  <span className="font-medium text-text tabular-nums">
                    {l.toFixed(2)} <span className="text-faint">/ {cores}</span>
                  </span>
                </div>
                <Bar pct={(l / cores) * 100} className="mt-1" />
              </div>
            ))}
          </div>
        </Section>

        <Section icon={<Memory size={15} weight="bold" />} title="内存">
          <p className="text-[22px] leading-none font-semibold text-text tabular-nums">
            {memPct.toFixed(0)}%<span className="ml-1.5 text-[12px] font-normal text-muted">已用</span>
          </p>
          <Bar pct={memPct} className="mt-3" />
          <p className="mt-2 text-[12px] text-muted tabular-nums">
            {formatSize(memUsed * 1024)} / {formatSize(info.mem_total_kb * 1024)} · 可用 {formatSize(info.mem_available_kb * 1024)}
          </p>
          <div className="mt-3 border-t border-border pt-3">
            <div className="flex justify-between text-[11.5px] text-muted">
              <span>交换分区</span>
              <span className="text-text tabular-nums">{info.swap_total_kb ? `${formatSize(swapUsed * 1024)} / ${formatSize(info.swap_total_kb * 1024)}` : '未启用'}</span>
            </div>
            {info.swap_total_kb > 0 && <Bar pct={(swapUsed / info.swap_total_kb) * 100} className="mt-1" />}
          </div>
        </Section>

        <Section icon={<HardDrive size={15} weight="bold" />} title="磁盘" extra={<span className="text-[11px] text-muted">{realDisks.length} 个挂载点</span>}>
          <div className="space-y-2.5">
            {realDisks.slice(0, 6).map((d) => {
              const pct = (d.used_kb / d.total_kb) * 100
              return (
                <div key={`${d.source}-${d.mount}`}>
                  <div className="flex items-baseline justify-between gap-2 text-[12px]">
                    <span className="min-w-0 truncate font-mono text-text" title={d.source}>
                      {d.mount}
                      {d.fstype && <span className="ml-1.5 text-[10.5px] text-faint">{d.fstype}</span>}
                    </span>
                    <span className="shrink-0 text-muted tabular-nums">
                      {formatSize(d.used_kb * 1024)} / {formatSize(d.total_kb * 1024)}
                    </span>
                  </div>
                  <Bar pct={pct} className="mt-1" />
                </div>
              )
            })}
            {realDisks.length === 0 && <p className="text-[12px] text-muted">没有读取到磁盘信息</p>}
          </div>
        </Section>
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        <Section icon={<Network size={15} weight="bold" />} title="网络">
          <ul className="space-y-1.5">
            {info.addrs.slice(0, 8).map((a) => (
              <li key={`${a.iface}-${a.addr}`} className="flex items-center gap-2 text-[12px]">
                <span className="w-16 shrink-0 truncate rounded-[5px] bg-surface-2 px-1.5 py-0.5 text-center font-mono text-[11px] text-muted">{a.iface}</span>
                <span className="min-w-0 truncate font-mono text-text" title={a.addr}>
                  {a.addr}
                </span>
                {a.v6 && <span className="text-[10px] text-faint">IPv6</span>}
              </li>
            ))}
          </ul>
          <div className="mt-3 space-y-1 border-t border-border pt-3 text-[12px]">
            <p className="flex gap-2">
              <span className="w-12 shrink-0 text-muted">网关</span>
              <span className="truncate font-mono text-text">{info.gateway.replace(/^default via /, '').split(' ')[0] || '—'}</span>
            </p>
            <p className="flex gap-2">
              <span className="w-12 shrink-0 text-muted">DNS</span>
              <span className="truncate font-mono text-text">{info.dns.join('  ') || '—'}</span>
            </p>
          </div>
        </Section>

        <Section icon={<Plugs size={15} weight="bold" />} title="监听端口" extra={<span className="text-[11px] text-muted">{info.ports.length} 个</span>}>
          <div className="flex flex-wrap gap-1.5">
            {info.ports.slice(0, 30).map((p) => (
              <span key={`${p.addr}-${p.port}-${p.process}`} className="inline-flex items-center gap-1 rounded-[7px] border border-border bg-surface px-2 py-1 text-[11.5px]" title={`${p.addr}:${p.port}`}>
                <b className="font-mono font-semibold text-text">{p.port}</b>
                {p.process && <span className="text-muted">{p.process}</span>}
                {(p.addr === '127.0.0.1' || p.addr === '::1') && <span className="text-[10px] text-faint">本地</span>}
              </span>
            ))}
            {info.ports.length === 0 && <p className="text-[12px] text-muted">没有读取到监听端口（可能缺少 ss / netstat）</p>}
          </div>
        </Section>

        <Section icon={<Stack size={15} weight="bold" />} title="CPU 占用最高的进程">
          <table className="w-full text-[12px]">
            <thead>
              <tr className="text-left text-[10.5px] text-muted">
                <th className="pb-1 font-medium">进程</th>
                <th className="pb-1 text-right font-medium">CPU</th>
                <th className="pb-1 text-right font-medium">内存</th>
              </tr>
            </thead>
            <tbody>
              {info.top.slice(0, 8).map((p) => (
                <tr key={p.pid} className="border-t border-border/60">
                  <td className="max-w-0 py-1">
                    <span className="block truncate font-mono text-text" title={`PID ${p.pid} · ${p.user}`}>
                      {p.comm}
                    </span>
                  </td>
                  <td className="py-1 text-right text-text tabular-nums">{p.cpu.toFixed(1)}%</td>
                  <td className="py-1 text-right text-muted tabular-nums">{formatSize(p.rss_kb * 1024)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Section>
      </div>

      {info.containers.length > 0 && (
        <Section icon={<Cube size={15} weight="bold" />} title="Docker 容器" extra={<span className="text-[11px] text-muted">{info.containers.length} 个运行中</span>}>
          <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-4">
            {info.containers.map((c) => (
              <div key={c.name} className="rounded-[10px] border border-border px-3 py-2">
                <p className="flex items-center gap-1.5 truncate text-[12.5px] font-medium text-text">
                  <span className="size-1.5 shrink-0 rounded-full bg-success" />
                  {c.name}
                </p>
                <p className="truncate font-mono text-[11px] text-muted" title={c.image}>
                  {c.image}
                </p>
                <p className="truncate text-[11px] text-faint">{c.status}</p>
              </div>
            ))}
          </div>
        </Section>
      )}
    </motion.div>
  )
}
