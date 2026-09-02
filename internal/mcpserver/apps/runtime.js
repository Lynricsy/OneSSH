;(function () {
  "use strict";

  /* OneSSH 卡片运行时。
     职责边界：这里独占与宿主的 JSON-RPC 桥接、状态机、DOM 原子与格式化；
     views/*.js 只消费 window.OneSSH，不直接碰 postMessage，也不自己造 class 名。
     全部资产会被拼进同一个内联 script，因此这里必须是一个不泄漏全局的 IIFE。 */

  var PROTOCOL_VERSION = "2026-01-26";
  var INIT_TIMEOUT_MS = 3000;
  /* 卡片自己支持的显示模式，握手时随 appCapabilities 一起报给宿主。
     CONTRACT §2.1 写的是 appCapabilities:{}，这里刻意多报一项：availableDisplayModes 才是宿主
     判断「能不能给它全屏」的依据，什么都不声明的宿主完全有理由直接拒绝 ui/request-display-mode，
     而卡片右上角还挂着一个点不动的全屏按钮——对用户来说那比少一个按钮更糟。
     canFullscreen() 也从这份列表出发，声明与按钮不会各说各话。 */
  var APP_DISPLAY_MODES = ["inline", "fullscreen"];
  /* 卡片发起的只读调用要覆盖服务端真实的执行边界，而不是凭感觉给一个短值：
     host_status 的现场采样带一秒静置、job_list 会逐台刷新任务，主机一多就远超 20 秒。
     超时太短的后果是服务端明明还在正常处理，卡片却已经报「宿主没有响应」并丢弃了回包。
     这里只用来兜住「宿主永远不回」导致 busy 卡死，所以给足余量。 */
  var CALL_TIMEOUT_MS = 180000;
  var TEXT_LIMIT = 200000;   // 终端块单次渲染上限：再长的输出对人已无意义，却足以让 iframe 卡住
  var TERM_LINES = 24;
  var TERM_PAGE = 500;       // 展开时每次追加的行数：一次铺完上万行会让 iframe 卡住
  var DIFF_LINES = 400;
  var JSON_LINES = 200;
  var TABLE_ROWS = 200;

  // 只读回调白名单。卡片里的任何交互都只能触发这些工具；写操作一律留给模型，
  // 避免用户点一下按钮就在远端机器上产生副作用。
  var CALLABLE = ["hosts_list", "hosts_manage_list", "file_list", "file_read", "output_read",
    "job_list", "job_status", "job_logs", "host_status", "memory_list", "memory_stats", "memory_recall"];

  var views = {};
  // 用无原型对象存待回调：宿主若发来 {id:"constructor"} 这类消息，
  // 普通对象会命中 Object.prototype 上的同名成员，waiter 为真但没有 resolve，直接抛 TypeError。
  var pending = Object.create(null);
  var backlog = [];          // 握手完成前到达的通知，完成后按序重放
  var seq = 0;
  var uid = 0;               // 卡片正文 id 计数器，供 caret 的 aria-controls 指向
  var root = null;
  var lastGlobals = null;    // 上次见到的 openai globals.toolOutput 快照

  var state = {
    expectedTool: "",        // boot 传入的工具名，也是没有导航时的渲染目标
    tool: "",
    result: null,
    data: {},
    input: {},
    inputSettled: false,     // 收到完整 tool-input 后，partial 不再覆盖
    isError: false,
    initialized: false,
    bridged: false,
    sizeReady: false,
    hostContext: {},
    hostCapabilities: {},
    locale: "",
    displayMode: "inline",
    displayModes: [],
    maxHeight: 0,
    stack: [],               // 导航返回栈
    busy: false,
    localResult: false,      // 当前展示的是 refresh/navigate 取回的结果，宿主重放的旧数据不得覆盖
    lastKey: null,
    lastHeight: -1
  };

  /* ------------------------------------------------------------------ *
   * 语言与 DOM 工厂
   * ------------------------------------------------------------------ */

  // 宿主没告诉我们 locale 时按中文处理：OneSSH 的使用者以中文为主，猜错英文的代价更大。
  function t(zh, en) {
    var loc = state.locale;
    if (!loc || typeof loc !== "string") return zh;
    return /^zh/i.test(loc) ? zh : (en == null ? zh : en);
  }

  // 文档 lang 必须跟着我们真正渲染出来的那套文案走，而不是照抄宿主 locale：
  // t() 只有中英两套，宿主报 fr-FR 时正文其实是英文，标成 fr 只会让读屏器用法语规则念英文。
  // 复用 t() 本身做判断，文案与发音规则不会各走各的。
  function applyLang() {
    document.documentElement.lang = t("zh-CN", "en");
  }

  function append(parent, child) {
    if (child == null || child === false || child === true) return;
    if (Array.isArray(child)) {
      for (var i = 0; i < child.length; i++) append(parent, child[i]);
      return;
    }
    if (child.nodeType) { parent.appendChild(child); return; }
    parent.appendChild(document.createTextNode(String(child)));
  }

  function setAttr(el, key, value) {
    if (value == null || value === false) return;
    if (key === "class" || key === "className") { el.className = String(value); return; }
    if (key === "text") { el.textContent = String(value); return; }
    if (key === "style" && typeof value === "object") {
      Object.keys(value).forEach(function (name) {
        if (value[name] != null) el.style.setProperty(name, String(value[name]));
      });
      return;
    }
    if (typeof value === "function" && key.length > 2 && key.slice(0, 2) === "on") {
      el.addEventListener(key.slice(2).toLowerCase(), value);
      return;
    }
    if (key === "disabled" || key === "hidden") { el[key] = !!value; return; }
    el.setAttribute(key, value === true ? "" : String(value));
  }

  function h(tag, attrs) {
    var el = document.createElement(tag);
    var start = 1;
    var isAttrs = attrs && typeof attrs === "object" && !attrs.nodeType && !Array.isArray(attrs);
    if (isAttrs) {
      start = 2;
      Object.keys(attrs).forEach(function (key) { setAttr(el, key, attrs[key]); });
    }
    for (var i = start; i < arguments.length; i++) append(el, arguments[i]);
    return el;
  }

  /* ------------------------------------------------------------------ *
   * fmt：格式化
   * ------------------------------------------------------------------ */

  var UNITS = ["B", "KB", "MB", "GB", "TB"];

  function numeric(n) {
    if (typeof n === "string" && n.trim() !== "") n = Number(n);
    return typeof n === "number" && isFinite(n) ? n : null;
  }

  function bytes(n) {
    var v = numeric(n);
    if (v === null) return "";
    var neg = v < 0;
    v = Math.abs(v);
    var i = 0;
    while (v >= 1024 && i < UNITS.length - 1) { v = v / 1024; i++; }
    var text = i === 0 ? String(Math.round(v)) : (v < 10 ? v.toFixed(1) : String(Math.round(v)));
    return (neg ? "-" : "") + text + " " + UNITS[i];
  }

  function kb(n) {
    var v = numeric(n);
    return v === null ? "" : bytes(v * 1024);
  }

  function num(n) {
    var v = numeric(n);
    if (v === null) return "";
    var parts = String(v).split(".");
    parts[0] = parts[0].replace(/\B(?=(\d{3})+(?!\d))/g, ",");
    return parts.join(".");
  }

  function pct(n) {
    var v = numeric(n);
    return v === null ? "—" : v.toFixed(1) + "%";
  }

  function pad2(n) { return (n < 10 ? "0" : "") + n; }

  // 时间戳可能来自秒（file mtime、created_at）或毫秒（job started_at），按量级区分。
  function toDate(ts) {
    var v = numeric(ts);
    if (v === null || v <= 0) return null;
    var d = new Date(v < 1e11 ? v * 1000 : v);
    return isFinite(d.getTime()) ? d : null;
  }

  // 手工补零而不是 toLocaleString：宿主 locale 会把格式换成 MM/DD/YYYY，破坏日志的可对齐性。
  function time(ts) {
    var d = toDate(ts);
    if (!d) return "";
    return d.getFullYear() + "-" + pad2(d.getMonth() + 1) + "-" + pad2(d.getDate()) + " " +
      pad2(d.getHours()) + ":" + pad2(d.getMinutes()) + ":" + pad2(d.getSeconds());
  }

  function rel(ts) {
    var d = toDate(ts);
    if (!d) return "";
    var diff = Date.now() - d.getTime();
    if (diff < 0) return time(ts);
    if (diff < 60000) return t("刚刚", "just now");
    if (diff < 3600000) return Math.floor(diff / 60000) + t(" 分钟前", "m ago");
    if (diff < 86400000) return Math.floor(diff / 3600000) + t(" 小时前", "h ago");
    if (diff < 30 * 86400000) return Math.floor(diff / 86400000) + t(" 天前", "d ago");
    // 30 天以上曾直接回落到 time()，于是记忆列表同一列里会并排出现「9 天前」和
    // 「2026-07-01 14:24:15」两种写法，还带着毫无意义的秒。记忆卡正是最容易出现
    // 「一两个月前」的地方，所以把相对时间一路铺到年；完整时间戳由调用方挂在
    // title 属性上（见 memory.js）供悬停查看，这里不再需要绝对时间兜底。
    // 月档钳到 11：364 天算出来是 12 个月，写成「12 个月前」不如「11 个月前」自然。
    if (diff < 365 * 86400000) {
      return Math.min(11, Math.floor(diff / (30 * 86400000))) + t(" 个月前", "mo ago");
    }
    return Math.floor(diff / (365 * 86400000)) + t(" 年前", "y ago");
  }

  function dur(ms) {
    var v = numeric(ms);
    if (v === null || v < 0) return "";
    if (v < 1000) return Math.round(v) + "ms";
    if (v < 60000) return (v / 1000).toFixed(1) + "s";
    if (v < 3600000) return Math.floor(v / 60000) + "m " + pad2(Math.floor((v % 60000) / 1000)) + "s";
    return Math.floor(v / 3600000) + "h " + pad2(Math.floor((v % 3600000) / 60000)) + "m";
  }

  function short(text, head, tail) {
    var s = text == null ? "" : String(text);
    var a = typeof head === "number" ? head : 8;
    var b = typeof tail === "number" ? tail : 6;
    if (s.length <= a + b + 1) return s;
    return s.slice(0, a) + "…" + s.slice(s.length - b);
  }

  function lines(text) {
    var s = text == null ? "" : String(text);
    var out = s.split("\n");
    while (out.length && out[out.length - 1] === "") out.pop();
    return out;
  }

  var fmt = { bytes: bytes, kb: kb, num: num, pct: pct, time: time, rel: rel, dur: dur, short: short, lines: lines };

  /* ------------------------------------------------------------------ *
   * ui：组件原子
   * ------------------------------------------------------------------ */

  function icon(name) {
    return h("span", { class: "icon icon-" + (name || "dot"), "aria-hidden": "true" });
  }

  function button(o) {
    o = o || {};
    var btn = h("button", { class: "btn btn-" + (o.variant || "ghost"), type: "button" });
    if (o.title) btn.setAttribute("title", o.title);
    if (o.disabled) btn.disabled = true;
    if (o.icon) btn.appendChild(icon(o.icon));
    var label = h("span", { text: o.label == null ? "" : String(o.label) });
    btn.appendChild(label);
    btn._label = label;
    if (typeof o.onClick === "function") {
      btn.addEventListener("click", function (event) {
        event.stopPropagation();
        try { o.onClick(event); } catch (err) { reportViewError(err); }
      });
    }
    return btn;
  }

  function legacyCopy(value) {
    var box = h("textarea", { "aria-hidden": "true", tabindex: "-1" });
    box.value = value;
    box.style.setProperty("position", "fixed");
    box.style.setProperty("top", "0");
    box.style.setProperty("opacity", "0");
    box.style.setProperty("pointer-events", "none");
    document.body.appendChild(box);
    box.select();
    var ok = false;
    try { ok = document.execCommand("copy"); } catch (err) { ok = false; }
    document.body.removeChild(box);
    return ok;
  }

  function copy(text, label) {
    var value = text == null ? "" : String(text);
    var base = label == null ? t("复制", "Copy") : String(label);
    var modern = !!(navigator.clipboard && navigator.clipboard.writeText);
    var legacy = typeof document.execCommand === "function";
    var btn = button({
      label: base,
      icon: "copy",
      title: t("复制到剪贴板", "Copy to clipboard"),
      onClick: function () {
        function flash(msg) {
          btn._label.textContent = msg;
          setTimeout(function () { btn._label.textContent = base; }, 1500);
        }
        function fallback() { flash(legacy && legacyCopy(value) ? t("已复制", "Copied") : t("复制失败", "Failed")); }
        if (modern) {
          navigator.clipboard.writeText(value).then(function () { flash(t("已复制", "Copied")); }, fallback);
        } else {
          fallback();
        }
      }
    });
    btn.classList.add("btn-copy");
    if (!modern && !legacy) {
      btn.disabled = true;
      btn.setAttribute("title", t("当前环境不允许访问剪贴板", "Clipboard is unavailable here"));
    }
    return btn;
  }

  function pill(kind, text) {
    return h("span", { class: "pill pill-" + (kind || "muted"), text: text == null ? "" : String(text) });
  }

  function chip(text, opt) {
    opt = opt || {};
    var el = h("span", { class: "chip" + (opt.mono ? " chip-mono" : ""), title: opt.title || null });
    if (opt.icon) el.appendChild(h("span", { class: "chip-icon" }, icon(opt.icon)));
    el.appendChild(document.createTextNode(text == null ? "" : String(text)));
    return el;
  }

  function metrics(items) {
    var list = Array.isArray(items) ? items.filter(Boolean) : [];
    var box = h("div", { class: "metrics" });
    list.forEach(function (item) {
      var cell = h("div", { class: "metric" + (item.kind ? " metric-" + item.kind : "") });
      if (item.hint) cell.setAttribute("title", String(item.hint));
      var value = item.value;
      var empty = value == null || value === "";
      var vNode = h("div", { class: "metric-value" });
      if (!empty && value.nodeType) vNode.appendChild(value);
      else vNode.textContent = empty ? "—" : String(value);
      cell.appendChild(vNode);
      cell.appendChild(h("div", { class: "metric-label", text: item.label == null ? "" : String(item.label) }));
      box.appendChild(cell);
    });
    return box;
  }

  function kv(rows) {
    var list = Array.isArray(rows) ? rows.filter(Boolean) : [];
    var dl = h("dl", { class: "kv" });
    list.forEach(function (row) {
      var value = row.value;
      if (value == null || value === "") return;   // 空值直接省行，比显示一堆「—」干净
      dl.appendChild(h("dt", { class: "kv-key", text: row.label == null ? "" : String(row.label) }));
      var dd = h("dd", { class: "kv-value" + (row.kind ? " kv-" + row.kind : "") });
      if (row.mono) dd.setAttribute("data-mono", "1");
      if (value.nodeType) dd.appendChild(value);
      else dd.appendChild(document.createTextNode(String(value)));
      if (row.copy) dd.appendChild(copy(value.nodeType ? value.textContent : String(value), t("复制", "Copy")));
      dl.appendChild(dd);
    });
    return dl;
  }

  function note(text, kind) {
    return h("div", { class: "note note-" + (kind || "info"), text: text == null ? "" : String(text) });
  }

  function empty(text) {
    return h("div", { class: "empty", text: text == null ? t("暂无内容", "Nothing here") : String(text) });
  }

  function group(title, meta) {
    var body = h("div", { class: "group-body" });
    var sec = h("section", { class: "group" },
      h("div", { class: "group-head" },
        h("span", { class: "group-title", text: title == null ? "" : String(title) }),
        h("span", { class: "group-meta", text: meta == null ? "" : String(meta) })),
      body);
    sec.body = body;
    return sec;
  }

  function stack() {
    var box = h("div", { class: "stack" });
    for (var i = 0; i < arguments.length; i++) append(box, arguments[i]);
    return box;
  }

  function row() {
    var box = h("div", { class: "row" });
    for (var i = 0; i < arguments.length; i++) append(box, arguments[i]);
    return box;
  }

  function skeleton(count) {
    var n = typeof count === "number" && count > 0 ? Math.min(Math.floor(count), 12) : 3;
    var box = h("div", { class: "skeleton" });
    for (var i = 0; i < n; i++) box.appendChild(h("div", { class: "skeleton-line" }));
    return box;
  }

  function progressDots() {
    return h("span", { class: "dots", "aria-hidden": "true" },
      h("span", { class: "dot" }), h("span", { class: "dot" }), h("span", { class: "dot" }));
  }

  function path(text) {
    var s = text == null ? "" : String(text);
    return h("span", { class: "path", title: s, text: s.length > 46 ? short(s, 26, 18) : s });
  }

  function code(text) {
    return h("code", { class: "code", text: text == null ? "" : String(text) });
  }

  function json(value) {
    var text;
    try { text = JSON.stringify(value, null, 2); } catch (err) { text = String(value); }
    if (text == null) text = String(value);
    var all = text.split("\n");
    var box = h("div", { class: "jsonbox", tabindex: "0", role: "region", "aria-label": t("JSON 数据", "JSON data") });
    var pre = h("pre", { class: "jsonbox-pre" });
    box.appendChild(pre);
    if (all.length <= JSON_LINES) {
      pre.textContent = text;
      return box;
    }
    pre.textContent = all.slice(0, JSON_LINES).join("\n");
    var more = button({
      label: t("展开全部（" + all.length + " 行）", "Show all " + all.length + " lines"),
      onClick: function () { pre.textContent = text; more.remove(); scheduleReport(); }
    });
    box.appendChild(row(more));
    return box;
  }

  function terminal(o) {
    o = o || {};
    var raw = o.text == null ? "" : String(o.text);
    var cut = raw.length > TEXT_LIMIT;
    if (cut) raw = raw.slice(0, TEXT_LIMIT);
    var all = lines(raw);
    if (!all.length) return empty(o.empty || t("没有输出", "No output"));

    var start = typeof o.startLine === "number" && o.startLine > 0 ? Math.floor(o.startLine) : 1;
    var fold = typeof o.collapsedLines === "number" && o.collapsedLines > 0 ? Math.floor(o.collapsedLines) : TERM_LINES;
    var title = o.title || t("输出", "Output");
    var box = h("div", { class: "term" });
    if (o.variant === "err") box.setAttribute("data-variant", "err");
    if (o.wrap) box.setAttribute("data-wrap", "1");

    var actions = h("div", { class: "term-bar-actions" });
    box.appendChild(h("div", { class: "term-bar" },
      h("span", { class: "term-title", text: title }), actions));
    // 终端行默认不换行，超长命令的后半截只能靠横向滚动看到；
    // 不给滚动容器焦点，纯键盘用户就永远读不到那部分内容。
    var body = h("div", { class: "term-body", tabindex: "0", role: "region", "aria-label": title });
    box.appendChild(body);

    function line(text, index, dim) {
      var el = h("div", { class: "term-line" });
      el.appendChild(h("span", { class: "term-ln", text: index === null ? "" : String(index) }));
      var tx = h("span", { class: "term-tx", text: text });
      if (dim) tx.style.setProperty("color", "var(--term-dim)");
      el.appendChild(tx);
      return el;
    }

    var shown = 0;
    var restLine = null;
    var cutLine = cut
      ? line(t("… 输出过长，已截断至 ", "… truncated at ") + num(TEXT_LIMIT) + t(" 字符", " chars"), null, true)
      : null;
    var toggle = null;

    function syncToggle() {
      if (!toggle) return;
      var left = all.length - shown;
      toggle.setAttribute("aria-expanded", shown > fold ? "true" : "false");
      toggle._label.textContent = left === 0
        ? t("收起", "Collapse")
        : (left <= TERM_PAGE
          ? t("展开全部（" + all.length + " 行）", "Show all " + all.length + " lines")
          : t("再展开 " + TERM_PAGE + " 行（还有 " + num(left) + " 行）",
            "Show " + TERM_PAGE + " more of " + num(left) + " lines"));
    }

    // 按页追加而不是一次铺完：TEXT_LIMIT 允许的输出可达上万行、几万个节点，
    // 整块塞进 DOM 会连带触发一次全量布局测量，肉眼可见地卡住。
    function paintTo(limit) {
      if (restLine) { restLine.remove(); restLine = null; }
      if (cutLine) cutLine.remove();
      var end = Math.min(all.length, limit);
      for (var i = shown; i < end; i++) body.appendChild(line(all[i], start + i, false));
      shown = end;
      if (shown < all.length) {
        restLine = line(t("… 还有 ", "… ") + num(all.length - shown) + t(" 行", " more lines"), null, true);
        body.appendChild(restLine);
      }
      if (cutLine) body.appendChild(cutLine);
      syncToggle();
    }

    function collapse() {
      while (body.firstChild) body.removeChild(body.firstChild);
      restLine = null;
      shown = 0;
      paintTo(fold);
    }

    if (all.length > fold) {
      toggle = button({
        label: "",
        onClick: function () {
          if (shown >= all.length) collapse();
          else paintTo(shown + Math.max(TERM_PAGE, fold));
          scheduleReport();
        }
      });
      actions.appendChild(toggle);
    }
    actions.appendChild(copy(raw, t("复制", "Copy")));
    paintTo(fold);
    return box;
  }

  function diffKind(text) {
    if (text.indexOf("@@") === 0) return "hunk";
    // 三字符前缀必须先判，否则 --- a/file 会被当成删除行
    if (text.indexOf("+++") === 0 || text.indexOf("---") === 0 ||
      text.indexOf("diff ") === 0 || text.indexOf("index ") === 0 ||
      text.indexOf("new file") === 0 || text.indexOf("deleted file") === 0) return "meta";
    if (text.charAt(0) === "+") return "add";
    if (text.charAt(0) === "-") return "del";
    return "ctx";
  }

  function diff(text) {
    var all = lines(text);
    if (!all.length) return empty(t("没有差异", "No diff"));
    var box = h("div", { class: "diff", tabindex: "0", role: "region", "aria-label": t("差异", "Diff") });
    var shown = 0;
    var more = null;
    function paint(limit) {
      if (more) { more.remove(); more = null; }
      var end = Math.min(all.length, limit);
      for (var i = shown; i < end; i++) {
        var raw = all[i];
        var kind = diffKind(raw);
        var sign = (kind === "add" || kind === "del") ? raw.charAt(0) : "";
        box.appendChild(h("div", { class: "diff-line diff-" + kind },
          h("span", { class: "diff-sign", text: sign }),
          h("span", { class: "diff-tx", text: sign ? raw.slice(1) : raw })));
      }
      shown = end;
      if (shown < all.length) {
        more = row(button({
          label: t("展开剩余 " + (all.length - shown) + " 行", "Show " + (all.length - shown) + " more lines"),
          onClick: function () { paint(all.length); scheduleReport(); }
        }));
        box.appendChild(more);
      }
    }
    paint(DIFF_LINES);
    return box;
  }

  function bar(o) {
    o = o || {};
    var value = numeric(o.pct);
    var kind = o.kind || (value === null ? null : (value >= 90 ? "danger" : (value >= 75 ? "warn" : null)));
    var box = h("div", { class: "bar" });
    if (kind) box.setAttribute("data-kind", kind);
    box.appendChild(h("div", { class: "bar-head" },
      h("span", { class: "bar-label", text: o.label == null ? "" : String(o.label) }),
      h("span", { class: "bar-detail", text: o.detail == null ? (value === null ? "—" : pct(value)) : String(o.detail) })));
    if (value === null) {
      box.appendChild(h("div", { class: "bar-unknown", title: t("指标不可用", "Metric unavailable") }));
      return box;
    }
    var fill = h("div", { class: "bar-fill" });
    fill.style.setProperty("width", Math.max(0, Math.min(100, value)) + "%");
    box.appendChild(h("div", { class: "bar-track" }, fill));
    return box;
  }

  function table(o) {
    o = o || {};
    var columns = Array.isArray(o.columns) ? o.columns.filter(Boolean) : [];
    var rows = Array.isArray(o.rows) ? o.rows.filter(function (r) { return r != null; }) : [];
    if (!columns.length || !rows.length) return empty(o.empty || t("没有数据", "No rows"));
    var step = typeof o.maxRows === "number" && o.maxRows > 0 ? Math.floor(o.maxRows) : TABLE_ROWS;

    var head = h("tr");
    columns.forEach(function (col) {
      var th = h("th", { class: "th", text: col.label == null ? "" : String(col.label) });
      if (col.align) th.setAttribute("data-align", col.align);
      if (col.secondary) th.setAttribute("data-secondary", "1");
      head.appendChild(th);
    });
    var body = h("tbody");
    var wrap = h("div", { class: "table-wrap", tabindex: "0", role: "region", "aria-label": t("表格", "Table") },
      h("table", { class: "table" }, h("thead", null, head), body));

    function cell(col, item) {
      var td = h("td", { class: "td" });
      if (col.align) td.setAttribute("data-align", col.align);
      if (col.mono) td.setAttribute("data-mono", "1");
      if (col.secondary) td.setAttribute("data-secondary", "1");
      var value;
      try {
        value = typeof col.render === "function" ? col.render(item) : (item ? item[col.key] : null);
      } catch (err) {
        value = null;
      }
      if (value == null || value === "") td.textContent = "—";
      else if (value.nodeType) td.appendChild(value);
      else {
        var text = String(value);
        td.textContent = text;
        // 单元格默认单行省略，长值就靠 title 兜底，鼠标悬停仍能看到完整内容
        if (text.length > 16) td.setAttribute("title", text);
      }
      return td;
    }

    var shown = 0;
    var moreRow = null;
    function paint() {
      if (moreRow) { moreRow.remove(); moreRow = null; }
      var end = Math.min(rows.length, shown + step);
      for (var i = shown; i < end; i++) {
        var item = rows[i];
        var tr = h("tr", { class: "tr" });
        columns.forEach(function (col) { tr.appendChild(cell(col, item)); });
        if (typeof o.onRow === "function") {
          tr.setAttribute("data-clickable", "1");
          tr.setAttribute("tabindex", "0");
          tr.setAttribute("role", "button");
          (function (bound) {
            function fire() { try { o.onRow(bound); } catch (err) { reportViewError(err); } }
            tr.addEventListener("click", fire);
            tr.addEventListener("keydown", function (event) {
              if (event.key === "Enter" || event.key === " ") { event.preventDefault(); fire(); }
            });
          })(item);
        }
        body.appendChild(tr);
      }
      shown = end;
      if (shown < rows.length) {
        moreRow = h("tr", { class: "tr tr-more" });
        var td = h("td", { class: "td", colspan: String(columns.length) });
        td.appendChild(button({
          label: t("显示更多（剩余 " + (rows.length - shown) + " 行）", "Show more (" + (rows.length - shown) + " left)"),
          onClick: function () { paint(); scheduleReport(); }
        }));
        moreRow.appendChild(td);
        body.appendChild(moreRow);
      }
    }
    paint();
    return wrap;
  }

  function tabs(items) {
    var list = Array.isArray(items) ? items.filter(function (item) { return item && item.node; }) : [];
    if (!list.length) return empty(t("没有可展示的内容", "Nothing to show"));
    if (list.length === 1) return list[0].node;
    var bar2 = h("div", { class: "tab-bar", role: "tablist" });
    var panel = h("div", { class: "tab-panel" });
    var buttons = [];
    function select(index) {
      buttons.forEach(function (btn, i) { btn.setAttribute("aria-selected", i === index ? "true" : "false"); });
      while (panel.firstChild) panel.removeChild(panel.firstChild);
      panel.appendChild(list[index].node);
      scheduleReport();
    }
    list.forEach(function (item, index) {
      var btn = h("button", { class: "tab", type: "button", role: "tab", "aria-selected": "false" },
        h("span", { text: item.label == null ? "" : String(item.label) }),
        item.badge == null || item.badge === "" ? null : pill("muted", item.badge));
      btn.addEventListener("click", function (event) { event.stopPropagation(); select(index); });
      buttons.push(btn);
      bar2.appendChild(btn);
    });
    var box = h("div", { class: "tabs" }, bar2, panel);
    select(0);
    return box;
  }

  function card(o) {
    o = o || {};
    var sec = h("section", { class: "card" });
    var head = h("header", { class: "card-head" });
    head.appendChild(h("span", { class: "card-kicker", text: o.kicker == null ? "" : String(o.kicker) }));
    var heading = h("div", { class: "card-heading" },
      h("span", { class: "card-title", text: o.title == null ? cardTitle() : String(o.title) }));
    if (o.subtitle) heading.appendChild(h("span", { class: "card-sub", text: String(o.subtitle) }));
    head.appendChild(heading);

    var chips = h("div", { class: "card-chips" });
    (Array.isArray(o.chips) ? o.chips : []).forEach(function (item) {
      if (item == null || item === false) return;
      chips.appendChild(item.nodeType ? item : chip(item));
    });
    var status = h("div", { class: "card-status" });
    append(status, o.status);
    var actions = h("div", { class: "card-actions" });
    append(actions, o.actions);
    var bodyId = "onessh-card-" + (++uid);
    var caret = h("button", {
      class: "card-caret", type: "button",
      "aria-label": t("折叠或展开卡片", "Collapse or expand card"),
      "aria-controls": bodyId, "aria-expanded": "true"
    }, icon("chevron"));
    head.appendChild(chips);
    head.appendChild(status);
    head.appendChild(actions);
    head.appendChild(caret);

    var body = h("div", { class: "card-body", id: bodyId });
    append(body, o.body);
    sec.appendChild(head);
    sec.appendChild(body);

    if (o.collapsible === false) {
      sec.setAttribute("data-collapsible", "false");
      caret.remove();   // 不能折叠的卡片不该留一个按下去什么都不发生的按钮
      return sec;
    }
    // 折叠开关就是 caret 这个真按钮：头部里还塞着复制、返回、全屏等控件，
    // 给头部套 role=button 会把它们全拼进一个巨型可访问名，且属于非法嵌套；
    // 键盘走 caret 的原生 Enter/Space（click 冒泡到头部触发 toggle），头部只留鼠标便捷点击。
    function toggle() {
      var collapsed = sec.getAttribute("data-collapsed") === "true";
      sec.setAttribute("data-collapsed", collapsed ? "false" : "true");
      caret.setAttribute("aria-expanded", collapsed ? "true" : "false");
      scheduleReport();
    }
    head.addEventListener("click", toggle);
    // 头部右侧是交互区（复制、返回、全屏…），点它们只应触发自身动作，不该顺带把卡片折起来
    [chips, status, actions].forEach(function (zone) {
      zone.addEventListener("click", function (event) { event.stopPropagation(); });
    });
    return sec;
  }

  var ui = {
    card: card, pill: pill, chip: chip, metrics: metrics, kv: kv, table: table, terminal: terminal,
    tabs: tabs, diff: diff, bar: bar, note: note, empty: empty, group: group, button: button,
    copy: copy, path: path, code: code, json: json, stack: stack, row: row, skeleton: skeleton,
    progressDots: progressDots, icon: icon
  };

  /* ------------------------------------------------------------------ *
   * 桥接：JSON-RPC over postMessage
   * ------------------------------------------------------------------ */

  function hasHost() { return !!window.parent && window.parent !== window; }

  function post(message) {
    if (!hasHost()) return;
    try { window.parent.postMessage(message, "*"); } catch (err) { /* 宿主已销毁时忽略 */ }
  }

  function notify(method, params) {
    post({ jsonrpc: "2.0", method: method, params: params || {} });
  }

  function request(method, params, timeoutMs) {
    return new Promise(function (resolve, reject) {
      if (!hasHost()) { reject(new Error(t("宿主桥接不可用", "Host bridge unavailable"))); return; }
      var id = "onessh-" + (++seq);
      var timer = setTimeout(function () {
        if (!pending[id]) return;
        delete pending[id];
        reject(new Error(t("宿主没有响应", "Host did not respond")));
      }, timeoutMs || CALL_TIMEOUT_MS);
      pending[id] = {
        resolve: function (value) { clearTimeout(timer); resolve(value); },
        reject: function (err) { clearTimeout(timer); reject(err); }
      };
      post({ jsonrpc: "2.0", id: id, method: method, params: params || {} });
    });
  }

  function respond(id, result, error) {
    var message = { jsonrpc: "2.0", id: id };
    if (error) message.error = error; else message.result = result || {};
    post(message);
  }

  function onMessage(event) {
    if (event.source !== window.parent) return;   // 只信任宿主帧，杜绝同页其他 iframe 冒充
    var msg = event.data;
    if (!msg || typeof msg !== "object" || Array.isArray(msg)) return;
    if (msg.jsonrpc !== "2.0") return;             // 宿主页面上的其他脚本也会往下发消息，只认 JSON-RPC 帧
    if (typeof msg.method === "string") {
      if (msg.id !== undefined && msg.id !== null) {
        if (msg.method === "ping" || msg.method === "ui/resource-teardown") respond(msg.id, {});
        else respond(msg.id, null, { code: -32601, message: "Method not found" });
        return;
      }
      if (!state.initialized) { backlog.push(msg); return; }
      dispatch(msg.method, msg.params || {});
      return;
    }
    if (msg.id === undefined || msg.id === null) return;
    var waiter = pending[msg.id];
    if (!waiter) return;
    delete pending[msg.id];
    if (msg.error) waiter.reject(new Error(msg.error.message || t("调用失败", "Call failed")));
    else waiter.resolve(msg.result);
  }

  function dispatch(method, params) {
    if (method === "ui/notifications/tool-input") {
      state.input = params.arguments && typeof params.arguments === "object" ? params.arguments : {};
      state.inputSettled = true;
      awaitResult();
      return;
    }
    if (method === "ui/notifications/tool-input-partial") {
      if (state.inputSettled) return;   // 完整参数已到，流式片段只会让骨架抖动
      state.input = params.arguments && typeof params.arguments === "object" ? params.arguments : {};
      awaitResult();
      return;
    }
    if (method === "ui/notifications/tool-result") { render(params); return; }
    if (method === "ui/notifications/tool-cancelled") { renderCancelled(); return; }
    if (method === "ui/notifications/host-context-changed") {
      applyHostContext(params);
      decorateChrome();
      scheduleReport();
    }
  }

  function handshake() {
    var settled = false;
    var timer = setTimeout(function () {
      if (settled) return;
      settled = true;
      degrade();
    }, INIT_TIMEOUT_MS);
    request("ui/initialize", {
      appInfo: { name: "onessh-" + state.expectedTool, version: "1.0.0" },
      appCapabilities: { availableDisplayModes: APP_DISPLAY_MODES },
      protocolVersion: PROTOCOL_VERSION
    }, INIT_TIMEOUT_MS).then(function (result) {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      result = result && typeof result === "object" ? result : {};
      state.bridged = true;
      state.hostCapabilities = result.hostCapabilities || result.capabilities || {};
      applyHostContext(result.hostContext || {});
      notify("ui/notifications/initialized", {});
      finishInit();
    }, function () {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      degrade();
    });
  }

  // 降级：有些宿主先推数据再握手，甚至根本不实现 ui/initialize。
  // 此时照常接收通知并挂上 window.openai 兼容层，卡片仍然可用，只是没有回调能力。
  function degrade() {
    adoptOpenAI();
    finishInit();
  }

  function finishInit() {
    state.initialized = true;
    state.sizeReady = true;
    var queued = backlog.splice(0, backlog.length);
    queued.forEach(function (msg) { dispatch(msg.method, msg.params || {}); });
    scheduleReport();
  }

  /* ------------------------------------------------------------------ *
   * window.openai 兼容层（旧版 ChatGPT）
   * ------------------------------------------------------------------ */

  function adoptOpenAI() {
    var api = window.openai;
    if (!api) return;
    if (api.toolInput && typeof api.toolInput === "object") {
      state.input = api.toolInput;
      state.inputSettled = true;
    }
    applyGlobals(api);
    lastGlobals = stringify(api.toolOutput);
    if (api.toolOutput != null && !state.result) {
      render({ structuredContent: api.toolOutput, content: [], isError: false });
    }
  }

  function applyGlobals(g) {
    if (!g || typeof g !== "object") return;
    var patch = {};
    if (g.theme) patch.theme = g.theme;
    if (g.displayMode) patch.displayMode = g.displayMode;
    if (g.locale) patch.locale = g.locale;
    if (numeric(g.maxHeight) !== null) patch.containerDimensions = { maxHeight: numeric(g.maxHeight) };
    if (Object.keys(patch).length) applyHostContext(patch);
  }

  function onOpenAIGlobals(event) {
    var g = event && event.detail && event.detail.globals;
    if (!g) return;
    applyGlobals(g);
    // 宿主为任何一次变化（切主题、切显示模式，甚至我们自己请求全屏）都会重放整份 globals，
    // 里面带的还是启动时那份 toolOutput/toolInput。卡片正在展示 refresh/navigate 取回的结果时
    // 采纳它，等于把用户看的内容擦回旧值，而工具名与返回栈还停在导航后的位置。
    var key = stringify(g.toolOutput);
    var fresh = key !== lastGlobals;
    lastGlobals = key;
    if (fresh || !state.localResult) {
      if (g.toolInput && typeof g.toolInput === "object") {
        state.input = g.toolInput;
        state.inputSettled = true;
      }
      if (g.toolOutput != null) render({ structuredContent: g.toolOutput, content: [], isError: false });
    }
    decorateChrome();
    scheduleReport();
  }

  /* ------------------------------------------------------------------ *
   * hostContext 与尺寸
   * ------------------------------------------------------------------ */

  function applyHostContext(patch) {
    if (!patch || typeof patch !== "object") return;
    Object.keys(patch).forEach(function (key) { state.hostContext[key] = patch[key]; });
    var docEl = document.documentElement;

    var theme = patch.theme;
    if (theme && typeof theme === "object") theme = theme.mode || theme.name || theme.theme;
    if (theme === "light" || theme === "dark") docEl.setAttribute("data-theme", theme);

    var styles = patch.styles;
    if (styles && styles.variables && typeof styles.variables === "object") {
      Object.keys(styles.variables).forEach(function (name) {
        // 只接受自定义属性，避免宿主顺手改掉 display 之类的关键样式
        if (name.indexOf("--") !== 0) return;
        var value = styles.variables[name];
        if (value != null) docEl.style.setProperty(name, String(value));
      });
    }
    if (typeof patch.displayMode === "string") {
      state.displayMode = patch.displayMode;
      docEl.setAttribute("data-display-mode", patch.displayMode);
    }
    if (Array.isArray(patch.availableDisplayModes)) state.displayModes = patch.availableDisplayModes;
    if (typeof patch.locale === "string") { state.locale = patch.locale; applyLang(); }
    var dims = patch.containerDimensions;
    if (dims && numeric(dims.maxHeight) !== null) {
      state.maxHeight = numeric(dims.maxHeight);
      docEl.style.setProperty("--host-max-height", state.maxHeight + "px");
    }
  }

  var reportQueued = false;
  function scheduleReport() {
    if (reportQueued) return;
    reportQueued = true;
    requestAnimationFrame(function () { reportQueued = false; reportSize(); });
  }

  function reportSize() {
    if (!state.sizeReady) return;
    var docEl = document.documentElement;
    // 量高度前把 html 撑成 max-content：宿主给的 iframe 高度会让 documentElement 反过来变高，
    // 直接量就会陷入「越报越高」的正反馈。
    var prev = docEl.style.height;
    docEl.style.setProperty("height", "max-content");
    var rect = docEl.getBoundingClientRect();
    if (prev) docEl.style.setProperty("height", prev); else docEl.style.removeProperty("height");
    var height = Math.ceil(rect.height);
    if (!height || Math.abs(height - state.lastHeight) < 2) return;
    state.lastHeight = height;
    var width = Math.ceil(rect.width);
    // 握手失败的宿主也可能认得这条通知，发了不亏；只有它确实不认时才轮到 openai 的旧接口。
    if (hasHost()) notify("ui/notifications/size-changed", width ? { height: height, width: width } : { height: height });
    if (!state.bridged && window.openai && typeof window.openai.notifyIntrinsicHeight === "function") {
      try { window.openai.notifyIntrinsicHeight(height); } catch (err) { /* 旧宿主实现不稳定，忽略 */ }
    }
  }

  function observeSize() {
    if (typeof ResizeObserver === "function") {
      var ro = new ResizeObserver(scheduleReport);
      ro.observe(document.body);
      if (root) ro.observe(root);
      return;
    }
    window.addEventListener("resize", scheduleReport);
  }

  /* ------------------------------------------------------------------ *
   * 回调、导航与显示模式
   * ------------------------------------------------------------------ */

  // 宿主是否宣告了 serverTools。can() 与 callTool() 共用同一判断，
  // 免得出现「按钮说能点、真按下去却发到一个没人应答的通道」。
  function hasServerTools() {
    return !!(state.hostCapabilities && state.hostCapabilities.serverTools);
  }

  function canCallHost() {
    return hasServerTools() ||
      !!(window.openai && typeof window.openai.callTool === "function");
  }

  function can(name) {
    return CALLABLE.indexOf(name) >= 0 && canCallHost();
  }

  function callTool(name, args) {
    if (!can(name)) {
      return Promise.reject(new Error(t("该工具不允许从卡片调用", "This tool cannot be called from the card")));
    }
    // 握手成功不等于宿主接 tools/call：只有它确实宣告了 serverTools 才走标准通道。
    // 从前只看 state.bridged，遇到「新桥握手 + 只有旧版 callTool」的宿主，请求会发进一个
    // 没人应答的方法，刷新与导航全部超时失败，而旁边那条旧接口本来是通的。
    if (state.bridged && hasServerTools()) return request("tools/call", { name: name, arguments: args || {} });
    if (window.openai && typeof window.openai.callTool === "function") {
      // 旧宿主的 callTool 可能永远不 settle，而 refresh/navigate 正卡在 setBusy(true) 上等它：
      // 没有超时就意味着按钮全禁用、状态永远停在 busy，用户毫无恢复手段。与 request() 同一套语义。
      return new Promise(function (resolve, reject) {
        var timer = setTimeout(function () {
          reject(new Error(t("宿主没有响应", "Host did not respond")));
        }, CALL_TIMEOUT_MS);
        function settle(fn) {
          return function (value) { clearTimeout(timer); fn(value); };
        }
        try { Promise.resolve(window.openai.callTool(name, args || {})).then(settle(resolve), settle(reject)); }
        catch (err) { clearTimeout(timer); reject(err); }
      });
    }
    return Promise.reject(new Error(t("宿主未提供工具回调能力", "Host does not expose tool calls")));
  }

  // 不同宿主对 tools/call 的返回包装不一致，统一收敛成 CallToolResult 形状。
  function normalizeResult(raw) {
    if (!raw || typeof raw !== "object") return { content: [], structuredContent: {}, isError: false };
    if (raw.result && typeof raw.result === "object" && (raw.result.content || raw.result.structuredContent)) return raw.result;
    if (raw.content || raw.structuredContent || raw.isError !== undefined) return raw;
    return { content: [], structuredContent: raw, isError: false };
  }

  function isFullscreen() { return state.displayMode === "fullscreen"; }

  function canFullscreen() {
    // 宿主只会授予我们声明过的模式，所以先看自己的声明，再跟宿主给的清单取交集。
    if (APP_DISPLAY_MODES.indexOf("fullscreen") < 0) return false;
    if (Array.isArray(state.displayModes) && state.displayModes.length) {
      return state.displayModes.indexOf("fullscreen") >= 0 || isFullscreen();
    }
    return !!(window.openai && typeof window.openai.requestDisplayMode === "function");
  }

  function setDisplayMode(mode) {
    var legacy = !!(window.openai && typeof window.openai.requestDisplayMode === "function");
    // 与 callTool 同理：宿主从没在 hostContext 里报过 availableDisplayModes，就没有证据说明它接
    // ui/request-display-mode——而 canFullscreen() 正是凭旧版接口的存在才把按钮显示出来的，
    // 这时候把请求发进新桥只会石沉大海，不如走那条已知可用的旧接口。
    if (state.bridged && (state.displayModes.length || !legacy)) {
      request("ui/request-display-mode", { mode: mode }).then(function (result) {
        applyHostContext({ displayMode: (result && result.mode) || mode });
        decorateChrome();
        scheduleReport();
      }, function () { /* 宿主拒绝时保持现状 */ });
      return;
    }
    if (legacy) {
      try { window.openai.requestDisplayMode({ mode: mode }); } catch (err) { /* 忽略 */ }
      applyHostContext({ displayMode: mode });
      decorateChrome();
    }
  }

  function notifyModel(text) {
    if (!text) return;
    // 规范要求 params 用 ContentBlock 数组，宿主会把它并进模型上下文；
    // 发扁平的 {text} 在部分宿主上会被直接丢弃。
    notify("ui/update-model-context", { content: [{ type: "text", text: String(text) }] });
  }

  function setBusy(on) {
    state.busy = on;
    setState(on ? "busy" : (state.isError ? "error" : "ready"));
    if (!root) return;
    var btns = root.querySelectorAll("button");
    for (var i = 0; i < btns.length; i++) {
      var btn = btns[i];
      if (on) {
        if (!btn.disabled) { btn.setAttribute("data-busy-lock", "1"); btn.disabled = true; }
      } else if (btn.getAttribute("data-busy-lock")) {
        btn.removeAttribute("data-busy-lock");
        btn.disabled = false;
      }
    }
  }

  // 回调失败不能把已经渲染好的内容擦掉：在卡片正文顶部插一条提示，其余保持原样。
  function showActionError(err) {
    if (!root) return;
    var host = root.querySelector(".card-body") || root;
    var msg = err && err.message ? err.message : String(err || t("操作失败", "Action failed"));
    host.insertBefore(note(msg, "danger"), host.firstChild);
    scheduleReport();
  }

  function mergeArgs(base, patch) {
    var out = {};
    Object.keys(base || {}).forEach(function (key) { out[key] = base[key]; });
    Object.keys(patch || {}).forEach(function (key) { out[key] = patch[key]; });
    return out;
  }

  function describeArgs(args) {
    var keys = ["host", "path", "command", "pattern", "job_id", "artifact_id", "query", "bank"];
    for (var i = 0; i < keys.length; i++) {
      var value = args ? args[keys[i]] : null;
      if (typeof value === "string" && value) return short(value, 40, 12);
      if (typeof value === "number") return String(value);
    }
    return "";
  }

  // 失败信息由 runtime 直接画在卡片上，因此这里把 promise 收敛为 resolve：
  // 视图多半是「点一下就走」的调用，不该因为没写 catch 就在宿主控制台里堆未处理的 rejection。
  function refresh(patchArgs) {
    var args = mergeArgs(state.input, patchArgs);
    setBusy(true);
    return callTool(state.tool, args).then(function (raw) {
      state.input = args;
      state.lastKey = null;
      state.localResult = true;
      applyResult(normalizeResult(raw));
    }, function (err) {
      setBusy(false);
      showActionError(err);
    });
  }

  function navigate(tool, args, label) {
    setBusy(true);
    return callTool(tool, args || {}).then(function (raw) {
      state.stack.push({ tool: state.tool, input: state.input, result: state.result, label: label || "" });
      state.tool = tool;
      state.input = args || {};
      state.lastKey = null;
      state.localResult = true;
      applyResult(normalizeResult(raw));
      var detail = describeArgs(args);
      notifyModel("用户在 OneSSH 卡片中查看了 " + tool + (detail ? " " + detail : ""));
    }, function (err) {
      setBusy(false);
      showActionError(err);
    });
  }

  function back() {
    if (!state.stack.length) return;
    var prev = state.stack.pop();
    state.tool = prev.tool;
    state.input = prev.input || {};
    state.lastKey = null;   // 回到上一层后，宿主若再推同一份结果应当重新生效
    state.localResult = true;
    applyResult(prev.result || {});
  }

  var ctx = {
    tool: "", data: {}, result: null, input: {}, isError: false,
    can: can,
    call: function (name, args) { return callTool(name, args); },
    refresh: refresh,
    navigate: navigate,
    back: back,
    fullscreen: function () { if (canFullscreen()) setDisplayMode("fullscreen"); },
    isFullscreen: isFullscreen,
    notifyModel: notifyModel,
    t: t
  };

  function syncCtx() {
    ctx.tool = state.tool;
    ctx.data = state.data;
    ctx.result = state.result;
    ctx.input = state.input;
    ctx.isError = state.isError;
  }

  /* ------------------------------------------------------------------ *
   * 渲染管线
   * ------------------------------------------------------------------ */

  function setState(name) {
    if (root) root.setAttribute("data-state", name);
  }

  // 卡片标题取自 shell.html 的 <title>（Go 侧写入的中文标题），比在运行时硬编码一份映射更可靠。
  function cardTitle() {
    var raw = String(document.title || "").split("·");
    var last = raw[raw.length - 1].trim();
    return last || state.expectedTool || "OneSSH";
  }

  function textOf(result) {
    var content = result && Array.isArray(result.content) ? result.content : [];
    var parts = [];
    content.forEach(function (item) {
      if (item && item.type === "text" && typeof item.text === "string") parts.push(item.text);
    });
    return parts.join("\n");
  }

  function mount(node) {
    if (!root) return;
    while (root.firstChild) root.removeChild(root.firstChild);
    if (node) root.appendChild(node);
    decorateChrome();
    requestAnimationFrame(reportSize);
  }

  // 返回与全屏按钮由 runtime 统一注入：视图不该知道自己处在导航栈的哪一层。
  function decorateChrome() {
    if (!root) return;
    var actions = root.querySelector(".card-actions");
    if (!actions) return;
    var old = actions.querySelectorAll("[data-chrome]");
    for (var i = 0; i < old.length; i++) old[i].remove();
    if (state.stack.length) {
      var prev = state.stack[state.stack.length - 1];
      var backBtn = button({
        label: t("返回", "Back"),
        icon: "back",
        title: prev.label ? t("返回：", "Back to ") + prev.label : t("返回上一层", "Back"),
        onClick: back
      });
      backBtn.setAttribute("data-chrome", "back");
      actions.insertBefore(backBtn, actions.firstChild);
    }
    if (canFullscreen()) {
      var full = isFullscreen();
      var fsBtn = button({
        label: full ? t("退出全屏", "Exit") : t("全屏", "Fullscreen"),
        icon: "expand",
        onClick: function () { setDisplayMode(full ? "inline" : "fullscreen"); }
      });
      fsBtn.setAttribute("data-chrome", "fullscreen");
      actions.appendChild(fsBtn);
    }
  }

  // 出错时最该先看清的是「哪台主机、对什么东西」，所以只挑主机与一个短标识做 chip。
  // 命令、路径这类长值放进标题或正文，塞进 chip 只会挤成一条读不出结构的灰条。
  function inputChips(skip) {
    var args = state.input && typeof state.input === "object" ? state.input : {};
    var out = [];
    // host_create 的入参用的是 name 而不是 host，不兜住它出错时整张卡说不出是在给哪台主机出错
    var host = args.host || args.src_host || args.name;
    if (typeof host === "string" && host && host !== skip) out.push(chip(host, { mono: true }));
    if (Array.isArray(args.hosts) && args.hosts.length) {
      out.push(chip(args.hosts.length + t(" 台主机", " hosts")));
    }
    if (typeof args.dst_host === "string" && args.dst_host) out.push(chip(args.dst_host, { mono: true }));
    // 带上字段名：光一个「deploy」「ci」看不出是会话还是 bank，正常态的视图都是带标签的
    var labelled = [
      { key: "job_id", zh: "", en: "" },
      { key: "artifact_id", zh: "", en: "" },
      { key: "session", zh: "会话 ", en: "session " },
      { key: "bank", zh: "bank ", en: "bank " }
    ];
    for (var i = 0; i < labelled.length && out.length < 3; i++) {
      var value = args[labelled[i].key];
      if (typeof value !== "string" || !value || value.length > 32 || value === skip) continue;
      out.push(chip(t(labelled[i].zh, labelled[i].en) + value, { mono: true }));
    }
    // 出错时也该说清「在读哪一段」，否则 output_read 的错误卡只剩一个 artifact id
    var offset = args.offset;
    if (out.length < 3 && typeof offset === "number" && offset > 1) {
      out.push(chip(t("第 " + offset + " 行起", "from line " + offset)));
    }
    return out;
  }

  // 错误卡的标题：优先用这次调用真正在操作的对象，实在没有再退回卡片自己的中文名。
  // pattern/query 必须排在 path/src_path 之前：grep、find 的成功卡标题就是 pattern，
  // 让 path 抢先会使同一次调用在成功态叫「ssl_protocols」、失败态叫「/etc/nginx」，
  // 换了主语用户就对不上是哪次搜索。其余带 path 的工具（file_read/file_write/file_list…）
  // 都没有 pattern/query 字段，这个顺序对它们没有任何影响。
  function inputTitle() {
    var args = state.input && typeof state.input === "object" ? state.input : {};
    var keys = ["command", "pattern", "query", "path", "src_path", "job_id", "artifact_id", "name"];
    for (var i = 0; i < keys.length; i++) {
      var value = args[keys[i]];
      if (typeof value === "string" && value.trim()) return value.trim();
    }
    return "";
  }

  // 纯数字 id（memory_update / memory_forget）单独走副标题，不参与上面的主标题竞争：
  // 成功卡是「更新记忆 · #184」，失败卡若反过来写成「#184 · 更新记忆」，
  // 在对话流里扫过去只剩一个裸号码，读不出出错的到底是更新还是删除。
  function inputId() {
    var args = state.input && typeof state.input === "object" ? state.input : {};
    if (typeof args.id !== "number" || !isFinite(args.id)) return "";
    return "#" + args.id;
  }

  // 宿主可能在结果之后重放参数（重新挂载 iframe 时尤其常见），
  // 那时只把状态切回 running，绝不能用骨架盖掉用户正在读的内容。
  function awaitResult() {
    if (state.result) { setState("running"); return; }
    renderSkeleton();
  }

  function renderSkeleton() {
    setState("running");
    mount(card({
      kicker: kickerFor(state.expectedTool),
      title: cardTitle(),
      subtitle: t("正在执行…", "Running…"),
      chips: inputChips(),
      status: pill("live", t("执行中", "Running")),
      collapsible: false,
      body: skeleton(4)
    }));
  }

  function renderCancelled() {
    setState("ready");
    mount(card({
      kicker: kickerFor(state.expectedTool),
      title: cardTitle(),
      chips: inputChips(),
      status: pill("warn", t("已取消", "Cancelled")),
      collapsible: false,
      body: note(t("工具调用已取消，没有产生结果。", "The tool call was cancelled; there is no result."), "warn")
    }));
  }

  /* 眉标：视图自己写的是「EXEC」「ENV」这类短标签，runtime 兜底时若直接甩出 state.tool，
     就会出现「HOST_RESET_FINGERPRINT」这种比标题还宽的长串，同一张卡在正常态和出错态
     看起来像换了一张。这张表让三种状态下的眉标保持一致，表里没有的按下划线取第一段。 */
  var KICKERS = {
    hosts_list: "HOSTS", hosts_manage_list: "HOSTS", host_create: "HOST", host_update: "HOST",
    host_test: "TEST", host_reset_fingerprint: "HOST", host_delete: "HOST",
    exec: "EXEC", session_env: "ENV", output_read: "OUTPUT", exec_many: "FANOUT",
    job_start: "JOB", job_list: "JOBS", job_status: "JOB", job_logs: "LOGS", job_kill: "JOB",
    file_read: "READ", file_write: "WRITE", file_edit: "EDIT", file_list: "LIST", file_transfer: "COPY",
    grep: "GREP", find: "FIND",
    memory_remember: "MEMORY", memory_recall: "RECALL", memory_list: "MEMORY", memory_update: "MEMORY",
    memory_forget: "MEMORY", memory_stats: "MEMORY", memory_sleep: "MEMORY",
    host_status: "STATUS", image_view: "IMAGE"
  };

  function kickerFor(tool) {
    var name = String(tool || state.expectedTool || "");
    return KICKERS[name] || name.split("_")[0];
  }

  function errorCard() {
    var text = textOf(state.result).trim();
    var body;
    // 绝大多数工具错误是一两句话的中文说明，用深色终端块去装反而显得比错误本身还重；
    // 只有真正成段的输出（多行日志、栈）才值得终端块的行号与折叠。
    var lineCount = text ? text.split("\n").length : 0;
    if (!text) body = note(t("工具返回了错误但没有说明。", "The tool failed without a message."), "danger");
    else if (lineCount <= 4 && text.length <= 400) body = note(text, "danger");
    else body = terminal({ text: text, title: t("错误输出", "Error output"), variant: "err", collapsedLines: 16, wrap: true });
    var subject = inputTitle();
    return card({
      kicker: kickerFor(state.tool),
      title: subject || cardTitle(),
      // 没有主语时标题让给卡片名，#id 退到副标题，与成功卡的「更新记忆 · #184」同序。
      subtitle: subject ? cardTitle() : inputId(),
      // 标题已经写了这个值时不要在 chip 里再写一遍：output_read 出错时头部会出现两个一样的 artifact id
      chips: inputChips(subject),
      status: pill("danger", t("调用失败", "Failed")),
      body: body
    });
  }

  // 未注册视图的兜底：把文本与结构化内容如实摊开，至少不让用户面对空白。
  function genericCard() {
    var body = [];
    var text = textOf(state.result).trim();
    if (text) body.push(terminal({ text: text, title: t("文本结果", "Text result"), collapsedLines: 16, wrap: true }));
    if (state.data && Object.keys(state.data).length) body.push(json(state.data));
    if (!body.length) body.push(empty(t("工具没有返回可展示的内容", "The tool returned nothing to display")));
    return card({
      kicker: kickerFor(state.tool),
      title: cardTitle(),
      subtitle: t("通用视图", "Generic view"),
      chips: inputChips(),
      body: stack.apply(null, body)
    });
  }

  function reportViewError(err) {
    showActionError(err);
  }

  function fallbackCard(err) {
    return card({
      kicker: kickerFor(state.tool),
      title: cardTitle(),
      status: pill("danger", t("渲染异常", "Render error")),
      body: stack(
        note(t("卡片渲染出错，已回退到原始数据：", "The card failed to render; showing raw data:") +
          (err && err.message ? " " + err.message : ""), "danger"),
        json(state.data))
    });
  }

  function buildNode() {
    var render = views[state.tool];
    try {
      var node = render ? render(state.data, ctx) : genericCard();
      if (!node || !node.nodeType) throw new Error(t("视图没有返回节点", "View returned no node"));
      return node;
    } catch (err) {
      return fallbackCard(err);
    }
  }

  function applyResult(result) {
    state.result = result && typeof result === "object" ? result : {};
    var structured = state.result.structuredContent;
    state.data = structured && typeof structured === "object" ? structured : {};
    state.isError = state.result.isError === true;
    state.busy = false;
    syncCtx();
    if (state.isError) {
      setState("error");
      mount(errorCard());
      return;
    }
    setState("ready");
    mount(buildNode());
  }

  function stringify(value) {
    try { return JSON.stringify(value); } catch (err) { return null; }
  }

  function render(result) {
    /* 去重键要同时含参数：宿主复用同一个 iframe 发起第二次调用时，两次结果完全可能
       字节级相同（连着列两个空目录都是 {"entries":[]}），只比结果就会跳过重建，
       卡片仍挂着上一次的路径/主机，看起来却像是这次调用的结果。

       但参数要取「宿主这次调用的那份」，也就是导航栈底那份原始参数：用户点进
       下一层之后 state.input 指向的是被导航工具的参数，此时宿主重放同一份结果
       会算出另一个键，白白重建一次卡片、丢掉折叠态还抖一下高度。 */
    var base = state.stack.length ? state.stack[0] : null;
    var baseInput = base && base.input && typeof base.input === "object" ? base.input : state.input;
    var key = stringify([baseInput, result]);
    // 宿主在同一次调用里可能重复推送同一份结果；重渲染会导致折叠态丢失和高度抖动。
    // 但 tool-input 会先把状态切成 running，直接早退就再没人把它切回去，
    // 「参数 → 内容相同的结果」这条常见重放序列会让卡片永远停在执行中。
    if (key !== null && key === state.lastKey) {
      state.localResult = false;
      setState(state.isError ? "error" : "ready");
      return;
    }
    state.lastKey = key;
    // 宿主推来的结果永远属于 expectedTool（契约 §2.3），而 navigate 之后 state.tool 指向的是被导航的工具。
    // 不复位就会拿被导航工具的视图去渲染原工具的数据，轻则内容错乱，重则掉进兜底卡片。
    if (base) {
      state.input = baseInput;
      state.stack.length = 0;
    }
    state.tool = state.expectedTool;
    state.localResult = false;
    applyResult(result);
  }

  /* ------------------------------------------------------------------ *
   * 对外接口
   * ------------------------------------------------------------------ */

  function view(tool, renderFn) {
    if (typeof tool === "string" && typeof renderFn === "function") views[tool] = renderFn;
  }

  function boot(tool) {
    state.expectedTool = typeof tool === "string" ? tool : "";
    state.tool = state.expectedTool;
    root = document.getElementById("root");
    syncCtx();
    applyLang();   // shell.html 写死的 lang 只在宿主没报 locale 时才碰巧是对的，这里统一由运行时负责
    // 即便 ui 桥可用也先记下 openai globals 的初始快照：宿主会为切主题、切显示模式
    // 这类变化重放整份 globals，认不出「其实没换」就会把本地结果擦回启动时那一份。
    if (window.openai) lastGlobals = stringify(window.openai.toolOutput);
    window.addEventListener("message", onMessage);
    window.addEventListener("openai:set_globals", onOpenAIGlobals);
    observeSize();
    if (!hasHost()) {
      // 直接打开（预览画廊之外）时没有可握手的对端，保持等待态并继续接收 postMessage。
      state.initialized = true;
      state.sizeReady = true;
      adoptOpenAI();
      scheduleReport();
      return;
    }
    handshake();
  }

  window.OneSSH = { boot: boot, view: view, ui: ui, fmt: fmt, t: t, h: h };
})();
