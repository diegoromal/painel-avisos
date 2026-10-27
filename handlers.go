package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		jsonErr(w, http.StatusBadRequest, "JSON inválido: "+err.Error())
		return false
	}
	return true
}

var reScreen = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

func normScreen(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if !reScreen.MatchString(s) {
		return ""
	}
	return s
}

func pathID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}

var reUpload = regexp.MustCompile(`^/uploads/[A-Za-z0-9_-]+\.(png|jpg|gif|webp)$`)

// ---------- announcements ----------

type AnnInput struct {
	Title           string   `json:"title"`
	Body            string   `json:"body"`
	Priority        string   `json:"priority"`
	StartsAt        int64    `json:"startsAt"` // unix seconds; 0 = agora
	EndsAt          *int64   `json:"endsAt"`   // unix seconds; null = sem fim
	DurationMinutes int      `json:"durationMinutes"`
	AllScreens      *bool    `json:"allScreens"`
	Screens         []string `json:"screens"`
	Image           string   `json:"image"`
}

type AnnOut struct {
	ID         int64    `json:"id"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	HTML       string   `json:"html"`
	Priority   string   `json:"priority"`
	StartsAt   int64    `json:"startsAt"`
	EndsAt     *int64   `json:"endsAt"`
	EndedAt    *int64   `json:"endedAt"`
	EndedBy    string   `json:"endedBy"`
	AllScreens bool     `json:"allScreens"`
	Screens    []string `json:"screens"`
	Image      string   `json:"image"`
	Author     string   `json:"author"`
	CreatedAt  int64    `json:"createdAt"`
	UpdatedAt  int64    `json:"updatedAt"`
	UpdatedBy  string   `json:"updatedBy"`
	Status     string   `json:"status"` // ativo | agendado | encerrado | expirado
}

func (a *App) validateAnn(in *AnnInput) (screenIDs []int64, err error) {
	in.Title = strings.TrimSpace(in.Title)
	in.Body = strings.TrimSpace(in.Body)
	if in.Title == "" {
		return nil, errors.New("título é obrigatório")
	}
	if len([]rune(in.Title)) > 120 {
		return nil, errors.New("título muito longo (máx. 120 caracteres)")
	}
	if len([]rune(in.Body)) > 3000 {
		return nil, errors.New("texto muito longo (máx. 3000 caracteres)")
	}
	if in.Priority == "" {
		in.Priority = "normal"
	}
	if in.Priority != "normal" && in.Priority != "urgent" {
		return nil, errors.New("prioridade deve ser normal ou urgent")
	}
	if in.StartsAt <= 0 {
		in.StartsAt = now()
	}
	if in.EndsAt == nil && in.DurationMinutes > 0 {
		e := in.StartsAt + int64(in.DurationMinutes)*60
		in.EndsAt = &e
	}
	if in.EndsAt != nil && *in.EndsAt <= in.StartsAt {
		return nil, errors.New("o fim deve ser depois do início")
	}
	if in.Image != "" && !reUpload.MatchString(in.Image) {
		return nil, errors.New("imagem inválida")
	}
	if in.AllScreens == nil {
		t := len(in.Screens) == 0
		in.AllScreens = &t
	}
	if !*in.AllScreens {
		if len(in.Screens) == 0 {
			return nil, errors.New("selecione ao menos uma tela ou marque 'todas as telas'")
		}
		for _, s := range in.Screens {
			var id int64
			if err := a.db.QueryRow(`SELECT id FROM screens WHERE name=?`, normScreen(s)).Scan(&id); err != nil {
				return nil, fmt.Errorf("tela desconhecida: %s", s)
			}
			screenIDs = append(screenIDs, id)
		}
	}
	return screenIDs, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (a *App) createAnn(in AnnInput, u *User) (int64, error) {
	ids, err := a.validateAnn(&in)
	if err != nil {
		return 0, err
	}
	tx, err := a.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var authorID any
	if u.ID > 0 {
		authorID = u.ID
	}
	n := now()
	res, err := tx.Exec(`INSERT INTO announcements(title,body,priority,starts_at,ends_at,all_screens,image,author_id,author_name,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`, in.Title, in.Body, in.Priority, in.StartsAt, in.EndsAt, b2i(*in.AllScreens), in.Image, authorID, u.display(), n, n)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	for _, sid := range ids {
		if _, err := tx.Exec(`INSERT INTO announcement_screens VALUES(?,?)`, id, sid); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	audit(a.db, u.Username, "aviso_criado", fmt.Sprintf("#%d %s [%s]", id, in.Title, in.Priority))
	a.changed()
	return id, nil
}

func (a *App) handleCreateAnn(w http.ResponseWriter, r *http.Request) {
	var in AnnInput
	if !readJSON(w, r, &in) {
		return
	}
	id, err := a.createAnn(in, currentUser(r))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
	out, _ := a.getAnn(id)
	writeJSON(w, out)
}

func (a *App) handleUpdateAnn(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	cur, err := a.getAnn(id)
	if err != nil {
		jsonErr(w, http.StatusNotFound, "aviso não encontrado")
		return
	}
	if cur.EndedAt != nil {
		jsonErr(w, http.StatusConflict, "aviso já encerrado; use 'Duplicar' para publicar de novo")
		return
	}
	var in AnnInput
	if !readJSON(w, r, &in) {
		return
	}
	if in.StartsAt <= 0 {
		in.StartsAt = cur.StartsAt
	}
	ids, err := a.validateAnn(&in)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u := currentUser(r)
	tx, err := a.db.Begin()
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	defer tx.Rollback()
	tx.Exec(`UPDATE announcements SET title=?,body=?,priority=?,starts_at=?,ends_at=?,all_screens=?,image=?,updated_at=?,updated_by_name=? WHERE id=?`,
		in.Title, in.Body, in.Priority, in.StartsAt, in.EndsAt, b2i(*in.AllScreens), in.Image, now(), u.display(), id)
	tx.Exec(`DELETE FROM announcement_screens WHERE announcement_id=?`, id)
	for _, sid := range ids {
		tx.Exec(`INSERT INTO announcement_screens VALUES(?,?)`, id, sid)
	}
	if err := tx.Commit(); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	audit(a.db, u.Username, "aviso_editado", fmt.Sprintf("#%d %s", id, in.Title))
	a.changed()
	out, _ := a.getAnn(id)
	writeJSON(w, out)
}

func (a *App) handleEndAnn(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	u := currentUser(r)
	res, err := a.db.Exec(`UPDATE announcements SET ended_at=?, ended_by_name=? WHERE id=? AND ended_at IS NULL`, now(), u.display(), id)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		jsonErr(w, http.StatusNotFound, "aviso não encontrado ou já encerrado")
		return
	}
	audit(a.db, u.Username, "aviso_encerrado", fmt.Sprintf("#%d", id))
	a.changed()
	out, _ := a.getAnn(id)
	writeJSON(w, out)
}

const annCols = `id,title,body,priority,starts_at,ends_at,ended_at,ended_by_name,all_screens,image,author_name,created_at,updated_at,updated_by_name`

func (a *App) scanAnn(sc interface{ Scan(...any) error }) (*AnnOut, error) {
	var o AnnOut
	var ends, ended sql.NullInt64
	var all int
	if err := sc.Scan(&o.ID, &o.Title, &o.Body, &o.Priority, &o.StartsAt, &ends, &ended, &o.EndedBy, &all, &o.Image, &o.Author, &o.CreatedAt, &o.UpdatedAt, &o.UpdatedBy); err != nil {
		return nil, err
	}
	if ends.Valid {
		v := ends.Int64
		o.EndsAt = &v
	}
	if ended.Valid {
		v := ended.Int64
		o.EndedAt = &v
	}
	o.AllScreens = all == 1
	o.HTML = renderText(o.Body)
	n := now()
	switch {
	case o.EndedAt != nil:
		o.Status = "encerrado"
	case o.EndsAt != nil && *o.EndsAt <= n:
		o.Status = "expirado"
	case o.StartsAt > n:
		o.Status = "agendado"
	default:
		o.Status = "ativo"
	}
	o.Screens = []string{}
	return &o, nil
}

func (a *App) fillScreens(list []*AnnOut) {
	for _, o := range list {
		if o.AllScreens {
			continue
		}
		rows, err := a.db.Query(`SELECT s.name FROM announcement_screens x JOIN screens s ON s.id=x.screen_id WHERE x.announcement_id=? ORDER BY s.name`, o.ID)
		if err != nil {
			continue
		}
		for rows.Next() {
			var n string
			rows.Scan(&n)
			o.Screens = append(o.Screens, n)
		}
		rows.Close()
	}
}

func (a *App) getAnn(id int64) (*AnnOut, error) {
	o, err := a.scanAnn(a.db.QueryRow(`SELECT `+annCols+` FROM announcements WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	a.fillScreens([]*AnnOut{o})
	return o, nil
}

func (a *App) listAnn(view string) ([]*AnnOut, error) {
	n := now()
	var q string
	switch view {
	case "scheduled":
		q = `WHERE ended_at IS NULL AND starts_at>? ORDER BY starts_at`
	case "history":
		q = `WHERE ended_at IS NOT NULL OR (ends_at IS NOT NULL AND ends_at<=?) ORDER BY COALESCE(ended_at, ends_at) DESC LIMIT 300`
	default:
		q = `WHERE ended_at IS NULL AND starts_at<=? AND (ends_at IS NULL OR ends_at>?1) ORDER BY CASE priority WHEN 'urgent' THEN 0 ELSE 1 END, starts_at DESC`
	}
	rows, err := a.db.Query(`SELECT `+annCols+` FROM announcements `+q, n)
	if err != nil {
		return nil, err
	}
	list := []*AnnOut{}
	for rows.Next() {
		o, err := a.scanAnn(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, o)
	}
	rows.Close()
	a.fillScreens(list)
	return list, nil
}

func (a *App) handleListAnn(w http.ResponseWriter, r *http.Request) {
	list, err := a.listAnn(r.URL.Query().Get("view"))
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	writeJSON(w, list)
}

// ---------- uploads ----------

func (a *App) handleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20+4096)
	f, _, err := r.FormFile("file")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "arquivo ausente ou maior que 8 MB")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "arquivo maior que 8 MB")
		return
	}
	ext := map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/gif": "gif", "image/webp": "webp"}[http.DetectContentType(data)]
	if ext == "" {
		jsonErr(w, http.StatusBadRequest, "formato não suportado (use PNG, JPG, GIF ou WEBP)")
		return
	}
	name := randomToken(12) + "." + ext
	if err := os.WriteFile(filepath.Join(a.dataDir, "uploads", name), data, 0o644); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]string{"path": "/uploads/" + name})
}

