CREATE TABLE memories (
  id INTEGER PRIMARY KEY,
  host_id INTEGER REFERENCES hosts(id),
  content TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT 'mcp',
  importance REAL NOT NULL DEFAULT 0.5,
  veracity TEXT NOT NULL DEFAULT 'stated'
    CHECK(veracity IN ('stated','inferred','tool','unknown')),
  token_id INTEGER,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  recall_count INTEGER NOT NULL DEFAULT 0,
  last_recalled INTEGER,
  embedding BLOB,
  embedding_model TEXT
);
CREATE INDEX memories_host ON memories(host_id);
CREATE VIRTUAL TABLE memories_fts USING fts5(
  content, content='memories', content_rowid='id', tokenize='trigram'
);
CREATE TRIGGER memories_ai AFTER INSERT ON memories BEGIN
  INSERT INTO memories_fts(rowid, content) VALUES (new.id, new.content);
END;
CREATE TRIGGER memories_ad AFTER DELETE ON memories BEGIN
  INSERT INTO memories_fts(memories_fts, rowid, content) VALUES ('delete', old.id, old.content);
END;
CREATE TRIGGER memories_au AFTER UPDATE OF content ON memories BEGIN
  INSERT INTO memories_fts(memories_fts, rowid, content) VALUES ('delete', old.id, old.content);
  INSERT INTO memories_fts(rowid, content) VALUES (new.id, new.content);
END;
