;(function () {
  "use strict";

  /* 主机组卡片。这一组里有三种性质完全不同的结果，因此刻意用三种不同的载体来承载：
     hosts_list / hosts_manage_list 是「多行同构」的清单 —— 表格；
     host_create / host_update 是「单机档案」—— kv；
     host_test / host_reset_fingerprint / host_delete 是「一次动作的回执」—— 结论 pill + 一句人话。
     把它们统一成字段列表会让用户每次都要重新读一遍才知道发生了什么。 */

  var ui = OneSSH.ui, fmt = OneSSH.fmt, h = OneSSH.h, t = OneSSH.t;

  // id / port / key_id / exit_code / total_lines 都可能是 null，而 0 是合法值，
  // 所以统一走「是不是有限数值」的判断，绝不用 falsy 顺手把 0 丢掉。
  function numOf(value) {
    return typeof value === "number" && isFinite(value) ? value : null;
  }

  function textOf(value) {
    return typeof value === "string" ? value : "";
  }

  function listOf(value) {
    return Array.isArray(value) ? value : [];
  }

  function hostsOf(data) {
    return listOf(data.hosts).filter(function (item) { return item && typeof item === "object"; });
  }

  function inputHost(ctx) {
    var value = ctx && ctx.input ? ctx.input.host : null;
    return typeof value === "string" ? value : "";
  }

  // 只有显式 true 才算在线：字段缺失时断言「离线」是误报，会让人去排查一台其实正常的机器。
  function onlinePill(online) {
    if (online === true) return ui.pill("ok", t("在线", "Online"));
    if (online === false) return ui.pill("muted", t("离线", "Offline"));
    return ui.pill("muted", t("状态未知", "Unknown"));
  }

  // 返回 null 而不是空容器：表格单元格会统一画「—」，空 div 反而会把行撑高。
  function tagCell(tags) {
    var list = cleanTags(tags);
    if (!list.length) return null;
    return ui.row.apply(null, list.map(function (tag) { return ui.chip(tag); }));
  }

  function cleanTags(tags) {
    return listOf(tags)
      .filter(function (tag) { return tag != null && tag !== ""; })
      .map(function (tag) { return String(tag); });
  }

  // hosts_list 的 addr 自带端口（Go 侧用 net.JoinHostPort 拼好再下发），管理接口则把 port
  // 拆了出来，这里只负责后者的拼接 —— 两张表并排时地址列必须是同一种写法。
  function endpoint(item) {
    var addr = textOf(item.addr);
    if (!addr) return "";
    var port = numOf(item.port);
    if (port === null) return addr;
    // IPv6 字面量必须套方括号：fe80::1:22 里的末段既可能是端口也可能是地址的一部分，
    // 而 sshpool 真正拨号用的也是 [fe80::1]:22，照抄这行才能直接粘进 ssh/scp。
    // addr 自带方括号时不再套一层，免得写出 [[fe80::1]]:22。
    var host = addr.indexOf(":") >= 0 && addr.charAt(0) !== "[" ? "[" + addr + "]" : addr;
    return host + ":" + port;
  }

  function authLabel(item) {
    var type = textOf(item.auth_type);
    var keyId = numOf(item.key_id);
    if (type === "key") return keyId === null ? t("密钥", "Key") : "key #" + keyId;
    if (type === "password") return t("密码", "Password");
    if (type === "agent") return "SSH Agent";
    return type;   // 未知认证方式原样透出：折成「其他」会让排错的人少一条线索
  }

  // 指纹很长且没人会逐字符读，但它是唯一能识别中间人的东西，所以缩写显示、完整值留在 title 里。
  function fingerprintCell(value) {
    var fp = textOf(value);
    if (!fp) return ui.pill("warn", t("未固定", "Unpinned"));
    return h("span", { title: fp, text: fmt.short(fp, 10, 6) });
  }

  // 这里曾经把文本和 title 写反了：整列 19 个字符的绝对时间既把表格撑宽一列，又和
  // file_list 的「修改时间」、job_list 的「开始/结束」、memory_stats 的「最后写入」
  // 撞成两种写法 —— 三张卡并排时同一列位置一会儿是「8 小时前」一会儿是长串日期。
  // 全站规则：表格单元格一律「相对时间当文本 + 绝对时间挂 title」，绝对时间只在
  // kv 明细块里（job_status 的开始/结束、host_create 的创建时间）直接写出来。
  // fmt.rel 超过 30 天也不会回落成绝对时间，而是一路铺到「N 个月前 / N 年前」，
  // 所以精确到秒的创建时间只能靠悬停 title 看，这正是表格该有的取舍。
  function stampCell(ts) {
    var text = fmt.rel(ts);
    if (!text) return null;
    return h("span", { title: fmt.time(ts), text: text });
  }

  // 宿主不支持工具回调时按钮点了没反应，不如不画，免得用户以为卡片坏了。
  function refreshAction(ctx, tool) {
    if (!ctx.can(tool)) return null;
    return ui.button({
      label: t("刷新", "Refresh"),
      icon: "refresh",
      title: t("重新拉取主机列表", "Reload the host list"),
      onClick: function () { ctx.refresh({}); }
    });
  }

  /* ------------------------------------------------------------------ *
   * hosts_list —— 令牌视角的可用主机
   * ------------------------------------------------------------------ */

  OneSSH.view("hosts_list", function (data, ctx) {
    data = data || {};
    var list = hostsOf(data);
    var online = list.filter(function (item) { return item.online === true; }).length;
    var offline = list.length - online;

    var status = [];
    if (list.length) {
      status.push(ui.pill("info", fmt.num(list.length) + t(" 台 · ", " hosts · ") + fmt.num(online) + t(" 在线", " online")));
      // 卡片折叠后只剩头部可见，掉线数单独成 pill 才不会被淹没在那串统计里。
      if (offline > 0) status.push(ui.pill("muted", fmt.num(offline) + t(" 离线", " offline")));
    } else {
      status.push(ui.pill("muted", t("没有主机", "No hosts")));
    }

    var body = list.length ? ui.table({
      columns: [
        { label: t("名称", "Name"), mono: true, render: function (item) { return textOf(item.name); } },
        { label: t("地址", "Address"), mono: true, render: function (item) { return textOf(item.addr); } },
        { label: t("用户", "User"), secondary: true, render: function (item) { return textOf(item.username); } },
        { label: t("状态", "State"), render: function (item) { return onlinePill(item.online); } },
        { label: t("标签", "Tags"), render: function (item) { return tagCell(item.tags); } }
      ],
      rows: list
    }) : ui.empty(t("当前令牌没有被授权访问任何主机", "This token is not authorized for any host"));

    return ui.card({
      kicker: "HOSTS",
      title: t("可用主机", "Available hosts"),
      status: status,
      actions: [refreshAction(ctx, "hosts_list")],
      body: body
    });
  });

  /* ------------------------------------------------------------------ *
   * hosts_manage_list —— 管理视角的完整配置
   * ------------------------------------------------------------------ */

  OneSSH.view("hosts_manage_list", function (data, ctx) {
    data = data || {};
    var list = hostsOf(data);
    var action = refreshAction(ctx, "hosts_manage_list");

    if (!list.length) {
      return ui.card({
        kicker: "HOSTS",
        title: t("主机配置", "Host configuration"),
        status: ui.pill("muted", t("没有主机", "No hosts")),
        actions: [action],
        body: ui.empty(t("还没有登记任何主机，可以用 host_create 添加一台",
          "No hosts registered yet; add one with host_create"))
      });
    }

    var online = 0, monitored = 0, pinned = 0;
    list.forEach(function (item) {
      if (item.online === true) online++;
      if (item.monitor_enabled === true) monitored++;
      if (textOf(item.hostkey_fp)) pinned++;
    });

    // 四个数字回答的是「这批主机健康吗、被看住了吗、安全基线补齐了吗」，
    // 比把同样的信息藏在表格里逐行数要快得多。
    var summary = ui.metrics([
      { label: t("主机总数", "Hosts"), value: fmt.num(list.length) },
      // 光一个「4」看不出是 4/5 还是 4/4；真正的告警交给表格里那一列 pill，指标条只负责报比例
      {
        label: t("在线", "Online"),
        value: fmt.num(online) + " / " + fmt.num(list.length),
        kind: online === list.length ? "ok" : "muted"
      },
      {
        label: t("监控已开", "Monitored"),
        value: fmt.num(monitored) + " / " + fmt.num(list.length),
        kind: "muted"
      },
      {
        label: t("指纹已固定", "Pinned"),
        value: fmt.num(pinned) + " / " + fmt.num(list.length),
        kind: pinned === list.length ? "ok" : "muted",
        hint: pinned === list.length ? null : t("未固定指纹的主机在首次连接时才会写入指纹",
          "Hosts without a pinned key get one on first connect")
      }
    ]);

    // 这张表刻意不做可点击行：本组能触发的只有管理类工具，误触的代价太大。
    var table = ui.table({
      columns: [
        { label: t("名称", "Name"), mono: true, render: function (item) { return textOf(item.name); } },
        { label: t("地址", "Address"), mono: true, render: function (item) { return endpoint(item); } },
        { label: t("用户", "User"), render: function (item) { return textOf(item.username); } },
        // 指标条会报「在线 4 / 5」，表里不给状态列的话，那台掉线的主机就无从查证
        { label: t("状态", "State"), render: function (item) { return onlinePill(item.online); } },
        { label: t("认证", "Auth"), render: function (item) { return authLabel(item); } },
        {
          label: t("跳板", "Jump"), secondary: true,
          render: function (item) { return textOf(item.jump_host) || t("直连", "Direct"); }
        },
        {
          label: t("监控", "Monitor"),
          render: function (item) {
            return item.monitor_enabled === true
              ? ui.pill("ok", t("开", "On"))
              : ui.pill("muted", t("关", "Off"));
          }
        },
        {
          label: t("指纹", "Host key"), secondary: true, mono: true,
          render: function (item) { return fingerprintCell(item.hostkey_fp); }
        },
        {
          label: t("创建时间", "Created"), secondary: true,
          render: function (item) { return stampCell(item.created_at); }
        }
      ],
      rows: list
    });

    return ui.card({
      kicker: "HOSTS",
      title: t("主机配置", "Host configuration"),
      status: ui.pill("info", fmt.num(list.length) + t(" 台", " hosts")),
      actions: [action],
      body: ui.stack(summary, table)
    });
  });

  /* ------------------------------------------------------------------ *
   * host_create / host_update —— 单机档案
   * ------------------------------------------------------------------ */

  function hostDetailCard(data, statusText, fallbackTitle, isNew) {
    var fp = textOf(data.hostkey_fp);
    var id = numOf(data.id);
    var keyId = numOf(data.key_id);
    var monitor = data.monitor_enabled === true;
    var tags = cleanTags(data.tags);

    // ui.kv 会自动跳过空值行，所以「不适用」的字段（如密码认证时的密钥 ID）留空即可消失，
    // 而「有意义的空」（跳板、标签、指纹）必须自己给出人话默认值，不能让它静默消失。
    var rows = [
      { label: t("地址", "Address"), value: endpoint(data), mono: true },
      { label: t("登录用户", "User"), value: textOf(data.username) },
      { label: t("认证方式", "Auth"), value: authLabel(data) },
      // authLabel 已经把 key_id 拼成「key #3」，正常情况下再列一行「密钥 ID」是同义重复；
      // 只有 auth_type 不是 key 却仍带着 key_id 这种反常组合才值得单独点出来。
      {
        label: t("密钥 ID", "Key ID"),
        value: keyId !== null && textOf(data.auth_type) !== "key" ? "#" + keyId : "",
        mono: true
      },
      { label: t("跳板主机", "Jump host"), value: textOf(data.jump_host) || t("直连", "Direct") },
      {
        label: t("监控", "Monitor"),
        value: monitor ? t("已启用", "Enabled") : t("已关闭", "Disabled"),
        kind: monitor ? "ok" : "muted"
      },
      { label: t("标签", "Tags"), value: tags.length ? tags.join(", ") : "—" },
      { label: t("主机 ID", "Host ID"), value: id === null ? "" : String(id), mono: true },
      { label: t("创建时间", "Created"), value: fmt.time(data.created_at) },
      fp
        ? { label: t("公钥指纹", "Host key"), value: fp, mono: true, copy: true }
        : {
            label: t("公钥指纹", "Host key"),
            value: t("尚未固定，首次连接时写入", "Not pinned yet; recorded on first connect"),
            // 底下那条 note 已经在说同一件事，这里再上一次 warn 色等于把一件事标两遍
            kind: "muted"
          }
    ];

    var body = [ui.kv(rows)];
    if (!fp) {
      // 同一张卡也用于 host_update：那时主机早就存在了，文案再自称「新主机」会和卡里的创建时间打架
      body.push(ui.note(isNew
        ? t("新主机的公钥指纹尚未固定，建议接着调用 host_test 完成 TOFU 固定与凭据验证",
          "The host key is not pinned yet; run host_test to finish TOFU pinning and verify the credentials")
        : t("这台主机还没有固定公钥指纹，调用 host_test 可完成 TOFU 固定与凭据验证",
          "This host has no pinned host key yet; run host_test to finish TOFU pinning and verify the credentials"),
        "warn"));
    }

    return ui.card({
      kicker: "HOST",
      title: textOf(data.name) || fallbackTitle,
      subtitle: endpoint(data),
      chips: [onlinePill(data.online)],
      status: ui.pill("ok", statusText),
      body: ui.stack.apply(null, body)
    });
  }

  OneSSH.view("host_create", function (data) {
    data = data || {};
    return hostDetailCard(data, t("已创建", "Created"), t("新主机", "New host"), true);
  });

  OneSSH.view("host_update", function (data) {
    data = data || {};
    return hostDetailCard(data, t("已更新", "Updated"), t("主机", "Host"), false);
  });

  /* ------------------------------------------------------------------ *
   * host_test —— 连通性与登录验证
   * ------------------------------------------------------------------ */

  OneSSH.view("host_test", function (data, ctx) {
    data = data || {};
    var host = inputHost(ctx);
    var code = numOf(data.exit_code);
    var timeout = data.timeout === true;
    var cwd = textOf(data.cwd);
    var lineCount = numOf(data.total_lines);
    var stderr = textOf(data.stderr);
    // execx.Runner 的 output 是「stdout 后接 stderr」拼成的合并流（再按 max_lines 截断），
    // 所以有 stderr 时拿 output 当标准输出，会让同一段报错在两个页签里各出现一遍。
    // 有 stderr 就只认 data.stdout；没有 stderr 时两者内容本就相同，但只有 output 走过
    // 行数截断，用它才和上面那个「输出行数」以及「已截断」提示对得上。
    // data.stdout 兜底是防御性的：万一网关只填了 stdout 没填 output，别把有内容说成空。
    var merged = textOf(data.output);
    var stdout = stderr ? textOf(data.stdout) : (merged || textOf(data.stdout));

    var status;
    if (timeout) status = ui.pill("warn", t("超时", "Timed out"));
    else if (code === 0) status = ui.pill("ok", t("连通", "Reachable"));
    else if (code === null) status = ui.pill("muted", t("结果未知", "Unknown"));
    else status = ui.pill("danger", t("退出码 ", "Exit ") + code);

    var body = [ui.metrics([
      {
        label: t("退出码", "Exit code"),
        value: code === null ? "" : String(code),
        kind: code === 0 ? "ok" : (code === null ? "muted" : "danger")
      },
      { label: t("工作目录", "Working dir"), value: cwd ? fmt.short(cwd, 16, 12) : "", hint: cwd || null },
      { label: t("输出行数", "Lines"), value: lineCount === null ? "" : fmt.num(lineCount) }
    ])];

    if (timeout) {
      body.push(ui.note(t("探测命令在超时前没有结束，通常是网络不通、端口被拦，或者登录卡在了交互式提示上",
        "The probe did not finish before the timeout: network blocked, port filtered, or login stuck on a prompt"), "warn"));
    }
    if (data.truncated === true) {
      body.push(ui.note(t("输出已被截断，只影响这里的显示，不影响连通性判断",
        "The output was truncated; this only affects the display, not the connectivity verdict"), "warn"));
    }

    if (!stdout && !stderr) {
      body.push(code === 0
        ? ui.empty(t("命令没有输出，退出码为 0 即表示连接与登录正常",
          "No output; exit code 0 already means the connection and login are fine"))
        : ui.empty(t("命令没有任何输出", "The command produced no output")));
    } else if (stderr) {
      // 有 stderr 就分栏：把报错和正常输出混在一起，用户要自己在文本里找问题出在哪。
      body.push(ui.tabs([
        {
          label: t("标准输出", "stdout"),
          node: stdout
            ? ui.terminal({ text: stdout, title: t("uptime 输出", "uptime output") })
            : ui.empty(t("标准输出为空", "stdout is empty"))
        },
        {
          label: t("标准错误", "stderr"),
          badge: fmt.bytes(data.stderr_bytes),
          node: ui.terminal({ text: stderr, title: t("标准错误", "stderr"), variant: "err", wrap: true })
        }
      ]));
    } else {
      body.push(ui.terminal({ text: stdout, title: t("uptime 输出", "uptime output") }));
    }

    return ui.card({
      kicker: "TEST",
      title: host || t("连通性测试", "Connectivity test"),
      subtitle: host ? t("连通性与登录验证", "Connectivity and login check") : "",
      status: status,
      body: ui.stack.apply(null, body)
    });
  });

  /* ------------------------------------------------------------------ *
   * host_reset_fingerprint / host_delete —— 动作回执
   * ------------------------------------------------------------------ */

  function hostChips(ctx) {
    var host = inputHost(ctx);
    return host ? [ui.chip(host, { mono: true, title: host })] : [];
  }

  OneSSH.view("host_reset_fingerprint", function (data, ctx) {
    data = data || {};
    var ok = data.ok === true;
    return ui.card({
      kicker: "HOST",
      title: t("重置公钥指纹", "Reset host key"),
      chips: hostChips(ctx),
      status: ok ? ui.pill("ok", t("已重置", "Reset")) : ui.pill("danger", t("未重置", "Not reset")),
      body: ok
        // 重置指纹等于自愿放弃一次 TOFU 保护，这条提醒必须比「成功」更显眼，所以用 warn 而不是 ok。
        ? ui.note(t("已清除固定指纹，下次连接会重新固定；如果指纹变化原因不明，请先排查中间人风险再连接",
          "The pinned key was cleared and will be re-pinned on the next connect; if you cannot explain why it changed, rule out a man-in-the-middle first"), "warn")
        : ui.empty(t("网关没有确认重置结果", "The gateway did not confirm the reset"))
    });
  });

  OneSSH.view("host_delete", function (data, ctx) {
    data = data || {};
    var ok = data.ok === true;
    return ui.card({
      kicker: "HOST",
      title: t("删除主机", "Delete host"),
      chips: hostChips(ctx),
      // 成功用绿：红色留给「这次调用失败了」，否则删成功和删失败在对话流里长得一模一样。
      // 「不可撤销」的分量由下面那条 danger note 来承担。
      status: ok ? ui.pill("ok", t("已删除", "Deleted")) : ui.pill("muted", t("未删除", "Not deleted")),
      body: ok
        ? ui.note(t("该主机的令牌授权、持久会话、后台任务记录、监控指标与记忆 bank 已一并删除，无法撤销",
          "Its token grants, persistent sessions, background job records, monitoring metrics and memory bank were removed as well; this cannot be undone"), "danger")
        : ui.empty(t("网关没有确认删除结果", "The gateway did not confirm the deletion"))
    });
  });
})();
