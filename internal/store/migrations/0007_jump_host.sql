ALTER TABLE hosts ADD COLUMN jump_host_id INTEGER REFERENCES hosts(id);
