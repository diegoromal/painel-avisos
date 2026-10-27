package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ---------- state sent to screens ----------

type ScreenAnnouncement struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	HTML      string `json:"html"`
	Priority  string `json:"priority"`
	StartsAt  int64  `json:"startsAt"`
	EndsAt    *int64 `json:"endsAt"`
	Image     string `json:"image"`
	UpdatedAt int64  `json:"updatedAt"`
	Author    string `json:"author"`
}

type ScreenState struct {
	ServerTime    int64  `json:"serverTime"`
	Screen        string `json:"screen"`
	ScreenLabel   string `json:"screenLabel"`
	CompanyName   string `json:"companyName"`
	Logo          string `json:"logo"`
	RotateSeconds int    `json:"rotateSeconds"`
	Sound         bool   `json:"sound"`
	// v1.2: "interleave" (urgente intercalado com os normais) ou "fixed" (urgente fixo, comportamento antigo)
	UrgentMode       string               `json:"urgentMode"`
	UrgentSeconds    int                  `json:"urgentSeconds"`
	UrgentBeepRepeat bool                 `json:"urgentBeepRepeat"`
	Version          string               `json:"version"` // a TV recarrega sozinha quando o servidor é atualizado
	Announcements    []ScreenAnnouncement `json:"announcements"`
}

// stateFor returns announcements that are active now or start within the next
// 24h (the screen filters by time itself, so scheduled ones appear on time even
// if the server becomes unreachable).
func (a *App) stateFor(screen string) (*ScreenState, error) {
	st, err := getSettings(a.db)
	if err != nil {
		return nil, err
	}
	n := now()
	s := &ScreenState{ServerTime: time.Now().UnixMilli(), Screen: screen, CompanyName: st["company_name"], Logo: st["logo"],
		RotateSeconds: settingInt(st, "rotate_seconds", 15), Sound: st["sound_enabled"] == "1", Announcements: []ScreenAnnouncement{},
		UrgentMode: urgentMode(st["urgent_mode"]), UrgentSeconds: clampInt(settingInt(st, "urgent_seconds", 60), 10, 600, 60),
		UrgentBeepRepeat: st["urgent_beep_repeat"] != "0", Version: version}
	a.db.QueryRow(`SELECT label FROM screens WHERE name=?`, screen).Scan(&s.ScreenLabel)
	rows, err := a.db.Query(`SELECT a.id,a.title,a.body,a.priority,a.starts_at,a.ends_at,a.image,a.updated_at,a.author_name
		FROM announcements a
		WHERE a.ended_at IS NULL AND (a.ends_at IS NULL OR a.ends_at>?) AND a.starts_at<=?
		  AND (a.all_screens=1 OR a.id IN (SELECT x.announcement_id FROM announcement_screens x JOIN screens sc ON sc.id=x.screen_id WHERE sc.name=?))
		ORDER BY CASE a.priority WHEN 'urgent' THEN 0 ELSE 1 END, a.starts_at, a.id`, n, n+86400, screen)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var x ScreenAnnouncement
		var body string
		var ends sql.NullInt64
		if err := rows.Scan(&x.ID, &x.Title, &body, &x.Priority, &x.StartsAt, &ends, &x.Image, &x.UpdatedAt, &x.Author); err != nil {
			return nil, err
		}
		if ends.Valid {
			v := ends.Int64
			x.EndsAt = &v
		}
		x.HTML = renderText(body)
		s.Announcements = append(s.Announcements, x)
	}
	return s, rows.Err()
}

// ---------- SSE hub ----------

type sseClient struct {
	screen string
	notify chan struct{}
	kicked chan struct{}
}

type Hub struct {
	mu      sync.Mutex
	clients map[*sseClient]struct{}
}

func newHub() *Hub { return &Hub{clients: map[*sseClient]struct{}{}} }

func (h *Hub) add(c *sseClient)    { h.mu.Lock(); h.clients[c] = struct{}{}; h.mu.Unlock() }
func (h *Hub) remove(c *sseClient) { h.mu.Lock(); delete(h.clients, c); h.mu.Unlock() }

func (h *Hub) broadcast() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c.notify <- struct{}{}:
		default: // already pending
		}
	}
}

// kick encerra as conexões SSE de uma tela (ex.: chave regenerada/revogada).
func (h *Hub) kick(screen string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if strings.EqualFold(c.screen, screen) {
			select {
			case <-c.kicked:
			default:
				close(c.kicked)
			}
		}
	}
}

func (h *Hub) connected() map[string]int {
	h.mu.Lock()
	defer h.mu.Unlock()
	m := map[string]int{}
	for c := range h.clients {
		m[strings.ToLower(c.screen)]++
	}
	return m
}

