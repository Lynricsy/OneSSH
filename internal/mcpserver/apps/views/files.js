;(function () {
  "use strict";

  /* 文件组卡片：file_read / file_write / file_edit / file_list / file_transfer。
     这五个工具围着同一个对象转——一条远端路径，所以卡片的信息骨架也统一成
     「哪台主机的哪个文件（头部）→ 一眼看懂的结论（pill + metrics）→ 内容本体（终端块 / diff / 表格）
       → 校验和等追溯用的细节（kv）→ 需要提醒的风险（note）」。
     四类工具里有三类会改远端状态，卡片是它们唯一的回执，所以「改了什么、能不能撤回、
     内容是否可信」必须写在正文里，而不是让用户自己去比对 sha256。
     只有 file_read / file_list 在只读白名单内，因此翻页、面包屑、行内下钻这些会真的再发一次调用的
     交互，一律先过 ctx.can 再渲染。 */

  var ui = OneSSH.ui, fmt = OneSSH.fmt, h = OneSSH.h, t = OneSSH.t;

  /* 读文件的折叠阈值。.term-body 高 368px、行高 18px，一屏最多放得下 19 行多一点，
     而折叠时末尾的「… 还有 N 行」自己也占一行，所以正文只能留 18 行。
     阈值一旦高过这个数，超出的正文既不折叠也没有提示，只是被推到不显眼的滚动区外——
     卡片头还写着「完整文件」，读者却看不到文件的尾巴。 */
  var READ_FOLD = 18;
  var WRITE_FOLD = 12;    // 写入回执里的正文只是佐证，不该盖过结论
  var DEFAULT_LIMIT = 500; // 与 file_read 的服务端默认值一致，翻页步长没有别的可信来源

  /* ------------------------------------------------------------------ *
   * 取值：bytes 0、verified false、size 0 都是有效结果，
   * 一律显式判类型，绝不用 `||` 兜底，否则「空文件」会被显示成「字段缺失」。
   * ------------------------------------------------------------------ */

  function numOf(value) {
    if (typeof value === "number") return isFinite(value) ? value : null;
    if (typeof value === "string" && value.trim() !== "") {
      var n = Number(value);
      return isFinite(n) ? n : null;
    }
    return null;
  }

  function strOf(value) { return typeof value === "string" ? value : ""; }

  function objOf(value) {
    return value && typeof value === "object" && !Array.isArray(value) ? value : null;
  }

  function argsOf(ctx) { return objOf(ctx && ctx.input) || {}; }

  function hostChip(name) {
    return name ? ui.chip(name, { mono: true, title: t("主机 ", "host ") + name }) : null;
  }

  /* 纯说明性的次要文字。描边只留给真正能点的东西：翻页那一行里两个按钮常处于禁用态，
     旁边的「每页 N 行」若用 chip 画成带描边的小方块，整行里最像按钮的反而是点不动的那个。
     形状与 memory.js 的 meta() 保持一致，同一句提示在两处卡片里读起来才是同一种东西。 */
  function hint(text) {
    return h("span", {
      style: { "font-size": "11px", "color": "var(--faint)", "white-space": "nowrap" },
      text: text
    });
  }

  /* ------------------------------------------------------------------ *
   * 路径：卡片标题只放文件名，完整路径留给副标题与 kv
   * ------------------------------------------------------------------ */

  // 末尾斜杠不代表一层目录，先剥掉再取最后一段；根目录取不出名字，交给调用方兜底。
  function baseName(pathText) {
    var raw = strOf(pathText);
    while (raw.length > 1 && raw.charAt(raw.length - 1) === "/") raw = raw.slice(0, -1);
    var cut = raw.lastIndexOf("/");
    return cut < 0 ? raw : raw.slice(cut + 1);
  }

  function joinPath(base, name) {
    var dir = strOf(base);
    if (!dir || dir === ".") return name;
    return dir.charAt(dir.length - 1) === "/" ? dir + name : dir + "/" + name;
  }

  /* 服务端在 path 省略时自己填 "."（internal/mcpserver/files.go），因此「不传 path」和
     「显式传 .」指的是同一个目录：卡片也必须当成同一个，否则从子目录退回起始目录之后，
     标题会从「起始目录」变成一个孤零零的 "."。 */
  function isHomePath(pathText) {
    var raw = strOf(pathText).trim();
    // "." 与 "./" 都是「登录起始目录」的合法写法，只认前者会让标题退化成一个孤零零的点
    return raw === "" || raw === "." || raw === "./";
  }

  /* 面包屑：每一段都要能单独还原成一个可用的目录路径。
     绝对路径额外补一个 "/" 头，`~` 与相对路径则原样保留首段——把 `~/logs` 改写成 `/logs`
     会指向完全不同的目录。
     相对路径的根是登录用户的起始目录，所以再补一段指向 "." 的入口：这张卡片靠 ctx.refresh
     原地换目录，而 runtime 的返回栈只认 ctx.navigate，返回按钮根本不会出现；起始目录下
     点进 logs/ 之后，面包屑里就只剩下当前这一层（还是不可点的），用户走进去就出不来了。 */
  function crumbsOf(pathText) {
    var raw = strOf(pathText);
    var absolute = raw.charAt(0) === "/";
    var tilde = raw === "~" || raw.indexOf("~/") === 0;   // "~" 自己会作为首段进循环
    var out = [];
    if (absolute) out.push({ label: "/", path: "/" });
    else if (!tilde) {
      out.push({
        label: t("起始目录", "Home"), path: ".",
        title: t("回到登录用户的起始目录", "Back to the login user's home directory")
      });
    }
    var parts = raw.split("/");
    var prefix = "";
    for (var i = 0; i < parts.length; i++) {
      var seg = parts[i];
      if (!seg || seg === ".") continue;   // 连续斜杠和 "." 不构成新的层级
      if (!prefix) prefix = absolute ? "/" + seg : seg;
      else prefix = prefix === "/" ? "/" + seg : prefix + "/" + seg;
      out.push({ label: seg, path: prefix });
    }
    return out;   // 三个分支各自都先塞了一段（"/"、起始目录、或 "~" 自己），不会是空的
  }

  function pathRow(label, value) {
    return { label: label, value: value, mono: true, copy: true };
  }

  function shaRow(label, value) {
    var sha = strOf(value);
    return sha ? { label: label, value: sha, mono: true, copy: true } : null;
  }

  /* ------------------------------------------------------------------ *
   * file_read 的行号列
   * ------------------------------------------------------------------ */

  var GUTTER = /^(\d+):([\s\S]*)$/;

  /* 网关返回的正文形如 "17:    ssl_protocols ...", 行号已经写死在文本里。
     直接交给 ui.terminal 会得到两列行号，所以先把行号列拆出来再还给它渲染。
     判定要求「每一行都匹配、号码逐行加一、首行号等于本次 offset」三条同时成立：
     少一条就可能把正文里恰好像行号的内容（例如 "80:443"）当成行号剥掉。 */
  function parseGutter(text, offset) {
    var all = fmt.lines(text);
    if (!all.length) return null;
    var rows = [], prev = null;
    for (var i = 0; i < all.length; i++) {
      var m = GUTTER.exec(all[i]);
      if (!m) return null;
      var n = Number(m[1]);
      if (prev !== null && n !== prev + 1) return null;
      prev = n;
      rows.push(m[2]);
    }
    var start = prev - rows.length + 1;
    if (start !== offset) return null;
    return { text: rows.join("\n"), start: start, count: rows.length, last: prev };
  }

  /* ------------------------------------------------------------------ *
   * file_read
   * ------------------------------------------------------------------ */

  OneSSH.view("file_read", function (data, ctx) {
    var args = argsOf(ctx);
    var pathText = strOf(args.path);
    var name = baseName(pathText);
    var content = strOf(data.content);
    var sha = strOf(data.sha256);
    var bytes = numOf(data.bytes);
    var total = numOf(data.total_lines);

    var offset = numOf(args.offset);
    if (offset === null || offset < 1) offset = 1;
    var limit = numOf(args.limit);

    /* 空文件只认 bytes===0。服务端（internal/files 的 Manager.Read）用 strings.Split 切行，
       0 字节的文件也会切出一个空行，回来的是 bytes:0 / total_lines:1 / content:"1:"——
       total_lines 恒 >= 1，等不到 0。按行数判的话空文件会被当成「一行的完整文件」，
       卡片就画出一个什么都没有的终端块，读者只能猜那一行里是不是有看不见的字符。
       total===0 留作兜底：服务端哪天不再补这个空行，那也是同一件事。 */
    var blank = bytes === 0 || total === 0;

    var parsed = blank ? null : parseGutter(content, offset);
    var text = parsed ? parsed.text : content;
    var shown = blank ? 0 : (parsed ? parsed.count : (content ? fmt.lines(content).length : 0));
    var first = parsed ? parsed.start : offset;
    var last = shown ? first + shown - 1 : 0;
    var whole = shown > 0 && first === 1 && (total === null || last >= total);

    var status;
    if (blank) status = ui.pill("muted", t("空文件", "Empty file"));
    else if (!shown) status = ui.pill("muted", t("这一段没有内容", "Empty range"));
    else if (whole) status = ui.pill("ok", t("完整文件", "Whole file"));
    else {
      status = ui.pill("info", t("部分内容 · 共 " + fmt.num(total) + " 行",
        "partial · " + fmt.num(total) + " lines"));
    }

    var body = [ui.metrics([
      { label: t("字节", "Bytes"), value: bytes === null ? null : fmt.bytes(bytes) },
      {
        label: t("总行数", "Lines"),
        // 空文件在服务端算作 1 行（切行的产物），照抄就会和旁边的「0 字节」当场打架
        value: blank ? fmt.num(0) : (total === null ? null : fmt.num(total))
      },
      {
        label: t("本次行范围", "Range"),
        value: shown ? fmt.num(first) + "–" + fmt.num(last) : null
      },
      { label: "SHA-256", value: sha ? fmt.short(sha, 8, 6) : null, hint: sha || null }
    ])];

    if (blank) {
      body.push(ui.empty(t("这是一个空文件（0 字节）", "This file is empty (0 bytes)")));
    } else if (!shown) {
      body.push(ui.empty(t("这一段没有内容，offset 可能已经越过文件末尾",
        "Nothing in this range; the offset may be past the end of the file")));
    } else {
      body.push(ui.terminal({
        text: text,
        startLine: first,
        collapsedLines: READ_FOLD,
        title: name || pathText || t("文件", "File")
      }));
    }

    body.push(ui.kv([
      pathRow(t("完整路径", "Path"), pathText),
      shaRow("SHA-256", sha)
    ]));

    // 翻页会真的再读一次远端文件，宿主不允许回调时按钮点了没反应，不如不画；
    // 空文件也不画：两个按钮必然同时是禁用态，摆在那里只是让人以为还有别的页
    if (ctx.can("file_read") && !blank) {
      var step = limit !== null && limit > 0 ? limit : (shown || DEFAULT_LIMIT);
      var atStart = first <= 1;
      var atEnd = !shown || (total !== null && last >= total);
      body.push(ui.row(
        // 两个按钮都不带图标：style.css 只画了 .icon-back(←) 而没有对应的右箭头，
        // 给「上一页」单独配一个箭头，会让这一对按钮看起来一主一次
        ui.button({
          label: t("上一页", "Previous"),
          disabled: atStart,
          title: atStart ? t("已经是文件开头", "Already at the top") : null,
          onClick: function () { ctx.refresh({ offset: Math.max(1, first - step), limit: step }); }
        }),
        ui.button({
          label: t("下一页", "Next"),
          disabled: atEnd,
          title: atEnd ? t("已经是文件末尾", "Already at the end") : null,
          onClick: function () { ctx.refresh({ offset: last + 1, limit: step }); }
        }),
        hint(t("每页 " + fmt.num(step) + " 行", fmt.num(step) + " lines/page"))
      ));
    }

    // file_read 同样在只读白名单里，这张卡不是静态快照：同族的 file_list 有刷新而这里没有，
    // 会让人以为文件内容读回来之后就不能再更新了
    var actions = [];
    if (ctx.can("file_read")) {
      actions.push(ui.button({
        label: t("刷新", "Refresh"),
        icon: "refresh",
        title: t("按当前行范围重新读一次", "Read this range again"),
        onClick: function () { ctx.refresh({}); }
      }));
    }

    return ui.card({
      kicker: "READ",
      title: name || pathText || t("文件内容", "File"),
      subtitle: pathText,
      chips: [hostChip(strOf(args.host))],
      status: status,
      actions: actions,
      body: ui.stack.apply(null, body)
    });
  });

  /* ------------------------------------------------------------------ *
   * file_write
   * ------------------------------------------------------------------ */

  OneSSH.view("file_write", function (data, ctx) {
    var args = argsOf(ctx);
    var pathText = strOf(args.path);
    var name = baseName(pathText);
    var sha = strOf(data.sha256);
    var bytes = numOf(data.bytes);
    var mode = strOf(args.mode) || "0644";
    var content = typeof args.content === "string" ? args.content : null;
    var lineCount = content === null ? null : (content ? fmt.lines(content).length : 0);

    var status = [ui.pill("ok", t("已写入", "Written"))];
    if (data.non_atomic === true) status.push(ui.pill("warn", t("非原子写入", "Non-atomic")));

    var body = [ui.metrics([
      { label: t("字节", "Bytes"), value: bytes === null ? null : fmt.bytes(bytes) },
      { label: t("行数", "Lines"), value: lineCount === null ? null : fmt.num(lineCount) },
      { label: t("权限", "Mode"), value: mode },
      { label: "SHA-256", value: sha ? fmt.short(sha, 8, 6) : null, hint: sha || null }
    ])];

    // 覆盖写的回执里，用户最想确认的是「到底写进去了什么」，所以正文优先于校验和
    if (content !== null && content !== "") {
      body.push(ui.terminal({
        text: content,
        startLine: 1,
        collapsedLines: WRITE_FOLD,
        title: name || t("写入内容", "Written content")
      }));
    } else if (content === "") {
      body.push(ui.note(t("本次写入的内容为空，该文件已被清空。",
        "The written content was empty, so the file has been truncated to zero bytes."), "warn"));
    }

    body.push(ui.kv([
      pathRow(t("完整路径", "Path"), pathText),
      shaRow("SHA-256", sha)
    ]));

    if (data.non_atomic === true) {
      body.push(ui.note(strOf(data.warning) || t(
        "目标文件系统不支持覆盖 rename，已退化为先删后写：写入过程中该文件短暂不存在。",
        "The target filesystem cannot rename over an existing file, so the write fell back to delete-then-create; the file was briefly missing."), "warn"));
    } else if (strOf(data.warning)) {
      body.push(ui.note(strOf(data.warning), "warn"));
    }

    body.push(ui.note(t(
      "这是整文件覆盖。只改局部请用 file_edit，否则未写进 content 的内容会丢失。",
      "This replaced the whole file. Use file_edit for partial changes, or anything absent from content is lost."), "info"));

    return ui.card({
      kicker: "WRITE",
      title: name || pathText || t("写入文件", "Write file"),
      subtitle: pathText,
      chips: [hostChip(strOf(args.host))],
      status: status,
      body: ui.stack.apply(null, body)
    });
  });

  /* ------------------------------------------------------------------ *
   * file_edit
   * ------------------------------------------------------------------ */

  // diff 自己带着改动量，不必再去比对新旧文件；+++/--- 是文件头，不能算进增删行数。
  function diffStat(text) {
    var all = fmt.lines(text);
    var added = 0, removed = 0;
    for (var i = 0; i < all.length; i++) {
      var line = all[i];
      if (line.indexOf("+++") === 0 || line.indexOf("---") === 0) continue;
      if (line.charAt(0) === "+") added++;
      else if (line.charAt(0) === "-") removed++;
    }
    return { added: added, removed: removed };
  }

  OneSSH.view("file_edit", function (data, ctx) {
    var args = argsOf(ctx);
    var pathText = strOf(args.path);
    var name = baseName(pathText);
    var sha = strOf(data.sha256);
    var bytes = numOf(data.bytes);
    var diffText = strOf(data.diff);
    var stat = diffStat(diffText);
    var edits = Array.isArray(args.edits) ? args.edits.length : null;
    var changed = stat.added > 0 || stat.removed > 0;

    var chips = [hostChip(strOf(args.host))];
    if (strOf(args.expected_sha256)) {
      chips.push(ui.chip(t("乐观锁", "CAS"), {
        title: t("本次编辑校验了 expected_sha256，文件在读取后没有被别人改过",
          "expected_sha256 was verified: the file did not change after it was read")
      }));
    }

    var status = [changed
      ? ui.pill("ok", t("已编辑", "Edited"))
      : ui.pill("muted", t("内容无变化", "No change"))];
    if (data.non_atomic === true) status.push(ui.pill("warn", t("非原子写入", "Non-atomic")));

    var body = [ui.metrics([
      { label: t("替换条数", "Edits"), value: edits === null ? null : fmt.num(edits) },
      { label: t("新增行", "Added"), value: "+" + fmt.num(stat.added), kind: stat.added ? "ok" : "muted" },
      { label: t("删除行", "Removed"), value: "-" + fmt.num(stat.removed), kind: stat.removed ? "danger" : "muted" },
      { label: t("新文件字节", "Bytes"), value: bytes === null ? null : fmt.bytes(bytes) }
    ])];

    body.push(diffText
      ? ui.diff(diffText)
      : ui.empty(t("编辑已应用，但文件内容没有变化", "The edit applied but the file content did not change")));

    body.push(ui.kv([
      pathRow(t("完整路径", "Path"), pathText),
      shaRow(t("新 SHA-256", "New SHA-256"), sha)
    ]));

    if (data.non_atomic === true) {
      body.push(ui.note(strOf(data.warning) || t(
        "目标文件系统不支持覆盖 rename，已退化为先删后写：写回过程中该文件短暂不存在。",
        "The target filesystem cannot rename over an existing file, so the write-back fell back to delete-then-create."), "warn"));
    } else if (strOf(data.warning)) {
      body.push(ui.note(strOf(data.warning), "warn"));
    }

    // 权限被重置是编辑私密配置时最容易踩到的坑，而 diff 里完全看不出来
    body.push(ui.note(t(
      "写回后文件权限固定为 0644；原本是 0600 之类的敏感文件，请再用 file_write 指定 mode 改回去。",
      "The file is written back with mode 0644; if it was 0600 or similar, restore it with file_write and an explicit mode."), "info"));

    return ui.card({
      kicker: "EDIT",
      title: name || pathText || t("编辑文件", "Edit file"),
      subtitle: pathText,
      chips: chips,
      status: status,
      body: ui.stack.apply(null, body)
    });
  });

  /* ------------------------------------------------------------------ *
   * file_list
   * ------------------------------------------------------------------ */

  function isSymlink(entry) {
    return !!strOf(entry.symlink_target) || strOf(entry.mode).charAt(0) === "l";
  }

  function nameCell(entry) {
    var name = strOf(entry.name);
    var kind = entry.directory === true ? "folder" : (isSymlink(entry) ? "link" : "file");
    var target = strOf(entry.symlink_target);
    // 用行内 span 而不是 flex 容器：首列靠 text-overflow 收敛长文件名，flex 会让省略号失效。
    // 链接目标接在名字后面，而不是单开一列：一个目录里通常只有个别软链，
    // 单独一列的结果是十行里九行都是占位破折号，却吃掉表格三分之一的宽度。
    // 单元格会截断，所以完整的「名字 → 目标」写进 title 兜底。
    return h("span", { title: target ? name + " → " + target : name },
      ui.icon(kind), " " + name,
      target ? h("span", { class: "path", text: " → " + target }) : null);
  }

  function timeCell(entry) {
    var ts = numOf(entry.mtime);
    var text = ts === null ? "" : fmt.rel(ts);
    return text ? h("span", { title: fmt.time(ts), text: text }) : null;
  }

  OneSSH.view("file_list", function (data, ctx) {
    var args = argsOf(ctx);
    var host = strOf(args.host);
    var pathText = strOf(args.path);
    var home = isHomePath(pathText);
    var entries = Array.isArray(data.entries) ? data.entries.filter(objOf) : [];

    var dirs = 0, links = 0;
    entries.forEach(function (entry) {
      if (entry.directory === true) dirs++;
      else if (isSymlink(entry)) links++;
    });
    // 软链已经单独占一栏，再算进「文件」里会让三栏之和大于总条目数，
    // 与左边那个「N 个条目」当场对不上
    var files = entries.length - dirs - links;

    var status = [ui.pill("info", t(fmt.num(entries.length) + " 个条目",
      fmt.num(entries.length) + " entries"))];
    if (entries.length) {
      var detail = t("目录 " + dirs + " · 文件 " + files, dirs + " dirs · " + files + " files");
      if (links) detail += t(" · 软链 " + links, " · " + links + " links");
      status.push(ui.pill("muted", detail));
    }

    var canList = ctx.can("file_list");
    var canRead = ctx.can("file_read");
    var body = [];

    // 面包屑是这张卡片最主要的导航方式（也是唯一的「往回走」入口）：
    // 每一段都直接把当前卡片切到那一层目录
    if (canList) {
      var crumbs = crumbsOf(pathText);
      // 只剩当前这一层时整行都点不动，等于把 subtitle 又抄了一遍，不如不占这一行
      if (crumbs.length > 1) {
        var trail = ui.row();
        crumbs.forEach(function (crumb, index) {
          // 根那一段本身就是一条斜杠，后面再补分隔符会读成「/ / etc」
          if (index && crumbs[index - 1].label !== "/") {
            trail.appendChild(h("span", { class: "path", text: "/" }));
          }
          // 当前这一层已经走不动了，画成普通文字：描边只留给可点的祖先段，
          // 否则一行面包屑里唯一带框的反而是唯一不能点的那个
          if (index === crumbs.length - 1) {
            trail.appendChild(h("span", { class: "path", title: crumb.path, text: crumb.label }));
            return;
          }
          trail.appendChild(ui.button({
            label: crumb.label,
            // 起始目录那一段的 path 是 "."，"进入 ." 读起来什么也没说，所以它自带说明
            title: crumb.title || (t("进入 ", "Open ") + crumb.path),
            onClick: function () { ctx.refresh({ path: crumb.path }); }
          }));
        });
        body.push(trail);
      }
    }

    if (!entries.length) {
      body.push(ui.empty(t("这个目录是空的", "This directory is empty")));
    } else {
      var columns = [
        { key: "name", label: t("名称", "Name"), mono: true, render: nameCell },
        {
          key: "size", label: t("大小", "Size"), align: "right",
          render: function (entry) {
            if (entry.directory === true) return null;
            var size = numOf(entry.size);
            return size === null ? null : fmt.bytes(size);
          }
        },
        { key: "mode", label: t("权限", "Mode"), mono: true, secondary: true },
        { key: "mtime", label: t("修改时间", "Modified"), secondary: true, render: timeCell }
      ];
      var onRow = null;
      if (canList || canRead) {
        onRow = function (entry) {
          var name = strOf(entry.name);
          if (!name) return;
          var full = joinPath(pathText, name);
          if (entry.directory === true) {
            // 目录留在同一张卡片里往下走，用户的心智模型是「在一个文件管理器里」
            if (canList) ctx.refresh({ path: full });
            return;
          }
          if (canRead) {
            ctx.navigate("file_read", { host: host, path: full, offset: 1, limit: DEFAULT_LIMIT }, name);
          }
        };
      }

      body.push(ui.table({ columns: columns, rows: entries, onRow: onRow }));
    }

    var actions = [];
    if (canList) {
      actions.push(ui.button({
        label: t("刷新", "Refresh"),
        icon: "refresh",
        title: t("重新列出该目录", "List this directory again"),
        onClick: function () { ctx.refresh({}); }
      }));
    }

    return ui.card({
      kicker: "LIST",
      // 从面包屑退回起始目录时 path 是 "."，与「没传 path」是同一个目录，
      // 标题就不能一个写「起始目录」、另一个写成一个孤零零的 "."
      title: home ? t("起始目录", "Home") : (baseName(pathText) || "/"),
      subtitle: home ? t("登录用户的起始目录", "the login user's home directory") : pathText,
      chips: [hostChip(host)],
      status: status,
      actions: actions,
      body: ui.stack.apply(null, body)
    });
  });

  /* ------------------------------------------------------------------ *
   * file_transfer
   * ------------------------------------------------------------------ */

  OneSSH.view("file_transfer", function (data, ctx) {
    var args = argsOf(ctx);
    var srcHost = strOf(args.src_host);
    var dstHost = strOf(args.dst_host);
    var srcPath = strOf(args.src_path);
    var dstPath = strOf(args.dst_path);
    var srcSha = strOf(data.source_sha256);
    var dstSha = strOf(data.destination_sha256);
    var bytes = numOf(data.bytes);
    // 两端都算出了校验和却对不上，是比「没校验」严重得多的情况，要单独认出来
    var mismatch = !!srcSha && !!dstSha && srcSha !== dstSha;
    var verified = data.verified === true && !mismatch;

    var status;
    if (mismatch) status = ui.pill("danger", t("校验不一致", "Checksum mismatch"));
    else if (verified) status = ui.pill("ok", t("校验一致", "Verified"));
    else status = ui.pill("warn", t("未校验", "Unverified"));

    /* 指标条只放三条互不重复的事实：传了多少、两端是否一致、落在哪台机器上。
       校验一致时两条 sha256 是同一串，把它们摆进最醒目的横幅只是把同一个值印四遍
       （短值两格 + 下面 kv 两行），肉眼也没法靠截断值判断异同——那个结论 pill 已经给了。
       完整值留在 kv 里，那里有复制按钮，需要核对的人拿得走。 */
    var checksum;
    if (mismatch) checksum = { text: t("两端不一致", "Mismatch"), kind: "danger" };
    else if (verified) checksum = { text: t("两端一致", "Identical"), kind: "ok" };
    else checksum = { text: t("未校验", "Unverified"), kind: "warn" };

    var body = [ui.metrics([
      { label: t("字节", "Bytes"), value: bytes === null ? null : fmt.bytes(bytes) },
      {
        label: t("校验", "Checksum"),
        value: checksum.text,
        kind: checksum.kind,
        hint: srcSha || dstSha || null
      },
      { label: t("目标主机", "Destination host"), value: dstHost || null }
    ])];

    body.push(ui.kv([
      pathRow(t("源路径", "Source path"), srcPath),
      pathRow(t("目标路径", "Destination path"), dstPath),
      shaRow(t("源 SHA-256", "Source SHA-256"), srcSha),
      shaRow(t("目标 SHA-256", "Destination SHA-256"), dstSha)
    ]));

    if (strOf(data.warning)) body.push(ui.note(strOf(data.warning), "warn"));

    if (mismatch) {
      body.push(ui.note(t(
        "两端校验和不同，目标文件与源文件内容不一致：先不要使用目标文件，重传一次再比对。",
        "The two checksums differ, so the destination does not match the source. Do not use it; transfer again and compare."), "danger"));
    } else if (!verified) {
      body.push(ui.note(t(
        "网关没有拿到目标端校验和，无法确认两端内容一致；关键文件请在目标主机上自行 sha256sum 复核。",
        "The gateway could not read a checksum from the destination, so the two sides are not proven identical; run sha256sum there for anything critical."), "warn"));
    }

    return ui.card({
      kicker: "COPY",
      title: baseName(srcPath) || t("跨主机复制", "File transfer"),
      subtitle: srcPath && dstPath ? srcPath + " → " + dstPath : (srcPath || dstPath),
      // 用箭头而不是 .icon-chevron：后者是向下的折角，夹在两个主机 chip 中间读起来像下拉标记，
      // 而且与卡片右上角那个真正可点的折叠 chevron 长得一模一样。这里与副标题的 `→` 统一
      chips: [hostChip(srcHost), h("span", { class: "path", text: "→" }), hostChip(dstHost)],
      status: status,
      body: ui.stack.apply(null, body)
    });
  });
})();
