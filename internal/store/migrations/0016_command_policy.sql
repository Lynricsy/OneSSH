-- 注意：版本 16 的迁移由 store.go 的 migration0016Columns 逐列判存执行（幂等）；
-- 本文件是 schema 的对照文档，单独编辑这里不会改变迁移行为。
-- TestMigration0016DDLMatchesIdempotentCase 钉死两者逐条一致，改动需两处同步。

-- 审计区分「策略拒绝」与「执行失败」：ok 位只能区分成功/失败，WebUI 无法稳定分辨
-- 一条记录是被网关拦下的还是真跑挂了，只能去猜错误文案。outcome 是持久化的分类，
-- deny_reason 记下具体命中的规则，历史记录留空表示迁移前无法判定。
ALTER TABLE audit ADD COLUMN outcome TEXT NOT NULL DEFAULT '';
ALTER TABLE audit ADD COLUMN deny_reason TEXT;

-- 令牌级命令策略与工具组 denylist 同构：令牌只能收紧实例级策略。
-- 授权码与刷新令牌也要带上，否则同意页收紧的选择会在换令牌时丢掉。
ALTER TABLE tokens ADD COLUMN command_policy_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE oauth_authorization_codes ADD COLUMN command_policy_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE oauth_refresh_tokens ADD COLUMN command_policy_json TEXT NOT NULL DEFAULT '{}';
