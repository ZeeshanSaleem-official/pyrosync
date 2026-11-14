#!/usr/bin/env bash
set -euo pipefail

BACKUP_DIR="${1:-./backups}"
mkdir -p "$BACKUP_DIR"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
OUT="$BACKUP_DIR/pyrosync_${STAMP}.dump"

docker compose -f deploy/docker-compose.yml exec -T db \
  pg_dump -U pyrosync -d pyrosync -Fc > "$OUT"

ls -1t "$BACKUP_DIR"/pyrosync_*.dump | tail -n +4 | xargs -r rm -f
echo "Saved backup: $OUT"
