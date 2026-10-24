# 变更日志

本文件记录 OneSSH 的重要变更，格式参考 [Keep a Changelog](https://keepachangelog.com/)。

## [未发布]

### 新增

- 后台任务自动清理：启动约 1 分钟后及此后每小时逐台主机探测全部运行中任务（每台独立预算，慢主机不会挤掉排队的主机），本轮确实探测失败且连续 7 天无法确认的任务标为 `lost` 并同步命令记录为失联；结束超过 30 天的任务记录自动删除，读取其日志时返回 410 并说明原因。升级时存量运行中任务从迁移时刻起算 7 天宽限期。
- 主机归档：WebUI 主机「⋯」菜单新增「归档 / 取消归档」，列表默认隐藏已归档主机并提供筛选。归档主机保留配置与历史，但断开连接、停止监控与任务刷新，并对所有 MCP 令牌隐藏；终端、文件、指标与概览不再列出它。仍被活跃主机用作跳板的主机不能归档。
- 新增 `ONESSH_MASTER_KEY_FILE`：从文件读取主密钥，便于配合 Docker secrets、systemd credential，避免主密钥明文出现在环境变量；与 `ONESSH_MASTER_KEY` 互斥，同时设置时启动报错。`docker-compose.yml` 允许把 `ONESSH_MASTER_KEY` 置空以改用文件，变量未定义时仍在 Compose 阶段直接报错；README 补充 Docker secrets 叠加示例（[#27](https://github.com/Lynricsy/OneSSH/pull/27)）。

### 修复

- WebUI「后台任务」页在存在离线主机上的运行中任务时一直停在骨架屏：任务列表（`GET /api/v1/jobs` 与 MCP `job_list`）原先逐个登录主机刷新，连接池对同一主机串行拨号，每个失联任务都要等满建连与握手超时。现在按主机分组、每台只连一次、主机间并发，整批硬性截止 8 秒：即使连接池主机锁被占用或已有连接不回应 session 请求，也会到点直接返回；仍挂着上一次刷新的主机不会重复叠加刷新。未能确认的任务保留在列表中并带 `refresh_error`，界面显示为「状态未知」，不再静默消失。

### 安全

- 构建工具链 Go 升级至 1.26.10（`go.mod`，CI 与发布构建据此安装），修复 govulncheck 报告的 16 个静态可达标准库告警，涉及 `net/http`（HTTP/1 与 HTTP/2 服务端/客户端、Range 头解析）、`net/textproto`/`mime/multipart`、`crypto/tls`、`crypto/x509`、`net/url`、`encoding/asn1`（GO-2026-6809、6617、6613、6612、6611、6610、6609、6608、6607、6605、6603、6218、6090、6089、5972、5026）。
- 前端构建依赖 `vite` 升级至 6.4.5，修复开发服务器相关的 GHSA-587h-7wmj-h5pm、GHSA-698v-4r3x-3vcv、GHSA-r825-w8mm-5pxw、GHSA-rq7h-c2jc-7f22、GHSA-9jrq-w75r-8gcw；只影响 `npm run dev`，不影响嵌入发布产物的静态资源。

## [0.1.19] - 2026-10-14

### 新增

- 正式发布的镜像同时推送到 Docker Hub（`lynricsy/onessh`）与 GHCR，两边标签相同、摘要一致，并各自附带构建 provenance。

### 安全

- 升级 `golang.org/x/crypto` 至 v0.56.0，修复 SSH 通道死锁导致的拒绝服务问题（GO-2026-6354、GO-2026-6355）。
- 升级 `golang.org/x/image` 至 v0.45.0，修复 WebP（VP8L）解码时的过量内存分配（GO-2026-6222），影响 `image_view`。
- 前端构建依赖 `source-map-js` 升级，修复 GHSA-68fv-2mgg-jv7q。

### 优化

- `host_status` 实时采样不再完整解析一遍随即丢弃的首轮输出，只提取计算 CPU 差值所需的计数。

## [0.1.18] - 2026-09-17

### 新增

- 令牌级 MCP 工具组 denylist：手动创建/更新令牌与 OAuth 同意页可选择与 `ONESSH_DISABLED_TOOLS` 相同的工具组；对该 Bearer 主体，`tools/list` 隐藏、`tools/call` 拒绝并记审计，MCP Apps 卡片同步不可见；有效禁用 = 进程级 ∪ 令牌级。WebUI / REST 管理面不受影响。新增 `GET /api/v1/tool-groups`、`PUT /api/v1/tokens/{id}`（名称与权限全量替换）。

## [0.1.17] - 2026-09-11

### 新增

- 新增 `ONESSH_DISABLED_TOOLS`：按工具组（`hosts`、`exec`、`fanout`、`jobs`、`files`、`search`、`monitor`、`image`、`memory`）关闭 MCP 暴露面，常用于 Agent 自带记忆系统时设置 `memory`。被关闭的组不注册为工具，服务器 `instructions` 中对应段落与该组的 MCP Apps 卡片资源同步移除；未知组名在启动时直接报错。WebUI 与 REST 管理接口不受影响（[#20](https://github.com/Lynricsy/OneSSH/issues/20)）。

## [0.1.16] - 2026-09-03

### 修复

- 修复长期在线的 MCP 客户端（如 ChatGPT connector）在 access token 到期刷新时可能被永久踢下线的问题（[#18](https://github.com/Lynricsy/OneSSH/issues/18)）：
  - 将 OAuth refresh token 消费、新 access token 与替代 refresh token 的签发合并为单个立即写事务；任何一步失败都整体回滚，旧 refresh token 保持可用，客户端重试即可自愈，不再出现凭据被烧毁却没有替代品的情况。
  - 所有数据库写事务改用 `BEGIN IMMEDIATE`（`_txlock=immediate`）：先取写锁再读取，消除 WAL 下「先读后写」事务因读快照过期立即失败的 `SQLITE_BUSY_SNAPSHOT`，该错误不受 `busy_timeout` 保护。此前监控采样等并发写入随时可能撞上刷新流程。
  - `/oauth/token` 的服务端内部错误改以 5xx 返回，与 `invalid_grant` 等协议级 4xx 区分开，客户端据此退避重试而不是判定凭据永久失效。
- `/oauth/token` 的服务端内部错误现在写入日志（含 grant_type 与底层错误），此前这类故障在服务端不留任何痕迹；OAuth 动态注册失败同样补上日志。

## [0.1.15] - 2026-09-03

### 新增

- MCP Apps 交互卡片：全部 32 个工具各带一张自包含 HTML 卡片，在 ChatGPT 网页版、Claude 等支持该扩展的客户端里把工具结果直接渲染成界面，而不是一段 JSON。
  - 工具描述符带 `_meta.ui.resourceUri`，资源以 `text/html;profile=mcp-app` 返回，走 `postMessage` 上的 `ui/*` JSON-RPC 与宿主通信。
  - 另附旧版 `openai/outputTemplate` 别名，指向同一份正文的 `text/html+skybridge` 版本；该资源走 URI 模板发布，不会把 `resources/list` 刷成两倍长。
  - 资源 URI 按内容哈希版本化，卡片内容变更后宿主缓存自动失效。
  - 卡片跟随宿主主题与设计令牌切换深浅色，交互限于翻页、进目录、预览、刷新等只读导航；有副作用的工具不允许由卡片发起调用。
  - 新增 `ONESSH_MCP_APPS` 开关（默认 `on`），`off` 时不发布任何卡片资源与 `_meta`。
  - 新增预览画廊 `internal/mcpserver/apps/preview/`，用静态服务器即可离线验收全部卡片。
  - 新增卡片运行时一致性测试并接入 CI：在 Node 里执行 `runtime.js`，覆盖 `ui/*` 协议报文、只读回调白名单与 32 张卡片对真实样例的渲染。

## [0.1.14] - 2026-08-27

### 修复

- 修复大量主机同时采样时的 SQLite 写锁竞争：
  - 串行化指标写入、指标清理与审计写入，保留数据库多连接读取能力。
  - 每轮监控固定使用最多 5 个采样 worker；上一轮未结束时跳过新的轮询，避免跨轮次突破并发上限或累积 goroutine。
  - 轮询取消后停止派发新任务，并等待活动 worker 退出。
- 每 10 分钟尝试执行 `PRAGMA wal_checkpoint(TRUNCATE)` 回收 WAL 文件；检查点被活动连接阻塞时记录 `busy=1` 并在下一周期重试。
- 为 SSH 协议握手增加 15 秒期限并接入调用上下文取消，避免目标接受 TCP 后不发送 SSH banner 时无限等待。
