/**
 * 浏览器侧的私钥文件预检。
 *
 * 真正的解析与校验仍以后端 sshkeys 为准；这里只做「选完文件立刻就能知道的事」：
 * 文件是不是太大、是不是误选了公钥 / .ppk、是否受口令保护、大概是什么算法。
 * 这样口令输入框只在需要时出现，选错文件也不必等一次网络往返才知道。
 */

/** 与后端 sshkeys.MaxPrivateKeyBytes 保持一致 */
export const MAX_KEY_FILE_BYTES = 64 << 10

export type KeyFileKind = 'openssh' | 'pem' | 'pkcs8' | 'public' | 'ppk' | 'unknown'

export type KeyFileInfo = {
  kind: KeyFileKind
  /** 是否受口令保护；无法判断时为 false，交给后端兜底报错 */
  encrypted: boolean
  /** 例如 ssh-ed25519 / RSA；未知时为空 */
  algorithm?: string
  /** 选错文件时给用户看的人话，存在即表示不能直接导入 */
  problem?: string
}

export type LoadedKeyFile = {
  fileName: string
  size: number
  text: string
  info: KeyFileInfo
}

const decoder = new TextDecoder()

function readSSHString(buf: Uint8Array, offset: number): [Uint8Array, number] | null {
  if (offset + 4 > buf.length) return null
  const len =
    ((buf[offset] << 24) | (buf[offset + 1] << 16) | (buf[offset + 2] << 8) | buf[offset + 3]) >>> 0
  const start = offset + 4
  const end = start + len
  if (end > buf.length) return null
  return [buf.subarray(start, end), end]
}

/** openssh-key-v1 的头部是明文：cipher 名为 none 即未加密，公钥段首个字符串即算法名 */
function inspectOpenSSH(body: string): KeyFileInfo {
  try {
    const bin = Uint8Array.from(atob(body.replace(/\s+/g, '')), (c) => c.charCodeAt(0))
    const magic = 'openssh-key-v1\0'
    if (decoder.decode(bin.subarray(0, magic.length)) !== magic) return { kind: 'openssh', encrypted: false }
    const cipher = readSSHString(bin, magic.length)
    if (!cipher) return { kind: 'openssh', encrypted: false }
    const kdf = readSSHString(bin, cipher[1])
    const kdfOptions = kdf && readSSHString(bin, kdf[1])
    const publicBlob = kdfOptions && readSSHString(bin, kdfOptions[1] + 4)
    const type = publicBlob && readSSHString(publicBlob[0], 0)
    return {
      kind: 'openssh',
      encrypted: decoder.decode(cipher[0]) !== 'none',
      algorithm: type ? decoder.decode(type[0]) : undefined,
    }
  } catch {
    return { kind: 'openssh', encrypted: false }
  }
}

export function inspectKeyText(raw: string): KeyFileInfo {
  const text = raw.replace(/^\uFEFF/, '').trim()
  if (!text) return { kind: 'unknown', encrypted: false, problem: '文件内容为空' }
  if (text.startsWith('PuTTY-User-Key-File')) {
    return {
      kind: 'ppk',
      encrypted: false,
      problem: '这是 PuTTY .ppk 文件，请先用 PuTTYgen 的 Conversions → Export OpenSSH key 导出为 OpenSSH 私钥',
    }
  }
  if (/^(ssh-(rsa|dss|ed25519)|ecdsa-sha2-|sk-(ssh|ecdsa))/.test(text)) {
    return {
      kind: 'public',
      encrypted: false,
      problem: '这是公钥（.pub）内容，请选择不带 .pub 后缀的同名私钥文件',
    }
  }
  const block = /-----BEGIN ([A-Z0-9 ]+)-----([\s\S]*?)-----END \1-----/.exec(text)
  if (!block) {
    return {
      kind: 'unknown',
      encrypted: false,
      problem: '没有找到 -----BEGIN … PRIVATE KEY----- 标记，看起来不是私钥文件',
    }
  }
  const [, label, body] = block
  switch (label) {
    case 'OPENSSH PRIVATE KEY':
      return inspectOpenSSH(body)
    case 'ENCRYPTED PRIVATE KEY':
      return { kind: 'pkcs8', encrypted: true }
    case 'PRIVATE KEY':
      return { kind: 'pkcs8', encrypted: false }
    case 'RSA PRIVATE KEY':
    case 'EC PRIVATE KEY':
    case 'DSA PRIVATE KEY':
      return {
        kind: 'pem',
        encrypted: /Proc-Type:\s*4,ENCRYPTED/i.test(body),
        algorithm: label === 'RSA PRIVATE KEY' ? 'RSA' : label === 'EC PRIVATE KEY' ? 'ECDSA' : 'DSA',
      }
    case 'SSH2 PUBLIC KEY':
    case 'PUBLIC KEY':
    case 'RSA PUBLIC KEY':
      return { kind: 'public', encrypted: false, problem: '这是公钥文件，请选择对应的私钥文件' }
    default:
      return { kind: 'unknown', encrypted: false, problem: `不支持的 PEM 类型：${label}` }
  }
}

