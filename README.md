# OneSSH

OneSSH 是面向 AI Agent 的集中式 SSH 网关。它以单个 Go 二进制运行，通过 WebUI 管理 SSH 主机、密钥和 Agent 令牌，并在同一端口提供 Streamable HTTP MCP、管理 API、SSE 活动流和 WebSocket 终端。

## 功能

- SSH 主机与 OpenSSH 密钥管理，支持密码和密钥认证
- AES-256-GCM 信封加密；私钥和密码仅在拨号时解密
- TOFU 主机指纹、指纹变更拒绝、连接复用与 keepalive
- Streamable HTTP MCP，支持按令牌限制可访问主机
- 持久 cwd/env 的命令会话、超时、并发执行和大输出 artifact
- 无远端常驻组件的后台任务启动、状态、日志与终止
- SFTP 读取、原子写入、乐观锁编辑、目录浏览和跨主机传输
- 面向编码 Agent 的 `grep`/`find`，调用远端 ripgrep/fd 并遵循项目忽略规则
- PNG、JPEG、GIF 首帧和 WebP 图片查看及缩放
- Linux CPU、内存、负载、磁盘指标采集和 7 天保留
- React 管理控制台、SSE 实时活动、Ghostty Web WASM/Canvas 终端
- 结构化审计；文件正文和编辑内容仅记录长度摘要

## 架构

```mermaid
flowchart LR
    Agent[AI Agent] -->|Bearer Token / MCP| Gateway[OneSSH Gateway]
    Browser[WebUI] -->|Cookie / REST / SSE / WS| Gateway
    Gateway --> SQLite[(SQLite /data/onessh.db)]
    Gateway --> Artifacts[/data/artifacts]
    Gateway -->|SSH + SFTP| Hosts[SSH Hosts]
```

后端使用 Go 标准库 HTTP 路由、`modelcontextprotocol/go-sdk`、`x/crypto/ssh`、`pkg/sftp` 和 CGO-free SQLite。前端使用 React 18、antd、Ghostty Web 和 Recharts，并通过 `go:embed` 打入二进制。

## 环境变量

| 变量 | 必填 | 默认值 | 说明 |
|---|---:|---|---|
| `ONESSH_MASTER_KEY` | 是 | — | 64 位十六进制字符串，即 32 字节主密钥 |
| `ONESSH_ADMIN_PASSWORD` | 是 | — | WebUI 管理员密码 |
| `ONESSH_LISTEN` | 否 | `:8866` | HTTP 监听地址 |
| `ONESSH_DATA_DIR` | 否 | `/data` | SQLite 和 artifact 数据目录 |
| `ONESSH_POLL_INTERVAL` | 否 | `60` | 监控轮询秒数；设为 `0` 关闭 |

生成主密钥：

```sh
openssl rand -hex 32
```

主密钥丢失后，已加密的 SSH 密钥和密码无法恢复。生产环境必须使用持久化密钥管理，不要在每次启动时重新生成。

## 使用 Docker Compose 启动

```sh
export ONESSH_MASTER_KEY="$(openssl rand -hex 32)"
export ONESSH_ADMIN_PASSWORD='replace-with-a-strong-password'
docker compose up -d --build
curl --fail http://localhost:8866/healthz
```

控制台地址：<http://localhost:8866/>。

首次使用流程：

1. 使用 `ONESSH_ADMIN_PASSWORD` 登录。
2. 在“密钥”页面生成 ed25519 密钥或导入 OpenSSH 私钥；也可直接使用主机密码。
3. 在“主机”页面添加目标并执行“测试”，首次连接会记录 TOFU 指纹。
4. 在“令牌”页面创建 Agent 令牌，并选择全部主机或指定主机。
5. 令牌明文只显示一次，立即复制到 Agent 的安全配置中。

停止服务不会删除数据：

```sh
docker compose down
```

只有明确需要清空数据库和密钥时才删除卷：

```sh
docker compose down -v
```

## MCP 接入

MCP 端点为：

```text
http://localhost:8866/mcp
```

所有请求必须携带创建令牌时返回的 Bearer Token：

```http
Authorization: Bearer osh_...
```

支持 Streamable HTTP 的客户端可使用类似配置：

```json
{
  "mcpServers": {
    "onessh": {
      "type": "streamable-http",
      "url": "http://localhost:8866/mcp",
      "headers": {
        "Authorization": "Bearer osh_REPLACE_ME"
      }
    }
  }
}
```

