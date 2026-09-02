;(function () {
  "use strict";

  /* 记忆组卡片。这七个工具其实只有两种性质：
     memory_recall / memory_list 输出「一条条记忆」，用统一的记忆块承载正文 + 元信息，
     让人能像扫便签一样读过去；其余五个输出「一次写入的回执」，结论落在 status pill 上，
     正文只回答「改了什么、还差什么」，绝不把回执字段摊成一长串 kv 让人自己找结论。
     记忆的可信度与重要度决定了模型后续会不会采信它，所以这两项在每个记忆块里都必须一眼可见，
     不能降级成 kv 行。 */

  var ui = OneSSH.ui, fmt = OneSSH.fmt, h = OneSSH.h, t = OneSSH.t;

  // importance / score / recall_count / id / offset 都可能是 0，而 0 在这里全都是有效值
  // （从未召回、重要度归零、第一页），所以一律先判「是不是有限数值」，不用 falsy 顺手吃掉 0。
  function numOf(value) {
    return typeof value === "number" && isFinite(value) ? value : null;
  }

  function textOf(value) {
    return typeof value === "string" ? value : "";
  }

  function listOf(value) {
    return Array.isArray(value) ? value : [];
  }

  function objOf(value) {
    return value && typeof value === "object" ? value : {};
  }

  function argsOf(ctx) {
    return objOf(ctx && ctx.input);
  }

  function records(value) {
    return listOf(value).filter(function (item) { return item && typeof item === "object"; });
  }

  function fixed2(value) {
    var v = numOf(value);
    return v === null ? "" : v.toFixed(2);
  }

  /* ------------------------------------------------------------------ *
   * 记忆块：recall / list / remember / update 共用的正文载体
   * ------------------------------------------------------------------ */

  // 可信度是这条记忆值不值得信的唯一标记，因此同时映射到 pill 颜色和记忆块的左侧色条：
  // 用户即使只是快速下滑，也能靠左边那道颜色分辨哪些是工具亲眼看到的、哪些只是推断。
  // inferred 用 --muted 而不是 --border：色条画在 --bg-2 上，--border 与底色几乎同值，
  // 两个主题下那道条都会消失，看起来像这一块渲染坏了——偏偏「推断」正是最该被认出来的一档。
  // 这里要的是「有没有」而不是「醒不醒目」，所以取一个中性灰，pill 仍保持 muted。
  var VERACITY = {
    tool: { kind: "ok", color: "var(--ok)", zh: "工具观测", en: "Tool-observed" },
    stated: { kind: "info", color: "var(--info)", zh: "明确陈述", en: "Stated" },
    inferred: { kind: "muted", color: "var(--muted)", zh: "推断", en: "Inferred" },
    unknown: { kind: "warn", color: "var(--warn)", zh: "存疑", en: "Unverified" }
  };

  // 用 hasOwnProperty 取值：veracity 是从宿主来的字符串，直接下标会命中 toString 之类的原型属性。
  function veracityOf(value) {
    var key = textOf(value);
    if (Object.prototype.hasOwnProperty.call(VERACITY, key)) {
      var hit = VERACITY[key];
      var label = t(hit.zh, hit.en);
      return { pill: ui.pill(hit.kind, label), color: hit.color, label: label };
    }
    // 后端将来新增取值时原样透出，比折成「未知」更能保留线索
    if (key) return { pill: ui.pill("muted", key), color: "var(--muted)", label: key };
    return { pill: null, color: "var(--border)", label: "" };
  }

  function meta(text, title) {
    if (!text) return null;
    var el = h("span", {
      style: { "font-size": "11px", "color": "var(--faint)", "white-space": "nowrap" },
      text: text
    });
    if (title) el.setAttribute("title", title);
    return el;
  }

  // 左侧色条 + 浅底，形状刻意与 ui.note 一致：两者在卡片里都是「一段要读的话」。
  function panel(accent) {
    return h("div", {
      style: {
        "display": "grid",
        "gap": "7px",
        "min-width": "0",
        "padding": "8px 11px",
        "background": "var(--bg-2)",
        "border-left": "2px solid " + (accent || "var(--border)"),
        "border-radius": "0 var(--radius-sm) var(--radius-sm) 0"
      }
    });
  }

  // 记忆正文可能是多行的操作笔记，pre-wrap 才不会把换行压平成一坨。
  function bodyText(value) {
    var text = textOf(value);
    if (!text) return meta(t("（这条记忆没有正文）", "(this memory has no content)"));
    return h("div", {
      style: {
        "font-size": "13px", "line-height": "1.55", "color": "var(--text)",
        "white-space": "pre-wrap", "overflow-wrap": "anywhere"
      }
    }, text);
  }

  // ui.bar 在 75/90 会自动转黄转红，那套阈值是给「资源用量」准备的。
  // 重要度高恰恰是好事，所以显式指定 info，免得 0.9 的关键记忆被画成告警色。
  function importanceBar(value) {
    var v = numOf(value);
    if (v === null) return null;
    return h("div", { style: { "width": "132px", "flex": "0 0 auto" } },
      ui.bar({
        pct: Math.max(0, Math.min(100, v * 100)),
        label: t("重要度", "Weight"),
        detail: v.toFixed(2),
        kind: "info"
      }));
  }

  function memoryCard(item) {
    item = objOf(item);
    var ver = veracityOf(item.veracity);
    var box = panel(ver.color);

    var head = ui.row();
    var id = numOf(item.id);
    if (id !== null) head.appendChild(ui.chip("#" + id, { mono: true }));
    var bank = textOf(item.bank);
    if (bank) head.appendChild(ui.chip(bank, { mono: true }));
    if (ver.pill) head.appendChild(ver.pill);
    // 综合分决定了召回排序，放头部才好横向比较；关键词/向量分是它的拆解，留在脚注即可。
    var score = numOf(item.score);
    if (score !== null) head.appendChild(ui.pill("info", t("匹配 ", "Score ") + score.toFixed(2)));
    if (head.firstChild) box.appendChild(head);

    box.appendChild(bodyText(item.content));

    // ui.row 默认 align-items:center，而重要度块是「标签行 + 轨道」两层、比纯文本高一倍，
    // 居中会把右边整串元信息顶到「重要度」标签与进度条之间，一行里出现两条参差的基线。
    // 改成 baseline 对齐：flex 会取重要度块首行（bar-label）的基线，元信息就落在同一条线上。
    var foot = ui.row();
    foot.style.setProperty("align-items", "baseline");
    var bar = importanceBar(item.importance);
    if (bar) foot.appendChild(bar);

    var created = numOf(item.created_at);
    if (created !== null) {
      foot.appendChild(meta(t("创建于 ", "created ") + fmt.rel(created), fmt.time(created)));
    }
    // 只有真的被改过才提更新时间：两个时间戳相同时并排显示只会让人以为刚被人动过。
    var updated = numOf(item.updated_at);
    if (updated !== null && created !== null && updated > created) {
      foot.appendChild(meta(t("更新于 ", "updated ") + fmt.rel(updated), fmt.time(updated)));
    }
    var recall = numOf(item.recall_count);
    if (recall !== null) {
      foot.appendChild(meta(recall === 0
        ? t("从未召回", "never recalled")
        : t("召回 " + fmt.num(recall) + " 次", "recalled " + fmt.num(recall) + "x")));
    }
    var detail = [];
    var fts = fixed2(item.fts_score);
    var dense = fixed2(item.dense_score);
    if (fts) detail.push(t("关键词 ", "keyword ") + fts);
    if (dense) detail.push(t("向量 ", "vector ") + dense);
    if (detail.length) foot.appendChild(meta(detail.join(" · ")));

    if (foot.firstChild) box.appendChild(foot);
    return box;
  }

  function memoryStack(items) {
    return ui.stack.apply(null, items.map(function (item) { return memoryCard(item); }));
  }

  function engineLabel(engine) {
    if (engine === "hybrid") return t("混合检索", "Hybrid");
    if (engine === "fts") return t("关键词", "Keyword");
    if (engine === "dense") return t("向量", "Vector");
    return textOf(engine);
  }

  function bankChip(name) {
    var bank = textOf(name);
    return bank ? ui.chip(bank, { mono: true, title: t("记忆 bank：", "Memory bank: ") + bank }) : null;
  }

  // 宿主不支持工具回调时按钮点了没反应，不如不画，免得用户以为卡片坏了。
  function refreshAction(ctx, tool, title) {
    if (!ctx.can(tool)) return null;
    return ui.button({ label: t("刷新", "Refresh"), icon: "refresh", title: title, onClick: function () { ctx.refresh({}); } });
  }

  /* ------------------------------------------------------------------ *
   * memory_remember —— 写入回执
   * ------------------------------------------------------------------ */

  OneSSH.view("memory_remember", function (data, ctx) {
    data = objOf(data);
    var args = argsOf(ctx);
    var bank = textOf(data.bank) || textOf(args.host);
    var deduped = data.deduped === true;
    var embedded = data.embedded;
    var id = numOf(data.id);

    var chips = [bankChip(bank)];
    if (embedded === true) chips.push(ui.chip(t("已建向量", "Embedded")));
    else if (embedded === false) chips.push(ui.pill("muted", t("无向量", "No vector")));

    var body = [];
    // 用户最关心的是「到底记下了什么」，所以把正文按记忆块原样回显在最前面，
    // 顺带把本次写入的重要度与可信度画进去——这三项一起看才知道这条记忆将来会怎么被用。
    var content = textOf(args.content);
    if (content) {
      body.push(memoryCard({
        id: data.id, bank: bank, content: content,
        importance: args.importance, veracity: args.veracity
      }));
    }
    if (deduped) {
      body.push(ui.note(
        t("同一 bank 内已存在完全相同的正文，本次没有新增记录。",
          "An identical memory already exists in this bank; nothing new was stored."), "info"));
    }
    if (embedded === false) {
      body.push(ui.note(
        t("没有配置 embedding 服务，这条记忆只能靠关键词召回。",
          "No embedding service is configured; this memory can only be recalled by keyword."), "warn"));
    }
    body.push(ui.kv([
      { label: t("记忆 ID", "Memory ID"), value: id === null ? "" : "#" + id, mono: true },
      { label: "bank", value: bank, mono: true },
      {
        label: t("去重", "Dedup"),
        value: typeof data.deduped !== "boolean" ? ""
          : (deduped ? t("命中已有记录", "Reused an existing record") : t("新建记录", "Stored a new record"))
      },
      {
        label: t("向量", "Embedding"),
        value: embedded === true ? t("已建立", "Built")
          : (embedded === false ? t("未建立", "Not built") : "")
      }
    ]));

    return ui.card({
      kicker: "MEMORY",
      title: t("保存长期记忆", "Remember"),
      chips: chips,
      status: deduped ? ui.pill("muted", t("命中已有记忆", "Duplicate")) : ui.pill("ok", t("已保存", "Saved")),
      body: ui.stack.apply(null, body)
    });
  });

  /* ------------------------------------------------------------------ *
   * memory_recall —— 检索结果
   * ------------------------------------------------------------------ */

  OneSSH.view("memory_recall", function (data, ctx) {
    data = objOf(data);
    var args = argsOf(ctx);
    var results = records(data.results);
    var engine = textOf(data.engine);

    var top = null;
    results.forEach(function (item) {
      var score = numOf(item.score);
      if (score !== null && (top === null || score > top)) top = score;
    });

    var chips = [];
    var host = textOf(args.host);
    if (host) chips.push(ui.chip(host, { mono: true }));
    if (engine) chips.push(ui.chip(engineLabel(engine), { title: "engine: " + engine }));

    var body;
    if (!results.length) {
      // 召回为空是常态，不是故障。这里必须说清楚，否则模型和用户都容易把「没记住」当成「不存在」。
      body = ui.empty(t("没有召回到相关记忆。这不代表事实不存在，继续正常调查即可。",
        "No memory matched. That does not mean the fact is untrue — just investigate as usual."));
    } else {
      body = ui.stack(
        ui.metrics([
          { label: t("命中", "Results"), value: fmt.num(results.length) },
          { label: t("最高分", "Top score"), value: top === null ? "" : top.toFixed(2), kind: "info" },
          { label: t("检索方式", "Engine"), value: engineLabel(engine), hint: engine }
        ]),
        memoryStack(results)
      );
    }

    return ui.card({
      kicker: "RECALL",
      title: textOf(args.query) || t("记忆召回", "Recall"),
      chips: chips,
      status: results.length
        ? ui.pill("info", fmt.num(results.length) + t(" 条", " hits"))
        : ui.pill("muted", t("没有命中", "No hits")),
      // memory_recall 在只读白名单里，和 memory_list / memory_stats 一样该给刷新入口：
      // 刚写完一条记忆再回头看这张卡时，重新检索一次比让用户另起一轮对话省事得多。
      actions: [refreshAction(ctx, "memory_recall", t("重新检索一次", "Search again"))],
      body: body
    });
  });

  /* ------------------------------------------------------------------ *
   * memory_list —— 按 bank 浏览
   * ------------------------------------------------------------------ */

  OneSSH.view("memory_list", function (data, ctx) {
    data = objOf(data);
    var args = argsOf(ctx);
    var list = records(data.memories);
    var bank = textOf(data.bank) || textOf(args.host);

    var offsetRaw = numOf(args.offset);
    var offset = offsetRaw === null || offsetRaw < 0 ? 0 : Math.floor(offsetRaw);
    var limitRaw = numOf(args.limit);
    // 翻页步长必须等于服务端真正使用的页大小，所以这里照抄 memory_list 的钳位规则（默认 50、下限 1、上限 200）：
    // 用本页条数兜底会把最后一页当成满页，传了超限的 limit 又会把真的满页当成末页，两头都会翻错。
    var limit = limitRaw !== null && limitRaw > 0 ? Math.max(1, Math.min(200, Math.floor(limitRaw))) : 50;
    var hasMore = list.length > 0 && list.length >= limit;

    var status = [];
    status.push(list.length
      ? ui.pill("info", fmt.num(list.length) + t(" 条", " items"))
      : ui.pill("muted", t("空", "Empty")));
    // 第一页不必强调起始位置，只有翻过页之后「我现在在哪」才是真问题。
    if (offset > 0) status.push(ui.pill("muted", t("第 " + fmt.num(offset + 1) + " 条起", "from #" + fmt.num(offset + 1))));

    var body = list.length ? memoryStack(list) : ui.empty(offset > 0
      ? t("这一页已经没有记忆了，回上一页看看。", "This page is empty — try the previous page.")
      : t("这个 bank 还没有记忆", "This bank has no memories yet"));

    var pager = null;
    if (ctx.can("memory_list")) {
      pager = ui.row(
        ui.button({
          label: t("上一页", "Previous"),
          disabled: offset <= 0,
          onClick: function () { ctx.refresh({ offset: Math.max(0, offset - limit), limit: limit }); }
        }),
        ui.button({
          label: t("下一页", "Next"),
          disabled: !hasMore,
          onClick: function () { ctx.refresh({ offset: offset + limit, limit: limit }); }
        }),
        meta(t("每页 " + fmt.num(limit) + " 条", fmt.num(limit) + " per page"))
      );
    }

    return ui.card({
      kicker: "MEMORY",
      title: t("记忆列表", "Memories"),
      chips: [bankChip(bank)],   // bank 已经由 chip 表达，再放进 subtitle 只是在卡片头重复一遍
      status: status,
      body: ui.stack(body, pager)
    });
  });

  /* ------------------------------------------------------------------ *
   * memory_update —— 修改回执
   * ------------------------------------------------------------------ */

  OneSSH.view("memory_update", function (data, ctx) {
    data = objOf(data);
    var args = argsOf(ctx);
    var id = numOf(data.id);
    if (id === null) id = numOf(args.id);
    var embedded = data.embedded;
    // embedded 是「这条记忆现在有没有向量」，不是「本次重建过向量」：后端只在正文真的变了时才重算，
    // 只改 importance/veracity 时向量原封不动。所以这里的措辞必须跟着 args.content 走，
    // 否则一次纯重要度调整会被写成「已随新正文重建」，整张卡在说一件没发生的事。
    var content = textOf(args.content);

    var chips = [];
    if (embedded === true) {
      chips.push(ui.chip(content ? t("向量已重建", "Re-embedded") : t("已有向量", "Embedded")));
    } else if (embedded === false) {
      chips.push(ui.pill("muted", content ? t("向量未重建", "Not re-embedded") : t("无向量", "No vector")));
    }

    var importance = numOf(args.importance);
    var veracity = textOf(args.veracity);
    var changed = ui.group(t("本次修改", "Changes"), "");
    if (content) {
      // 改了正文就用记忆块回显新内容，重要度与可信度作为块内元信息一并呈现，不再重复列一遍。
      changed.body.appendChild(memoryCard({ id: id, content: content, importance: args.importance, veracity: args.veracity }));
    } else if (importance !== null || veracity) {
      changed.body.appendChild(ui.kv([
        { label: t("重要度", "Weight"), value: importance === null ? "" : importance.toFixed(2) },
        { label: t("可信度", "Veracity"), value: veracity ? veracityOf(veracity).label : "" }
      ]));
    } else {
      changed.body.appendChild(ui.note(
        t("宿主没有回传本次调用的参数，无法显示改动了哪些字段。",
          "The host did not provide the call arguments, so the changed fields are unknown."), "info"));
    }

    return ui.card({
      kicker: "MEMORY",
      title: t("更新记忆", "Update memory"),
      subtitle: id === null ? "" : "#" + id,
      chips: chips,
      status: ui.pill("ok", t("已更新", "Updated")),
      body: ui.stack(
        changed,
        ui.kv([
          { label: t("记忆 ID", "Memory ID"), value: id === null ? "" : "#" + id, mono: true },
          {
            label: t("向量", "Embedding"),
            value: embedded === true
              ? (content ? t("已随新正文重建", "Rebuilt for the new content")
                : t("未受本次修改影响", "Untouched by this update"))
              : (embedded === false
                ? (content ? t("正文已改，但没能重建向量", "Content changed but re-embedding failed")
                  : t("这条记忆没有向量", "This memory has no vector"))
                : "")
          }
        ])
      )
    });
  });

  /* ------------------------------------------------------------------ *
   * memory_forget —— 删除回执
   * ------------------------------------------------------------------ */

  OneSSH.view("memory_forget", function (data, ctx) {
    data = objOf(data);
    var args = argsOf(ctx);
    var id = numOf(args.id);
    var deleted = data.deleted === true;

    return ui.card({
      kicker: "MEMORY",
      title: t("删除记忆", "Forget memory"),
      subtitle: id === null ? "" : "#" + id,
      status: deleted ? ui.pill("danger", t("已删除", "Deleted")) : ui.pill("muted", t("未找到", "Not found")),
      body: ui.note(
        deleted
          ? t("记忆已永久删除，无法恢复。", "The memory is permanently deleted and cannot be restored.")
          : t("没有找到该 ID 对应的记忆，可能已被删除。", "No memory with this ID — it may already be gone."),
        deleted ? "info" : "warn")
    });
  });

  /* ------------------------------------------------------------------ *
   * memory_stats —— 全局盘点
   * ------------------------------------------------------------------ */

  // 覆盖率越高越好，与 ui.bar 默认的「越高越危险」正相反，所以三处用到的地方都显式给 kind。
  function coverageKind(value) {
    if (value === null) return null;
    if (value >= 99) return "ok";
    if (value >= 60) return "info";
    return "warn";
  }

  function coverageOf(count, embedded) {
    var c = numOf(count);
    var e = numOf(embedded);
    if (c === null || e === null || c <= 0) return null;
    return (e / c) * 100;
  }

  function coverageCell(item) {
    var pct = coverageOf(item.count, item.embedded);
    var c = numOf(item.count);
    var e = numOf(item.embedded);
    // 覆盖率列在自动表格布局下会分到远比 132px 宽的空间，条子写死宽度就会在右边空出一大块死区，
    // 显得表格右半边是坏的。改成撑满单元格、只留 132px 下限，窄屏时百分比仍读得出来。
    return h("div", { style: { "width": "100%", "min-width": "132px" } }, ui.bar({
      pct: pct,
      label: pct === null ? t("无法计算", "n/a") : fmt.pct(pct),
      detail: c === null || e === null ? "" : fmt.num(e) + "/" + fmt.num(c),
      kind: coverageKind(pct)
    }));
  }

  OneSSH.view("memory_stats", function (data, ctx) {
    data = objOf(data);
    var banks = records(data.banks).slice();
    var sum = 0, embedded = 0;
    banks.forEach(function (item) {
      var count = numOf(item.count);
      if (count !== null) sum += count;
      var vec = numOf(item.embedded);
      if (vec !== null) embedded += vec;
    });
    // total 由后端直接给；缺失时用各 bank 之和兜底，免得统计卡片自己先空了一半。
    var total = numOf(data.total);
    if (total === null) total = sum;
    var coverage = total > 0 ? (embedded / total) * 100 : null;

    // 按条数降序：统计表要回答的是「记忆都堆在哪」，最大的 bank 排在第一行才对得上这个问题。
    banks.sort(function (a, b) {
      var x = numOf(b.count), y = numOf(a.count);
      return (x === null ? 0 : x) - (y === null ? 0 : y);
    });

    return ui.card({
      kicker: "MEMORY",
      title: t("记忆统计", "Memory stats"),
      // 主 pill 只放「数字 + 量词」，和其他卡片的计数 pill 保持同一句式；
      // 多一个「共」字就成了这一屏里唯一带前缀的写法，扫一眼反而抓不到数字。
      status: ui.pill("info", fmt.num(total) + t(" 条", " items")),
      actions: [refreshAction(ctx, "memory_stats", t("重新统计", "Recount"))],
      body: ui.stack(
        ui.metrics([
          { label: t("记忆总数", "Total"), value: fmt.num(total) },
          // .metric-label 与 .th 都会 uppercase，裸写 "bank" 会渲染成 BANK：
          // 既是这一屏唯一的英文标签、和邻座的中文标签不是一种字形，又和下面那列同名却指两回事。
          // 指标格回答「有几个库」，列头回答「哪个库」，所以指标格带「数」字把两者分开。
          { label: t("记忆库数", "Banks"), value: fmt.num(banks.length) },
          { label: t("已建向量", "Embedded"), value: fmt.num(embedded) },
          { label: t("向量覆盖率", "Coverage"), value: fmt.pct(coverage), kind: coverageKind(coverage) }
        ]),
        ui.table({
          columns: [
            { label: t("记忆库", "Bank"), mono: true, render: function (item) { return textOf(item.bank); } },
            {
              label: t("条数", "Count"), align: "right",
              render: function (item) { var v = numOf(item.count); return v === null ? "" : fmt.num(v); }
            },
            {
              label: t("已建向量", "Embedded"), align: "right", secondary: true,
              render: function (item) { var v = numOf(item.embedded); return v === null ? "" : fmt.num(v); }
            },
            { label: t("向量覆盖率", "Coverage"), render: coverageCell },
            {
              label: t("最后写入", "Last write"),
              render: function (item) {
                var ts = numOf(item.last_written);
                // null 不是「拿不到」而是「这个 bank 从来没被写过」，要说成人话
                if (ts === null) return h("span", { style: { "color": "var(--faint)" } }, t("从未", "Never"));
                return h("span", { title: fmt.time(ts) }, fmt.rel(ts));
              }
            }
          ],
          rows: banks,
          empty: t("还没有任何记忆 bank", "No memory banks yet")
        })
      )
    });
  });

  /* ------------------------------------------------------------------ *
   * memory_sleep —— 维护回执
   * ------------------------------------------------------------------ */

  OneSSH.view("memory_sleep", function (data, ctx) {
    data = objOf(data);
    var args = argsOf(ctx);
    var deduped = numOf(data.deduped);
    var decayed = numOf(data.decayed);
    var pruned = numOf(data.pruned);
    var touched = (deduped === null ? 0 : deduped) + (decayed === null ? 0 : decayed) + (pruned === null ? 0 : pruned);

    var summary = touched === 0
      ? ui.empty(t("这个 bank 当前没有需要整理的记忆", "Nothing in this bank needed cleaning up"))
      : ui.metrics([
        { label: t("去重合并", "Deduped"), value: fmt.num(deduped) },
        { label: t("重要度衰减", "Decayed"), value: fmt.num(decayed) },
        // 清理是唯一的破坏性动作，删掉了东西就该有颜色提醒，删了 0 条则不必大惊小怪
        { label: t("清理删除", "Pruned"), value: fmt.num(pruned), kind: pruned !== null && pruned > 0 ? "warn" : null }
      ]);

    return ui.card({
      kicker: "MEMORY",
      title: t("记忆维护", "Memory upkeep"),
      chips: [bankChip(args.host)],
      status: ui.pill("ok", t("已完成", "Done")),
      body: ui.stack(
        summary,
        ui.note(
          t("维护是确定性规则：合并重复正文、30 天未召回的重要度乘 0.9、90 天从未召回且重要度不足 0.1 的清理掉。",
            "Upkeep follows fixed rules: merge duplicate content, multiply weight by 0.9 for memories not recalled in 30 days, and prune those never recalled in 90 days whose weight is below 0.1."),
          "info")
      )
    });
  });
})();
