import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { cn } from '@/lib/cn'

export type MenuItem =
  | {
      label: string
      icon?: React.ReactNode
      shortcut?: string
      danger?: boolean
      disabled?: boolean
      onSelect: () => void
    }
  | 'separator'

export type MenuState = { x: number; y: number; items: MenuItem[] } | null

/** 右键菜单：跟随鼠标位置，自动避开视口边缘；点外部 / Esc / 滚动即关闭 */
export function ContextMenu({ menu, onClose }: { menu: MenuState; onClose: () => void }) {
  const ref = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState({ x: 0, y: 0 })

  useLayoutEffect(() => {
    if (!menu || !ref.current) return
    const rect = ref.current.getBoundingClientRect()
    setPos({
      x: Math.min(menu.x, window.innerWidth - rect.width - 8),
      y: Math.min(menu.y, window.innerHeight - rect.height - 8),
    })
  }, [menu])

  useEffect(() => {
    if (!menu) return
    const close = (event: Event) => {
      if (event instanceof MouseEvent && ref.current?.contains(event.target as Node)) return
      onClose()
    }
    const onKey = (event: KeyboardEvent) => event.key === 'Escape' && onClose()
    window.addEventListener('mousedown', close, true)
    window.addEventListener('blur', close)
    window.addEventListener('resize', close)
    window.addEventListener('scroll', close, true)
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('mousedown', close, true)
      window.removeEventListener('blur', close)
      window.removeEventListener('resize', close)
      window.removeEventListener('scroll', close, true)
      window.removeEventListener('keydown', onKey)
    }
  }, [menu, onClose])

  if (!menu) return null
  return createPortal(
    <div
      ref={ref}
      role="menu"
      className="fixed z-[70] min-w-[210px] overflow-hidden rounded-[12px] border border-border bg-surface/95 p-1 shadow-pop backdrop-blur"
      style={{ left: pos.x || menu.x, top: pos.y || menu.y }}
      onContextMenu={(event) => event.preventDefault()}
    >
      {menu.items.map((item, index) =>
        item === 'separator' ? (
          <div key={`sep-${index}`} className="my-1 h-px bg-border" />
        ) : (
          <button
            key={item.label}
            type="button"
            role="menuitem"
            disabled={item.disabled}
            onClick={() => {
              onClose()
              item.onSelect()
            }}
            className={cn(
              'flex w-full items-center gap-2.5 rounded-[8px] px-2.5 py-1.5 text-left text-[13px] transition-colors disabled:pointer-events-none disabled:opacity-40',
              item.danger ? 'text-danger hover:bg-danger/10' : 'text-text hover:bg-surface-2',
            )}
          >
            <span className={cn('grid size-4 shrink-0 place-items-center', item.danger ? 'text-danger' : 'text-muted')}>{item.icon}</span>
            <span className="flex-1 truncate">{item.label}</span>
            {item.shortcut && <kbd className="font-mono text-[10.5px] text-faint">{item.shortcut}</kbd>}
          </button>
        ),
      )}
    </div>,
    document.body,
  )
}
