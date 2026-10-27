package main

import (
	"crypto/subtle"
	"database/sql"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ---------- autenticação das telas (chave por tela) ----------

func screenKeyFrom(r *http.Request) string {
	if k := strings.TrimSpace(r.Header.Get("X-Screen-Key")); k != "" {
		return k
	}
	return strings.TrimSpace(r.URL.Query().Get("key"))
}

// screenAuth valida ?screen= + chave. Regras:
//   - tela com chave cadastrada: chave obrigatória e correta (mesmo no modo sem chave);
//   - tela sem chave ou inexistente: só é aceita se allowKeyless (modo LAN legado).
//
// Retorna o nome normalizado e 0, ou o status HTTP de erro (400/401/403) e a mensagem.
func (a *App) screenAuth(r *http.Request) (string, int, string) {
	name := normScreen(r.URL.Query().Get("screen"))
	if name == "" {
		return "", http.StatusBadRequest, "parâmetro screen ausente ou inválido"
	}
	key := screenKeyFrom(r)
	var hash string
	err := a.db.QueryRow(`SELECT key_hash FROM screens WHERE name=?`, name).Scan(&hash)
	if err != nil && err != sql.ErrNoRows {
		return "", http.StatusInternalServerError, "erro interno"
	}
	if hash != "" {
		if key == "" {
			return "", http.StatusUnauthorized, "tela não autorizada: chave ausente"
		}
		if subtle.ConstantTimeCompare([]byte(sha(key)), []byte(hash)) != 1 {
			return "", http.StatusForbidden, "tela não autorizada: chave inválida ou revogada"
		}
		return name, 0, ""
	}
	if a.allowKeyless {
		if err == sql.ErrNoRows && !a.autoRegister {
			return "", http.StatusForbidden, "tela não cadastrada"
		}
		return name, 0, ""
	}
	if key == "" {
		return "", http.StatusUnauthorized, "tela não autorizada: chave ausente"
	}
	return "", http.StatusForbidden, "tela não autorizada"
}

// /tela: só entrega a página com chave válida; caso contrário, página "não autorizada".
func (a *App) handleTela(w http.ResponseWriter, r *http.Request) {
	_, code, msg := a.screenAuth(r)
	name := "tela.html"
	if code != 0 {
		name = "tela-negada.html"
		a.logScreenDenied(r, msg)
	}
	b, err := fs.ReadFile(a.static, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if code != 0 {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
	}
	w.Write(b)
}

var deniedSeen sync.Map // tela|ip -> último registro (evita encher o log a cada reconexão)

func (a *App) logScreenDenied(r *http.Request, msg string) {
	scr, ip := normScreen(r.URL.Query().Get("screen")), a.clientIP(r)
	k := scr + "|" + ip
	if t, ok := deniedSeen.Load(k); ok && time.Since(t.(time.Time)) < 5*time.Minute {
		return
	}
	deniedSeen.Store(k, time.Now())
	log.Printf("acesso de tela negado: screen=%q ip=%s (%s)", scr, ip, msg)
	audit(a.db, "tela:"+scr, "tela_negada", ip+" "+msg)
}

// wrapper para endpoints /api/screen/*
func (a *App) requireScreen(h func(w http.ResponseWriter, r *http.Request, screen string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name, code, msg := a.screenAuth(r)
		if code != 0 {
			if code != http.StatusBadRequest {
				a.logScreenDenied(r, msg)
			}
			jsonErr(w, code, msg)
			return
		}
		h(w, r, name)
	}
}

// /uploads: sessão logada OU chave de tela válida.
func (a *App) uploadsAuth(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if userFromRequest(a.db, r) == nil {
			if _, code, _ := a.screenAuth(r); code != 0 {
				if code == http.StatusBadRequest {
					code = http.StatusUnauthorized
				}
				http.Error(w, "não autorizado", code)
				return
			}
		}
		w.Header().Set("Cache-Control", "private, max-age=86400")
		h.ServeHTTP(w, r)
	})
}

// ---------- gestão de chaves (admin) ----------

func (a *App) setScreenKey(id int64) (string, error) {
	key := "tk_" + randomToken(24)
	_, err := a.db.Exec(`UPDATE screens SET key_hash=?, key_prefix=?, key_created_at=? WHERE id=?`, sha(key), key[:7], now(), id)
	return key, err
}

func (a *App) screenName(id int64) string {
	var n string
	a.db.QueryRow(`SELECT name FROM screens WHERE id=?`, id).Scan(&n)
	return n
}

