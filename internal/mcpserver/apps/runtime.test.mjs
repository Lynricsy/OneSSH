// 卡片运行时的协议与安全一致性测试。
//
// 截图能验收「长得对不对」，Go 测试能验收「发出去的元数据对不对」，
// 但两者都碰不到卡片自己在浏览器里跑的那段逻辑。这里在 Node 里把 runtime.js
// 真的执行起来，覆盖三件不能出错的事：
//   1. ui/* 握手与通知的报文形状（宿主据此决定给不给能力、怎么排版）；
//   2. 只读回调白名单（这是卡片唯一能主动触碰服务端的地方）；
//   3. 32 张卡片对真实样例数据的渲染不抛异常、不静默空白。
//
// 跑法：node --test internal/mcpserver/apps/runtime.test.mjs

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import vm from 'node:vm';
import { createEnvironment } from './dom-stub.mjs';

const DIR = dirname(fileURLToPath(import.meta.url));
const read = p => readFileSync(join(DIR, p), 'utf8');
const RUNTIME = read('runtime.js');
const FIXTURES = join(DIR, 'preview', 'fixtures');

const GROUPS = {
  hosts: ['hosts_list', 'hosts_manage_list', 'host_create', 'host_update', 'host_test', 'host_reset_fingerprint', 'host_delete'],
  exec: ['exec', 'session_env', 'output_read', 'exec_many'],
  jobs: ['job_start', 'job_list', 'job_status', 'job_logs', 'job_kill'],
  files: ['file_read', 'file_write', 'file_edit', 'file_list', 'file_transfer'],
  search: ['grep', 'find'],
  memory: ['memory_remember', 'memory_recall', 'memory_list', 'memory_update', 'memory_forget', 'memory_stats', 'memory_sleep'],
  monitor: ['host_status'],
  image: ['image_view'],
};
const GROUP_OF = {};
for (const [group, tools] of Object.entries(GROUPS)) tools.forEach(t => (GROUP_OF[t] = group));
const ALL_TOOLS = Object.keys(GROUP_OF);

const fixture = tool => JSON.parse(readFileSync(join(FIXTURES, tool + '.json'), 'utf8'));

/* 浏览器里 window 就是全局对象，runtime.js 写 window.OneSSH、视图读裸 OneSSH 才对得上。
   所以这里直接把替身 window 本身 contextify，而不是把它当成全局上的一个属性。 */
function contextify(env) {
  const sandbox = env.window;
  sandbox.window = sandbox;
  sandbox.globalThis = sandbox;
  sandbox.console = console;
  return vm.createContext(sandbox);
}

const settle = () => new Promise(r => setTimeout(r, 0));

// 起一张卡片：装载运行时（可选装载视图），完成握手，返回操作句柄。
// 必须是 async：握手结果是 Promise 兑现的，同步断言会跑在 initialized 发出之前。
async function boot(tool, { view = true, hostCapabilities = { serverTools: {} }, hostContext = {} } = {}) {
  const env = createEnvironment({ tool });
  const context = contextify(env);
  vm.runInContext(RUNTIME, context, { filename: 'runtime.js' });
  if (view) vm.runInContext(read(join('views', GROUP_OF[tool] + '.js')), context, { filename: GROUP_OF[tool] + '.js' });
  vm.runInContext(`OneSSH.boot(${JSON.stringify(tool)});`, context, { filename: 'boot' });

  const init = env.sent.find(m => m.method === 'ui/initialize');
  assert.ok(init, tool + ' 没有发起 ui/initialize');
  env.deliver({
    jsonrpc: '2.0', id: init.id,
    result: {
      protocolVersion: '2026-01-26',
      hostInfo: { name: 'test-host', version: '1' },
      hostCapabilities,
      hostContext: Object.assign(
        { theme: 'light', displayMode: 'inline', availableDisplayModes: ['inline', 'fullscreen'], locale: 'zh-CN' },
        hostContext),
    },
  });
  await settle();
  const card = {
    ...env, init, context,
    of: method => env.sent.filter(m => m.method === method),
    api: () => vm.runInContext('OneSSH', context, {}),
    /* ctx 只在视图渲染时才拿得到。这里临时注册一个探针视图把它捞出来，
       而不是在运行时里开一个只为测试存在的后门。 */
    async ctx() {
      const api = this.api();
      let captured = null;
      api.view(tool, (data, ctx) => { captured = ctx; return api.ui.empty('probe'); });
      this.deliver({ jsonrpc: '2.0', method: 'ui/notifications/tool-result', params: { structuredContent: {}, content: [] } });
      await settle();
      assert.ok(captured, '探针视图没有拿到 ctx');
      return captured;
    },
  };
  return card;
}

test('握手声明协议版本与显示模式能力', async () => {
  const card = await boot('exec');
  assert.equal(card.init.params.protocolVersion, '2026-01-26');
  // 不声明支持的显示模式，宿主有理由拒绝后续的全屏请求，而卡片却一直显示着全屏按钮
  const modes = card.init.params.appCapabilities.availableDisplayModes;
  assert.ok(Array.isArray(modes) && modes.includes('fullscreen') && modes.includes('inline'),
    'appCapabilities.availableDisplayModes = ' + JSON.stringify(modes));
  assert.equal(card.of('ui/notifications/initialized').length, 1);
});

