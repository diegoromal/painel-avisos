#!/bin/sh
# Backup diário do Painel de Avisos (banco SQLite consistente + imagens), com retenção.
# Cron (root ou usuário do grupo docker), ex. todo dia às 03:15:
#   15 3 * * * /opt/painel-avisos/deploy/traefik/backup.sh >> /var/log/painel-avisos-backup.log 2>&1
set -eu
cd "$(dirname "$0")"
[ -f .env ] && set -a && . ./.env && set +a
BACKUP_DIR="${BACKUP_DIR:-/opt/painel-avisos/backups}"
RETENTION="${BACKUP_RETENTION_DAYS:-14}"
SVC=painel-avisos
TS=$(date +%Y%m%d-%H%M%S)
mkdir -p "$BACKUP_DIR"
chmod 700 "$BACKUP_DIR"

exec 9>"$BACKUP_DIR/.lock"
flock -n 9 || { echo "$(date -Is) backup já em execução"; exit 1; }

# 1) cópia consistente do SQLite com o servidor ligado (VACUUM INTO = equivalente ao .backup)
docker compose exec -T "$SVC" painel-avisos backup -data /data -out "/data/backup-$TS.db"
docker compose cp "$SVC:/data/backup-$TS.db" "$BACKUP_DIR/painel-$TS.db"
docker compose exec -T "$SVC" rm -f "/data/backup-$TS.db"
gzip -9 "$BACKUP_DIR/painel-$TS.db"

# 2) imagens enviadas
docker compose exec -T "$SVC" tar -C /data -czf - uploads > "$BACKUP_DIR/uploads-$TS.tar.gz"

chmod 600 "$BACKUP_DIR"/painel-$TS.db.gz "$BACKUP_DIR"/uploads-$TS.tar.gz

# 3) retenção
find "$BACKUP_DIR" -maxdepth 1 -type f \( -name 'painel-*.db.gz' -o -name 'uploads-*.tar.gz' \) -mtime +"$RETENTION" -print -delete

echo "$(date -Is) backup ok: $BACKUP_DIR/painel-$TS.db.gz ($(du -h "$BACKUP_DIR/painel-$TS.db.gz" | cut -f1)) + uploads-$TS.tar.gz"
