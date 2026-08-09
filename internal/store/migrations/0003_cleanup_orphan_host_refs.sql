DELETE FROM token_hosts WHERE NOT EXISTS (SELECT 1 FROM hosts WHERE hosts.id = token_hosts.host_id);
DELETE FROM sessions WHERE NOT EXISTS (SELECT 1 FROM hosts WHERE hosts.id = sessions.host_id);
DELETE FROM jobs WHERE NOT EXISTS (SELECT 1 FROM hosts WHERE hosts.id = jobs.host_id);
DELETE FROM metrics WHERE NOT EXISTS (SELECT 1 FROM hosts WHERE hosts.id = metrics.host_id);
