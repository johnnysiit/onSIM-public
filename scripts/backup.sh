#!/bin/sh
set -eu

DB_PATH=${ONSIM_DB_PATH:-/var/lib/onsim/onsim.db}
BACKUP_DIR=${ONSIM_BACKUP_DIR:-/var/backups/onsim}
install -d -m 0750 -o onsim -g onsim "$BACKUP_DIR"
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
TARGET="$BACKUP_DIR/onsim-$STAMP.db"
sqlite3 "$DB_PATH" ".backup '$TARGET'"
chown onsim:onsim "$TARGET"
chmod 0640 "$TARGET"
find "$BACKUP_DIR" -type f -name 'onsim-*.db' -mtime +7 -delete
