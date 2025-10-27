-- users: bcrypt password hash + enabled flag
CREATE TABLE IF NOT EXISTS users (
  username       TEXT PRIMARY KEY,
  password_hash  TEXT NOT NULL,
  enabled        BOOLEAN NOT NULL DEFAULT TRUE,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- optional clientId binding (if enforce_bind=true)
CREATE TABLE IF NOT EXISTS client_bindings (
  username  TEXT NOT NULL,
  client_id TEXT NOT NULL,
  PRIMARY KEY (username, client_id),
  FOREIGN KEY (username) REFERENCES users(username) ON DELETE CASCADE
);

-- ACLs: topic pattern (+/# supported), bitmask acc: 1=read, 2=write, 4=subscribe
CREATE TABLE IF NOT EXISTS acls (
  username TEXT NOT NULL,           -- use '*' for global rules
  pattern  TEXT NOT NULL,           -- supports placeholders {username}/{clientid}
  acc      INTEGER NOT NULL,
  PRIMARY KEY (username, pattern)
);
CREATE INDEX IF NOT EXISTS acls_user_idx ON acls(username);
