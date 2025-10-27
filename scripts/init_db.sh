#!/usr/bin/env bash
set -euo pipefail

: "${PGHOST:=127.0.0.1}"
: "${PGPORT:=5432}"
: "${PGUSER:=postgres}"
: "${PGDATABASE:=mqtt}"
: "${PGPASSWORD:=}"
: "${MQTT_DB_USER:=mqtt_auth}"
: "${MQTT_DB_PASS:=StrongPass}"

export PGPASSWORD="$PGPASSWORD"

psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d postgres -v ON_ERROR_STOP=1 <<'SQL'
DO $$
BEGIN
   IF NOT EXISTS (SELECT FROM pg_database WHERE datname = current_setting('PGDATABASE')) THEN
      EXECUTE format('CREATE DATABASE %I', current_setting('PGDATABASE'));
   END IF;
END$$;
SQL

psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d "$PGDATABASE" -v ON_ERROR_STOP=1 <<SQL
DO $$
BEGIN
   IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '$MQTT_DB_USER') THEN
      EXECUTE format('CREATE ROLE %I LOGIN PASSWORD %L', '$MQTT_DB_USER', '$MQTT_DB_PASS');
   END IF;
END$$;
GRANT CONNECT ON DATABASE "$PGDATABASE" TO "$MQTT_DB_USER";
GRANT USAGE ON SCHEMA public TO "$MQTT_DB_USER";
SQL

# apply schema
psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d "$PGDATABASE" -v ON_ERROR_STOP=1 -f "$(dirname "$0")/init_db.sql"

# minimal privileges
psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d "$PGDATABASE" -v ON_ERROR_STOP=1 <<SQL
GRANT SELECT ON TABLE users, acls, client_bindings TO "$MQTT_DB_USER";
SQL

echo "DB initialized. DSN example:"
echo "postgres://$MQTT_DB_USER:$MQTT_DB_PASS@$PGHOST:$PGPORT/$PGDATABASE?sslmode=disable"
