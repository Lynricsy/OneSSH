import { expect, test } from '@playwright/test'
import type { Host } from '../src/api/types'

test('clearing an existing jump host stays direct and omits jump_host when saved', async ({ page }) => {
  const defaults = {
    port: 22,
    username: 'root',
    auth_type: 'password' as const,
    monitor_enabled: false,
    archived: false,
    tags: [],
    created_at: 1,
  }
  let hosts: Host[] = [
    { ...defaults, id: 1, name: 'bastion', addr: '192.0.2.1' },
    { ...defaults, id: 2, name: 'target', addr: '192.0.2.2', jump_host_id: 1 },
  ]

  // All API traffic stays in this browser fixture; no real gateway is needed.
  await page.route((url) => url.pathname.startsWith('/api/'), async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (path === '/api/v1/hosts' && request.method() === 'GET') {
      return route.fulfill({ json: hosts })
    }
    if (path === '/api/v1/keys' && request.method() === 'GET') {
      return route.fulfill({ json: [] })
    }
    // 外壳组件（账户菜单、分布地图、延迟、传输队列）的只读请求：给空数据即可，与本用例无关
    const shell: Record<string, unknown> = {
      '/api/v1/account': { username: 'admin', password_source: 'env', password_changed_at: 0, store_enabled: true, sessions: [], logins: [], failed_24h: 0 },
      '/api/v1/geo': { hosts: [], origin: null },
      '/api/v1/geo/latency': [],
      '/api/v1/sftp/transfers': [],
    }
    if (request.method() === 'GET' && path in shell) {
      return route.fulfill({ json: shell[path] })
    }
    if (request.method() === 'GET' && path === '/api/v1/events') {
      return route.fulfill({ status: 204, body: '' })
    }
    if (path === '/api/v1/hosts/2' && request.method() === 'PUT') {
      const payload = request.postDataJSON()
      hosts = hosts.map((host) => host.id === 2
        ? { ...host, ...payload, jump_host_id: hosts.find((item) => item.name === payload.jump_host)?.id }
        : host)
      return route.fulfill({ json: hosts[1] })
    }
    console.log('UNMOCKED', request.method(), path)
    await route.abort()
    throw new Error(`Unexpected fixture API: ${request.method()} ${path}`)
  })

  await page.goto('/hosts')
  await page.getByRole('button', { name: 'target 操作', exact: true }).click()
  await page.getByRole('menuitem', { name: '编辑', exact: true }).click()

  const dialog = page.getByRole('dialog', { name: '编辑主机', exact: true })
  const jumpHost = dialog.getByRole('combobox', { name: '跳板主机', exact: true })
  await expect(jumpHost).toHaveText('bastion')
  await jumpHost.click()
  await page.getByRole('option', { name: '直连（不使用跳板）', exact: true }).click()
  // This fails with the old Controller.onChange(undefined) fallback to bastion.
  await expect(jumpHost).toHaveText('直连（不使用跳板）')

  const savedRequest = page.waitForRequest((request) =>
    new URL(request.url()).pathname === '/api/v1/hosts/2' && request.method() === 'PUT')
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  const payload = (await savedRequest).postDataJSON()
  expect(payload).toMatchObject({ name: 'target', addr: '192.0.2.2', username: 'root' })
  expect(payload).not.toHaveProperty('jump_host')
  expect(payload).not.toHaveProperty('password')
  await expect(dialog).not.toBeVisible()

  await page.reload()
  await page.getByRole('button', { name: 'target 操作', exact: true }).click()
  await page.getByRole('menuitem', { name: '编辑', exact: true }).click()
  await expect(jumpHost).toHaveText('直连（不使用跳板）')
})
