import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { fileURLToPath, URL } from 'node:url'

const src = (p: string) => fileURLToPath(new URL(`./src/${p}`, import.meta.url))

export default defineConfig(({ command }) => ({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': src(''),
      // 国旗：构建时 glob 出带哈希的资源；开发时直接走 /node_modules，避免首屏数百个模块请求
      'virtual:flags': src(command === 'build' ? 'lib/flags.prod.ts' : 'lib/flags.dev.ts'),
    },
  },
  server: { proxy: { '/api': 'http://localhost:8866', '/mcp': 'http://localhost:8866' } },
}))