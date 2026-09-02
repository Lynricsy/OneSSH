;(function () {
  "use strict";

  /* 图片组卡片：image_view。
     这张卡和其他卡有个根本差别——用户要看的是图本身，尺寸和字节数只是佐证，
     所以正文第一屏必须是图，指标压到图下面；宿主没把图片块递进来时才退回纯数据展示。
     结论（缩放了没有、缩到多大）放在卡头的 pill 上，一眼可读，不必展开正文。
     image_view 不在只读回调白名单里，因此卡片内不做任何会再次触发工具调用的交互，
     只保留纯客户端的缩放切换与复制路径。 */

  var ui = OneSSH.ui, fmt = OneSSH.fmt, h = OneSSH.h, t = OneSSH.t;

  // 图片高度上限用固定像素而不是 vh：卡片高度本身是我们上报给宿主的，
  // iframe 视口高度又反过来跟着这个上报值走，用 vh 会形成互相追高的正反馈。
  var CAP_INLINE = 420;
  var CAP_FULL = 720;

  // data: URI 的类型直接决定浏览器怎么解释这段字节，所以只放行 image/*。
  var IMAGE_MIME = /^image\/[a-z0-9][a-z0-9.+-]*$/i;

  /* ------------------------------------------------------------------ *
   * 取值：bytes 可能是 0、width 可能缺失，两者含义完全不同，
   * 一律显式判类型，不用 `||` 兜底，免得「0 字节」被当成「字段缺失」。
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

  // 末尾斜杠不代表一层目录，先剥掉再取最后一段。
  function baseName(pathText) {
    var raw = strOf(pathText);
    while (raw.length > 1 && raw.charAt(raw.length - 1) === "/") raw = raw.slice(0, -1);
    var cut = raw.lastIndexOf("/");
    return cut < 0 ? raw : raw.slice(cut + 1);
  }

  function dims(w, height) {
    // 像素数不加千分位：1,920 × 1,080 反而比 1920 × 1080 更难一眼比对
    return (w === null || height === null) ? "" : String(w) + " × " + String(height);
  }

  /* ------------------------------------------------------------------ *
   * 图片本体：只在 ctx.result.content 里，structuredContent 只有元数据
   * ------------------------------------------------------------------ */

  function pickImage(result) {
    var content = result && Array.isArray(result.content) ? result.content : [];
    for (var i = 0; i < content.length; i++) {
      var block = content[i];
      if (!block || typeof block !== "object" || block.type !== "image") continue;
      var raw = typeof block.data === "string" ? block.data : "";
      if (!raw) continue;
      // base64 里混进换行会让 data: URI 解析失败；先探一次再决定要不要复制这份大字符串，
      // 正常情况下（无空白）不产生任何额外副本。
      var payload = /\s/.test(raw) ? raw.replace(/\s+/g, "") : raw;
      return { data: payload, mime: strOf(block.mimeType) };
    }
    return null;
  }

  function pickText(result) {
    var content = result && Array.isArray(result.content) ? result.content : [];
    var parts = [];
    for (var i = 0; i < content.length; i++) {
      var block = content[i];
      if (block && block.type === "text" && typeof block.text === "string") parts.push(block.text);
    }
    return parts.join("\n").trim();
  }

  // 宿主给了非 image/* 的类型就别照抄进 <img>，统一退回 png：
  // 渲染不出来顶多是一张坏图，不会让浏览器按别的类型去解释这段字节。
  function resolveMime() {
    for (var i = 0; i < arguments.length; i++) {
      var value = strOf(arguments[i]).trim();
      if (IMAGE_MIME.test(value)) return value.toLowerCase();
    }
    return "image/png";
  }

  function knownMime(a, b) {
    return IMAGE_MIME.test(strOf(a).trim()) || IMAGE_MIME.test(strOf(b).trim());
  }

  function mimeLabel(mime) {
    var cut = mime.indexOf("/");
    var sub = cut < 0 ? mime : mime.slice(cut + 1);
    return (sub || mime).toUpperCase();
  }

  /* ------------------------------------------------------------------ *
   * 预览区：style.css 里没有 .preview 规则，尺寸相关的样式全部走内联 style
   * ------------------------------------------------------------------ */

  function buildPreview(opt) {
    var cap = opt.cap;
    var img = h("img", {
      src: opt.src,
      alt: opt.name,
      decoding: "async",
      style: {
        "display": "block",
        "max-width": "100%",
        "max-height": cap + "px",
        "border-radius": "6px"
      }
    });

    var box = h("div", {
      class: "preview",
      "data-fit": "contain",
      role: "button",
      tabindex: "0",
      title: t("点击查看实际像素", "Click for actual pixels"),
      style: {
        "display": "flex",
        "justify-content": "center",
        "align-items": "flex-start",
        "padding": "8px",
        "overflow": "auto",
        "max-height": (cap + 16) + "px",
        "background": "var(--bg-2)",
        "border": "1px solid var(--border-2)",
        "border-radius": "var(--radius-sm)",
        "cursor": "zoom-in"
      }
    }, img);

    var hint = h("span", {
      style: { "font-size": "11px", "color": "var(--faint)", "white-space": "nowrap" },
      text: ""
    });

    var toggle = ui.button({
      label: t("实际像素", "Actual size"),
      icon: "expand",
      title: t("在「适应宽度」和「原始像素」之间切换", "Toggle between fit-width and actual pixels"),
      onClick: function () { flip(); }
    });

    function setFit(actual) {
      box.setAttribute("data-fit", actual ? "actual" : "contain");
      img.style.setProperty("max-width", actual ? "none" : "100%");
      img.style.setProperty("max-height", actual ? "none" : cap + "px");
      // 原始像素下从左上角开始看，居中会让横向滚动条一上来就停在图片中间
      box.style.setProperty("justify-content", actual ? "flex-start" : "center");
      box.style.setProperty("cursor", actual ? "zoom-out" : "zoom-in");
      box.setAttribute("title", actual
        ? t("点击恢复适应宽度", "Click to fit width")
        : t("点击查看实际像素", "Click for actual pixels"));
      // 改文字而不是重建按钮：键盘用户用空格切换后，焦点才不会掉回文档开头
      var label = toggle._label || toggle.lastChild;
      if (label) label.textContent = actual ? t("适应宽度", "Fit width") : t("实际像素", "Actual size");
    }

    function flip() { setFit(box.getAttribute("data-fit") !== "actual"); }

    box.addEventListener("click", flip);
    box.addEventListener("keydown", function (event) {
      if (event.key === "Enter" || event.key === " ") { event.preventDefault(); flip(); }
    });

    var wrap = ui.stack(box, ui.row(toggle, hint));

    img.addEventListener("load", function () {
      var nw = img.naturalWidth, nh = img.naturalHeight;
      if (!nw || !nh) return;
      hint.textContent = t("画布 ", "Canvas ") + nw + " × " + nh + t(" 像素", " px");
      if (typeof opt.onSize === "function") opt.onSize(nw, nh);
    });

    // base64 被截断时浏览器只会静静地留一个破图占位，必须自己说清楚
    img.addEventListener("error", function () {
      while (wrap.firstChild) wrap.removeChild(wrap.firstChild);
      wrap.appendChild(ui.note(
        t("图片数据无法解码，可能在传输过程中被截断。", "The image data could not be decoded; it may have been truncated."),
        "danger"));
    });

    return wrap;
  }

  /* ------------------------------------------------------------------ *
   * image_view
   * ------------------------------------------------------------------ */

  OneSSH.view("image_view", function (data, ctx) {
    var d = objOf(data) || {};
    var args = objOf(ctx && ctx.input) || {};
    var result = ctx ? ctx.result : null;

    var pathText = strOf(args.path);
    var name = baseName(pathText);
    var host = strOf(args.host);

    var ow = numOf(d.original_width), oh = numOf(d.original_height);
    var w = numOf(d.width), hgt = numOf(d.height);
    var size = numOf(d.bytes);
    var maxDim = numOf(args.max_dim);

    var pic = pickImage(result);
    var hasMime = knownMime(pic && pic.mime, d.mime_type);
    // 类型以图片块自带的为准：它描述的就是这段字节，元数据只是旁证
    var mime = resolveMime(pic ? pic.mime : "", d.mime_type);

    var haveOrig = ow !== null && oh !== null;
    var haveOut = w !== null && hgt !== null;
    var scaled = haveOrig && haveOut && (ow !== w || oh !== hgt);

    // 卡头 pill 只回答一个问题：这是原图，还是被缩过的图，缩到了多大
    var status = null;
    if (scaled) status = ui.pill("info", dims(ow, oh) + " → " + dims(w, hgt));
    else if (haveOrig && haveOut) status = ui.pill("ok", t("原始尺寸", "Original size"));
    else if (haveOut) status = ui.pill("info", dims(w, hgt));
    else if (haveOrig) status = ui.pill("info", dims(ow, oh));

    // 输出尺寸缺失时留一个可写节点，等图片解码完再用真实画布尺寸补上
    var outText = dims(w, hgt);
    var outNode = outText ? null : h("span", { text: "—" });

    var scaleHint = null;
    if (scaled && ow) {
      scaleHint = t("已缩放至原图的 ", "Scaled to ") + fmt.pct((w / ow) * 100) +
        (maxDim === null ? "" : t("，长边上限 ", ", long edge capped at ") + maxDim + "px");
    } else if (maxDim !== null) {
      scaleHint = t("长边上限 ", "Long edge capped at ") + maxDim + "px";
    }

    var facts = ui.metrics([
      { label: t("原始尺寸", "Original"), value: dims(ow, oh) },
      {
        label: t("输出尺寸", "Output"),
        value: outNode || outText,
        kind: scaled ? "info" : null,
        hint: scaleHint
      },
      {
        // d.bytes 是服务端缩放并重新编码后、这次回传给卡片的那段图片数据的长度（imagex.Process 总会重编码，
        // 即使没缩放也不等于原文件），叫「文件大小」等于对远端 path 的体积下了个错误断言——
        // 用户会据此判断要不要 file_transfer 原图。改成与上一格「输出尺寸」同一口径的「输出字节」。
        label: t("输出字节", "Output bytes"),
        value: size === null ? null : fmt.bytes(size),
        hint: size === null ? null : t("本次回传的图片体积，不是远端原文件大小",
          "Size of the image returned here, not of the remote file")
      },
      { label: t("类型", "Type"), value: hasMime ? mimeLabel(mime) : null, hint: hasMime ? mime : null }
    ]);

    var body;
    if (pic) {
      body = [
        buildPreview({
          src: "data:" + mime + ";base64," + pic.data,
          name: name || t("远程图片", "Remote image"),
          cap: (ctx && typeof ctx.isFullscreen === "function" && ctx.isFullscreen()) ? CAP_FULL : CAP_INLINE,
          onSize: function (nw, nh) {
            if (outNode) outNode.textContent = dims(nw, nh);
          }
        }),
        facts
      ];
    } else if (haveOrig || haveOut || size !== null || hasMime) {
      body = [
        ui.note(t("宿主没有把图片内容传给卡片，只能显示尺寸信息。",
          "The host did not pass the image data to this card; only metadata is available."), "warn"),
        facts
      ];
    } else {
      // 调用成功但什么都没带回来：服务端的文字说明是此时唯一还有信息量的东西
      var summary = pickText(result);
      body = [summary
        ? ui.note(summary, "warn")
        : ui.empty(t("工具没有返回图片内容", "The tool returned no image"))];
    }

    return ui.card({
      kicker: "IMAGE",
      title: name || t("远程图片", "Remote image"),
      subtitle: pathText && pathText !== name ? pathText : "",
      chips: [
        host ? ui.chip(host, { mono: true, title: t("主机 ", "host ") + host }) : null,
        hasMime ? ui.chip(mimeLabel(mime), { title: mime }) : null
      ],
      status: status,
      actions: pathText ? [ui.copy(pathText, t("复制路径", "Copy path"))] : null,
      body: ui.stack.apply(null, body)
    });
  });
})();
