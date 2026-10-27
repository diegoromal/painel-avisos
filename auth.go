package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const sessionCookie = "painel_sess"
const sessionTTL = 12 * time.Hour

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	Active   bool   `json:"active"`
	Created  int64  `json:"createdAt"`
	TOTP     bool   `json:"totp"`
}

func (u *User) display() string {
	if u.Name != "" {
		return u.Name
	}
	return u.Username
}

type ctxKey int

const userKey ctxKey = 1

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// randomPassword returns an easy-to-type password (no ambiguous chars).
func randomPassword() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 14)
	rand.Read(b)
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = alphabet[int(c)%len(alphabet)]
	}
	return string(out)
}

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func hashPassword(p string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(p), 12)
	return string(h), err
}

func userFromRequest(db *sql.DB, r *http.Request) *User {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	u := &User{}
	var active int
	var totp string
	err = db.QueryRow(`SELECT u.id,u.username,u.name,u.role,u.active,u.created_at,u.totp_secret FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.token_hash=? AND s.expires_at>?`, sha(c.Value), now()).Scan(&u.ID, &u.Username, &u.Name, &u.Role, &active, &u.Created, &totp)
	u.TOTP = totp != ""
	if err != nil || active == 0 {
		return nil
	}
	u.Active = true
	return u
}

func currentUser(r *http.Request) *User {
	u, _ := r.Context().Value(userKey).(*User)
	return u
}

// requireUser protects JSON admin APIs. Mutating requests must carry the
// X-Requested-With header (simple CSRF defense together with SameSite=Strict).
func (a *App) requireUser(role string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := userFromRequest(a.db, r)
		if u == nil {
			jsonErr(w, http.StatusUnauthorized, "não autenticado")
			return
		}
		if role == "admin" && u.Role != "admin" {
			jsonErr(w, http.StatusForbidden, "apenas administradores")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Requested-With") != "painel" {
			jsonErr(w, http.StatusForbidden, "requisição bloqueada (CSRF)")
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
	}
}

// requireToken protects the programmatic API (Authorization: Bearer <token>).
func (a *App) requireToken(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		tok, ok := strings.CutPrefix(auth, "Bearer ")
		if !ok || strings.TrimSpace(tok) == "" {
			jsonErr(w, http.StatusUnauthorized, "token ausente (Authorization: Bearer <token>)")
			return
		}
		var id int64
		var name string
		if err := a.db.QueryRow(`SELECT id,name FROM api_tokens WHERE token_hash=?`, sha(strings.TrimSpace(tok))).Scan(&id, &name); err != nil {
			jsonErr(w, http.StatusUnauthorized, "token inválido")
			return
		}
		a.db.Exec(`UPDATE api_tokens SET last_used=? WHERE id=?`, now(), id)
		u := &User{ID: 0, Username: "api:" + name, Name: "API (" + name + ")", Role: "leader", Active: true}
		h(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
	}
}

// ---- login throttling (per client IP) ----

type limiter struct {
	mu   sync.Mutex
	fail map[string][]time.Time
}

func newLimiter() *limiter { return &limiter{fail: map[string][]time.Time{}} }

// blocked: more than 8 failures in the last 10 minutes.
func (l *limiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := time.Now().Add(-10 * time.Minute)
	var keep []time.Time
	for _, t := range l.fail[ip] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	l.fail[ip] = keep
	return len(keep) >= 8
}

func (l *limiter) failed(ip string) {
	l.mu.Lock()
	l.fail[ip] = append(l.fail[ip], time.Now())
	l.mu.Unlock()
}

func (l *limiter) reset(ip string) {
	l.mu.Lock()
	delete(l.fail, ip)
	l.mu.Unlock()
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := a.clientIP(r)
	if a.lim.blocked(ip) {
		jsonErr(w, http.StatusTooManyRequests, "muitas tentativas; aguarde alguns minutos")
		return
	}
	var in struct{ Username, Password, Code string }
	if !readJSON(w, r, &in) {
		return
	}
	var id, totpLast int64
	var hash, totpSecret string
	var active int
	err := a.db.QueryRow(`SELECT id,pass_hash,active,totp_secret,totp_last FROM users WHERE username=?`, strings.TrimSpace(in.Username)).Scan(&id, &hash, &active, &totpSecret, &totpLast)
	if err != nil || active == 0 || bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
		a.lim.failed(ip)
		time.Sleep(400 * time.Millisecond)
		jsonErr(w, http.StatusUnauthorized, "usuário ou senha inválidos")
		return
	}
	if totpSecret != "" {
		if strings.TrimSpace(in.Code) == "" {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"informe o código do aplicativo autenticador","totpRequired":true}`))
			return
		}
		c := verifyTOTP(totpSecret, in.Code, totpLast, time.Now())
		if c < 0 {
			a.lim.failed(ip)
			time.Sleep(400 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"código de verificação inválido","totpRequired":true}`))
			return
		}
		a.db.Exec(`UPDATE users SET totp_last=? WHERE id=?`, c, id)
	}
	a.lim.reset(ip)
	tok := randomToken(32)
	a.db.Exec(`DELETE FROM sessions WHERE expires_at<?`, now())
	if _, err := a.db.Exec(`INSERT INTO sessions(token_hash,user_id,expires_at) VALUES(?,?,?)`, sha(tok), id, time.Now().Add(sessionTTL).Unix()); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true, Secure: a.secureCookie(r), SameSite: http.SameSiteStrictMode, MaxAge: int(sessionTTL.Seconds())})
	audit(a.db, in.Username, "login", ip)
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.db.Exec(`DELETE FROM sessions WHERE token_hash=?`, sha(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.secureCookie(r), SameSite: http.SameSiteStrictMode})
	writeJSON(w, map[string]any{"ok": true})
}
