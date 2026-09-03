;(function () {
  "use strict";

  /* 搜索组卡片。grep 与 find 都在回答「东西在哪」，但答案的粒度完全不同：
     grep 给的是「文件 + 行号 + 那一行长什么样」，find 给的只是一串路径。
     所以 grep 按文件分组、手工搭终端块 —— ui.terminal 的行号是从 startLine 连续递增的，
     而 grep 的行号天生带断层，套上去等于给用户一份错的行号；
     find 则整份交给 ui.terminal，它自带的折叠与复制刚好够用。 */

  var ui = OneSSH.ui, fmt = OneSSH.fmt, h = OneSSH.h, t = OneSSH.t;

  var GROUP_LINES = 40;   // 单个文件在卡片里最多画的行数：再多就该去读原文件，而不是在卡片里翻
  var GROUP_FILES = 25;   // 首屏最多画的文件数，其余按需展开，避免一次塞进上百个终端块
  var PATH_MAX = 58;      // 超过这个长度的路径改用中间省略

  function textOf(value) { return typeof value === "string" ? value : ""; }

  // 行号 0、匹配数 0 都是合法值，所以一律判「是不是有限数值」，绝不用 falsy 顺手吃掉 0。
  function numOf(value) { return typeof value === "number" && isFinite(value) ? value : null; }

  function listOf(value) { return Array.isArray(value) ? value : []; }

  function inputOf(ctx) {
    var input = ctx && ctx.input;
    return input && typeof input === "object" ? input : {};
  }

  function has(obj, key) { return Object.prototype.hasOwnProperty.call(obj, key); }

  /* ------------------------------------------------------------------ *
   * 卡片头部的共用零件
   * ------------------------------------------------------------------ */

  // 引擎决定了这次搜索的完整度和速度：rg/fd 是全功能的，helper 与 sftp 是降级路径。
  // 用户追问「为什么这次特别慢」「为什么正则没生效」时，这条线索是唯一的答案。
  var ENGINES = {
    rg: { zh: "原生 rg", en: "native rg", tipZh: "远端装有 ripgrep，正则与忽略规则都完整", tipEn: "ripgrep is installed on the host" },
    fd: { zh: "原生 fd", en: "native fd", tipZh: "远端装有 fd，遍历由远端完成", tipEn: "fd is installed on the host" },
    grep: { zh: "原生 grep", en: "native grep", tipZh: "回退到系统自带 grep，正则方言可能不同", tipEn: "Fell back to the system grep" },
    find: { zh: "原生 find", en: "native find", tipZh: "回退到系统自带 find", tipEn: "Fell back to the system find" },
    helper: { zh: "临时 helper", en: "temp helper", tipZh: "远端没有可用的搜索工具，OneSSH 上传了临时程序来执行", tipEn: "No native search tool on the host; OneSSH uploaded a temporary helper" },
    sftp: { zh: "SFTP 回退", en: "SFTP fallback", tipZh: "只能通过 SFTP 逐个文件读取，速度慢且结果可能不完整", tipEn: "Walked the tree over SFTP; slow and possibly incomplete" }
  };

  function engineChip(engine) {
    var key = textOf(engine);
    if (!key) return null;
    if (!has(ENGINES, key)) {
      // 未知引擎原样透出：折成「其他」只会让排查的人少一条线索
      return ui.chip(key, { mono: true, title: t("搜索引擎：", "Engine: ") + key });
    }
    var meta = ENGINES[key];
    return ui.chip(t(meta.zh, meta.en), { title: t(meta.tipZh, meta.tipEn) });
  }

  function hostChip(input) {
    var host = textOf(input.host);
    if (!host) return null;
    // 不挂 ↗ 图标：这只是一条「这次搜的是哪台机器」的标签，不会点开任何东西。
    // ↗（icon-link）在 jobs.js 里是 ctx.navigate 按钮的标记，混用会让图标语义失效。
    return ui.chip(host, { mono: true, title: t("主机 ", "Host ") + host });
  }

  // 截断意味着「你看到的不是全部」，这是会影响判断的事实，必须写成正文里的一条提示，
  // 只靠 pill 上那三个字很容易被读者跳过。
  function truncatedNote(input, zh, en) {
    var limit = numOf(input.limit);
    var tail = limit === null ? "" : t("（当前 limit=" + limit + "）", " (limit=" + limit + ")");
    return ui.note(t("结果已达到上限被截断，" + zh + tail + "。",
      "Results hit the limit and were truncated; " + en + tail + "."), "warn");
  }

  // grep / find 不在 runtime 的只读回调白名单里，所以这个按钮当下不会出现；
  // 留着是因为重跑同一次搜索本身是幂等只读操作，白名单一旦放开就能直接用。
  function rerunAction(ctx, tool) {
    if (!ctx || typeof ctx.can !== "function" || !ctx.can(tool)) return null;
    return ui.button({
      label: t("重新搜索", "Search again"),
      icon: "refresh",
      title: t("用同样的参数再跑一次", "Run the same search again"),
      onClick: function () { ctx.refresh({}); }
    });
  }

  /* ------------------------------------------------------------------ *
   * grep —— 按文件分组的匹配行
   * ------------------------------------------------------------------ */

  // 只有显式 false 才算上下文行。不带 context 的调用可能根本不写这个字段，
  // 那时每一行本身就是匹配，默认成「非匹配」会把整份结果画成一片灰。
  function isMatch(item) { return item.match !== false; }

  function groupByPath(rows) {
    var order = [], index = {};
    rows.forEach(function (item) {
      var path = textOf(item.path);
      var key = "p:" + path;   // 加前缀，免得 __proto__ 之类的路径名撞上原型链
      var group = has(index, key) ? index[key] : null;
      if (!group) {
        group = { path: path, rows: [], matches: 0 };
        index[key] = group;
        order.push(group);     // 保持结果原有顺序：引擎给的顺序通常已按相关性/目录排好
      }
      group.rows.push(item);
      if (isMatch(item)) group.matches++;
    });
    return order;
  }

  function countMatches(rows) {
    var n = 0;
    rows.forEach(function (item) { if (isMatch(item)) n++; });
    return n;
  }

  function groupMeta(group) {
    var meta = fmt.num(group.matches) + t(" 处匹配", " matches");
    // 有上下文行时补一句总行数，否则「3 处匹配」配上 9 行内容会让人以为哪里错了
    if (group.rows.length > group.matches) {
      meta += " · " + fmt.num(group.rows.length) + t(" 行", " lines");
    }
    return meta;
  }

  function termLine(no, text, dim) {
    var tx = h("span", { class: "term-tx", text: text });
    if (dim) tx.style.setProperty("color", "var(--term-dim)");
    return h("div", { class: "term-line" }, h("span", { class: "term-ln", text: no }), tx);
  }

  function paintRows(body, rows) {
    var end = Math.min(rows.length, GROUP_LINES);
    var prev = null;
    for (var i = 0; i < end; i++) {
      var item = rows[i] || {};
      var no = numOf(item.line);
      // 行号出现断层说明中间整段被跳过了，补一条分隔行，
      // 否则两段毫不相干的代码会被读成连续的一片。
      if (no !== null && prev !== null && no > prev + 1) body.appendChild(termLine("", "⋯", true));
      body.appendChild(termLine(no === null ? "" : String(no), textOf(item.text), item.match === false));
      prev = no;
    }
    if (rows.length > end) {
      body.appendChild(termLine("", t("… 还有 ", "… ") + fmt.num(rows.length - end) + t(" 行", " more lines"), true));
    }
  }

  // 复制时保留行号：同一个文件里的多段匹配未必相邻，去掉行号会把不相干的代码粘成一片。
  function copyText(group) {
    return group.rows.map(function (item) {
      var no = numOf(item.line);
      var text = textOf(item.text);
      return no === null ? text : no + ":" + text;
    }).join("\n");
  }

  /* 一个文件 = 一个终端块，路径由终端块自己的标题栏认领。
     早先是 ui.group 的路径行（约 30px）上面再叠一条只写着「匹配行」的 term-bar（28px）：
     58px 的框架配 70px 正文，而第二层只是在复述「下面是匹配的行」，三个分组白占 84px。
     旧注释说 .term-title 会把路径拉成「/ETC/NGINX」，这条已经不成立——style.css 的
     .term-title 明确写了不做 text-transform，files.js 也早就把文件名放进这个位置。 */
  function fileGroup(group) {
    var full = group.path || t("未知路径", "Unknown path");
    // CSS 的 text-overflow 只从尾部截，而尾部（文件名）恰恰最能区分文件，所以超长时改中间省略；
    // 完整值留在原生 tooltip 里，需要时鼠标一停就能看到。
    var title = h("span", {
      class: "term-title",
      text: full.length > PATH_MAX ? fmt.short(full, 30, 24) : full,
      title: full
    });
    // 匹配数/行数跟着路径走。复用 group-meta 的弱化字号与颜色，让它读起来是注解，
    // 而不是「复制」旁边的第二个按钮；term-bar-actions 的 2px gap 是给按钮排的，这里手动拉开。
    var meta = h("span", {
      class: "group-meta",
      text: groupMeta(group),
      style: { "margin-right": "6px" }
    });
    var body = h("div", { class: "term-body" });
    paintRows(body, group.rows);
    return h("div", { class: "term" },
      h("div", { class: "term-bar" }, title,
        h("div", { class: "term-bar-actions" }, meta, ui.copy(copyText(group), t("复制", "Copy")))),
      body);
  }

  function fileGroups(groups) {
    var box = ui.stack();
    var shown = 0;
    var more = null;
    function paint(limit) {
      if (more) { more.remove(); more = null; }
      var end = Math.min(groups.length, limit);
      for (var i = shown; i < end; i++) box.appendChild(fileGroup(groups[i]));
      shown = end;
      if (shown < groups.length) {
        more = ui.row(ui.button({
          label: t("展开其余 " + (groups.length - shown) + " 个文件",
            "Show " + (groups.length - shown) + " more files"),
          onClick: function () { paint(groups.length); }
        }));
        box.appendChild(more);
      }
    }
    paint(GROUP_FILES);
    return box;
  }

  OneSSH.view("grep", function (data, ctx) {
    data = data || {};
    var input = inputOf(ctx);
    var rows = listOf(data.lines).filter(function (item) { return item && typeof item === "object"; });
    var groups = groupByPath(rows);
    var truncated = data.truncated === true;
    var total = numOf(data.match_count);
    if (total === null) total = countMatches(rows);

    var status = [];
    if (rows.length) {
      status.push(truncated
        ? ui.pill("warn", fmt.num(total) + t(" 处匹配（已截断）", " matches (truncated)"))
        : ui.pill("info", fmt.num(total) + t(" 处匹配", " matches")));
      // 命中散在几个文件里，直接决定下一步是逐个看还是换个词重搜，值得单独占一个 pill
      if (groups.length > 1) status.push(ui.pill("muted", fmt.num(groups.length) + t(" 个文件", " files")));
    } else {
      status.push(ui.pill("muted", t("没有匹配", "No matches")));
    }

    var chips = [hostChip(input), engineChip(data.engine)];
    if (input.ignoreCase === true) chips.push(ui.chip(t("忽略大小写", "Ignore case")));
    if (input.literal === true) chips.push(ui.chip(t("字面匹配", "Literal")));
    var context = numOf(input.context);
    // 上下文行数解释了终端块里那些灰行是哪来的，不写出来会让人以为匹配错了
    if (context !== null && context > 0) chips.push(ui.chip(t("上下文 ±", "context ±") + context));

    var glob = textOf(input.glob);
    var subtitle = (textOf(input.path) || "~") + (glob ? " · " + glob : "");

    var body = [];
    var warning = textOf(data.warning);
    if (warning) body.push(ui.note(warning, "warn"));
    if (truncated) {
      body.push(truncatedNote(input, "还有更多匹配没有列出", "more matches exist"));
    }
    body.push(groups.length ? fileGroups(groups) : ui.empty(t("没有匹配到内容", "Nothing matched")));

    return ui.card({
      kicker: "GREP",
      title: textOf(input.pattern) || t("内容搜索", "Content search"),
      subtitle: subtitle,
      chips: chips,
      status: status,
      actions: [rerunAction(ctx, "grep")],
      body: ui.stack.apply(null, body)
    });
  });

  /* ------------------------------------------------------------------ *
   * find —— 一份路径清单
   * ------------------------------------------------------------------ */

  // 结果落在多少个目录里，决定用户是「进某个目录细看」还是「换个模式重搜」。
  function countDirs(paths) {
    var seen = {}, n = 0;
    paths.forEach(function (item) {
      var cut = item.lastIndexOf("/");
      var dir = "d:" + (cut > 0 ? item.slice(0, cut) : (cut === 0 ? "/" : "."));
      if (has(seen, dir)) return;
      seen[dir] = true;
      n++;
    });
    return n;
  }

  OneSSH.view("find", function (data, ctx) {
    data = data || {};
    var input = inputOf(ctx);
    var paths = listOf(data.paths)
      .filter(function (item) { return item != null && item !== "" && typeof item !== "object"; })
      .map(function (item) { return String(item); });
    var truncated = data.truncated === true;

    var status = [];
    if (paths.length) {
      status.push(truncated
        ? ui.pill("warn", fmt.num(paths.length) + t(" 条（已截断）", " paths (truncated)"))
        : ui.pill("info", fmt.num(paths.length) + t(" 条", " paths")));
      var dirs = countDirs(paths);
      if (dirs > 1) status.push(ui.pill("muted", fmt.num(dirs) + t(" 个目录", " dirs")));
    } else {
      status.push(ui.pill("muted", t("没有结果", "No results")));
    }

    var body = [];
    var warning = textOf(data.warning);
    if (warning) body.push(ui.note(warning, "warn"));
    if (truncated) {
      body.push(truncatedNote(input, "还有更多路径没有列出", "more paths exist"));
    }
    body.push(paths.length
      // 路径本身就是一列等宽文本，终端块比表格更省空间，还自带折叠与整份复制；
      // 行号在这里当序号用，正好回答「第几条」。
      ? ui.terminal({
        text: paths.join("\n"),
        startLine: null,
        collapsedLines: 20,
        title: t("路径", "Paths"),
        wrap: true
      })
      : ui.empty(t("没有找到匹配的路径", "No paths matched")));

    return ui.card({
      kicker: "FIND",
      title: textOf(input.pattern) || t("路径搜索", "Path search"),
      subtitle: textOf(input.path) || "~",
      chips: [hostChip(input), engineChip(data.engine)],
      status: status,
      actions: [rerunAction(ctx, "find")],
      body: ui.stack.apply(null, body)
    });
  });
})();
