#!/bin/sh
# Restaura um backup: ./restore.sh /opt/painel-avisos/backups/painel-AAAAMMDD-HHMMSS.db.gz [uploads-....tar.gz]
set -eu
cd "$(dirname "$0")"
DB_GZ="$1"; UP="${2:-}"
[ -f "$DB_GZ" ] || { echo "arquivo não encontrado: $DB_GZ"; exit 1; }
SVC=painel-avisos
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT
gunzip -c "$DB_GZ" > "$TMP/painel.db"
docker compose stop "$SVC"
docker compose cp "$TMP/painel.db" "$SVC:/data/painel.db"
if [ -n "$UP" ]; then
  docker compose run --rm --no-deps -T -u 0 --entrypoint sh "$SVC" -c 'rm -rf /data/uploads && tar -C /data -xzf -' < "$UP"
fi
# docker cp grava como root: devolve a posse ao usuário do app (uid 10001) e descarta WAL antigo
docker compose run --rm --no-deps -u 0 --entrypoint sh "$SVC" -c 'rm -f /data/painel.db-wal /data/painel.db-shm && chown -R 10001:10001 /data'
docker compose start "$SVC"
echo "restaurado de $DB_GZ"
