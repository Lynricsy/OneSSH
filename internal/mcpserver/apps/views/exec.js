;(function () {
  "use strict";

  /* exec 组卡片：exec / session_env / output_read / exec_many。
     这四个工具共用同一套「命令 → 输出」心智模型，所以退出状态判定、输出区渲染、
     自带行号的解析都在这里集中实现一次；否则同一段判断在四个视图里各写一遍，
     迟早会出现「这里把 exit_code=0 当成功、那里把它当缺失」的分歧。 */

  var ui = OneSSH.ui, fmt = OneSSH.fmt, h = OneSSH.h, t = OneSSH.t;

  // 20 行正好等于 .term-body 的可视高度（368px = 8px padding ×2 + 20×18px 行高）。
  // 折叠阈值大于可视行数时，最后一行会被切成半截，"… 还有 N 行" 的提示也被挤出滚动区。
  var TERM_FOLD = 20;
  var ARTIFACT_FOLD = 40;   // artifact 本身就是翻页看的，折叠阈值给大一些
  var FANOUT_FOLD = 12;     // 批量执行按主机分块，每块只留一个概览的高度

  /* 手绘终端块要复刻 ui.terminal 的两道防卡死闸门：字符上限 + 分页追加。
     直接取 runtime 导出的那份，别再抄一遍数值——抄一份就意味着改一边忘另一边时，
     这条分支会悄无声息地退回到没有保护的状态。 */
  var TEXT_CAP = OneSSH.limits.text;
  var TERM_PAGE = OneSSH.limits.page;

  /* ------------------------------------------------------------------ *
   * 取值：exit_code 0、timeout false、空字符串都是有效结果，
   * 一律显式判 null，绝不用 `||` 兜底，否则「成功」会被当成「字段缺失」。
   * ------------------------------------------------------------------ */

  function int(value) {
    if (typeof value === "number") return isFinite(value) ? value : null;
    if (typeof value === "string" && value.trim() !== "") {
      var n = Number(value);
      return isFinite(n) ? n : null;
    }
    return null;
  }

  function str(value) { return typeof value === "string" ? value : ""; }

  function obj(value) {
    return value && typeof value === "object" && !Array.isArray(value) ? value : null;
  }

  function argsOf(ctx) { return obj(ctx && ctx.input) || {}; }

  // set 可能是对象 {K:V}，unset 多半是数组 [K]，两种形状都要能数出条数
  function countOf(value) {
    if (Array.isArray(value)) return value.length;
    var box = obj(value);
    return box ? Object.keys(box).length : 0;
  }

  function lineCount(text) { return text ? fmt.lines(text).length : 0; }

  function hostChip(name) {
    return name ? ui.chip(name, { mono: true, title: t("主机 ", "host ") + name }) : null;
  }

  /* ------------------------------------------------------------------ *
   * 退出状态：卡片头上唯一那一眼要看懂的结论
   * ------------------------------------------------------------------ */

  function execStatus(data, args) {
    var code = int(data.exit_code);
    if (data.timeout === true) {
      var secs = int(args && args.timeout_s);
      return ui.pill("warn", secs === null
        ? t("超时中断", "Timed out")
        : t("超时 " + secs + " 秒", "Timed out after " + secs + "s"));
    }
    if (code === 0) return ui.pill("ok", t("退出码 0", "Exit 0"));
    if (code === null) return ui.pill("muted", t("无退出码", "No exit code"));
    // 负数是网关侧的哨兵值（进程被信号打断 / 根本没跑起来），不能当真实退出码展示
    if (code < 0) return ui.pill("danger", t("未取得退出码", "No exit code"));
    return ui.pill("danger", t("退出码 " + code, "Exit " + code));
  }

  function bytesBadge(value, text) {
    var n = int(value);
    if (n === null) n = text ? text.length : 0;
    return n > 0 ? fmt.bytes(n) : null;
  }

  // stderr 为空时没必要让用户先点一下标签页才看到输出；有 stderr 才值得分栏。
  function outputNode(data, opts) {
    opts = opts || {};
    var fold = opts.fold || TERM_FOLD;
    var title = opts.title || t("输出", "Output");
    var merged = str(data.output);
    var stdout = str(data.stdout);
    var stderr = str(data.stderr);
    if (!merged && !stdout && !stderr) return null;
    if (!stderr) {
      return ui.terminal({ text: merged || stdout, title: title, collapsedLines: fold });
    }
    var items = [];
    if (merged) {
      items.push({
        label: t("合并输出", "Combined"),
        node: ui.terminal({ text: merged, title: title, collapsedLines: fold })
      });
    }
    if (stdout) {
      items.push({
        label: t("标准输出", "stdout"),
        badge: bytesBadge(data.stdout_bytes, stdout),
        node: ui.terminal({ text: stdout, title: t("标准输出", "stdout"), collapsedLines: fold })
      });
    }
    items.push({
      label: t("标准错误", "stderr"),
      badge: bytesBadge(data.stderr_bytes, stderr),
      node: ui.terminal({ text: stderr, title: t("标准错误", "stderr"), variant: "err", collapsedLines: fold })
    });
    return ui.tabs(items);
  }

  /* ------------------------------------------------------------------ *
   * 自带行号的正文
   * ------------------------------------------------------------------ */

  /* 服务端 execx.ReadArtifact 用 fmt.Sprintf("%d:%s", i+1, line) 拼行号，正文形如
     "401:2026-09-02T09:41:47 ..."；另有对齐成 "   401  正文" 的老格式。两种分隔符
     都要认，只认其中一种就会在另一种上整段解析失败，退回顺推行号从而画出两列号码。
     分隔符单独捕获成 m[2]：只有一行时「行号递增」这条佐证不存在，得靠它顶上。 */
  var GUTTER = /^\s*(\d+)(:|\s\s|$)([\s\S]*)$/;

  /* artifact 正文形如 "401:正文"，行号已经写死在文本里。直接丢给 ui.terminal
     会得到两列行号，所以先把行号列拆出来：
     - 行号连续 → 交回 ui.terminal（它的行号列是从 startLine 顺推的，正好对得上）；
     - 行号有跳跃（被 grep 过滤过）→ 顺推出来的号码是编造的，只能自己画一遍。
     minLine 收本次请求的 offset：服务端从第 offset 条结果起切片（带 grep 时按匹配数计），
     所以第一行的真实行号必然不小于它——这是单行正文唯一拿得到的校验。 */
  function parseGutter(text, minLine) {
    var all = fmt.lines(text);
    if (!all.length) return null;
    if (!(minLine >= 1)) minLine = 1;
    var rows = [], last = null, firstIndex = -1, firstSep = "", matched = 0, consecutive = true;
    for (var i = 0; i < all.length; i++) {
      if (all[i] === "") {
        // 空行没有可信的号码，此时顺推必然错位，改走手绘分支
        rows.push({ n: null, text: "" });
        consecutive = false;
        continue;
      }
      var m = GUTTER.exec(all[i]);
      if (!m) return null;
      var n = Number(m[1]);
      if (last !== null) {
        if (n <= last) return null;   // 行号必须递增，否则这只是碰巧像行号的正文
        if (n !== last + 1) consecutive = false;
      }
      if (firstIndex < 0) { firstIndex = i; firstSep = m[2]; }
      last = n;
      matched++;
      rows.push({ n: n, text: m[3] == null ? "" : m[3] });
    }
    if (!matched) return null;
    /* 单行也必须解析出来：grep 只命中一条、或翻到最后一页时，output_read 就只返回一行。
       不解析的话侧栏行号和正文自带的 "N:" 会一起出现（两列号码），连复制出去的都是带前缀的原文。
       一行没有「递增」可比对，换两条弱一些的佐证兜底：分隔符必须真的出现过
       ——否则 "1842" 这种纯数字正文会被当成一个空内容的行号，正文直接消失——
       且号码不小于 offset。 */
    if (matched === 1 && (firstSep === "" || rows[firstIndex].n < minLine)) return null;
    return {
      rows: rows,
      consecutive: consecutive,
      start: rows[firstIndex].n - firstIndex,
      first: rows[firstIndex].n,
      last: last
    };
  }

  function plainText(rows) {
    var out = [];
    for (var i = 0; i < rows.length; i++) out.push(rows[i].text);
    return out.join("\n");
  }

  // 行号不连续时手工搭终端块。class 与 ui.terminal 完全一致，视觉上看不出区别，
  // 区别只在行号列填的是原文里的真实号码，而不是顺推出来的。
  // 既然是复刻，ui.terminal 那两道防卡死闸门也得一并复刻，否则这条分支等于绕开了保护。
  function gutterTerm(rows, title, fold) {
    /* output_read 一页可达 5000 行（limit 上限），每行长度不设限：一页上千条长匹配
       全建成节点，光是布局测量就能把 iframe 卡死。所以先按字符上限裁一刀，
       而且必须裁在建节点之前——裁完还要留一条明确提示，否则用户会以为 grep 就只匹配到这些。 */
    var view = [], used = 0, dropped = 0, clipped = false;
    for (var i = 0; i < rows.length; i++) {
      if (used >= TEXT_CAP) { dropped = rows.length - i; break; }
      var text = rows[i].text;
      if (used + text.length > TEXT_CAP) {
        // 和 ui.terminal 一样从行中间切：单行就撑爆预算时，半行内容仍比整行丢掉有用
        text = text.slice(0, TEXT_CAP - used);
        clipped = true;
      }
      view.push({ n: rows[i].n, text: text });
      used += text.length + 1;   // +1 抵掉行分隔符，字符口径与 ui.terminal 的整段正文一致
    }

    var box = h("div", { class: "term" });
    var actions = h("div", { class: "term-bar-actions" });
    box.appendChild(h("div", { class: "term-bar" },
      h("span", { class: "term-title", text: title }), actions));
    // 终端行不换行，长匹配的后半截只能横向滚动看到；滚动容器不可聚焦，纯键盘用户就读不到那截
    var body = h("div", { class: "term-body", tabindex: "0", role: "region", "aria-label": title });
    box.appendChild(body);

    function line(number, text, dim) {
      var tx = h("span", { class: "term-tx", text: text });
      if (dim) tx.style.setProperty("color", "var(--term-dim)");
      return h("div", { class: "term-line" },
        h("span", { class: "term-ln", text: number == null ? "" : String(number) }), tx);
    }

    var cap = fmt.num(TEXT_CAP);
    var cutLine = null;
    if (dropped > 0) {
      // 说清「还有多少行」和「怎么拿到它们」：这一页翻不出被裁的部分，只能靠收窄查询
      cutLine = line(null, t(
        "… 内容过长，已截断至 " + cap + " 字符，另有 " + fmt.num(dropped) + " 行未渲染；缩小 limit 或收紧 grep 可以看到其余部分",
        "… truncated at " + cap + " chars; " + fmt.num(dropped) + " more lines not rendered — narrow limit or tighten grep"), true);
    } else if (clipped) {
      cutLine = line(null, t("… 这一行过长，已截断至 " + cap + " 字符",
        "… this line was truncated at " + cap + " chars"), true);
    }

    var shown = 0, restLine = null, toggle = null;

    function syncToggle() {
      if (!toggle) return;
      var left = view.length - shown;
      toggle.setAttribute("aria-expanded", shown > fold ? "true" : "false");
      toggle._label.textContent = left === 0
        ? t("收起", "Collapse")
        : (left <= TERM_PAGE
          ? t("展开全部（" + fmt.num(view.length) + " 行）", "Show all " + fmt.num(view.length) + " lines")
          : t("再展开 " + TERM_PAGE + " 行（还有 " + fmt.num(left) + " 行）",
            "Show " + TERM_PAGE + " more of " + fmt.num(left) + " lines"));
    }

    // 按页追加而不是一次铺完：字符上限之内照样能有几千行，一次建完节点同样会卡
    function paintTo(upTo) {
      if (restLine) { restLine.remove(); restLine = null; }
      if (cutLine) cutLine.remove();
      var end = Math.min(view.length, upTo);
      for (var j = shown; j < end; j++) body.appendChild(line(view[j].n, view[j].text, false));
      shown = end;
      if (shown < view.length) {
        restLine = line(null, t("… 还有 ", "… ") + fmt.num(view.length - shown) + t(" 行", " more lines"), true);
        body.appendChild(restLine);
      }
      if (cutLine) body.appendChild(cutLine);   // 截断提示永远压在最后一行
      syncToggle();
    }

    function collapse() {
      while (body.firstChild) body.removeChild(body.firstChild);
      restLine = null;
      shown = 0;
      paintTo(fold);
    }

    if (view.length > fold) {
      toggle = ui.button({
        label: "",   // 文案随展开进度变，交给 syncToggle 填
        onClick: function () {
          if (shown >= view.length) collapse();
          else paintTo(shown + Math.max(TERM_PAGE, fold));
        }
      });
      actions.appendChild(toggle);
    }
    // 复制的是裁剪后的 view 而非原始 rows：交出去的文本得和眼前看到的一致，
    // 否则「复制」会悄悄塞进一份卡片从未渲染、也没提示过的内容。
    actions.appendChild(ui.copy(plainText(view), t("复制", "Copy")));
    paintTo(fold);
    return box;
  }

  /* ------------------------------------------------------------------ *
   * exec
   * ------------------------------------------------------------------ */

  OneSSH.view("exec", function (data, ctx) {
    var args = argsOf(ctx);
    var command = str(args.command) || str(data.command);
    var cwd = str(data.cwd);
    var session = str(args.session);
    var artifact = str(data.artifact_id);
    var code = int(data.exit_code);

    var chips = [hostChip(str(args.host))];
    // default 会话是隐式的，标出来只会占地方；非默认会话意味着这条命令带着上下文，必须显眼
    if (session && session !== "default") {
      chips.push(ui.chip(t("会话 ", "session ") + session, { mono: true }));
    }

    var actions = [];
    if (data.truncated === true && artifact && ctx.can("output_read")) {
      actions.push(ui.button({
        label: t("查看完整输出", "Full output"),
        /* 跳到另一张卡看全量，和 job_status 的「查看日志」是同一件事，就得长得一样：
           ghost 底 + link(↗) 图标。实心按钮留给将来真正需要抢焦点的场合——这里不需要，
           截断这件事已经由正文那条 warn note 说清楚了，再用配色喊一遍只会盖过卡片本身的结论。
           也不用 expand：那个四角括号图标归 runtime 注入的全屏按钮独用。 */
        icon: "link",
        onClick: function () {
          ctx.navigate("output_read",
            { artifact_id: artifact, offset: 1, limit: 200 },
            t("完整输出", "Full output"));
        }
      }));
    }

    var totalLines = int(data.total_lines);
    if (totalLines === null) totalLines = lineCount(str(data.output) || str(data.stdout));
    var totalBytes = int(data.total_bytes);
    var stderrBytes = int(data.stderr_bytes);
    if (stderrBytes === null) stderrBytes = str(data.stderr).length;

    var body = [ui.metrics([
      { label: t("输出行数", "Lines"), value: fmt.num(totalLines) },
      // 值显示的是 KB/MB，标签再写「字节」就自相矛盾；量纲交给值，标签只说这是什么东西
      { label: t("输出大小", "Output size"), value: totalBytes === null ? null : fmt.bytes(totalBytes) },
      /* 这一格原本是 cwd，但那串路径已经写在副标题里，而且四格一行的指标条是给人横扫数值的，
         中间插一个长字符串会把对齐节奏打断。换成退出码：exec 唯一还没进指标条的数值，
         写法与 job_status 的同名格子一致。 */
      {
        label: t("退出码", "Exit code"),
        // 负数是网关侧的哨兵值，不是进程真的返回了负数，只能按「没拿到」显示
        value: code === null || code < 0 ? "" : String(code),
        kind: code === 0 ? "ok" : (code === null || code < 0 ? "muted" : "danger")
      },
      {
        label: t("标准错误", "Stderr"),
        value: fmt.bytes(stderrBytes),
        kind: stderrBytes > 0 ? "warn" : "muted"
      }
    ])];

    if (data.truncated === true) {
      if (artifact) {
        body.push(ui.note(t(
          "输出已截断，卡片里只展示了保留下来的部分；完整内容存在 artifact " + artifact + " 中，保留 7 天。",
          "Output was truncated; the full text is kept in artifact " + artifact + " for 7 days."), "warn"));
      } else {
        body.push(ui.note(t(
          "输出已截断，且没有留下 artifact，被截断的部分无法再取回。",
          "Output was truncated and no artifact was kept; the dropped part is gone."), "warn"));
      }
    }

    var out = outputNode(data, { title: t("输出", "Output"), fold: TERM_FOLD });
    if (out) {
      body.push(out);
    } else if (code === 0) {
      body.push(ui.empty(t("命令执行成功，没有任何输出", "Command succeeded with no output")));
    } else if (code === null) {
      body.push(ui.empty(t("没有输出，也没有拿到退出状态",
        "No output and no exit status was reported")));
    } else {
      body.push(ui.empty(t("命令没有任何输出，只返回了退出状态",
        "Command produced no output, only an exit status")));
    }

    var captureError = str(data.output_capture_error);
    if (captureError) {
      body.push(ui.note(t("输出留存失败：", "Failed to record output: ") + captureError, "warn"));
    } else if (data.output_recorded === false && data.truncated === true) {
      body.push(ui.note(t("本次输出没有被记录，无法用 output_read 回看。",
        "This output was not recorded, so output_read cannot replay it."), "warn"));
    }

    return ui.card({
      kicker: "EXEC",
      title: command || t("命令执行", "Command"),
      subtitle: cwd,
      chips: chips,
      status: execStatus(data, args),
      actions: actions,
      body: ui.stack.apply(null, body)
    });
  });

  /* ------------------------------------------------------------------ *
   * session_env
   * ------------------------------------------------------------------ */

  OneSSH.view("session_env", function (data, ctx) {
    var args = argsOf(ctx);
    var env = obj(data.env) || {};
    var names = Object.keys(env).sort();
    var session = str(args.session) || "default";
    var justSet = obj(args.set) || {};
    var setCount = countOf(args.set);
    var unsetCount = countOf(args.unset);

    var body = [];
    if (setCount > 0 || unsetCount > 0) {
      var text;
      if (setCount > 0 && unsetCount > 0) {
        text = t("本次设置了 " + setCount + " 项、删除了 " + unsetCount + " 项",
          "Set " + setCount + ", removed " + unsetCount);
      } else if (setCount > 0) {
        text = t("本次设置了 " + setCount + " 项", "Set " + setCount + " variable(s)");
      } else {
        text = t("本次删除了 " + unsetCount + " 项", "Removed " + unsetCount + " variable(s)");
      }
      if (setCount > 0) text += t("，下表中新值已标绿", "; new values are highlighted below");
      body.push(ui.note(text, "ok"));
    }

    if (!names.length) {
      body.push(ui.empty(t("该会话没有设置任何环境变量",
        "This session has no environment variables")));
    } else {
      body.push(ui.kv(names.map(function (name) {
        return {
          label: name,
          value: str(env[name]),
          mono: true,
          copy: true,
          // 刚写进去的变量标绿，用户一眼能确认这次改动确实生效了
          kind: Object.prototype.hasOwnProperty.call(justSet, name) ? "ok" : null
        };
      })));
    }

    return ui.card({
      kicker: "ENV",
      title: t("会话环境变量", "Session environment"),
      chips: [hostChip(str(args.host)), ui.chip(t("会话 ", "session ") + session, { mono: true })],
      status: ui.pill("info", t(names.length + " 项", names.length + " vars")),
      body: ui.stack.apply(null, body)
    });
  });

  /* ------------------------------------------------------------------ *
   * output_read
   * ------------------------------------------------------------------ */

  OneSSH.view("output_read", function (data, ctx) {
    var args = argsOf(ctx);
    var content = str(data.content);
    var artifact = str(args.artifact_id);
    var grep = str(args.grep);
    var total = int(data.total_lines);
    var offset = int(args.offset);
    if (offset === null || offset < 1) offset = 1;
    var limit = int(args.limit);

    var title = artifact ? "artifact " + artifact : t("完整输出", "Full output");
    var parsed = content ? parseGutter(content, offset) : null;
    var shown = parsed ? parsed.rows.length : lineCount(content);
    var first = parsed ? parsed.first : offset;
    var last = parsed ? parsed.last : (shown ? offset + shown - 1 : offset);

    var status = [];
    if (!shown) {
      status.push(ui.pill("muted", t("没有内容", "Empty")));
    } else {
      status.push(ui.pill("info", total === null
        ? t("第 " + fmt.num(first) + "–" + fmt.num(last) + " 行",
            "lines " + fmt.num(first) + "–" + fmt.num(last))
        : t("第 " + fmt.num(first) + "–" + fmt.num(last) + " 行 / 共 " + fmt.num(total) + " 行",
            "lines " + fmt.num(first) + "–" + fmt.num(last) + " of " + fmt.num(total))));
      if (grep) status.push(ui.pill("warn", t(fmt.num(shown) + " 行匹配", fmt.num(shown) + " matched")));
    }

    var body = [];
    if (!shown) {
      body.push(ui.empty(t("这一段没有内容，可能已经翻过了输出末尾",
        "Nothing in this range; the output may have ended earlier")));
    } else if (parsed && parsed.consecutive) {
      body.push(ui.terminal({
        text: plainText(parsed.rows),
        startLine: parsed.start,
        collapsedLines: ARTIFACT_FOLD,
        title: title
      }));
    } else if (parsed) {
      body.push(gutterTerm(parsed.rows, title, ARTIFACT_FOLD));
    } else {
      body.push(ui.terminal({
        text: content,
        startLine: offset,
        collapsedLines: ARTIFACT_FOLD,
        title: title
      }));
    }

    // 翻页会真的再调一次工具，所以只有宿主允许回调时才画出来
    if (ctx.can("output_read")) {
      var step = limit !== null && limit > 0 ? limit : (shown || 200);
      var atStart = offset <= 1;
      /* 翻页一律按「这次拿到了多少行」推进：普通读取时它等于 last + 1，
         而 grep 过滤过的正文里行号是跳跃的，用行号推进会一次跳过成百上千行。
         同理，拿回来的行数不足一页就说明后面没有了，比拿末行号跟总行数比更可靠。 */
      var nextOffset = offset + (shown || step);
      var atEnd = !shown || shown < step || (total !== null && last >= total);
      body.push(ui.row(
        ui.button({
          label: t("上一页", "Previous"),
          disabled: atStart,
          title: atStart ? t("已经是第一页", "Already at the first page") : null,
          onClick: function () {
            ctx.refresh({ offset: Math.max(1, offset - step), limit: step });
          }
        }),
        ui.button({
          label: t("下一页", "Next"),
          disabled: atEnd,
          title: atEnd ? t("已经到末尾", "Already at the end") : null,
          onClick: function () { ctx.refresh({ offset: nextOffset, limit: step }); }
        }),
        // 不用 chip：chip 带边框，会成为这一行里最像按钮的东西，而它恰恰不能点
        h("span", { class: "group-meta", text: t("每页 " + fmt.num(step) + " 行", fmt.num(step) + " lines/page") })
      ));
    }

    return ui.card({
      kicker: "OUTPUT",
      title: t("完整输出", "Full output"),
      subtitle: grep ? t("过滤：", "filter: ") + grep : "",
      chips: artifact ? [ui.chip(artifact, { mono: true, title: "artifact " + artifact })] : [],
      status: status,
      body: ui.stack.apply(null, body)
    });
  });

  /* ------------------------------------------------------------------ *
   * exec_many
   * ------------------------------------------------------------------ */

  function fanoutStatus(item) {
    var code = int(item.exit_code);
    if (str(item.error)) return ui.pill("danger", t("执行失败", "Failed"));
    if (item.timeout === true) return ui.pill("warn", t("超时", "Timed out"));
    if (code === 0) return ui.pill("ok", t("退出码 0", "Exit 0"));
    if (code === null) return ui.pill("muted", t("无退出码", "No exit code"));
    if (code < 0) return ui.pill("danger", t("未取得退出码", "No exit code"));
    return ui.pill("danger", t("退出码 " + code, "Exit " + code));
  }

  /* runtime 的 ui.group 只有静态标题栏，这里给它补上折叠：成功的主机默认收起，
     出问题的默认展开——批量执行时用户真正要找的就是那几台没跑通的机器。
     标题栏右侧另起一个 row 承载状态与摘要，避免 space-between 把 pill 甩到中间。 */
  function foldGroup(title, meta, statusPill, collapsed, nodes) {
    var sec = ui.group(title, "");
    var head = sec.querySelector(".group-head");
    var titleEl = head.querySelector(".group-title");
    var chevron = ui.icon("chevron");
    chevron.style.setProperty("transition", "transform .15s ease");
    titleEl.insertBefore(document.createTextNode(" "), titleEl.firstChild);
    titleEl.insertBefore(chevron, titleEl.firstChild);

    var right = h("div", { class: "row" });
    if (statusPill) right.appendChild(statusPill);
    if (meta) right.appendChild(h("span", { class: "group-meta", text: meta }));
    head.appendChild(right);

    var body = sec.body;
    for (var i = 0; i < nodes.length; i++) {
      if (nodes[i]) body.appendChild(nodes[i]);
    }

    function apply() {
      head.setAttribute("aria-expanded", collapsed ? "false" : "true");
      // group-body 在 CSS 里是 grid，靠 hidden 属性关不掉，只能改内联 display
      if (collapsed) body.style.setProperty("display", "none");
      else body.style.removeProperty("display");
      chevron.style.setProperty("transform", collapsed ? "rotate(-90deg)" : "rotate(0deg)");
    }
    function toggle() { collapsed = !collapsed; apply(); }
    head.setAttribute("role", "button");
    head.setAttribute("tabindex", "0");
    head.style.setProperty("cursor", "pointer");
    head.style.setProperty("align-items", "center");
    head.addEventListener("click", toggle);
    head.addEventListener("keydown", function (event) {
      if (event.key === "Enter" || event.key === " ") { event.preventDefault(); toggle(); }
    });
    apply();
    return sec;
  }

  OneSSH.view("exec_many", function (data, ctx) {
    var args = argsOf(ctx);
    var results = (Array.isArray(data.results) ? data.results : []).filter(function (item) {
      return !!obj(item);
    });
    var total = results.length;
    var okCount = 0, timeoutCount = 0, failCount = 0;
    results.forEach(function (item) {
      if (str(item.error)) { failCount++; return; }
      if (item.timeout === true) { timeoutCount++; failCount++; return; }
      if (int(item.exit_code) === 0) okCount++; else failCount++;
    });

    var status;
    if (!total) status = ui.pill("muted", t("没有结果", "No results"));
    else if (failCount === 0) status = ui.pill("ok", t(total + " 台全部成功", "all " + total + " succeeded"));
    else status = ui.pill("danger", t(failCount + " 失败 / " + total + " 台",
      failCount + " failed of " + total));

    var body = [];
    if (!total) {
      body.push(ui.empty(t("没有主机返回结果，请确认 hosts 参数里的主机名可用",
        "No host returned a result; check the hosts argument")));
    } else {
      body.push(ui.metrics([
        { label: t("主机数", "Hosts"), value: fmt.num(total) },
        { label: t("成功", "Succeeded"), value: fmt.num(okCount), kind: okCount ? "ok" : "muted" },
        { label: t("失败", "Failed"), value: fmt.num(failCount), kind: failCount ? "danger" : "muted" },
        { label: t("超时", "Timed out"), value: fmt.num(timeoutCount), kind: timeoutCount ? "warn" : "muted" }
      ]));

      results.forEach(function (item, index) {
        var host = str(item.host) || t("主机 #", "host #") + (index + 1);
        var error = str(item.error);
        var out = str(item.output);
        var good = !error && item.timeout !== true && int(item.exit_code) === 0;
        var rows = lineCount(out);
        /* 批量执行的重点是横向比对各台的结果。输出只有一行时（版本号、状态字这类）
           直接把这行正文当摘要，用户不点开就能比；多行时行数才是有用的信息。 */
        var firstLine = rows === 1 ? fmt.lines(out)[0] : "";
        var meta = firstLine && firstLine.length <= 60
          ? firstLine
          : (rows ? t(fmt.num(rows) + " 行", fmt.num(rows) + " lines") : t("无输出", "no output"));

        var nodes = [];
        // 连不上的主机没有「输出」可看，真正要读的是那句连接错误，用 note 比终端块更直给
        if (error) nodes.push(ui.note(error, "danger"));
        /* 终端标题只标内容性质，不重复上方分组标题里已经出现过的主机名——
           同一个名字连着出现两遍，读者会以为这是两个不同的东西。 */
        if (out) nodes.push(ui.terminal({ text: out, title: t("输出", "Output"), collapsedLines: FANOUT_FOLD }));
        if (!nodes.length) nodes.push(ui.empty(t("这台主机没有输出", "No output from this host")));

        body.push(foldGroup(host, meta, fanoutStatus(item), good, nodes));
      });
    }

    return ui.card({
      kicker: "FANOUT",
      title: str(args.command) || t("批量执行", "Fan-out"),
      subtitle: total ? t(total + " 台主机", total + " hosts") : "",
      status: status,
      body: ui.stack.apply(null, body)
    });
  });
})();
