# 变更日志

本文件记录 OneSSH 的重要变更，格式参考 [Keep a Changelog](https://keepachangelog.com/)。

## [未发布]

## [0.1.14] - 2026-08-27

### 修复

- 修复大量主机同时采样时的 SQLite 写锁竞争：
  - 串行化指标写入、指标清理与审计写入，保留数据库多连接读取能力。
  - 每轮监控固定使用最多 5 个采样 worker；上一轮未结束时跳过新的轮询，避免跨轮次突破并发上限或累积 goroutine。
  - 轮询取消后停止派发新任务，并等待活动 worker 退出。
- 每 10 分钟尝试执行 `PRAGMA wal_checkpoint(TRUNCATE)` 回收 WAL 文件；检查点被活动连接阻塞时记录 `busy=1` 并在下一周期重试。
- 为 SSH 协议握手增加 15 秒期限并接入调用上下文取消，避免目标接受 TCP 后不发送 SSH banner 时无限等待。
