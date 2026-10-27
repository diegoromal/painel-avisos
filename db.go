package main

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE COLLATE NOCASE,
  name TEXT NOT NULL DEFAULT '',
  pass_hash TEXT NOT NULL,
  role TEXT NOT NULL DEFAULT 'leader',
  active INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
  token_hash TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS screens (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE COLLATE NOCASE,
  label TEXT NOT NULL DEFAULT '',
  last_seen INTEGER NOT NULL DEFAULT 0,
  last_ip TEXT NOT NULL DEFAULT '',
  user_agent TEXT NOT NULL DEFAULT '',
  offline_notified INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS announcements (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '',
  priority TEXT NOT NULL DEFAULT 'normal',
  starts_at INTEGER NOT NULL,
  ends_at INTEGER,
  ended_at INTEGER,
  ended_by_name TEXT NOT NULL DEFAULT '',
  all_screens INTEGER NOT NULL DEFAULT 1,
  image TEXT NOT NULL DEFAULT '',
  author_id INTEGER,
  author_name TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  updated_by_name TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS announcement_screens (
  announcement_id INTEGER NOT NULL REFERENCES announcements(id) ON DELETE CASCADE,
  screen_id INTEGER NOT NULL REFERENCES screens(id) ON DELETE CASCADE,
  PRIMARY KEY (announcement_id, screen_id)
);
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS api_tokens (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  prefix TEXT NOT NULL,
  created_by TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  last_used INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS audit_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  at INTEGER NOT NULL,
  actor TEXT NOT NULL,
  action TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_ann_times ON announcements(ended_at, starts_at, ends_at);
`

var defaultSettings = map[string]string{
	"company_name":    "Painel de Avisos",
	"logo":            "",
	"rotate_seconds":  "15",
	"webhook_url":     "",
	"offline_minutes": "2",
	"sound_enabled":   "1",
	// v1.2: aviso urgente intercalado com os normais (instalações antigas recebem estes padrões ao iniciar)
	"urgent_mode":        "interleave", // interleave | fixed
	"urgent_seconds":     "60",
	"urgent_beep_repeat": "1",
}

func openDB(dataDir string) (*sql.DB, error) {
	dsn := "file:" + filepath.ToSlash(filepath.Join(dataDir, "painel.db")) +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: serialize writers, avoids SQLITE_BUSY
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	// migrações (v1.1): chaves por tela e 2FA
	for _, m := range []string{
		`ALTER TABLE screens ADD COLUMN key_hash TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE screens ADD COLUMN key_prefix TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE screens ADD COLUMN key_created_at INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE users ADD COLUMN totp_secret TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN totp_pending TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN totp_last INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := db.Exec(m); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return nil, fmt.Errorf("migração: %w", err)
		}
	}
	for k, v := range defaultSettings {
		if _, err := db.Exec(`INSERT OR IGNORE INTO settings(key,value) VALUES(?,?)`, k, v); err != nil {
			return nil, err
		}
	}
	return db, nil
}

func now() int64 { return time.Now().Unix() }

func getSettings(db *sql.DB) (map[string]string, error) {
	rows, err := db.Query(`SELECT key,value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, rows.Err()
}

func settingInt(m map[string]string, k string, def int) int {
	n, err := strconv.Atoi(m[k])
	if err != nil {
		return def
	}
	return n
}

func audit(db *sql.DB, actor, action, detail string) {
	db.Exec(`INSERT INTO audit_log(at,actor,action,detail) VALUES(?,?,?,?)`, now(), actor, action, detail)
}