test('握手结果里的主题与语言写到文档上', async () => {
  const card = await boot('exec', { hostContext: { theme: 'dark', locale: 'en-US' } });
  assert.equal(card.documentElement.dataset.theme, 'dark');
  // lang 要跟着实际渲染出来的文案语言走：宿主报 en-US 时卡片渲染英文，lang 就该是 en
  assert.equal(card.documentElement.lang, 'en');
  const zh = await boot('exec', { hostContext: { locale: 'zh-CN' } });
  assert.equal(zh.documentElement.lang, 'zh-CN');
});

test('tool-input 先进入执行中，tool-result 后渲染并上报高度', async () => {
  const card = await boot('exec');
  const data = fixture('exec');
  card.deliver({ jsonrpc: '2.0', method: 'ui/notifications/tool-input', params: { arguments: data.input } });
  assert.equal(card.root.getAttribute('data-state'), 'running');
  card.deliver({ jsonrpc: '2.0', method: 'ui/notifications/tool-result', params: data.result });
  assert.equal(card.root.getAttribute('data-state'), 'ready');
  assert.ok(card.root.querySelector('.card'), '结果到达后应当渲染出卡片');
  assert.ok(card.of('ui/notifications/size-changed').length >= 1, '应当上报高度');
});

test('重复推送同一结果不重建，但换了参数要重建', async () => {
  const card = await boot('file_list');
  const data = fixture('file_list');
  card.deliver({ jsonrpc: '2.0', method: 'ui/notifications/tool-input', params: { arguments: { host: 'web-01', path: '/a' } } });
  card.deliver({ jsonrpc: '2.0', method: 'ui/notifications/tool-result', params: data.result });
  const first = card.root.querySelector('.card-title').textContent;
  card.deliver({ jsonrpc: '2.0', method: 'ui/notifications/tool-result', params: data.result });
  assert.equal(card.root.getAttribute('data-state'), 'ready', '重复推送不该把卡片留在执行中');
  // 结果字节级相同但参数变了：宿主复用 iframe 发起第二次调用就是这种形态
  card.deliver({ jsonrpc: '2.0', method: 'ui/notifications/tool-input', params: { arguments: { host: 'web-01', path: '/b/second' } } });
  card.deliver({ jsonrpc: '2.0', method: 'ui/notifications/tool-result', params: data.result });
  const second = card.root.querySelector('.card-title').textContent;
  assert.notEqual(second, first, '参数变了却没重建，卡片会挂着上一次的路径');
});

test('isError 渲染错误卡而不是把零值当成功', async () => {
  const card = await boot('exec');
  const data = fixture('exec');
  card.deliver({ jsonrpc: '2.0', method: 'ui/notifications/tool-input', params: { arguments: data.input } });
  card.deliver({ jsonrpc: '2.0', method: 'ui/notifications/tool-result', params: data.error });
  assert.equal(card.root.getAttribute('data-state'), 'error');
  assert.match(card.root.textContent, /host not authorized/);
});

test('未知方法回 -32601，ping 与 teardown 正常应答', async () => {
  const card = await boot('exec');
  card.deliver({ jsonrpc: '2.0', id: 91, method: 'ping', params: {} });
  card.deliver({ jsonrpc: '2.0', id: 92, method: 'ui/resource-teardown', params: { reason: 'x' } });
  card.deliver({ jsonrpc: '2.0', id: 93, method: 'ui/nonexistent', params: {} });
  const replies = card.sent.filter(m => m.id === 91 || m.id === 92 || m.id === 93);
  assert.equal(replies.length, 3);
  assert.equal(Object.keys(replies.find(r => r.id === 91).result).length, 0, 'ping 应当回一个空结果');
  assert.equal(replies.find(r => r.id === 93).error.code, -32601);
});

test('非 JSON-RPC 帧一律忽略，不会被原型链上的成员骗到', async () => {
  const card = await boot('exec');
  const before = card.sent.length;
  card.deliver({ hello: 'world' });
  card.deliver({ jsonrpc: '1.0', id: 'constructor', result: {} });
  card.deliver({ id: 'toString', result: {} });
  assert.equal(card.sent.length, before, '不该对非法帧产生任何回包');
  assert.equal(card.root.getAttribute('data-state'), 'waiting');
});