// changed is called after every modification: pushes to screens and wakes the scheduler.
func (a *App) changed() {
	a.hub.broadcast()
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

func (a *App) handleEvents(w http.ResponseWriter, r *http.Request, screen string) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming não suportado", 500)
		return
	}
	a.touchScreen(screen, r)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	c := &sseClient{screen: screen, notify: make(chan struct{}, 1), kicked: make(chan struct{})}
	a.hub.add(c)
	defer a.hub.remove(c)

	send := func() bool {
		st, err := a.stateFor(screen)
		if err != nil {
			log.Printf("state: %v", err)
			return true
		}
		b, _ := json.Marshal(st)
		if _, err := w.Write([]byte("retry: 3000\nevent: state\ndata: " + string(b) + "\n\n")); err != nil {
			return false
		}
		fl.Flush()
		return true
	}
	if !send() {
		return
	}
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-c.kicked:
			return
		case <-c.notify:
			if !send() {
				return
			}
		case <-ping.C:
			if _, err := w.Write([]byte("event: ping\ndata: " + jsonInt(time.Now().UnixMilli()) + "\n\n")); err != nil {
				return
			}
			fl.Flush()
			a.touchScreen(screen, r)
		}
	}
}

func urgentMode(v string) string {
	if v == "fixed" {
		return "fixed"
	}
	return "interleave"
}

func clampInt(n, lo, hi, def int) int {
	if n < lo || n > hi {
		return def
	}
	return n
}

func jsonInt(n int64) string { b, _ := json.Marshal(n); return string(b) }

// ---------- scheduler: push when a notice starts or ends ----------

func (a *App) scheduler() {
	for {
		n := now()
		var next sql.NullInt64
		a.db.QueryRow(`SELECT MIN(t) FROM (
			SELECT starts_at AS t FROM announcements WHERE ended_at IS NULL AND starts_at>?
			UNION ALL SELECT ends_at FROM announcements WHERE ended_at IS NULL AND ends_at>?)`, n, n).Scan(&next)
		wait := 60 * time.Second
		if next.Valid {
			d := time.Until(time.Unix(next.Int64, 0)) + 200*time.Millisecond
			if d < wait {
				wait = d
			}
		}
		t := time.NewTimer(wait)
		select {
		case <-t.C:
			a.hub.broadcast()
		case <-a.wake:
			t.Stop()
		}
	}
}

// ---------- heartbeat / offline monitor / webhook ----------

func (a *App) touchScreen(name string, r *http.Request) {
	if name == "" {
		return
	}
	ua := r.UserAgent()
	if len(ua) > 200 {
		ua = ua[:200]
	}
	res, _ := a.db.Exec(`UPDATE screens SET last_seen=?, last_ip=?, user_agent=? WHERE name=?`, now(), a.clientIP(r), ua, name)
	if n, _ := res.RowsAffected(); n == 0 && a.autoRegister && a.allowKeyless {
		a.db.Exec(`INSERT OR IGNORE INTO screens(name,label,last_seen,last_ip,user_agent,created_at) VALUES(?,?,?,?,?,?)`, name, name, now(), a.clientIP(r), ua, now())
	}
}

func (a *App) monitor() {
	for {
		time.Sleep(15 * time.Second)
		a.checkOffline()
	}
}

func (a *App) checkOffline() {
	st, err := getSettings(a.db)
	if err != nil {
		return
	}
	limit := int64(settingInt(st, "offline_minutes", 2)) * 60
	hook := strings.TrimSpace(st["webhook_url"])
	type scr struct {
		id          int64
		name, label string
		seen        int64
		notified    int
	}
	rows, err := a.db.Query(`SELECT id,name,label,last_seen,offline_notified FROM screens WHERE last_seen>0`)
	if err != nil {
		return
	}
	var list []scr
	for rows.Next() {
		var s scr
		rows.Scan(&s.id, &s.name, &s.label, &s.seen, &s.notified)
		list = append(list, s)
	}
	rows.Close()
	n := now()
	for _, s := range list {
		offline := n-s.seen > limit
		if offline && s.notified == 0 {
			a.db.Exec(`UPDATE screens SET offline_notified=1 WHERE id=?`, s.id)
			log.Printf("tela %q OFFLINE (último sinal há %ds)", s.name, n-s.seen)
			audit(a.db, "sistema", "tela_offline", s.name)
			if hook != "" {
				go a.postWebhook(hook, map[string]any{"event": "screen_offline", "screen": s.name, "label": s.label, "lastSeen": time.Unix(s.seen, 0).Format(time.RFC3339), "offlineSeconds": n - s.seen, "source": "painel-avisos"})
			}
		} else if !offline && s.notified == 1 {
			a.db.Exec(`UPDATE screens SET offline_notified=0 WHERE id=?`, s.id)
			log.Printf("tela %q voltou ONLINE", s.name)
			audit(a.db, "sistema", "tela_online", s.name)
			if hook != "" {
				go a.postWebhook(hook, map[string]any{"event": "screen_online", "screen": s.name, "label": s.label, "lastSeen": time.Unix(s.seen, 0).Format(time.RFC3339), "source": "painel-avisos"})
			}
		}
	}
}

func (a *App) postWebhook(url string, payload map[string]any) {
	b, _ := json.Marshal(payload)
	cl := a.safeClient()
	for attempt := 1; attempt <= 3; attempt++ {
		resp, err := cl.Post(url, "application/json", bytes.NewReader(b))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 300 {
				log.Printf("webhook %s -> %d", payload["event"], resp.StatusCode)
				return
			}
			err = &httpStatusErr{resp.StatusCode}
		}
		log.Printf("webhook falhou (tentativa %d): %v", attempt, err)
		time.Sleep(time.Duration(attempt*5) * time.Second)
	}
}

type httpStatusErr struct{ code int }

func (e *httpStatusErr) Error() string { return "HTTP " + http.StatusText(e.code) }
