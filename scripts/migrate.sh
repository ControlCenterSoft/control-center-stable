#!/usr/bin/env sh
set -eu

MIGRATIONS_DIR=${MIGRATIONS_DIR:-/migrations}

if ! command -v psql >/dev/null 2>&1; then
    echo "psql is required." >&2
    exit 2
fi
if ! command -v sha256sum >/dev/null 2>&1; then
    echo "sha256sum is required." >&2
    exit 2
fi
if [ ! -d "$MIGRATIONS_DIR" ]; then
    echo "Migration directory not found: $MIGRATIONS_DIR" >&2
    exit 2
fi

psql -X --set=ON_ERROR_STOP=1 <<'SQL'
CREATE TABLE IF NOT EXISTS schema_migrations (
    version     VARCHAR(128) PRIMARY KEY,
    checksum   CHAR(64) NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
SQL

found=0
for migration in "$MIGRATIONS_DIR"/*.up.sql; do
    if [ ! -f "$migration" ]; then
        continue
    fi
    found=1
    filename=${migration##*/}
    version=${filename%.up.sql}
    case "$version" in
        ''|*[!0-9A-Za-z._-]*) echo "Invalid migration filename: $filename" >&2; exit 2 ;;
    esac
    checksum=$(sha256sum "$migration" | awk '{print $1}')
    applied_checksum=$(psql -X --tuples-only --no-align --set=ON_ERROR_STOP=1 --set=version="$version" <<'SQL'
SELECT checksum FROM schema_migrations WHERE version = :'version';
SQL
)
    if [ -n "$applied_checksum" ]; then
        if [ "$applied_checksum" != "$checksum" ]; then
            echo "Checksum mismatch for applied migration $filename" >&2
            exit 1
        fi
        echo "Migration already applied: $filename"
        continue
    fi
    echo "Applying migration: $filename"
    normalized=$(mktemp)
    trap 'rm -f "$normalized"' EXIT HUP INT TERM
    awk '{ statement=$0; gsub(/^[[:space:]]+|[[:space:]]+$/, "", statement); statement=toupper(statement); if (statement=="BEGIN;" || statement=="COMMIT;") next; print }' "$migration" > "$normalized"
    {
        printf '%s\n' '\set ON_ERROR_STOP on' 'BEGIN;'
        printf '\\i %s\n' "$normalized"
        printf "INSERT INTO schema_migrations (version, checksum) VALUES ('%s', '%s');\n" "$version" "$checksum"
        printf '%s\n' 'COMMIT;'
    } | psql -X --set=ON_ERROR_STOP=1
    rm -f "$normalized"
    trap - EXIT HUP INT TERM
done

if [ "$found" -eq 0 ]; then
    echo "No forward migrations found in $MIGRATIONS_DIR" >&2
    exit 2
fi

echo "Database migrations are current."