test('只读白名单：破坏性工具一律不能从卡片发起', async () => {
  const card = await boot('file_list');
  const ctx = await card.ctx();
  const allowed = ['hosts_list', 'hosts_manage_list', 'file_list', 'file_read', 'output_read',
    'job_list', 'job_status', 'job_logs', 'host_status', 'memory_list', 'memory_stats', 'memory_recall'];
  // grep / find 虽然只读，但可能向远端上传临时 helper、开销也不确定，
  // 不适合由界面随手触发，所以同样不在白名单里。
  const forbidden = ['exec', 'exec_many', 'job_start', 'job_kill', 'file_write', 'file_edit', 'file_transfer',
    'host_create', 'host_update', 'host_delete', 'host_test', 'host_reset_fingerprint', 'session_env',
    'memory_remember', 'memory_update', 'memory_forget', 'memory_sleep', 'image_view', 'grep', 'find'];
  assert.equal(allowed.length + forbidden.length, ALL_TOOLS.length, '白名单与禁用名单应当覆盖全部工具');
  for (const name of allowed) assert.ok(ctx.can(name), name + ' 应当允许卡片回调');
  for (const name of forbidden) {
    assert.ok(!ctx.can(name), name + ' 有副作用，不该允许卡片回调');
    const before = card.sent.length;
    await assert.rejects(ctx.call(name, {}), new RegExp('.'), name + ' 的调用应当被拒绝');
    assert.equal(card.sent.length, before, name + ' 被拒绝时不该把请求发出去');
  }
});

test('宿主没宣告 serverTools 时不发 tools/call', async () => {
  const card = await boot('file_list', { hostCapabilities: {} });
  const ctx = await card.ctx();
  assert.ok(!ctx.can('file_read'), '宿主没有工具回调能力时不该显示动作');
  const before = card.sent.length;
  await assert.rejects(ctx.call('file_read', {}));
  assert.equal(card.sent.length, before);
});

test('导航会把看到的内容同步给模型', async () => {
  const card = await boot('file_list');
  const ctx = await card.ctx();
  const promise = ctx.navigate('file_read', { host: 'web-01', path: '/etc/nginx/nginx.conf' }, '文件');
  const call = card.sent.find(m => m.method === 'tools/call');
  assert.ok(call, '应当发出 tools/call');
  assert.equal(call.params.name, 'file_read');
  card.deliver({ jsonrpc: '2.0', id: call.id, result: fixture('file_read').result });
  await promise;
  const note = card.of('ui/update-model-context')[0];
  assert.ok(note, '导航后应当告知模型');
  // 规范要求 params 用 ContentBlock 数组，发扁平的 {text} 会被宿主直接丢弃
  assert.ok(Array.isArray(note.params.content) && note.params.content[0].type === 'text',
    'ui/update-model-context params = ' + JSON.stringify(note.params));
});

test('握手失败也要渲染后到的结果', async () => {
  const env = createEnvironment({ tool: 'exec' });
  const context = contextify(env);
  vm.runInContext(RUNTIME, context, { filename: 'runtime.js' });
  vm.runInContext(read(join('views', 'exec.js')), context, { filename: 'exec.js' });
  vm.runInContext('OneSSH.boot("exec");', context, {});
  // 宿主不应答 ui/initialize，直接推结果：运行时会先把通知压在队列里，
  // 等握手超时进入降级模式后再统一放行，所以这里必须等过那个窗口。
  env.deliver({ jsonrpc: '2.0', method: 'ui/notifications/tool-result', params: fixture('exec').result });
  await new Promise(r => setTimeout(r, 3400));
  assert.ok(env.root.querySelector('.card'), '宿主不握手时也必须把结果渲染出来');
});

test('32 张卡片都能渲染真实样例，成功态与错误态都不抛异常', async () => {
  const files = readdirSync(FIXTURES).filter(f => f.endsWith('.json')).map(f => f.slice(0, -5)).sort();
  assert.deepEqual(files, ALL_TOOLS.slice().sort(), '样例数据必须与工具清单一一对应');
  for (const tool of ALL_TOOLS) {
    const data = fixture(tool);
    for (const [label, result] of [['成功', data.result], ['错误', data.error]]) {
      const card = await boot(tool);
      card.deliver({ jsonrpc: '2.0', method: 'ui/notifications/tool-input', params: { arguments: data.input || {} } });
      card.deliver({ jsonrpc: '2.0', method: 'ui/notifications/tool-result', params: result });
      const state = card.root.getAttribute('data-state');
      assert.equal(state, label === '成功' ? 'ready' : 'error', tool + ' 的' + label + '态 data-state = ' + state);
      assert.ok(card.root.querySelector('.card'), tool + ' 的' + label + '态没有渲染出卡片');
      assert.ok(card.root.textContent.trim().length > 0, tool + ' 的' + label + '态渲染成了空白');
    }
  }
});

test('缺字段、空数组、零值都不会把卡片打崩', async () => {
  for (const tool of ALL_TOOLS) {
    for (const payload of [{}, { structuredContent: {}, content: [] },
      { structuredContent: null, content: [] }, { structuredContent: { results: [], entries: [], hosts: [], jobs: [] }, content: [] }]) {
      const card = await boot(tool);
      card.deliver({ jsonrpc: '2.0', method: 'ui/notifications/tool-result', params: payload });
      assert.ok(card.root.textContent.trim().length > 0, tool + ' 在空结果下渲染成了空白');
    }
  }
});
