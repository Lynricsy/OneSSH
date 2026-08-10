#!/usr/bin/env python3
"""离线生成 GitHub Star History 折线图（SVG），不依赖任何第三方服务。

背景：GitHub 自 2026-06-30 起把 stargazers API 收窄为仓库 admin/collaborator 可读，
star-history.com 这类外部渲染服务对所有仓库都失效；官方给出的补救方式要求把一个带
Contents 写权限的 token 交给第三方服务器保管，本项目不接受这种凭据外流。
因此改由仓库自身的 Actions 用 GITHUB_TOKEN 拉数据并在本地渲染 SVG。

用法：
    GITHUB_TOKEN=... python3 .github/scripts/star_history.py <owner/repo> <输出目录>
"""

from __future__ import annotations

import json
import os
import sys
import urllib.error
import urllib.request
from datetime import datetime, timezone

API = "https://api.github.com"
PER_PAGE = 100
# GitHub 分页硬上限：page * per_page 不得超过 40000
MAX_PAGES = 400
# 单条折线的最大顶点数，超出后等距抽稀，避免 SVG 体积失控
MAX_POINTS = 720

# 画布与绘图区
WIDTH, HEIGHT = 840, 400
PAD_L, PAD_R, PAD_T, PAD_B = 68, 28, 64, 44
PLOT_W = WIDTH - PAD_L - PAD_R
PLOT_H = HEIGHT - PAD_T - PAD_B

THEMES = {
    "light": {
        "bg": "#ffffff",
        "line": "#0d9488",
        "fill": "#0d9488",
        "fill_opacity": "0.16",
        "title": "#0f172a",
        "muted": "#64748b",
        "grid": "#e2e8f0",
        "axis": "#cbd5e1",
        "dot": "#0d9488",
        "dot_ring": "#ffffff",
    },
    "dark": {
        "bg": "#0b1220",
        "line": "#2dd4bf",
        "fill": "#2dd4bf",
        "fill_opacity": "0.18",
        "title": "#e2e8f0",
        "muted": "#94a3b8",
        "grid": "#1e293b",
        "axis": "#334155",
        "dot": "#2dd4bf",
        "dot_ring": "#0b1220",
    },
}

FONT = (
    "ui-sans-serif,-apple-system,BlinkMacSystemFont,'Segoe UI',"
    "Roboto,'Helvetica Neue',Arial,sans-serif"
)


def api_get(path: str, token: str, accept: str) -> tuple[object, dict[str, str]]:
    """调用 GitHub REST API，返回解析后的 JSON 与响应头。"""
    req = urllib.request.Request(f"{API}{path}")
    req.add_header("Accept", accept)
    req.add_header("X-GitHub-Api-Version", "2022-11-28")
    req.add_header("User-Agent", "onessh-star-history")
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.load(resp), dict(resp.headers)


def fetch_stargazers(repo: str, token: str) -> tuple[list[datetime], int]:
    """拉取全部 star 事件时间，并返回仓库当前 star 总数。

    star 总数取自仓库元数据而非事件条数：分页上限截断时，曲线末端仍能锚定真实值。
    """
    meta, _ = api_get(f"/repos/{repo}", token, "application/vnd.github+json")
    total = int(meta.get("stargazers_count", 0))

    stamps: list[datetime] = []
    for page in range(1, MAX_PAGES + 1):
        path = f"/repos/{repo}/stargazers?per_page={PER_PAGE}&page={page}"
        try:
            batch, _ = api_get(path, token, "application/vnd.github.star+json")
        except urllib.error.HTTPError as err:
            if err.code in (401, 403, 404):
                raise SystemExit(
                    f"stargazers API 返回 {err.code}：GitHub 只向仓库 admin/collaborator "
                    f"开放该端点，请确认 token 对 {repo} 有写权限。"
                ) from err
            raise
        if not batch:
            break
        for item in batch:
            at = item.get("starred_at")
            if at:
                stamps.append(
                    datetime.strptime(at, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)
                )
        if len(batch) < PER_PAGE:
            break

    stamps.sort()
    return stamps, total