// ---------- screens ----------

type ScreenOut struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Label     string `json:"label"`
	LastSeen  int64  `json:"lastSeen"`
	LastIP    string `json:"lastIp"`
	UserAgent string `json:"userAgent"`
	Online    bool   `json:"online"`
	Conns     int    `json:"connections"`
	CreatedAt int64  `json:"createdAt"`
	HasKey    bool   `json:"hasKey"`
	KeyPrefix string `json:"keyPrefix"`
	KeyAt     int64  `json:"keyCreatedAt"`
}

func (a *App) handleListScreens(w http.ResponseWriter, r *http.Request) {
	st, _ := getSettings(a.db)
	limit := int64(settingInt(st, "offline_minutes", 2)) * 60
	conns := a.hub.connected()
	rows, err := a.db.Query(`SELECT id,name,label,last_seen,last_ip,user_agent,created_at,key_prefix,key_created_at FROM screens ORDER BY name`)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	list := []ScreenOut{}
	n := now()
	for rows.Next() {
		var s ScreenOut
		rows.Scan(&s.ID, &s.Name, &s.Label, &s.LastSeen, &s.LastIP, &s.UserAgent, &s.CreatedAt, &s.KeyPrefix, &s.KeyAt)
		s.HasKey = s.KeyPrefix != ""
		s.Conns = conns[s.Name]
		s.Online = s.LastSeen > 0 && n-s.LastSeen <= limit
		list = append(list, s)
	}
	writeJSON(w, list)
}