func (a *App) handleScreenKey(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	name := a.screenName(id)
	if name == "" {
		jsonErr(w, http.StatusNotFound, "tela não encontrada")
		return
	}
	key, err := a.setScreenKey(id)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	a.hub.kick(name) // derruba conexões com a chave antiga
	audit(a.db, currentUser(r).Username, "tela_chave_gerada", name)
	writeJSON(w, map[string]any{"key": key, "screen": name, "note": "copie agora: a chave não será exibida novamente"})
}

func (a *App) handleScreenKeyRevoke(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	name := a.screenName(id)
	if name == "" {
		jsonErr(w, http.StatusNotFound, "tela não encontrada")
		return
	}
	a.db.Exec(`UPDATE screens SET key_hash='', key_prefix='', key_created_at=0 WHERE id=?`, id)
	a.hub.kick(name)
	audit(a.db, currentUser(r).Username, "tela_chave_revogada", name)
	msg := "Chave revogada. A TV deixa de receber avisos até receber uma nova chave."
	if a.allowKeyless {
		msg = "Chave revogada. Atenção: esta instalação também aceita TVs sem chave; para bloquear esta TV, remova a tela."
	}
	writeJSON(w, map[string]any{"ok": true, "note": msg})
}

// ---------- 2FA (TOTP) ----------

func (a *App) handleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u.TOTP {
		jsonErr(w, http.StatusConflict, "2FA já está ativo")
		return
	}
	sec := newTOTPSecret()
	a.db.Exec(`UPDATE users SET totp_pending=? WHERE id=?`, sec, u.ID)
	st, _ := getSettings(a.db)
	issuer := "Painel de Avisos"
	if c := strings.TrimSpace(st["company_name"]); c != "" && c != issuer {
		issuer += " - " + c
	}
	uri := totpURI(issuer, u.Username, sec)
	writeJSON(w, map[string]any{"secret": sec, "uri": uri, "qr": qrDataURL(uri)})
}

func (a *App) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	var in struct{ Code string }
	if !readJSON(w, r, &in) {
		return
	}
	u := currentUser(r)
	var pending string
	a.db.QueryRow(`SELECT totp_pending FROM users WHERE id=?`, u.ID).Scan(&pending)
	if pending == "" {
		jsonErr(w, 400, "inicie a configuração do 2FA primeiro")
		return
	}
	c := verifyTOTP(pending, in.Code, 0, timeNow())
	if c < 0 {
		jsonErr(w, 400, "código inválido; confira o horário do celular e tente de novo")
		return
	}
	a.db.Exec(`UPDATE users SET totp_secret=?, totp_pending='', totp_last=? WHERE id=?`, pending, c, u.ID)
	audit(a.db, u.Username, "2fa_ativado", "")
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	var in struct{ Password, Code string }
	if !readJSON(w, r, &in) {
		return
	}
	u := currentUser(r)
	var hash, sec string
	var last int64
	a.db.QueryRow(`SELECT pass_hash,totp_secret,totp_last FROM users WHERE id=?`, u.ID).Scan(&hash, &sec, &last)
	if bcryptCheck(hash, in.Password) != nil || (sec != "" && verifyTOTP(sec, in.Code, last, timeNow()) < 0) {
		jsonErr(w, 400, "senha ou código inválido")
		return
	}
	a.db.Exec(`UPDATE users SET totp_secret='', totp_pending='', totp_last=0 WHERE id=?`, u.ID)
	audit(a.db, u.Username, "2fa_desativado", "")
	writeJSON(w, map[string]any{"ok": true})
}

// ---------- informações do modo de operação (admin) ----------

func (a *App) handleInfo(w http.ResponseWriter, r *http.Request) {
	// Por padrão só a versão e a URL pública vão ao navegador. Detalhes de segurança/rede (modo, CIDRs de proxy,
	// IP visto) só com SHOW_SERVER_INFO=1 e apenas para admin; sempre ficam no log de inicialização.
	out := map[string]any{"version": version, "baseUrl": a.baseURL(r), "showServerInfo": false}
	if u := currentUser(r); a.showServerInfo && u != nil && u.Role == "admin" {
		nets := []string{}
		for _, n := range a.trusted {
			nets = append(nets, n.String())
		}
		out["showServerInfo"] = true
		out["server"] = map[string]any{
			"publicUrl": a.publicURL, "publicHttps": a.publicHTTPS,
			"allowKeyless": a.allowKeyless, "webhookAllowPrivate": a.webhookAllowPrivate,
			"trustProxy": nets, "autoRegister": a.autoRegister && a.allowKeyless,
			"https": a.isHTTPS(r), "clientIp": a.clientIP(r),
		}
	}
	writeJSON(w, out)
}