def thin(points: list[tuple[datetime, int]]) -> list[tuple[datetime, int]]:
    """等距抽稀，始终保留首尾点。"""
    if len(points) <= MAX_POINTS:
        return points
    step = (len(points) - 1) / (MAX_POINTS - 1)
    picked = [points[round(i * step)] for i in range(MAX_POINTS)]
    picked[-1] = points[-1]
    return picked


def nice_ceiling(value: int) -> tuple[int, int]:
    """把 y 轴上界抬到一个好看的整数，返回（上界, 步长）。"""
    if value <= 4:
        return 4, 1
    for unit in (1, 2, 5, 10, 20, 25, 50, 100, 200, 250, 500, 1000, 2000, 2500, 5000):
        # 3~5 条网格线都可接受，取能覆盖数据的最小上界，避免曲线被压在图底
        for ticks in (3, 4, 5):
            step = unit
            if step * ticks >= value:
                return step * ticks, step
    step = 10 ** (len(str(value)) - 1)
    while step * 4 < value:
        step *= 2
    return step * 4, step


def date_format(span_days: float) -> str:
    # 新仓库的跨度可能只有几天，日期粒度不够会让 5 个刻度显示成同一天
    if span_days <= 3:
        return "%b %d %H:%M"
    if span_days <= 92:
        return "%b %d"
    if span_days <= 800:
        return "%b %Y"
    return "%Y"