func (a *App) handleSaveScreen(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name, Label string }
	if !readJSON(w, r, &in) {
		return
	}
	in.Label = strings.TrimSpace(in.Label)
	id := pathID(r)
	u := currentUser(r)
	if id == 0 {
		name := normScreen(in.Name)
		if name == "" {
			jsonErr(w, 400, "nome inválido: use letras minúsculas, números, - ou _ (ex.: tv-producao)")
			return
		}
		if in.Label == "" {
			in.Label = name
		}
		res, err := a.db.Exec(`INSERT INTO screens(name,label,created_at) VALUES(?,?,?)`, name, in.Label, now())
		if err != nil {
			jsonErr(w, 409, "já existe uma tela com esse nome")
			return
		}
		sid, _ := res.LastInsertId()
		key, err := a.setScreenKey(sid)
		if err != nil {
			jsonErr(w, 500, err.Error())
			return
		}
		audit(a.db, u.Username, "tela_criada", name)
		a.changed()
		writeJSON(w, map[string]any{"ok": true, "id": sid, "screen": name, "key": key, "note": "copie agora: a chave não será exibida novamente"})
		return
	} else {
		if _, err := a.db.Exec(`UPDATE screens SET label=? WHERE id=?`, in.Label, id); err != nil {
			jsonErr(w, 500, err.Error())
			return
		}
	}
	a.changed()
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) handleDeleteScreen(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	var name string
	a.db.QueryRow(`SELECT name FROM screens WHERE id=?`, id).Scan(&name)
	a.db.Exec(`DELETE FROM screens WHERE id=?`, id)
	a.hub.kick(name)
	audit(a.db, currentUser(r).Username, "tela_removida", name)
	a.changed()
	writeJSON(w, map[string]any{"ok": true})
}

