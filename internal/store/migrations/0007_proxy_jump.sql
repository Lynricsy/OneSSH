-- Add proxy_jump_host column to support SSH ProxyJump (jumphost/bastion).
-- Stores the host name of an intermediary jump host; NULL means direct connection.
-- Chain is implicit: A.proxy_jump_host=B, B.proxy_jump_host=C → connect via C→B→A.
ALTER TABLE hosts ADD COLUMN proxy_jump_host TEXT;