def esc(text: str) -> str:
    return text.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def build_svg(repo: str, points: list[tuple[datetime, int]], total: int, theme: str) -> str:
    c = THEMES[theme]
    now = datetime.now(timezone.utc)

    if points:
        t0 = points[0][0]
    else:
        t0 = now
    t1 = max(now, points[-1][0]) if points else now
    span = (t1 - t0).total_seconds()
    if span <= 0:
        # 单点或零点：给一天的横向跨度，避免除零
        span = 86400.0
        t1 = t0

    y_max, y_step = nice_ceiling(max(total, points[-1][1] if points else 0))

    def px(t: datetime) -> float:
        return PAD_L + (t - t0).total_seconds() / span * PLOT_W

    def py(v: int) -> float:
        return PAD_T + PLOT_H - v / y_max * PLOT_H

    parts: list[str] = []
    add = parts.append

    add(
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{WIDTH}" height="{HEIGHT}" '
        f'viewBox="0 0 {WIDTH} {HEIGHT}" role="img" '
        f'aria-label="{esc(repo)} star history, {total} stars">'
    )
    add(
        f'<defs><linearGradient id="area" x1="0" y1="0" x2="0" y2="1">'
        f'<stop offset="0%" stop-color="{c["fill"]}" stop-opacity="{c["fill_opacity"]}"/>'
        f'<stop offset="100%" stop-color="{c["fill"]}" stop-opacity="0"/>'
        f"</linearGradient></defs>"
    )
    add(f'<rect width="{WIDTH}" height="{HEIGHT}" fill="{c["bg"]}"/>')
    add(f'<g font-family="{FONT}">')

    # 标题区
    add(
        f'<text x="{PAD_L}" y="32" fill="{c["title"]}" font-size="17" '
        f'font-weight="600">{esc(repo)}</text>'
    )
    add(
        f'<text x="{PAD_L}" y="50" fill="{c["muted"]}" font-size="12">Star History</text>'
    )
    add(
        f'<text x="{WIDTH - PAD_R}" y="32" fill="{c["line"]}" font-size="20" '
        f'font-weight="700" text-anchor="end">\u2605 {total}</text>'
    )
    add(
        f'<text x="{WIDTH - PAD_R}" y="50" fill="{c["muted"]}" font-size="11" '
        f'text-anchor="end">updated {now:%Y-%m-%d}</text>'
    )

    # y 轴网格与刻度
    v = 0
    while v <= y_max:
        y = py(v)
        add(
            f'<line x1="{PAD_L}" y1="{y:.1f}" x2="{WIDTH - PAD_R}" y2="{y:.1f}" '
            f'stroke="{c["grid"]}" stroke-width="1"/>'
        )
        add(
            f'<text x="{PAD_L - 12}" y="{y + 4:.1f}" fill="{c["muted"]}" font-size="11" '
            f'text-anchor="end">{v}</text>'
        )
        v += y_step

    # x 轴刻度
    fmt = date_format(span / 86400)
    for i in range(5):
        frac = i / 4
        x = PAD_L + frac * PLOT_W
        tick = datetime.fromtimestamp(t0.timestamp() + span * frac, tz=timezone.utc)
        anchor = "start" if i == 0 else "end" if i == 4 else "middle"
        add(
            f'<text x="{x:.1f}" y="{PAD_T + PLOT_H + 22:.1f}" fill="{c["muted"]}" '
            f'font-size="11" text-anchor="{anchor}">{tick.strftime(fmt)}</text>'
        )
    add(
        f'<line x1="{PAD_L}" y1="{PAD_T + PLOT_H}" x2="{WIDTH - PAD_R}" '
        f'y2="{PAD_T + PLOT_H}" stroke="{c["axis"]}" stroke-width="1"/>'
    )

    if points:
        # star 是离散事件，用阶梯线才是真实累计曲线，不做插值美化
        segs = [f"M {px(points[0][0]):.1f} {py(0):.1f}"]
        prev = 0
        for t, count in points:
            segs.append(f"L {px(t):.1f} {py(prev):.1f}")
            segs.append(f"L {px(t):.1f} {py(count):.1f}")
            prev = count
        last_x = px(t1)
        segs.append(f"L {last_x:.1f} {py(prev):.1f}")
        line = " ".join(segs)

        add(
            f'<path d="{line} L {last_x:.1f} {py(0):.1f} L {px(points[0][0]):.1f} '
            f'{py(0):.1f} Z" fill="url(#area)"/>'
        )
        add(
            f'<path d="{line}" fill="none" stroke="{c["line"]}" stroke-width="2.2" '
            f'stroke-linejoin="round" stroke-linecap="round"/>'
        )
        add(
            f'<circle cx="{last_x:.1f}" cy="{py(prev):.1f}" r="4.5" fill="{c["dot"]}" '
            f'stroke="{c["dot_ring"]}" stroke-width="2"/>'
        )
    else:
        add(
            f'<text x="{WIDTH / 2:.0f}" y="{PAD_T + PLOT_H / 2:.0f}" fill="{c["muted"]}" '
            f'font-size="13" text-anchor="middle">还没有 star 数据</text>'
        )

    add("</g></svg>")
    return "\n".join(parts) + "\n"


def main() -> None:
    if len(sys.argv) != 3:
        raise SystemExit("用法: star_history.py <owner/repo> <输出目录>")
    repo, out_dir = sys.argv[1], sys.argv[2]
    token = os.environ.get("GITHUB_TOKEN", "")
    if not token:
        raise SystemExit("缺少 GITHUB_TOKEN：stargazers API 不接受匿名访问")

    stamps, total = fetch_stargazers(repo, token)
    points = thin([(t, i + 1) for i, t in enumerate(stamps)])
    if points and total > points[-1][1]:
        # 分页被截断时把末点抬到真实总数，保持曲线终点与徽章一致
        points[-1] = (points[-1][0], total)

    os.makedirs(out_dir, exist_ok=True)
    for theme in THEMES:
        path = os.path.join(out_dir, f"star-history-{theme}.svg")
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(build_svg(repo, points, total, theme))
        print(f"wrote {path} ({len(points)} points, {total} stars)")


if __name__ == "__main__":
    main()
