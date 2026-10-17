-- 归档时刻；NULL 表示未归档。归档主机保留全部配置与历史，但不再被监控、不再对 MCP 令牌可见。
ALTER TABLE hosts ADD COLUMN archived_at INTEGER;