export async function readKeyFile(file: File): Promise<LoadedKeyFile> {
  if (file.size > MAX_KEY_FILE_BYTES) {
    throw new Error(`文件 ${file.name} 超过 64 KiB，请确认选择的是私钥文件`)
  }
  const text = await file.text()
  return { fileName: file.name, size: file.size, text, info: inspectKeyText(text) }
}

/** id_ed25519 / deploy.pem / server.key → 去掉常见后缀作为默认密钥名 */
export function keyNameFromFile(fileName: string) {
  const base = fileName.split(/[\\/]/).pop() ?? ''
  const name = base.replace(/\.(pem|key|txt|openssh|priv|private)$/i, '').trim()
  return (name || 'imported-key').slice(0, 64)
}

/** 名称在网关内唯一：撞名时自动追加 -2、-3…，用户仍可手动改 */
export function uniqueKeyName(base: string, existing: string[]) {
  const taken = new Set(existing.map((name) => name.toLowerCase()))
  if (!taken.has(base.toLowerCase())) return base
  for (let i = 2; i < 1000; i++) {
    const candidate = `${base}-${i}`
    if (!taken.has(candidate.toLowerCase())) return candidate
  }
  return `${base}-${Date.now()}`
}

const ALGORITHM_LABELS: Record<string, string> = {
  'ssh-ed25519': 'Ed25519',
  'ssh-rsa': 'RSA',
  'ssh-dss': 'DSA',
  'ecdsa-sha2-nistp256': 'ECDSA P-256',
  'ecdsa-sha2-nistp384': 'ECDSA P-384',
  'ecdsa-sha2-nistp521': 'ECDSA P-521',
  'sk-ssh-ed25519@openssh.com': 'Ed25519-SK',
  'sk-ecdsa-sha2-nistp256@openssh.com': 'ECDSA-SK',
}

export function algorithmLabel(algorithm?: string) {
  if (!algorithm) return undefined
  return ALGORITHM_LABELS[algorithm] ?? algorithm
}

/** 与 ssh-keygen -lf 相同的 SHA256 指纹；非安全上下文拿不到 crypto.subtle 时返回 null */
export async function fingerprintOf(publicKey: string): Promise<string | null> {
  try {
    const blob = publicKey.trim().split(/\s+/)[1]
    if (!blob || !globalThis.crypto?.subtle) return null
    const bytes = Uint8Array.from(atob(blob), (c) => c.charCodeAt(0))
    const digest = new Uint8Array(await crypto.subtle.digest('SHA-256', bytes))
    let binary = ''
    for (const byte of digest) binary += String.fromCharCode(byte)
    return `SHA256:${btoa(binary).replace(/=+$/, '')}`
  } catch {
    return null
  }
}

export function formatFileSize(bytes: number) {
  return bytes < 1024 ? `${bytes} B` : `${(bytes / 1024).toFixed(1)} KiB`
}
