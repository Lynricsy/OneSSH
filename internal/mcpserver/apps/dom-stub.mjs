// 给卡片运行时用的极简 DOM/宿主替身。
//
// 卡片是纯浏览器代码，但它最关键的两件事——ui/* 协议握手和「哪些工具允许从卡片回调」——
// 恰恰是截图看不出、Go 测试也够不着的。这里用一个手写替身把 runtime.js 真跑起来，
// 从而让这两条路径进 CI。不引 jsdom 是为了让卡片资产不依赖前端应用的工具链：
// 运行时只用到下面这些 DOM 接口，替身覆盖它们就够了，多一个依赖反而多一处会坏的地方。

class ClassList {
  constructor(el) { this.el = el; this.set = new Set(); }
  add(...names) { names.forEach(n => n && this.set.add(n)); }
  remove(...names) { names.forEach(n => this.set.delete(n)); }
  contains(name) { return this.set.has(name); }
  toggle(name, force) {
    const want = force === undefined ? !this.set.has(name) : !!force;
    if (want) this.set.add(name); else this.set.delete(name);
    return want;
  }
  toString() { return [...this.set].join(' '); }
}

class Style {
  constructor() { this.props = new Map(); }
  setProperty(name, value) { this.props.set(name, value); }
  removeProperty(name) { this.props.delete(name); }
  getPropertyValue(name) { return this.props.get(name) || ''; }
  set height(v) { this.props.set('height', v); }
  get height() { return this.props.get('height') || ''; }
}

let nextId = 1;

class Node {
  constructor(tag) {
    this.tagName = String(tag || '').toUpperCase();
    this.nodeType = 1;
    this.childNodes = [];
    this.attributes = new Map();
    this.listeners = new Map();
    this.style = new Style();
    this.dataset = {};
    this._text = '';
    this._class = '';
    this.parentNode = null;
    this.disabled = false;
    this.hidden = false;
    this.lang = '';
    this._uid = nextId++;
  }
  get className() { return this._class; }
  set className(v) {
    this._class = String(v == null ? '' : v);
    this._classList = null;
  }
  get classList() {
    if (!this._classList) {
      this._classList = new ClassList(this);
      String(this._class).split(/\s+/).filter(Boolean).forEach(n => this._classList.set.add(n));
      const self = this;
      const sync = () => { self._class = self._classList.toString(); };
      ['add', 'remove', 'toggle'].forEach(m => {
        const orig = this._classList[m].bind(this._classList);
        this._classList[m] = (...args) => { const r = orig(...args); sync(); return r; };
      });
    }
    return this._classList;
  }
  get firstChild() { return this.childNodes[0] || null; }
  get children() { return this.childNodes.filter(n => n.nodeType === 1); }
  appendChild(child) {
    if (!child) return child;
    if (child.parentNode) child.parentNode.removeChild(child);
    child.parentNode = this;
    this.childNodes.push(child);
    return child;
  }
  append(...nodes) { nodes.forEach(n => n != null && this.appendChild(typeof n === 'string' ? new Text(n) : n)); }
  insertBefore(child, ref) {
    if (!child) return child;
    if (child.parentNode) child.parentNode.removeChild(child);
    const i = ref ? this.childNodes.indexOf(ref) : -1;
    child.parentNode = this;
    if (i < 0) this.childNodes.push(child); else this.childNodes.splice(i, 0, child);
    return child;
  }
  prepend(...nodes) { nodes.slice().reverse().forEach(n => n != null && this.insertBefore(n, this.childNodes[0] || null)); }
  removeChild(child) {
    const i = this.childNodes.indexOf(child);
    if (i >= 0) { this.childNodes.splice(i, 1); child.parentNode = null; }
    return child;
  }
  remove() { if (this.parentNode) this.parentNode.removeChild(this); }
  replaceChildren(...nodes) {
    this.childNodes.slice().forEach(c => this.removeChild(c));
    nodes.forEach(n => n != null && this.appendChild(n));
  }
  setAttribute(name, value) {
    this.attributes.set(name, String(value));
    if (name === 'class') this.className = value;
    if (name.startsWith('data-')) this.dataset[name.slice(5).replace(/-(\w)/g, (_, c) => c.toUpperCase())] = String(value);
  }
  getAttribute(name) { return this.attributes.has(name) ? this.attributes.get(name) : null; }
  removeAttribute(name) {
    this.attributes.delete(name);
    if (name.startsWith('data-')) delete this.dataset[name.slice(5).replace(/-(\w)/g, (_, c) => c.toUpperCase())];
  }
  hasAttribute(name) { return this.attributes.has(name); }
  addEventListener(type, fn) {
    if (!this.listeners.has(type)) this.listeners.set(type, []);
    this.listeners.get(type).push(fn);
  }
  removeEventListener(type, fn) {
    const list = this.listeners.get(type) || [];
    const i = list.indexOf(fn);
    if (i >= 0) list.splice(i, 1);
  }
  dispatch(type, event) {
    (this.listeners.get(type) || []).slice().forEach(fn => fn.call(this, event || { type, target: this, preventDefault() {}, stopPropagation() {} }));
  }
  click() { this.dispatch('click', { type: 'click', target: this, preventDefault() {}, stopPropagation() {} }); }
  get textContent() {
    if (this.nodeType === 3) return this._text;
    return this.childNodes.map(c => c.textContent).join('') || this._text;
  }
  set textContent(v) {
    this.childNodes.slice().forEach(c => this.removeChild(c));
    this._text = String(v == null ? '' : v);
  }
  getBoundingClientRect() { return { width: 640, height: 200, top: 0, left: 0 }; }
  matchesSelector(sel) {
    if (sel.startsWith('.')) return this.classList.contains(sel.slice(1));
    if (sel.startsWith('[')) {
      const name = sel.slice(1, -1).split('=')[0];
      return this.attributes.has(name);
    }
    return this.tagName === sel.toUpperCase();
  }
  walk(out = []) {
    this.childNodes.forEach(c => { if (c.nodeType === 1) { out.push(c); c.walk(out); } });
    return out;
  }
  querySelectorAll(sel) {
    const parts = sel.split(',').map(s => s.trim()).filter(Boolean);
    return this.walk().filter(el => parts.some(p => el.matchesSelector(p)));
  }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
}

