ALTER TABLE jobs ADD COLUMN last_checked_at INTEGER;
-- 存量运行中任务没有确认记录，从迁移时刻起算宽限期：按启动时间算会把刚离线几天的主机直接判成失联。
UPDATE jobs SET last_checked_at = strftime('%s','now') WHERE status = 'running';
