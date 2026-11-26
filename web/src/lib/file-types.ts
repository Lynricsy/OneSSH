import type { Icon } from '@phosphor-icons/react'
import {
  File,
  FileArchive,
  FileAudio,
  FileCode,
  FileCss,
  FileDoc,
  FileHtml,
  FileImage,
  FileJs,
  FileLock,
  FilePdf,
  FilePy,
  FileSql,
  FileText,
  FileTs,
  FileVideo,
  FileXls,
  Folder,
  FolderSimpleUser,
  Gear,
  LinkSimple,
  TerminalWindow,
} from '@phosphor-icons/react'

export type FileKind = 'dir' | 'image' | 'video' | 'audio' | 'pdf' | 'archive' | 'text' | 'binary'

const ext = (name: string) => {
  const lower = name.toLowerCase()
  for (const multi of ['.tar.gz', '.tar.xz', '.tar.bz2', '.tar.zst']) if (lower.endsWith(multi)) return multi.slice(1)
  const i = lower.lastIndexOf('.')
  return i > 0 ? lower.slice(i + 1) : ''
}

const IMAGE = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp', 'ico', 'svg', 'avif'])
const VIDEO = new Set(['mp4', 'webm', 'mov', 'mkv', 'm4v', 'ogv'])
const AUDIO = new Set(['mp3', 'wav', 'ogg', 'flac', 'm4a', 'aac', 'opus'])
const ARCHIVE = new Set(['zip', 'tar', 'gz', 'tgz', 'tar.gz', 'tar.xz', 'txz', 'tar.bz2', 'tbz2', 'tar.zst', '7z', 'rar', 'xz', 'bz2', 'zst'])
export const EXTRACTABLE = new Set(['zip', 'tar', 'gz', 'tgz', 'tar.gz', 'tar.xz', 'txz', 'tar.bz2', 'tbz2', 'tar.zst', '7z'])
const BINARY = new Set(['exe', 'dll', 'so', 'o', 'a', 'bin', 'iso', 'img', 'deb', 'rpm', 'apk', 'jar', 'class', 'pyc', 'db', 'sqlite', 'woff', 'woff2', 'ttf', 'otf', 'docx', 'xlsx', 'pptx'])

export function kindOf(name: string, directory = false): FileKind {
  if (directory) return 'dir'
  const e = ext(name)
  if (IMAGE.has(e)) return 'image'
  if (VIDEO.has(e)) return 'video'
  if (AUDIO.has(e)) return 'audio'
  if (e === 'pdf') return 'pdf'
  if (ARCHIVE.has(e)) return 'archive'
  if (BINARY.has(e)) return 'binary'
  return 'text'
}

export const isExtractable = (name: string) => EXTRACTABLE.has(ext(name))

const ICONS: Record<string, { icon: Icon; color: string }> = {
  js: { icon: FileJs, color: '#eab308' },
  mjs: { icon: FileJs, color: '#eab308' },
  cjs: { icon: FileJs, color: '#eab308' },
  jsx: { icon: FileJs, color: '#38bdf8' },
  ts: { icon: FileTs, color: '#3b82f6' },
  tsx: { icon: FileTs, color: '#38bdf8' },
  py: { icon: FilePy, color: '#3b82f6' },
  html: { icon: FileHtml, color: '#f97316' },
  htm: { icon: FileHtml, color: '#f97316' },
  css: { icon: FileCss, color: '#8b5cf6' },
  scss: { icon: FileCss, color: '#ec4899' },
  sql: { icon: FileSql, color: '#0ea5e9' },
  md: { icon: FileText, color: '#64748b' },
  txt: { icon: FileText, color: '#64748b' },
  log: { icon: FileText, color: '#94a3b8' },
  pdf: { icon: FilePdf, color: '#ef4444' },
  doc: { icon: FileDoc, color: '#2563eb' },
  docx: { icon: FileDoc, color: '#2563eb' },
  xls: { icon: FileXls, color: '#16a34a' },
  xlsx: { icon: FileXls, color: '#16a34a' },
  csv: { icon: FileXls, color: '#16a34a' },
  sh: { icon: TerminalWindow, color: '#22c55e' },
  bash: { icon: TerminalWindow, color: '#22c55e' },
  zsh: { icon: TerminalWindow, color: '#22c55e' },
  conf: { icon: Gear, color: '#64748b' },
  cfg: { icon: Gear, color: '#64748b' },
  ini: { icon: Gear, color: '#64748b' },
  toml: { icon: Gear, color: '#64748b' },
  yml: { icon: Gear, color: '#a855f7' },
  yaml: { icon: Gear, color: '#a855f7' },
  json: { icon: FileCode, color: '#eab308' },
  go: { icon: FileCode, color: '#06b6d4' },
  rs: { icon: FileCode, color: '#f97316' },
  java: { icon: FileCode, color: '#ef4444' },
  php: { icon: FileCode, color: '#6366f1' },
  key: { icon: FileLock, color: '#f59e0b' },
  pem: { icon: FileLock, color: '#f59e0b' },
  pub: { icon: FileLock, color: '#f59e0b' },
}

