;(function () {
  "use strict";

  /* 后台任务组。五个工具其实只回答三类问题，卡片就按这三类来分形态：
     「任务现在怎么样」（job_list / job_status）—— 结论放 pill、可量化的放 metrics、可追踪的放跳转；
     「它输出了什么」（job_logs）—— 只有终端块能让人真正读日志，别的都是干扰；
     「我刚刚对它做了什么」（job_start / job_kill）—— 是一次性回执，重点在于说清远端此刻的状态
     和接下来该看哪里，而不是把返回字段罗列一遍。
     后台任务的特殊性在于「卡片是快照、远端还在跑」，所以能回调时一律给出刷新与下钻入口。 */

  var ui = OneSSH.ui, fmt = OneSSH.fmt, h = OneSSH.h, t = OneSSH.t;

  // exit_code、pid、log_bytes、host_id 都可能是 null，而 0 是完全合法的值
  //（退出码 0 是成功，日志 0 字节是「还没输出」），所以统一按「是不是有限数值」判断。
  function numOf(value) {
    return typeof value === "number" && isFinite(value) ? value : null;
  }

  function textOf(value) {
    return typeof value === "string" ? value : "";
  }

  function listOf(value) {
    return Array.isArray(value) ? value : [];
  }

  function inputOf(ctx) {
    return ctx && ctx.input && typeof ctx.input === "object" ? ctx.input : {};
  }

  function inputText(ctx, key) {
    var value = inputOf(ctx)[key];
    if (typeof value === "string") return value;
    return typeof value === "number" && isFinite(value) ? String(value) : "";
  }

  // job_list 的元素是 {job, log_bytes}，job_status 直接就是这个壳。
  // 但网关若哪天把 job 本体平铺过来，这里也不该整卡片崩掉——认 id 字段即可判别。
  function shellOf(item) {
    if (!item || typeof item !== "object") return { job: {}, log_bytes: null };
    if (item.job && typeof item.job === "object") return { job: item.job, log_bytes: item.log_bytes };
    if (typeof item.id === "string") return { job: item, log_bytes: item.log_bytes };
    return { job: {}, log_bytes: item.log_bytes };
  }

  /* 状态是这组卡片唯一的「结论」，五个工具都要用同一套措辞和配色，
     否则同一个任务在列表里和详情里显示成两种说法，用户会以为状态变了。 */
  function jobStatusPill(job) {
    var status = textOf(job.status);
    var code = numOf(job.exit_code);
    if (status === "running") return ui.pill("live", t("运行中", "Running"));
    if (status === "exited") {
      // 退出码缺失时不能默认成 0：把一次结果不明的收尾说成成功，比说不知道更糟。
      if (code === null) return ui.pill("muted", t("已退出", "Exited"));
      // 措辞对齐 exec / exec_many / host_test 的「退出码 N」：同一件事（进程带着退出码结束）
      // 在任务卡里叫「已退出 23」、在执行卡里叫「退出码 127」，用户得先在脑子里翻译一次
      // 才能把两张卡对上，而这两张卡经常是同一次排查里前后脚看的。
      return code === 0
        ? ui.pill("ok", t("退出码 0", "Exit 0"))
        : ui.pill("danger", t("退出码 ", "Exit ") + code);
    }
    if (status === "lost") return ui.pill("warn", t("已失联", "Lost"));
    // job_kill 成功后网关写入的就是 killed，它有结论但没有退出码，
    // 落进 muted 兜底会和「状态未知」长得一模一样，还会吐出英文原文。
    if (status === "killed") return ui.pill("warn", t("已终止", "Killed"));
    return ui.pill("muted", status || t("状态未知", "Unknown"));
  }

  // 非零退出才算失败；running / lost / killed 是「还没有结论」或人为终止，不能计入失败数。
  function isFailed(job) {
    var code = numOf(job.exit_code);
    return textOf(job.status) === "exited" && code !== null && code !== 0;
  }

  // 网关的 started_at/finished_at 是 Unix 秒（internal/jobs/manager.go 用 time.Now().Unix()），
  // 而 Date.now() 是毫秒。fmt.time / fmt.rel 走 runtime 的 toDate 会自动判定秒还是毫秒，
  // 只有这里的减法得自己做，否则同一张卡里时间点是对的、时长却差三个数量级。
  function msOf(ts) {
    var v = numOf(ts);
    if (v === null || v <= 0) return null;
    return v < 1e11 ? v * 1000 : v;
  }

  // 运行中的任务只能给出「到此刻为止」的时长，用 Date.now() 只为这个相对展示服务。
  function elapsedOf(job) {
    var started = msOf(job.started_at);
    if (started === null) return null;
    var finished = msOf(job.finished_at);
    var end = finished === null ? Date.now() : finished;
    var span = end - started;
    return span >= 0 ? span : null;
  }

  // 精确时间用于和远端日志对齐，相对时间给「多久以前」的直觉，两个都要，所以一个进 title。
  function relCell(ts) {
    var text = fmt.rel(ts);
    if (!text) return null;
    return h("span", { title: fmt.time(ts), text: text });
  }

  /* 命令往往比表格列宽长得多，中间省略比截尾更能保住尾部的目标文件/版本号这类关键信息。
     但手工省略的长度必须小于单元格真正放得下的字符数，否则 .td 的 text-overflow 会从尾部
     再截一次，一行里出现两个省略号，而且中间省略想保住的尾部照样被砍掉，等于白做。
     去掉重复的主机列之后，760px 宽的卡片里这一列约有 300px 可用，按等宽 11.5px
     （约 6.9px 一个字符）算是 40 出头个字符，所以手工省略按 40 个字符封顶：
     既不会长到让单元格再截一刀，也不至于短得让右边空出一大片。完整命令仍在 title 里。 */
  function commandCell(command) {
    var value = textOf(command);
    if (!value) return null;
    return h("span", { title: value, text: value.length > 40 ? fmt.short(value, 24, 15) : value });
  }

  function hostChip(hostId) {
    var id = numOf(hostId);
    if (id === null) return null;
    return ui.chip("#" + id, { mono: true, title: t("主机 ID ", "Host ID ") + id });
  }

  // 宿主不支持工具回调时按钮点了没反应，不如不画，免得用户以为卡片坏了。
  function refreshAction(ctx, tool, title) {
    if (!ctx.can(tool)) return null;
    return ui.button({
      label: t("刷新", "Refresh"),
      icon: "refresh",
      title: title,
      onClick: function () { ctx.refresh({}); }
    });
  }

  function statusAction(ctx, jobId, label) {
    if (!jobId || !ctx.can("job_status")) return null;
    return ui.button({
      label: label,
      icon: "link",
      title: t("查看该任务的最新状态", "Check the latest status of this job"),
      onClick: function () { ctx.navigate("job_status", { job_id: jobId }, t("任务状态", "Job status")); }
    });
  }

  /* ------------------------------------------------------------------ *
   * job_start —— 启动回执
   * ------------------------------------------------------------------ */

  OneSSH.view("job_start", function (data, ctx) {
    data = data || {};
    var jobId = textOf(data.job_id);
    var runId = textOf(data.run_id);
    var pid = numOf(data.pid);
    var command = inputText(ctx, "command");
    var host = inputText(ctx, "host");
    var cwd = inputText(ctx, "cwd");

    var chips = [];
    if (host) chips.push(ui.chip(host, { mono: true, title: host }));

    // 没拿到 job_id 就等于失去了这个任务的唯一句柄，这比「启动成功」重要得多，必须先说。
    if (!jobId) {
      return ui.card({
        kicker: "JOB",
        title: command || t("后台任务", "Background job"),
        chips: chips,
        status: ui.pill("warn", t("缺少任务 ID", "No job id")),
        body: ui.note(t("网关没有返回任务 ID，进程可能已经起来了但无法再被追踪；请用 job_list 确认是否有新任务。",
          "The gateway returned no job id: the process may be running but cannot be tracked; use job_list to check for a new job."), "warn")
      });
    }

    var rows = [
      { label: t("任务 ID", "Job ID"), value: jobId, mono: true, copy: true },
      { label: t("执行记录 ID", "Run ID"), value: runId, mono: true },
      // PID 不能走 fmt.num：千分位会把它变成一个不能直接拿去 kill 的字符串。
      { label: "PID", value: pid === null ? "" : String(pid), mono: true },
      { label: t("工作目录", "Working dir"), value: cwd ? ui.path(cwd) : "" }
    ];

    return ui.card({
      kicker: "JOB",
      title: command || t("后台任务", "Background job"),
      chips: chips,
      status: ui.pill("live", t("已启动", "Started")),
      actions: [statusAction(ctx, jobId, t("查看状态", "Status"))],
      body: ui.stack(
        ui.kv(rows),
        ui.note(t("任务在远端 setsid 后台运行，SSH 断开也会继续；日志在 ~/.onessh/jobs/" + jobId + "/out.log",
          "The job runs detached via setsid and survives SSH disconnects; its log lives at ~/.onessh/jobs/" + jobId + "/out.log"), "info")
      )
    });
  });

  /* ------------------------------------------------------------------ *
   * job_list —— 任务总览
   * ------------------------------------------------------------------ */

  OneSSH.view("job_list", function (data, ctx) {
    data = data || {};
    var entries = listOf(data.jobs).map(shellOf);
    var action = refreshAction(ctx, "job_list", t("重新拉取任务列表", "Reload the job list"));

    if (!entries.length) {
      return ui.card({
        kicker: "JOBS",
        title: t("后台任务", "Background jobs"),
        status: ui.pill("muted", t("没有任务", "No jobs")),
        actions: [action],
        body: ui.empty(t("当前令牌没有启动过后台任务", "This token has not started any background job"))
      });
    }

    // killed 必须自成一项：漏掉它，分项之和就对不上总数，被终止的任务会凭空消失。
    var running = 0, exited = 0, lost = 0, killed = 0, failed = 0;
    entries.forEach(function (entry) {
      var status = textOf(entry.job.status);
      if (status === "running") running++;
      else if (status === "exited") exited++;
      else if (status === "killed") killed++;
      else if (status === "lost") lost++;
      if (isFailed(entry.job)) failed++;
    });

    /* 卡片折叠后只剩头部可见，所以规模和大致健康度必须挤进 status。
       但逐项着色是下面 metrics 和表格行内 pill 的活：头部一口气挂三枚彩色 pill（绿/橙/红）
       会和「这是一张任务列表」的结论抢注意力，也和 hosts_list、file_list
       「一枚 info 计数 + 一枚 muted 明细」的头部写法对不上。这里统一收成两枚。
       明细里 lost 不能省——网关连进程状态都确认不了，比人为的 killed 更需要被看见。
       「失败」沿用 exec_many 的措辞，和表格行内的「退出码 N」指的是同一批任务。 */
    var status = [ui.pill("info", fmt.num(entries.length) + t(" 个", " jobs"))];
    var detail = [];
    if (running > 0) detail.push(fmt.num(running) + t(" 运行中", " running"));
    if (killed > 0) detail.push(fmt.num(killed) + t(" 已终止", " killed"));
    if (lost > 0) detail.push(fmt.num(lost) + t(" 失联", " lost"));
    if (failed > 0) detail.push(fmt.num(failed) + t(" 失败", " failed"));
    if (detail.length) status.push(ui.pill("muted", detail.join(" · ")));

    var summary = ui.metrics([
      { label: t("任务总数", "Jobs"), value: fmt.num(entries.length) },
      { label: t("运行中", "Running"), value: fmt.num(running), kind: running > 0 ? "info" : "muted" },
      {
        // 这一格数的是「已经有结局的任务」，退出码 0 的成功收尾也在里面，
        // 所以整格不能因为其中有失败就涂成 warn：那会让一次正常结束看起来也在报警。
        // 失败数交给这格的 hint、头部的「N 失败」和表格里的 danger pill 去说。
        label: t("已退出", "Exited"), value: fmt.num(exited), kind: "muted",
        hint: failed > 0 ? fmt.num(failed) + t(" 个以非零退出码结束", " ended with a non-zero exit code") : null
      },
      {
        label: t("已终止", "Killed"), value: fmt.num(killed),
        kind: killed > 0 ? "warn" : "muted",
        hint: killed > 0 ? t("被 job_kill 终止，因此没有退出码", "Terminated by job_kill, so no exit code was recorded") : null
      },
      {
        label: t("失联", "Lost"), value: fmt.num(lost),
        kind: lost > 0 ? "warn" : "muted",
        hint: lost > 0 ? t("网关无法再确认这些进程的状态", "The gateway can no longer confirm these processes") : null
      }
    ]);

    /* 后台任务常常整批跑在同一台主机上，这时「主机」列每行都是同一个 #id，
       白占一列宽度却不提供任何区分度，不如收成卡头一枚 chip，把宽度让给最需要的命令列。
       只要有一行的主机不同（或缺失 host_id），就退回逐行显示，否则会掩盖差异。 */
    var sharedHost = null;
    var mixedHost = false;
    entries.forEach(function (entry) {
      var id = numOf(entry.job.host_id);
      if (id === null) { mixedHost = true; return; }
      if (sharedHost === null) sharedHost = id;
      else if (sharedHost !== id) mixedHost = true;
    });
    if (mixedHost) sharedHost = null;

    // 行可点击只在能真正跳转时才挂：否则鼠标变成手型却什么都不发生，比不可点更糟。
    var clickable = ctx.can("job_status") && entries.some(function (entry) { return textOf(entry.job.id); });
    var columns = [
      { label: t("状态", "State"), render: function (entry) { return jobStatusPill(entry.job); } },
      { label: t("命令", "Command"), mono: true, render: function (entry) { return commandCell(entry.job.command); } }
    ];
    if (sharedHost === null) {
      columns.push({
        // 网关的 job 结构里只有 host_id，没有主机名。列头写「主机」而格子里是个纯数字，
        // 会被当成被截断的主机名，也没法和 exec 卡上的 web-01 对上号，所以照实写「主机 ID」。
        label: t("主机 ID", "Host ID"), secondary: true, mono: true,
        render: function (entry) {
          var id = numOf(entry.job.host_id);
          return id === null ? null : "#" + id;
        }
      });
    }
    columns.push(
      {
        label: "PID", secondary: true, mono: true, align: "right",
        render: function (entry) {
          var pid = numOf(entry.job.pid);
          return pid === null ? null : String(pid);
        }
      },
      { label: t("开始", "Started"), render: function (entry) { return relCell(entry.job.started_at); } },
      {
        label: t("结束", "Finished"), secondary: true,
        render: function (entry) { return relCell(entry.job.finished_at); }
      },
      {
        label: t("日志", "Log"), align: "right",
        render: function (entry) { return fmt.bytes(entry.log_bytes); }
      }
    );

    var table = ui.table({
      columns: columns,
      rows: entries,
      onRow: clickable ? function (entry) {
        var id = textOf(entry.job.id);
        if (id) ctx.navigate("job_status", { job_id: id }, t("任务状态", "Job status"));
      } : null
    });

    return ui.card({
      kicker: "JOBS",
      title: t("后台任务", "Background jobs"),
      subtitle: clickable ? t("点击任意一行查看详情", "Click a row for details") : "",
      chips: [hostChip(sharedHost)],
      status: status,
      actions: [action],
      body: ui.stack(summary, table)
    });
  });

  /* ------------------------------------------------------------------ *
   * job_status —— 单个任务的详情
   * ------------------------------------------------------------------ */

  OneSSH.view("job_status", function (data, ctx) {
    data = data || {};
    var shell = shellOf(data);
    var job = shell.job;
    var jobId = textOf(job.id) || inputText(ctx, "job_id");
    var status = textOf(job.status);
    var code = numOf(job.exit_code);
    var pid = numOf(job.pid);
    var cwd = textOf(job.cwd);
    var running = status === "running";
    var span = elapsedOf(job);

    if (!textOf(job.id) && !status) {
      return ui.card({
        kicker: "JOB",
        title: t("任务状态", "Job status"),
        subtitle: jobId,
        status: ui.pill("muted", t("没有记录", "Not found")),
        body: ui.empty(t("网关里没有这个任务的记录，它可能属于别的令牌，或者记录已被清理",
          "The gateway has no record of this job: it may belong to another token, or the record was pruned"))
      });
    }

    var actions = [
      refreshAction(ctx, "job_status", t("重新拉取任务状态", "Reload the job status"))
    ];
    if (jobId && ctx.can("job_logs")) {
      actions.push(ui.button({
        label: t("查看日志", "Logs"),
        icon: "link",
        title: t("读取该任务的输出日志", "Read this job's output log"),
        onClick: function () {
          ctx.navigate("job_logs", { job_id: jobId, tail_lines: 200 }, t("任务日志", "Job logs"));
        }
      }));
    }

    // 这四个数字回答的是「进程在不在、跑了多久、留下多少输出、结局如何」，
    // 是判断一个后台任务是否健康所需的全部信息，其余细节才下沉到 kv。
    var summary = ui.metrics([
      { label: "PID", value: pid === null ? "" : String(pid), kind: pid === null ? "muted" : null },
      { label: t("日志大小", "Log size"), value: fmt.bytes(shell.log_bytes) },
      {
        label: running ? t("已运行", "Elapsed") : t("总耗时", "Duration"),
        value: span === null ? "" : fmt.dur(span),
        hint: running ? t("运行中的任务只能给出截至此刻的时长", "A running job can only report the time so far") : null
      },
      {
        label: t("退出码", "Exit code"),
        value: code === null ? "" : String(code),
        kind: code === null ? "muted" : (code === 0 ? "ok" : "danger"),
        hint: code === null
          ? (running
            ? t("任务尚未结束", "The job has not finished yet")
            : (status === "killed" ? t("任务被信号终止，没有留下退出码", "The job was signalled, so no exit code was recorded") : null))
          : null
      }
    ]);

    var finishedValue = "";
    if (numOf(job.finished_at) !== null) finishedValue = fmt.time(job.finished_at);
    else if (running) finishedValue = t("仍在运行", "Still running");

    // used_setsid 为 false 是有意义的信号（任务会随 SSH 断开被带走），不能被空值判断吃掉。
    var setsid = "";
    var setsidKind = null;
    if (job.used_setsid === true) {
      setsid = t("已 setsid，脱离 SSH 会话独立运行", "Detached with setsid; independent of the SSH session");
    } else if (job.used_setsid === false) {
      setsid = t("未 setsid，SSH 断开时可能被一并终止", "No setsid; may be killed when the SSH session drops");
      setsidKind = "warn";
    }

    var rows = [
      { label: t("任务 ID", "Job ID"), value: jobId, mono: true, copy: true },
      { label: t("工作目录", "Working dir"), value: cwd ? ui.path(cwd) : "" },
      { label: t("开始时间", "Started"), value: fmt.time(job.started_at) },
      { label: t("结束时间", "Finished"), value: finishedValue, kind: running ? "muted" : null },
      { label: t("后台方式", "Detach"), value: setsid, kind: setsidKind }
    ];

    var body = [summary, ui.kv(rows)];
    if (status === "lost") {
      body.push(ui.note(t("网关无法再确认该进程状态：可能是主机重启或进程被外部清理，日志文件仍在远端",
        "The gateway can no longer confirm this process: the host may have rebooted or the process was reaped externally; the log file is still on the remote host"), "warn"));
    } else if (status === "killed") {
      body.push(ui.note(t("任务被 job_kill 终止，进程组已收到信号，因此没有留下退出码；日志文件仍在远端",
        "The job was terminated by job_kill: the process group was signalled, so no exit code was recorded; the log file is still on the remote host"), "warn"));
    } else if (isFailed(job)) {
      body.push(ui.note(t("任务以非零退出码结束，失败原因通常写在日志末尾", "The job ended with a non-zero exit code; the reason is usually at the end of the log"), "danger"));
    }

    return ui.card({
      kicker: "JOB",
      title: textOf(job.command) || t("后台任务", "Background job"),
      subtitle: jobId,
      chips: [hostChip(job.host_id)],
      status: jobStatusPill(job),
      actions: actions,
      body: ui.stack.apply(null, body)
    });
  });

  /* ------------------------------------------------------------------ *
   * job_logs —— 输出日志
   * ------------------------------------------------------------------ */

  OneSSH.view("job_logs", function (data, ctx) {
    data = data || {};
    var out = textOf(data.output);
    var jobId = inputText(ctx, "job_id");
    var grep = inputText(ctx, "grep");
    var offset = numOf(inputOf(ctx).offset_bytes);
    var tail = numOf(inputOf(ctx).tail_lines);
    var count = out ? fmt.lines(out).length : 0;

    // 这些 chip 描述的是「你看到的是日志的哪一段」。没有它们，
    // 一段被 grep 过或从中间截取的日志会被误当成全部输出。
    var chips = [];
    if (grep) chips.push(ui.chip(t("过滤 ", "grep ") + grep, { mono: true, title: grep }));
    if (offset !== null && offset > 0) chips.push(ui.chip(t("自 ", "from ") + fmt.bytes(offset), { mono: true }));
    if (tail !== null && tail > 0) chips.push(ui.chip(t("末 ", "last ") + fmt.num(tail) + t(" 行", " lines"), { mono: true }));

    return ui.card({
      kicker: "LOGS",
      title: t("任务日志", "Job logs"),
      subtitle: jobId,
      chips: chips,
      status: count
        ? ui.pill("info", fmt.num(count) + t(" 行", " lines"))
        : ui.pill("muted", t("没有内容", "Empty")),
      actions: [refreshAction(ctx, "job_logs", t("重新读取这段日志", "Re-read this slice of the log"))],
      body: count
        ? ui.terminal({ text: out, collapsedLines: 40, title: "out.log" })
        : ui.empty(grep
          ? t("这段范围内没有匹配该过滤条件的日志", "No log lines in this range match the filter")
          : t("这段范围内没有日志", "No log lines in this range"))
    });
  });

  /* ------------------------------------------------------------------ *
   * job_kill —— 终止回执
   * ------------------------------------------------------------------ */

  OneSSH.view("job_kill", function (data, ctx) {
    data = data || {};
    var ok = data.ok === true;
    var jobId = inputText(ctx, "job_id");
    var signal = inputText(ctx, "signal") || "TERM";

    return ui.card({
      kicker: "JOB",
      title: t("终止后台任务", "Terminate job"),
      subtitle: jobId,
      status: ok
        ? ui.pill("warn", t("已发送 ", "Sent ") + signal)
        : ui.pill("muted", t("未发送", "Not sent")),
      // 发信号成功不等于进程已经结束，所以这里给的是「查看状态」而不是任何再次终止的入口。
      actions: [statusAction(ctx, jobId, t("确认状态", "Confirm status"))],
      body: ok
        ? ui.note(t("信号已发往整个进程组。TERM 是优雅退出，进程可能需要一点时间才结束；用 job_status 确认最终状态。",
          "The signal was delivered to the whole process group. TERM is a graceful stop and the process may take a moment to exit; confirm the final state with job_status."), "info")
        : ui.empty(t("网关没有确认信号发送结果，任务可能已经自己结束了",
          "The gateway did not confirm the signal; the job may have already finished on its own"))
    });
  });
})();