具体字段名取决于 MCP 客户端。若客户端没有独立的 `headers` 配置，需要通过其 HTTP transport 注入 `Authorization`。

主要工具：

| 类别 | 工具 |
|---|---|
| 主机与执行 | `hosts_list`、`exec`、`session_env`、`exec_many`、`output_read` |
| 后台任务 | `job_start`、`job_list`、`job_status`、`job_logs`、`job_kill` |
| 文件 | `file_read`、`file_write`、`file_edit`、`file_list`、`file_transfer` |
| 搜索 | `grep`（ripgrep）、`find`（fd/fdfind） |
| 资源 | `image_view`、`host_status` |

`file_edit` 支持 `expected_sha256` 乐观锁；冲突时应重新读取。大输出会返回 `artifact_id`，再通过 `output_read` 分段读取或正则过滤。

Pi 风格的简单编码工具已完整覆盖：`file_read`、`file_write`、`file_edit`、`exec`、`grep`、`find`、`file_list` 分别对应 read、write、edit、bash、grep、find、ls。`grep` 和 `find` 直接在目标主机运行 `rg` 与 `fd`（Debian 的 `fdfind` 也受支持），以保留原生性能和 `.gitignore` 语义；OneSSH 不会自动修改远端主机，使用前需自行安装这两个可选命令。搜索请求最多运行 30 秒，结构化结果最大 256 KiB，并受各工具的条目上限约束。

## 本地构建

要求 Go 1.23 或更高版本、Node.js 22 或更高版本。

```sh
cd web
npm install
npm run build
cd ..
CGO_ENABLED=0 go build -o onessh ./cmd/onessh
```

直接运行：

```sh
export ONESSH_MASTER_KEY="$(openssl rand -hex 32)"
export ONESSH_ADMIN_PASSWORD='replace-with-a-strong-password'
export ONESSH_DATA_DIR="$PWD/data"
./onessh
```

## 测试

单元测试和前端生产构建：

```sh
go test -count=1 ./...
(cd web && npm run build)
```

完整端到端测试会启动 OneSSH 和两个 OpenSSH 容器。该测试固定使用管理员密码 `test123`，并会通过管理 API 创建 `ssh1`、`ssh2` 和测试令牌：

```sh
export ONESSH_MASTER_KEY="$(openssl rand -hex 32)"
export ONESSH_ADMIN_PASSWORD='test123'
docker compose -f docker-compose.yml -f docker-compose.test.yml down -v
docker compose -f docker-compose.yml -f docker-compose.test.yml up -d --build
ONESSH_URL=http://localhost:8866/mcp \
  go test -count=1 -tags e2e -run TestEndToEnd -v ./e2e
```

测试覆盖持久 cwd、大输出 artifact、后台任务、结构化文件编辑、跨主机传输、图片、主机授权、现场监控、审计脱敏和 WebSocket 终端。

## GitHub CI 与 GHCR

`.github/workflows/ci.yml` 会在 push、针对 `main` 的 pull request 以及手动触发时执行：

- `gofmt`、`go mod tidy` 差异、`go vet`、单元测试和 CGO-free 构建
- 前端依赖锁定安装、TypeScript 检查和生产构建
- Docker Compose 双主机端到端测试

只有 `main` 分支或 `v*` 标签的 push 在全部检查通过后才发布镜像。工作流使用仓库自带的 `GITHUB_TOKEN` 写入 GHCR，并生成镜像来源证明，不需要额外配置 PAT。

```sh
docker pull ghcr.io/lynricsy/onessh:latest
```

同时会生成分支、Git 标签和 `sha-<commit>` 镜像标签。GHCR 首次发布的包默认为私有；如需匿名拉取，应在 GitHub Packages 设置中显式改为公开。

## 数据与安全

- `/data/onessh.db`：主机、加密凭据、令牌哈希、任务、审计和指标。
- `/data/artifacts/`：截断命令输出；服务启动时及每小时删除超过 7 天的文件。指标数据采用相同保留策略。
- Agent 令牌明文不入库；数据库只保存 SHA-256 哈希。
- WebUI 使用 24 小时 HMAC Cookie；生产环境应通过 HTTPS 反向代理访问。
- 首次连接会接受并保存主机指纹。指纹变化会拒绝连接，确认主机已重装后才能在 WebUI 重置。
- 不要将 `ONESSH_MASTER_KEY`、管理员密码、Agent 令牌或导出的数据卷提交到版本控制。
- 令牌应按最小权限分配主机；不再使用时立即删除。