// ---------- public screen endpoints ----------

func (a *App) handleScreenState(w http.ResponseWriter, r *http.Request, screen string) {
	a.touchScreen(screen, r)
	st, err := a.stateFor(screen)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	writeJSON(w, st)
}

func (a *App) handleHeartbeat(w http.ResponseWriter, r *http.Request, screen string) {
	a.touchScreen(screen, r)
	writeJSON(w, map[string]any{"ok": true, "serverTime": time.Now().UnixMilli()})
}

// ---------- users ----------

func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	u := userFromRequest(a.db, r)
	if u == nil {
		jsonErr(w, http.StatusUnauthorized, "não autenticado")
		return
	}
	writeJSON(w, u)
}

func (a *App) handleListUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(`SELECT id,username,name,role,active,created_at,totp_secret<>'' FROM users ORDER BY username`)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	list := []User{}
	for rows.Next() {
		var u User
		var act int
		rows.Scan(&u.ID, &u.Username, &u.Name, &u.Role, &act, &u.Created, &u.TOTP)
		u.Active = act == 1
		list = append(list, u)
	}
	writeJSON(w, list)
}

var reUser = regexp.MustCompile(`^[A-Za-z0-9._-]{2,40}$`)

func (a *App) handleSaveUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username, Name, Role, Password string
		Active                         *bool
		ResetTOTP                      bool `json:"resetTotp"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	me := currentUser(r)
	id := pathID(r)
	if in.Role != "" && in.Role != "admin" && in.Role != "leader" {
		jsonErr(w, 400, "perfil deve ser admin ou leader")
		return
	}
	if in.Password != "" && len(in.Password) < 8 {
		jsonErr(w, 400, "a senha precisa ter ao menos 8 caracteres")
		return
	}
	if id == 0 {
		if !reUser.MatchString(in.Username) {
			jsonErr(w, 400, "usuário inválido (2-40 caracteres: letras, números, . _ -)")
			return
		}
		if in.Password == "" {
			jsonErr(w, 400, "defina uma senha")
			return
		}
		if in.Role == "" {
			in.Role = "leader"
		}
		h, _ := hashPassword(in.Password)
		if _, err := a.db.Exec(`INSERT INTO users(username,name,pass_hash,role,created_at) VALUES(?,?,?,?,?)`, in.Username, strings.TrimSpace(in.Name), h, in.Role, now()); err != nil {
			jsonErr(w, 409, "usuário já existe")
			return
		}
		audit(a.db, me.Username, "usuario_criado", in.Username)
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	if id == me.ID && ((in.Active != nil && !*in.Active) || (in.Role != "" && in.Role != "admin")) {
		jsonErr(w, 400, "você não pode desativar ou rebaixar a própria conta")
		return
	}
	if in.Name != "" {
		a.db.Exec(`UPDATE users SET name=? WHERE id=?`, strings.TrimSpace(in.Name), id)
	}
	if in.Role != "" {
		a.db.Exec(`UPDATE users SET role=? WHERE id=?`, in.Role, id)
	}
	if in.Active != nil {
		a.db.Exec(`UPDATE users SET active=? WHERE id=?`, b2i(*in.Active), id)
		if !*in.Active {
			a.db.Exec(`DELETE FROM sessions WHERE user_id=?`, id)
		}
	}
	if in.Password != "" {
		h, _ := hashPassword(in.Password)
		a.db.Exec(`UPDATE users SET pass_hash=? WHERE id=?`, h, id)
		a.db.Exec(`DELETE FROM sessions WHERE user_id=?`, id)
	}
	if in.ResetTOTP {
		a.db.Exec(`UPDATE users SET totp_secret='', totp_pending='', totp_last=0 WHERE id=?`, id)
		audit(a.db, me.Username, "2fa_resetado", strconv.FormatInt(id, 10))
	}
	audit(a.db, me.Username, "usuario_editado", strconv.FormatInt(id, 10))
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) handleMyPassword(w http.ResponseWriter, r *http.Request) {
	var in struct{ Current, New string }
	if !readJSON(w, r, &in) {
		return
	}
	u := currentUser(r)
	var h string
	a.db.QueryRow(`SELECT pass_hash FROM users WHERE id=?`, u.ID).Scan(&h)
	if bcryptCheck(h, in.Current) != nil {
		jsonErr(w, 400, "senha atual incorreta")
		return
	}
	if len(in.New) < 8 {
		jsonErr(w, 400, "a nova senha precisa ter ao menos 8 caracteres")
		return
	}
	nh, _ := hashPassword(in.New)
	a.db.Exec(`UPDATE users SET pass_hash=? WHERE id=?`, nh, u.ID)
	audit(a.db, u.Username, "senha_alterada", "")
	writeJSON(w, map[string]any{"ok": true})
}

// ---------- settings ----------

func (a *App) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	st, err := getSettings(a.db)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	writeJSON(w, st)
}

func (a *App) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	var in map[string]string
	if !readJSON(w, r, &in) {
		return
	}
	for k, v := range in {
		if _, ok := defaultSettings[k]; !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "rotate_seconds":
			n, err := strconv.Atoi(v)
			if err != nil || n < 5 || n > 600 {
				jsonErr(w, 400, "tempo de rodízio deve ser entre 5 e 600 segundos")
				return
			}
		case "urgent_seconds":
			n, err := strconv.Atoi(v)
			if err != nil || n < 10 || n > 600 {
				jsonErr(w, 400, "o tempo do aviso urgente na tela deve ser entre 10 e 600 segundos")
				return
			}
		case "urgent_mode":
			if v != "interleave" && v != "fixed" {
				jsonErr(w, 400, "opção inválida para os avisos urgentes")
				return
			}
		case "urgent_beep_repeat", "sound_enabled":
			if v != "0" && v != "1" {
				jsonErr(w, 400, "opção inválida para o alerta sonoro")
				return
			}
		case "offline_minutes":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 1440 {
				jsonErr(w, 400, "o tempo para considerar a TV fora do ar deve ser entre 1 e 1440 minutos")
				return
			}
		case "webhook_url":
			if v != "" {
				if err := a.checkWebhookTarget(v); err != nil {
					log.Printf("webhook recusado ao salvar: %v", err)
					jsonErr(w, 400, webhookFriendlyErr(err))
					return
				}
			}
		case "logo":
			if v != "" && !reUpload.MatchString(v) {
				jsonErr(w, 400, "logo inválido")
				return
			}
		case "company_name":
			if len([]rune(v)) > 80 {
				jsonErr(w, 400, "nome muito longo")
				return
			}
		}
		a.db.Exec(`UPDATE settings SET value=? WHERE key=?`, v, k)
	}
	audit(a.db, currentUser(r).Username, "config_alterada", "")
	a.changed()
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) handleWebhookTest(w http.ResponseWriter, r *http.Request) {
	st, _ := getSettings(a.db)
	url := strings.TrimSpace(st["webhook_url"])
	if url == "" {
		jsonErr(w, 400, "nenhum endereço de aviso automático configurado; preencha e salve primeiro")
		return
	}
	if _, err := validateWebhookURL(url); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	b, _ := json.Marshal(map[string]any{"event": "test", "source": "painel-avisos", "at": time.Now().Format(time.RFC3339)})
	resp, err := a.safeClient().Post(url, "application/json", strings.NewReader(string(b)))
	if err != nil {
		log.Printf("teste de webhook falhou: %v", err)
		jsonErr(w, 502, webhookFriendlyErr(err))
		return
	}
	resp.Body.Close()
	writeJSON(w, map[string]any{"ok": resp.StatusCode < 300, "status": resp.StatusCode})
}

// ---------- API tokens ----------

func (a *App) handleListTokens(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(`SELECT id,name,prefix,created_by,created_at,last_used FROM api_tokens ORDER BY id`)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	type T struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		Prefix    string `json:"prefix"`
		CreatedBy string `json:"createdBy"`
		CreatedAt int64  `json:"createdAt"`
		LastUsed  int64  `json:"lastUsed"`
	}
	list := []T{}
	for rows.Next() {
		var t T
		rows.Scan(&t.ID, &t.Name, &t.Prefix, &t.CreatedBy, &t.CreatedAt, &t.LastUsed)
		list = append(list, t)
	}
	writeJSON(w, list)
}

func (a *App) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name string }
	if !readJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		jsonErr(w, 400, "dê um nome ao token (ex.: zabbix)")
		return
	}
	tok := "pa_" + randomToken(24)
	u := currentUser(r)
	if _, err := a.db.Exec(`INSERT INTO api_tokens(name,token_hash,prefix,created_by,created_at) VALUES(?,?,?,?,?)`, in.Name, sha(tok), tok[:7], u.Username, now()); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	audit(a.db, u.Username, "token_criado", in.Name)
	writeJSON(w, map[string]any{"token": tok, "note": "copie agora: o token não será exibido novamente"})
}

func (a *App) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	a.db.Exec(`DELETE FROM api_tokens WHERE id=?`, pathID(r))
	audit(a.db, currentUser(r).Username, "token_revogado", r.PathValue("id"))
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) handleAudit(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(`SELECT at,actor,action,detail FROM audit_log ORDER BY id DESC LIMIT 200`)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	type E struct {
		At     int64  `json:"at"`
		Actor  string `json:"actor"`
		Action string `json:"action"`
		Detail string `json:"detail"`
	}
	list := []E{}
	for rows.Next() {
		var e E
		rows.Scan(&e.At, &e.Actor, &e.Action, &e.Detail)
		list = append(list, e)
	}
	writeJSON(w, list)
}

// webhookFriendlyErr traduz erros de rede/validação em mensagens para o cliente final
// (o detalhe técnico vai só para o log do servidor).
func webhookFriendlyErr(err error) string {
	switch {
	case errors.Is(err, errBlockedTarget):
		return "endereço bloqueado por segurança: ele aponta para uma rede interna ou reservada"
	case errors.Is(err, errBadWebhookURL):
		return err.Error()
	case strings.Contains(err.Error(), "não foi possível resolver"):
		return "endereço não encontrado: confira se o domínio está correto"
	}
	return "não foi possível enviar: o endereço não respondeu ou recusou a conexão"
}
