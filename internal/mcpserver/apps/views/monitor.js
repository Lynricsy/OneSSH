;(function () {
  "use strict";

  /* 监控组卡片：host_status。
     一份资源快照的阅读顺序是「有没有事 → 是哪一项 → 具体多少」，
     所以结论压在卡片头的 pill 上（折叠后仍然可见），四个关键数走 metrics，
     CPU / 内存用条形把「还剩多少」画出来，磁盘按使用率倒序进表格。
     倒序是这张卡的关键：会先写满的那块盘必须落在第一行，
     否则它会被 /boot、/snap 这类永远很空的挂载点挤出视线。 */

  var ui = OneSSH.ui, fmt = OneSSH.fmt, h = OneSSH.h, t = OneSSH.t;

  // 阈值与 ui.bar 的自动着色保持一致：条已经变红、结论 pill 却写「正常」，
  // 用户就不知道该信哪一个了。
  var WARN_AT = 75;
  var DANGER_AT = 90;

  /* ------------------------------------------------------------------ *
   * 取值：cpu_pct / mem_* / load1 都可能是 null，而 0 是完全合法的采样值，
   * 所以一律走「是不是有限数值」的判断，绝不用 falsy 顺手把 0 丢掉。
   * ------------------------------------------------------------------ */

  function numOf(value) {
    if (typeof value === "string" && value.trim() !== "") value = Number(value);
    return typeof value === "number" && isFinite(value) ? value : null;
  }

  function textOf(value) {
    return typeof value === "string" ? value : "";
  }

  function listOf(value) {
    return Array.isArray(value) ? value : [];
  }

  // 只有分母是正数时百分比才有意义：total_kb 缺失或为 0 时返回 null，
  // 交给 ui.bar 画「无数据」，好过算出 Infinity 或一个看着很安全的 0%。
  function ratio(used, total) {
    var u = numOf(used), all = numOf(total);
    if (u === null || all === null || all <= 0) return null;
    return u / all * 100;
  }

  /* level 同时是所有进度条的 kind。ui.bar 不给 kind 时「健康」会落到默认的 --accent 青绿，
     而同一屏里 memory_stats 的健康条是 --ok 绿、--info 蓝，三种「没问题」的颜色摆在一起，
     用户没法判断它们是不是三档严重度。这里一律显式给 ok，让绿色是唯一的「健康」，
     --accent 不再出现在进度条里。 */
  function level(p) {
    if (p === null) return null;
    if (p >= DANGER_AT) return "danger";
    if (p >= WARN_AT) return "warn";
    return "ok";
  }

  // 正常值不着色：一屏里只有真正需要处理的数字带颜色，眼睛才会被带到该去的地方。
  function alertKind(p) {
    var lv = level(p);
    return lv === "warn" || lv === "danger" ? lv : null;
  }

  // 折叠后卡片只剩头部，pill 里点名「是哪块盘 / 是内存」比一句「资源紧张」有用得多。
  function tightest(gauges) {
    var top = null;
    gauges.forEach(function (item) {
      if (item.pct === null) return;
      if (!top || item.pct > top.pct) top = item;
    });
    return top;
  }

  // 已用 / 总量 有一半缺失时也要说清是哪一半，含糊的「—」会让人以为整台机器没数据。
  function memText(used, total) {
    var u = numOf(used), all = numOf(total);
    if (u === null && all === null) return null;
    if (all === null) return t("已用 ", "used ") + fmt.kb(u);
    if (u === null) return t("总量 ", "total ") + fmt.kb(all);
    return fmt.kb(u) + " / " + fmt.kb(all);
  }

  /* ------------------------------------------------------------------ *
   * 卡片头
   * ------------------------------------------------------------------ */

  // ts 缺失时不能拼出「后台轮询 · 」这种半截副标题。
  function subtitleOf(data, ctx) {
    var fresh = ctx && ctx.input && ctx.input.fresh === true;
    var mode = fresh ? t("现场采样", "Live sample") : t("后台轮询", "Background poll");
    var when = fmt.rel(data.ts);
    return when ? mode + " · " + when : mode;
  }

  function chipsOf(data, host) {
    var id = numOf(data.host_id);
    var label = host || (id === null ? "" : "host #" + id);
    if (!label) return [];
    // 主机名是人给的别名，host_id 才是这条记录在库里的身份，挂在 title 上备查。
    return [ui.chip(label, { mono: true, title: id === null ? null : "host_id " + id })];
  }

  // pill 是 nowrap 的，卡头也没给它留收缩空间，一个 /var/lib/docker/overlay2/… 这样的
  // 长挂载点会把标题整个挤出去，所以只留尾段，完整路径回到 title 里。
  var LABEL_MAX = 22;

  function shortLabel(name) {
    var s = textOf(name);
    return s.length > LABEL_MAX ? "…" + s.slice(s.length - LABEL_MAX + 1) : s;
  }

  /* 「到底是哪块盘要满了」是整张卡最可操作的一条信息，原先它单独挂在一颗 muted 灰 pill 上，
     反而成了卡头里对比度最低的元素（深色下是深灰底上的灰字，比旁边的主机 chip 还弱）。
     现在把它并进结论 pill：一颗按严重度着色的 pill 同时回答「有没有事」和「是哪一项」，
     折叠后卡片只剩头部时，这一颗也仍然把话说完整。 */
  function statusOf(top) {
    var lv = top ? level(top.pct) : null;
    var kind, text;
    if (lv === "danger") { kind = "danger"; text = t("资源紧张", "Critical"); }
    else if (lv === "warn") { kind = "warn"; text = t("接近上限", "Tight"); }
    else if (lv === "ok") { kind = "ok"; text = t("正常", "Healthy"); }
    else { kind = "muted"; text = t("暂无指标", "No metrics"); }

    // 正常 / 无指标时点名没有意义：没有哪一项需要处理，多写一截只会稀释结论。
    var full = null;
    if (top && (lv === "warn" || lv === "danger")) {
      var name = textOf(top.label);
      var shown = shortLabel(name);
      text += " · " + shown + " " + fmt.pct(top.pct);
      if (shown !== name) full = name + " " + fmt.pct(top.pct);
    }

    var node = ui.pill(kind, text);
    if (full) node.setAttribute("title", full);   // 截断过才挂 title，否则是句废话
    return [node];
  }

  // 宿主不支持工具回调时按钮点了没反应，不如不画，免得用户以为卡片坏了。
  function actionsOf(ctx) {
    if (!ctx.can("host_status")) return null;
    var list = [ui.button({
      label: t("刷新", "Refresh"),
      icon: "refresh",
      title: t("按同样的参数重新读一次", "Re-read with the same arguments"),
      onClick: function () { ctx.refresh({}); }
    })];
    // 这次本来就是现场采样，refresh({}) 会原样带上 fresh:true，
    // 再画一个同义按钮只会让人以为两者有区别。
    if (ctx.input && ctx.input.fresh === true) return list;
    list.push(ui.button({
      label: t("现场采样", "Sample now"),
      title: t("跳过后台轮询的缓存，直接到主机上取一次", "Skip the cached poll and sample the host directly"),
      onClick: function () { ctx.refresh({ fresh: true }); }
    }));
    return list;
  }

  /* ------------------------------------------------------------------ *
   * 正文
   * ------------------------------------------------------------------ */

  function metricsOf(data, cpu, memPct, mounts) {
    var load1 = numOf(data.load1);
    return ui.metrics([
      {
        label: t("CPU", "CPU"),
        value: cpu === null ? null : fmt.pct(cpu),
        kind: alertKind(cpu)
      },
      {
        label: t("内存", "Memory"),
        value: memText(data.mem_used_kb, data.mem_total_kb),
        kind: alertKind(memPct),
        hint: memPct === null ? null : t("已用 ", "used ") + fmt.pct(memPct)
      },
      {
        label: t("1 分钟负载", "Load 1m"),
        // 负载不知道这台机器有几个核，换算不成百分比，所以只如实给数、不着色。
        value: load1 === null ? null : fmt.num(load1),
        hint: t("最近 1 分钟的平均运行队列长度，需要对照 CPU 核数看",
          "Average run-queue length over the last minute; read it against the core count")
      },
      {
        label: t("挂载点", "Mounts"),
        // 0 个挂载点是有效结论（没采到盘），必须显示成 0 而不是「—」。
        value: fmt.num(mounts)
      }
    ]);
  }

  function gaugeBars(data, cpu, memPct) {
    var cpuBar = ui.bar({
      pct: cpu,
      kind: level(cpu),
      label: "CPU",
      detail: cpu === null ? t("无数据", "No data") : fmt.pct(cpu)
    });
    var text = memText(data.mem_used_kb, data.mem_total_kb);
    var memDetail;
    if (memPct === null) memDetail = text || t("无数据", "No data");
    else memDetail = (text ? text + " · " : "") + fmt.pct(memPct);
    var memBar = ui.bar({ pct: memPct, kind: level(memPct), label: t("内存", "Memory"), detail: memDetail });
    // 两条同类的量表放同一层，靠 .bar + .bar 的间距贴在一起，读起来是一组而不是两件事。
    return h("div", null, cpuBar, memBar);
  }

  function diskRows(data) {
    var rows = listOf(data.disks)
      .filter(function (item) { return item && typeof item === "object"; })
      .map(function (item) {
        var used = numOf(item.used_kb);
        var total = numOf(item.total_kb);
        return {
          mount: textOf(item.mount),
          used: used,
          total: total,
          // 可用量只能在两端都拿到时算，否则宁可留空也不给一个编出来的数字。
          free: used === null || total === null ? null : Math.max(0, total - used),
          pct: ratio(used, total)
        };
      });
    // 使用率高的排前面；算不出使用率的沉底 —— 「未知」不该占据最危险的那个位置。
    rows.sort(function (a, b) {
      if (a.pct === null && b.pct === null) return 0;
      if (a.pct === null) return 1;
      if (b.pct === null) return -1;
      return b.pct - a.pct;
    });
    return rows;
  }

  function usageCell(row) {
    var bar = ui.bar({
      pct: row.pct,
      kind: level(row.pct),
      // 百分比放在 label 位（表格里靠左更好扫），detail 显式给空串以免 ui.bar 把百分比再画一遍。
      label: row.pct === null ? t("未知", "Unknown") : fmt.pct(row.pct),
      detail: ""
    });
    bar.style.setProperty("min-width", "116px");   // 条太窄就失去了「一眼看长短」的意义
    return bar;
  }

  function diskTable(rows) {
    return ui.table({
      columns: [
        {
          label: t("挂载点", "Mount"), mono: true,
          // 首列会被 CSS 截断，完整路径留在 title 里，长挂载点仍然可辨认。
          render: function (row) { return row.mount ? h("span", { title: row.mount, text: row.mount }) : null; }
        },
        { label: t("已用", "Used"), align: "right", render: function (row) { return fmt.kb(row.used); } },
        { label: t("总量", "Size"), align: "right", secondary: true, render: function (row) { return fmt.kb(row.total); } },
        { label: t("可用", "Free"), align: "right", secondary: true, render: function (row) { return fmt.kb(row.free); } },
        { label: t("使用率", "Usage"), render: usageCell }
      ],
      rows: rows
    });
  }

  /* ------------------------------------------------------------------ *
   * host_status —— 一台主机的资源快照
   * ------------------------------------------------------------------ */

  OneSSH.view("host_status", function (data, ctx) {
    data = data || {};
    var host = ctx && ctx.input ? textOf(ctx.input.host) : "";
    var cpu = numOf(data.cpu_pct);
    var memPct = ratio(data.mem_used_kb, data.mem_total_kb);
    var rows = diskRows(data);

    // 结论只看拿得到的维度：缺数据不等于告警，把 null 算成 0% 或 100% 都是误导。
    var gauges = [{ label: "CPU", pct: cpu }, { label: t("内存", "Memory"), pct: memPct }];
    rows.forEach(function (row) {
      gauges.push({ label: row.mount || t("磁盘", "Disk"), pct: row.pct });
    });
    var top = tightest(gauges);

    var body;
    var hasNumbers = cpu !== null || memPct !== null ||
      numOf(data.mem_used_kb) !== null || numOf(data.mem_total_kb) !== null || numOf(data.load1) !== null;
    if (!hasNumbers && !rows.length) {
      // 调用成功但一个指标都没有，多半是监控没开或采集器还没上报，说清楚比留一片空白强。
      body = ui.stack(
        ui.empty(t("这次采样没有返回任何指标", "This sample returned no metrics")),
        ui.note(t("这台主机可能没有开启监控，或者采集器还没上报第一份数据。",
          "Monitoring may be disabled on this host, or the collector has not reported yet."), "warn"));
    } else {
      var disks = ui.group(t("磁盘", "Disks"),
        rows.length ? fmt.num(rows.length) + t(" 个挂载点", " mounts") : "");
      disks.body.appendChild(rows.length
        ? diskTable(rows)
        : ui.empty(t("没有采集到磁盘信息", "No disk data collected")));
      body = ui.stack(
        metricsOf(data, cpu, memPct, rows.length),
        gaugeBars(data, cpu, memPct),
        disks);
    }

    return ui.card({
      kicker: "STATUS",
      title: host || t("资源指标", "Resource metrics"),
      subtitle: subtitleOf(data, ctx),
      chips: chipsOf(data, host),
      status: statusOf(top),
      actions: actionsOf(ctx),
      body: body
    });
  });
})();
