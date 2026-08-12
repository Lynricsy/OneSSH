import { useEffect, useRef, useState } from 'react'
import {
  ArrowClockwise,
  CaretRight,
  DownloadSimple,
  File,
  FolderOpen,
  PencilSimple,
  UploadSimple,
  WarningCircle,
} from '@phosphor-icons/react'
import { toast } from 'sonner'
import { downloadUrl, fetchDownload } from '@/api/client'
import { useHosts, useSftpList, useUpload } from '@/api/queries'
import type { FileEntry } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { DataTable } from '@/components/ui/data-table'
import { EmptyState } from '@/components/ui/empty-state'
import { Input } from '@/components/ui/input'
import { PageHeader } from '@/components/ui/page-header'
import { PageTransition } from '@/components/ui/page-transition'
import { Select } from '@/components/ui/select'
import { SelectionBar } from '@/components/ui/selection-bar'
import { Sheet } from '@/components/ui/sheet'
import { Tooltip } from '@/components/ui/tooltip'
import { formatBytes, formatTime } from '@/lib/format'

/** 固定 id：sonner 复用同一条 toast，SFTP 连续失败时不会堆叠刷屏 */
const SFTP_ERROR_TOAST = 'sftp-error'

/** blob → 触发一次浏览器下载；object URL 延一拍再回收，点击后浏览器还要异步取用它 */
function saveBlob(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = name
  document.body.append(anchor)
  anchor.click()
  anchor.remove()
  window.setTimeout(() => URL.revokeObjectURL(url), 0)
}

