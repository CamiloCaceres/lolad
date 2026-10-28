CREATE TABLE IF NOT EXISTS amp_identities (
 name TEXT PRIMARY KEY, address TEXT NOT NULL UNIQUE, kind TEXT NOT NULL,
 fingerprint TEXT NOT NULL, created_at TEXT NOT NULL, public_key BLOB NOT NULL,
 api_key_hash TEXT NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS amp_messages (
 id TEXT PRIMARY KEY, from_name TEXT NOT NULL, to_name TEXT NOT NULL,
 thread_id TEXT NOT NULL, created_at TEXT NOT NULL, status TEXT NOT NULL,
 notified_at TEXT NOT NULL DEFAULT '', read_at TEXT NOT NULL DEFAULT '',
 attempts INTEGER NOT NULL DEFAULT 0, mail TEXT NOT NULL, wire TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS amp_inbox ON amp_messages(to_name, status, created_at);
CREATE INDEX IF NOT EXISTS amp_sent ON amp_messages(from_name, created_at);
CREATE TABLE IF NOT EXISTS amp_idempotency (
 owner TEXT NOT NULL, key TEXT NOT NULL, request_hash TEXT NOT NULL,
 response TEXT NOT NULL, expires_at TEXT NOT NULL, PRIMARY KEY(owner,key)
);
CREATE TABLE IF NOT EXISTS amp_attachments (
 id TEXT PRIMARY KEY, owner TEXT NOT NULL, meta TEXT NOT NULL,
 message_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, expires_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS amp_attachments_message ON amp_attachments(message_id);