class Text extends Node {
  constructor(text) { super('#text'); this.nodeType = 3; this._text = String(text == null ? '' : text); }
}

export function createEnvironment({ title = 'OneSSH · 测试', tool = 'exec' } = {}) {
  const sent = [];
  const documentElement = new Node('html');
  const body = new Node('body');
  const root = new Node('main');
  root.setAttribute('id', 'root');
  root.className = 'root';
  root.setAttribute('data-state', 'waiting');
  body.appendChild(root);

  const document = {
    documentElement,
    body,
    title,
    createElement: tag => new Node(tag),
    createTextNode: text => new Text(text),
    getElementById: id => (id === 'root' ? root : null),
    querySelector: sel => body.querySelector(sel),
    querySelectorAll: sel => body.querySelectorAll(sel),
    addEventListener() {},
  };

  const window = {
    document,
    postMessage(msg) { sent.push(msg); },
    addEventListener(type, fn) { (window._on[type] = window._on[type] || []).push(fn); },
    removeEventListener() {},
    _on: {},
    requestAnimationFrame(fn) { fn(); return 1; },
    setTimeout: (fn, ms) => setTimeout(fn, ms),
    clearTimeout: id => clearTimeout(id),
    ResizeObserver: class { observe() {} disconnect() {} },
    navigator: {},
  };
  // 卡片只信任来自 window.parent 的消息，测试里让宿主帧就是这个替身自己
  window.parent = { postMessage(msg) { sent.push(msg); } };
  window.self = window;

  const deliver = msg => (window._on.message || []).forEach(fn => fn({ source: window.parent, data: msg }));

  return { window, document, root, documentElement, sent, deliver, tool };
}

export { Node, Text };
