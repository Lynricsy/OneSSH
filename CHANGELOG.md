# 变更日志

本文件记录 OneSSH 的重要变更，格式参考 [Keep a Changelog](https://keepachangelog.com/)。

## [未发布]

### 新增

- MCP Apps 交互卡片：全部 32 个工具各带一张自包含 HTML 卡片，在 ChatGPT 网页版、Claude 等支持该扩展的客户端里把工具结果直接渲染成界面，而不是一段 JSON。
  - 工具描述符带 `_meta.ui.resourceUri`，资源以 `text/html;profile=mcp-app` 返回，走 `postMessage` 上的 `ui/*` JSON-RPC 与宿主通信。
  - 另附旧版 `openai/outputTemplate` 别名，指向同一份正文的 `text/html+skybridge` 版本；该资源走 URI 模板发布，不会把 `resources/list` 刷成两倍长。
  - 资源 URI 按内容哈希版本化，卡片内容变更后宿主缓存自动失效。
  - 卡片跟随宿主主题与设计令牌切换深浅色，交互限于翻页、进目录、预览、刷新等只读导航；有副作用的工具不允许由卡片发起调用。
  - 新增 `ONESSH_MCP_APPS` 开关（默认 `on`），`off` 时不发布任何卡片资源与 `_meta`。
  - 新增预览画廊 `internal/mcpserver/apps/preview/`，用静态服务器即可离线验收全部卡片。

## [0.1.14] - 2026-08-27

### 修复

- 修复大量主机同时采样时的 SQLite 写锁竞争：
  - 串行化指标写入、指标清理与审计写入，保留数据库多连接读取能力。
  - 每轮监控固定使用最多 5 个采样 worker；上一轮未结束时跳过新的轮询，避免跨轮次突破并发上限或累积 goroutine。
  - 轮询取消后停止派发新任务，并等待活动 worker 退出。
- 每 10 分钟尝试执行 `PRAGMA wal_checkpoint(TRUNCATE)` 回收 WAL 文件；检查点被活动连接阻塞时记录 `busy=1` 并在下一周期重试。
- 为 SSH 协议握手增加 15 秒期限并接入调用上下文取消，避免目标接受 TCP 后不发送 SSH banner 时无限等待。