export function iconOf(name: string, directory: boolean, symlink = false): { icon: Icon; color: string } {
  if (directory) {
    if (/^(home|root)$/.test(name)) return { icon: FolderSimpleUser, color: '#f59e0b' }
    return { icon: Folder, color: symlink ? '#38bdf8' : '#f59e0b' }
  }
  if (symlink) return { icon: LinkSimple, color: '#38bdf8' }
  const e = ext(name)
  if (ICONS[e]) return ICONS[e]
  if (/^(dockerfile|makefile|\.env.*|\.bashrc|\.profile|\.zshrc|\.gitignore)$/i.test(name)) return { icon: Gear, color: '#64748b' }
  switch (kindOf(name)) {
    case 'image':
      return { icon: FileImage, color: '#ec4899' }
    case 'video':
      return { icon: FileVideo, color: '#8b5cf6' }
    case 'audio':
      return { icon: FileAudio, color: '#14b8a6' }
    case 'archive':
      return { icon: FileArchive, color: '#a16207' }
    case 'text':
      return { icon: FileText, color: '#94a3b8' }
    default:
      return { icon: File, color: '#94a3b8' }
  }
}

/** 编辑器语言：按扩展名与常见文件名推断 */
export function languageOf(name: string): string {
  const lower = name.toLowerCase()
  if (lower === 'dockerfile' || lower.startsWith('dockerfile.')) return 'dockerfile'
  if (lower === 'nginx.conf' || lower.endsWith('.nginx') || /sites-(available|enabled)/.test(lower)) return 'nginx'
  if (/^\.(bashrc|bash_profile|profile|zshrc|bash_aliases)$/.test(lower)) return 'shell'
  if (lower.startsWith('.env')) return 'properties'
  const e = ext(name)
  const map: Record<string, string> = {
    js: 'javascript', mjs: 'javascript', cjs: 'javascript', jsx: 'jsx', ts: 'typescript', tsx: 'tsx',
    json: 'json', json5: 'json', py: 'python', yml: 'yaml', yaml: 'yaml', md: 'markdown', markdown: 'markdown',
    html: 'html', htm: 'html', vue: 'html', css: 'css', scss: 'css', less: 'css', sql: 'sql', xml: 'xml', svg: 'xml',
    go: 'go', sh: 'shell', bash: 'shell', zsh: 'shell', toml: 'toml', ini: 'properties', conf: 'nginx', cfg: 'properties',
    properties: 'properties', service: 'properties', lua: 'lua', rb: 'ruby', pl: 'perl', php: 'php', rs: 'rust',
    java: 'java', c: 'c', h: 'c', cpp: 'cpp', hpp: 'cpp', cs: 'csharp', kt: 'kotlin', swift: 'swift', ps1: 'powershell',
  }
  return map[e] ?? 'text'
}

export function formatSize(bytes: number) {
  if (bytes < 1024) return `${bytes} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let v = bytes / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v >= 100 ? v.toFixed(0) : v.toFixed(1)} ${units[i]}`
}

const dateFmt = new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false })
export const formatMtime = (sec: number) => (sec ? dateFmt.format(sec * 1000) : '—')

/** 编辑器里可手动切换的语言 */
export const LANGUAGES: Array<{ value: string; label: string }> = [
  { value: 'text', label: '纯文本' },
  { value: 'shell', label: 'Shell' },
  { value: 'nginx', label: 'Nginx' },
  { value: 'yaml', label: 'YAML' },
  { value: 'json', label: 'JSON' },
  { value: 'toml', label: 'TOML' },
  { value: 'properties', label: 'INI / .env' },
  { value: 'dockerfile', label: 'Dockerfile' },
  { value: 'python', label: 'Python' },
  { value: 'javascript', label: 'JavaScript' },
  { value: 'typescript', label: 'TypeScript' },
  { value: 'go', label: 'Go' },
  { value: 'html', label: 'HTML' },
  { value: 'css', label: 'CSS' },
  { value: 'markdown', label: 'Markdown' },
  { value: 'sql', label: 'SQL' },
  { value: 'xml', label: 'XML' },
  { value: 'lua', label: 'Lua' },
  { value: 'rust', label: 'Rust' },
  { value: 'java', label: 'Java' },
  { value: 'c', label: 'C' },
  { value: 'cpp', label: 'C++' },
]
