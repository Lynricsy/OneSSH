import { useEffect, useMemo, useState } from 'react'
import CodeMirror, { EditorView, type Extension } from '@uiw/react-codemirror'
import { StreamLanguage } from '@codemirror/language'
import { oneDark } from '@codemirror/theme-one-dark'
import { useTheme } from '@/lib/theme'

/** 按语言异步加载语法包：只在打开对应文件时才下载那一门语言 */
async function loadLanguage(lang: string): Promise<Extension | null> {
  switch (lang) {
    case 'javascript':
    case 'jsx':
    case 'typescript':
    case 'tsx': {
      const { javascript } = await import('@codemirror/lang-javascript')
      return javascript({ jsx: lang.endsWith('x'), typescript: lang.startsWith('t') })
    }
    case 'json':
      return (await import('@codemirror/lang-json')).json()
    case 'python':
      return (await import('@codemirror/lang-python')).python()
    case 'yaml':
      return (await import('@codemirror/lang-yaml')).yaml()
    case 'markdown':
      return (await import('@codemirror/lang-markdown')).markdown()
    case 'html':
      return (await import('@codemirror/lang-html')).html()
    case 'css':
      return (await import('@codemirror/lang-css')).css()
    case 'sql':
      return (await import('@codemirror/lang-sql')).sql()
    case 'xml':
      return (await import('@codemirror/lang-xml')).xml()
    case 'go':
      return (await import('@codemirror/lang-go')).go()
    case 'shell':
      return StreamLanguage.define((await import('@codemirror/legacy-modes/mode/shell')).shell)
    case 'nginx':
      return StreamLanguage.define((await import('@codemirror/legacy-modes/mode/nginx')).nginx)
    case 'dockerfile':
      return StreamLanguage.define((await import('@codemirror/legacy-modes/mode/dockerfile')).dockerFile)
    case 'toml':
      return StreamLanguage.define((await import('@codemirror/legacy-modes/mode/toml')).toml)
    case 'properties':
      return StreamLanguage.define((await import('@codemirror/legacy-modes/mode/properties')).properties)
    case 'lua':
      return StreamLanguage.define((await import('@codemirror/legacy-modes/mode/lua')).lua)
    case 'ruby':
      return StreamLanguage.define((await import('@codemirror/legacy-modes/mode/ruby')).ruby)
    case 'perl':
      return StreamLanguage.define((await import('@codemirror/legacy-modes/mode/perl')).perl)
    case 'rust':
      return StreamLanguage.define((await import('@codemirror/legacy-modes/mode/rust')).rust)
    case 'powershell':
      return StreamLanguage.define((await import('@codemirror/legacy-modes/mode/powershell')).powerShell)
    case 'c':
    case 'cpp':
    case 'java':
    case 'csharp':
    case 'kotlin': {
      const clike = await import('@codemirror/legacy-modes/mode/clike')
      const mode = { c: clike.c, cpp: clike.cpp, java: clike.java, csharp: clike.csharp, kotlin: clike.kotlin }[lang]
      return StreamLanguage.define(mode)
    }
    case 'swift':
      return StreamLanguage.define((await import('@codemirror/legacy-modes/mode/swift')).swift)
    default:
      return null
  }
}


const baseTheme = EditorView.theme({
  '&': { height: '100%', fontSize: '13px' },
  '.cm-scroller': { fontFamily: '"JetBrains Mono Variable", ui-monospace, monospace', lineHeight: '1.6' },
  '.cm-gutters': { borderRight: '1px solid var(--border)' },
})

export function CodeEditor({
  value,
  onChange,
  language,
  wrap,
  readOnly,
  onCursor,
}: {
  value: string
  onChange: (value: string) => void
  language: string
  wrap: boolean
  readOnly?: boolean
  onCursor?: (line: number, col: number) => void
}) {
  const { resolved } = useTheme()
  const [lang, setLang] = useState<Extension | null>(null)

  useEffect(() => {
    let alive = true
    setLang(null)
    void loadLanguage(language).then((ext) => alive && setLang(ext))
    return () => {
      alive = false
    }
  }, [language])

  const extensions = useMemo(() => {
    const list: Extension[] = [
      baseTheme,
      EditorView.updateListener.of((update) => {
        if (!onCursor || !update.selectionSet) return
        const head = update.state.selection.main.head
        const line = update.state.doc.lineAt(head)
        onCursor(line.number, head - line.from + 1)
      }),
    ]
    if (wrap) list.push(EditorView.lineWrapping)
    if (lang) list.push(lang)
    return list
  }, [lang, wrap, onCursor])

  return (
    <CodeMirror
      value={value}
      onChange={onChange}
      height="100%"
      className="h-full"
      theme={resolved === 'dark' ? oneDark : 'light'}
      readOnly={readOnly}
      extensions={extensions}
      basicSetup={{ highlightActiveLine: true, foldGutter: true, bracketMatching: true, autocompletion: false, searchKeymap: true }}
      autoFocus
    />
  )
}