export function FilesPage() {
  const [host, setHost] = useState<number>()
  const [path, setPath] = useState('~')
  // draft 非空即为路径编辑态：用一个状态代替「editing 布尔 + 直接改 path」，
  // 后者每敲一个字符就会发一次 SFTP 请求（并弹一条错误 toast）
  const [draft, setDraft] = useState<string>()
  const [preview, setPreview] = useState<string>()
  // 选中项按文件名记录，只对当前目录有意义——换主机/换目录即失效
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [downloading, setDownloading] = useState(false)
  const fileRef = useRef<HTMLInputElement>(null)
  const crumbsRef = useRef<HTMLElement>(null)
  const selectionScope = useRef(0)

  const hosts = useHosts()
  const query = useSftpList(host, path)
  const upload = useUpload(host, path)
  const errorMessage = query.error instanceof Error ? query.error.message : undefined

  useEffect(() => {
    // 下载可能跨越主机/目录切换；递增作用域可阻止旧请求把失败项写回新目录。
    selectionScope.current++
    setSelected(new Set())
  }, [host, path])

  useEffect(() => {
    // 依赖是消息字符串而不是 error 对象：对象每次 fetch 都是新引用，会让 effect 反复触发
    if (errorMessage) toast.error(errorMessage, { id: SFTP_ERROR_TOAST })
  }, [errorMessage])

  useEffect(() => {
    // 路径变深时面包屑会横向溢出，默认停在最左端会把「当前目录」推出视野——始终滚到末尾
    const el = crumbsRef.current
    if (el) el.scrollLeft = el.scrollWidth
  }, [path, draft])

  const commitDraft = () => {
    if (draft == null) return
    const next = draft.trim()
    setDraft(undefined)
    if (next && next !== path) setPath(next)
  }

  const openEntry = (entry: FileEntry) => {
    if (host == null) return
    const nextPath = path.replace(/\/$/, '') + '/' + entry.name
    if (entry.directory) {
      setPath(nextPath)
    } else if (/\.(png|jpe?g|gif|webp)$/i.test(entry.name)) {
      setPreview(downloadUrl(host, nextPath))
    } else {
      window.open(downloadUrl(host, nextPath))
    }
  }

  /**
   * 逐个 fetch 再用 object URL 触发下载。直链 <a download> 拿不到状态码：会话过期时
   * 浏览器会把 401 的错误页当成文件存下来，用户还以为下载成功了。串行发起也顺带避开了
   * 浏览器对同一拍内多个下载的合并丢弃，不用再拿 setTimeout 错开。
   */
  const downloadSelected = async () => {
    if (host == null || downloading) return
    const base = path.replace(/\/$/, '') + '/'
    const names = [...selected]
    const scope = selectionScope.current
    const failed: string[] = []
    let firstError: string | undefined

    setDownloading(true)
    try {
      for (const name of names) {
        try {
          saveBlob(await fetchDownload(host, base + name), name)
        } catch (error) {
          failed.push(name)
          firstError ??= (error as Error).message
        }
      }
    } finally {
      setDownloading(false)
    }

    const ok = names.length - failed.length
    if (failed.length === 0) toast.success(`已下载 ${ok} 个文件`)
    else if (ok === 0) toast.error(`下载失败：${firstError ?? '未知错误'}`)
    else toast.warning(`已下载 ${ok} 个文件，${failed.length} 个失败`)
    // 只有用户仍停留在启动下载的目录时才回写失败项；导航后的选择归新目录所有。
    if (selectionScope.current === scope) setSelected(new Set(failed))
  }

  const segments = path === '/' ? ['/'] : path.split('/').filter(Boolean)
  const prefix = path.startsWith('/') ? '/' : ''

  return (
    <PageTransition>
      <PageHeader title="文件" subtitle="SFTP 浏览、上传、下载与图片预览" />

      <div className="space-y-4">
        <Card className="flex flex-wrap items-center gap-2 p-2.5">
          <Select
            className="w-full sm:w-52"
            value={host}
            onChange={(nextHost) => {
              setHost(nextHost)
              setPath('~')
              setDraft(undefined)
            }}
            options={(hosts.data ?? []).map((item) => ({ value: item.id, label: item.name }))}
            placeholder="选择主机"
          />

          <div className="flex min-w-0 flex-1 items-center gap-2">
            {/* 固定 h-9：面包屑与输入框两种形态共用同一块宽高，切换时版面不跳 */}
            <div className="flex h-9 min-w-0 flex-1 items-center gap-1">
              {host == null ? (
                <p className="truncate px-1 text-[13px] text-muted">选择主机后浏览其目录</p>
              ) : draft != null ? (
                <Input
                  autoFocus
                  // 进入编辑态即全选：路径栏的常见意图是整段替换，而不是接着旧路径续写
                  onFocus={(event) => event.currentTarget.select()}
                  value={draft}
                  onChange={(event) => setDraft(event.target.value)}
                  onBlur={commitDraft}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter') commitDraft()
                    // Escape 放弃编辑：draft 置空即退出，原路径保持不变
                    if (event.key === 'Escape') setDraft(undefined)
                  }}
                  spellCheck={false}
                  className="font-mono text-[13px]"
                  aria-label="SFTP 路径"
                />
              ) : (
                <>
                  {/* 滚动条藏起来：36px 高的一行里出现横向滚动条只会更挤，滚动仍然可用 */}
                  <nav
                    ref={crumbsRef}
                    aria-label="当前路径"
                    className="flex min-w-0 items-center gap-0.5 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
                  >
                    {segments.map((segment, index) =>
                      index === segments.length - 1 ? (
                        // 末段是当前目录，不可点击——用文本权重区分「可回退」与「所在位置」
                        <span
                          key={`${segment}-${index}`}
                          className="shrink-0 px-2 font-mono text-[13px] font-medium text-text"
                        >
                          {segment}
                        </span>
                      ) : (
                        <span key={`${segment}-${index}`} className="flex shrink-0 items-center">
                          <Button
                            variant="ghost"
                            size="sm"
                            className="max-w-[12rem] px-2 font-mono text-[13px]"
                            onClick={() =>
                              setPath(prefix + segments.slice(0, index + 1).join('/'))
                            }
                          >
                            <span className="truncate">{segment}</span>
                          </Button>
                          <CaretRight size={11} className="shrink-0 text-faint" />
                        </span>
                      ),
                    )}
                  </nav>
                  {/* 编辑按钮留在滚动区外：路径再深也够得着 */}
                  <Tooltip content="编辑路径" side="bottom">
                    <Button
                      variant="ghost"
                      size="icon"
                      className="size-8 shrink-0"
                      onClick={() => setDraft(path)}
                      aria-label="编辑路径"
                    >
                      <PencilSimple size={14} />
                    </Button>
                  </Tooltip>
                </>
              )}
            </div>

            <Tooltip content="刷新目录" side="bottom">
              <Button
                variant="ghost"
                size="icon"
                className="size-9 shrink-0"
                disabled={host == null}
                onClick={() => void query.refetch()}
                aria-label="刷新目录"
              >
                <ArrowClockwise size={15} className={query.isFetching ? 'animate-spin' : undefined} />
              </Button>
            </Tooltip>

            <input
              ref={fileRef}
              type="file"
              className="hidden"
              onChange={(event) => {
                const input = event.currentTarget
                const file = input.files?.[0]
                if (file) upload.mutate(file)
                // 同名文件也应能连续上传，因此每次选择后清空原生输入值
                input.value = ''
              }}
            />
            <Button
              variant="outline"
              className="shrink-0"
              loading={upload.isPending}
              disabled={host == null}
              onClick={() => fileRef.current?.click()}
            >
              <UploadSimple size={15} />
              上传
            </Button>
          </div>
        </Card>

        {host == null ? (
          // min-h 保证空态是一块有分量的区域，而不是塌成一条缝
          <Card className="flex min-h-[340px] items-center justify-center">
            <EmptyState
              icon={<FolderOpen size={22} />}
              title="选择一台主机"
              description="选择主机后即可浏览其 SFTP 文件系统、上传与下载文件。"
            />
          </Card>
        ) : query.isError ? (
          <Card className="flex min-h-[340px] items-center justify-center">
            <EmptyState
              icon={<WarningCircle size={22} className="text-danger" />}
              title="无法读取目录"
              description={errorMessage ?? '连接目标主机失败。'}
              action={
                <Button
                  variant="outline"
                  loading={query.isFetching}
                  onClick={() => void query.refetch()}
                >
                  <ArrowClockwise size={15} />
                  重试
                </Button>
              }
            />
          </Card>
        ) : (
          <Card>
            <DataTable<FileEntry, string>
              columns={[
                {
                  key: 'icon',
                  title: '',
                  className: 'w-10 pr-0',
                  render: (entry) =>
                    entry.directory ? (
                      <FolderOpen size={17} weight="fill" className="text-accent" />
                    ) : (
                      <File size={17} className="text-faint" />
                    ),
                },
                {
                  key: 'name',
                  title: '名称',
                  render: (entry) => <span className="break-all">{entry.name}</span>,
                },
                {
                  key: 'size',
                  title: '大小',
                  className: 'w-24 tabular-nums text-muted',
                  render: (entry) =>
                    entry.directory ? <span className="text-muted">—</span> : formatBytes(entry.size),
                },
                {
                  key: 'mode',
                  title: '权限',
                  className: 'hidden md:table-cell w-28 font-mono text-[12px] text-muted',
                  render: (entry) => entry.mode,
                },
                {
                  key: 'mtime',
                  title: '修改时间',
                  className: 'hidden sm:table-cell w-44 tabular-nums text-muted',
                  render: (entry) => formatTime(entry.mtime),
                },
              ]}
              rows={query.data}
              rowKey={(entry) => entry.name}
              loading={query.isPending}
              onRowClick={openEntry}
              selection={{
                selected,
                onChange: setSelected,
                // 目录不能下载，只允许勾选文件
                isRowSelectable: (entry) => !entry.directory,
              }}
              empty={
                <EmptyState
                  icon={<FolderOpen size={22} />}
                  title="目录为空"
                  description="当前目录中没有文件或子目录。"
                />
              }
            />
          </Card>
        )}
      </div>

      <SelectionBar count={selected.size} onClear={() => setSelected(new Set())}>
        <Button
          variant="primary"
          size="sm"
          loading={downloading}
          onClick={() => void downloadSelected()}
        >
          {!downloading && <DownloadSimple size={14} />}
          {downloading ? '下载中…' : '下载'}
        </Button>
      </SelectionBar>

      <Sheet
        open={!!preview}
        onOpenChange={() => setPreview(undefined)}
        title="图片预览"
        width="min(720px, 92vw)"
      >
        <div className="p-4">
          {preview && (
            <img
              src={preview}
              className="w-full rounded-[12px] border border-border"
              alt="文件预览"
            />
          )}
        </div>
      </Sheet>
    </PageTransition>
  )
}
