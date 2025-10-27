# mosq-pg-v5 — Mosquitto v5 plugin (Go + PostgreSQL)

**What it does:** Authenticate on CONNECT and authorize on PUBLISH/SUBSCRIBE by looking up users/ACLs in PostgreSQL — no HTTP hop, low latency.

## Layout

```
.
├── bridge.c                # Thin C shim: registers Go callbacks with Mosquitto
├── plugin.go               # Go plugin (cgo): BASIC_AUTH + ACL_CHECK -> PostgreSQL
├── cmd/bcryptgen/main.go   # Small CLI to generate bcrypt hashes
├── scripts/
│   ├── init_db.sql         # Minimal schema (users, acls, client_bindings)
│   └── init_db.sh          # Convenience DB initializer
├── mosquitto.conf          # Example Mosquitto config using this plugin
├── Dockerfile              # Multi-stage build to produce a runnable broker image
├── Makefile                # `make build` -> build/mosq_pg_auth.so
└── go.mod
```

## Quick start

### 1) Initialize PostgreSQL
```bash
# Set PG env if needed (defaults PGHOST=127.0.0.1, PGUSER=postgres, PGDATABASE=mqtt)
./scripts/init_db.sh
# Remember the DSN printed at the end; plug it into mosquitto.conf or plugin_opt_pg_dsn
```

Insert a user and ACLs (example for user `alice`):
```sql
-- Generate a bcrypt hash with ./build/bcryptgen 'alice-password'
INSERT INTO users (username, password_hash, enabled)
VALUES ('alice', '$2a$12$REPLACE_ME_WITH_BCRYPT...', TRUE)
ON CONFLICT (username) DO UPDATE SET password_hash=EXCLUDED.password_hash, enabled=EXCLUDED.enabled;

-- Allow alice to read/subscribe to her namespace and write to her "up" topic
INSERT INTO acls (username, pattern, acc) VALUES
('alice', 'devices/{username}/#', 1|4),   -- READ(1) + SUBSCRIBE(4)
('alice', 'devices/{username}/up', 2)     -- WRITE(2)
ON CONFLICT DO NOTHING;
```

### 2) Build the plugin
```bash
make build
# artifacts: build/mosq_pg_auth.so
```

### 3) Generate a bcrypt hash (optional helper)
```bash
make bcryptgen
./build/bcryptgen 'alice-password'
```

### 4) Run Mosquitto (host-installed)
Edit `mosquitto.conf` and set:
```
plugin /absolute/path/to/build/mosq_pg_auth.so
plugin_opt_pg_dsn postgres://mqtt_auth:StrongPass@127.0.0.1:5432/mqtt?sslmode=disable
```

Then start Mosquitto:
```bash
sudo mosquitto -c /path/to/mosquitto.conf -v
```

### 5) Test
```bash
# Subscribe
mosquitto_sub -h 127.0.0.1 -u alice -P 'alice-password' -t devices/alice/# -v

# Publish allowed
mosquitto_pub -h 127.0.0.1 -u alice -P 'alice-password' -t devices/alice/up -m hi

# Publish denied (not alice's namespace)
mosquitto_pub -h 127.0.0.1 -u alice -P 'alice-password' -t devices/bob/up -m x
```

## Docker option

Build an image that includes the plugin and a sample config:
```bash
docker build -t mosq-pg-v5:latest .
# Run Mosquitto (expects a Postgres reachable at 'postgres:5432' by default in mosquitto.conf)
docker run --rm -it --name mosq --network host mosq-pg-v5:latest
```

> Tip: In production, mount your own `mosquitto.conf` and point `plugin_opt_pg_dsn` to a secured DSN
> (e.g., `sslmode=verify-full` with a proper CA).

## Plugin options (set via `plugin_opt_*`)

- `plugin_opt_pg_dsn` — PostgreSQL DSN, e.g. `postgres://user:pass@host:5432/db?sslmode=verify-full`
- `plugin_opt_timeout_ms` — Query timeout in milliseconds (default 1500)
- `plugin_opt_fail_open` — `true/false` (default false). If true, allow when DB is unavailable (not recommended).
- `plugin_opt_enforce_bind` — `true/false` (default false). If true, require a (username, client_id) pair to exist in `client_bindings`.

## Notes

- Requires Mosquitto development headers at build time. On Debian/Ubuntu: `sudo apt-get install -y libmosquitto-dev`.
- The plugin is built with Go using `-buildmode=c-shared`. Mosquitto loads the resulting `.so` directly.
- The ACL matcher supports `+` and `#` and the placeholders `{username}` and `{clientid}` inside patterns.

## Security

- Use TLS for Postgres (`sslmode=verify-full`) and restrict the DB role to `SELECT` only.
- Keep `auth_plugin_deny_special_chars` enabled in Mosquitto unless you have a strong reason to disable it.
- Keep `fail_open=false` for strict security.
