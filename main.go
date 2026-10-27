// Painel de Avisos — quadro de comunicados para TVs na rede local.
package main

import (
	"database/sql"
	"embed"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var version = "1.3.0"

//go:embed web
var webFS embed.FS

type App struct {
	db                  *sql.DB
	dataDir             string
	hub                 *Hub
	wake                chan struct{}
	lim                 *limiter
	autoRegister        bool
	static              fs.FS
	publicURL           string
	publicHTTPS         bool
	trusted             []*net.IPNet
	allowKeyless        bool
	webhookAllowPrivate bool
	showServerInfo      bool
}

func timeNow() time.Time { return time.Now() }

// env lê PAINEL_<NOME> e, como alternativa, <NOME>.
func env(name, def string) string {
	if v := os.Getenv("PAINEL_" + name); v != "" {
		return v
	}
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// triBool: "auto" -> def; "1/true/on" -> true; "0/false/off" -> false
func triBool(v string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "on", "yes", "sim":
		return true
	case "0", "false", "off", "no", "nao", "não":
		return false
	}
	return def
}

func bcryptCheck(hash, pw string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw))
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func defaultDataDir() string {
	if p := os.Getenv("PAINEL_DATA"); p != "" {
		return p
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "data")
	}
	return "data"
}

func usage() {
	fmt.Fprintf(os.Stderr, `Painel de Avisos %s

Uso:
  painel-avisos [opções]                 inicia o servidor
  painel-avisos reset-admin [opções]     cria/redefine o usuário "admin" (senha nova, 2FA removido)
  painel-avisos backup -out ARQ [opções] cópia consistente do banco (VACUUM INTO), pode rodar com o servidor ligado
  painel-avisos version

Opções (também via variáveis de ambiente):
`, version)
	flag.PrintDefaults()
}

func main() {
	cmd := ""
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		cmd = os.Args[1]
		os.Args = append(os.Args[:1], os.Args[2:]...)
	}
	port := flag.Int("port", atoiOr(envOr("PAINEL_PORT", "8080"), 8080), "porta HTTP (PAINEL_PORT)")
	bind := flag.String("bind", envOr("PAINEL_BIND", ""), "endereço de escuta, vazio = todas as interfaces (PAINEL_BIND)")
	dataDir := flag.String("data", defaultDataDir(), "pasta de dados: banco, imagens e log (PAINEL_DATA)")
	adminPw := flag.String("admin-password", os.Getenv("PAINEL_ADMIN_PASSWORD"), "senha inicial do admin no primeiro uso (PAINEL_ADMIN_PASSWORD); se vazio, é gerada e exibida")
	autoReg := flag.Bool("auto-register", envOr("PAINEL_AUTO_REGISTER", "1") != "0", "cadastra automaticamente telas novas que se conectarem (PAINEL_AUTO_REGISTER=0 desativa)")
	webhook := flag.String("webhook", os.Getenv("PAINEL_WEBHOOK_URL"), "URL de webhook para tela offline; sobrescreve a configuração do painel (PAINEL_WEBHOOK_URL)")
	publicURL := flag.String("public-url", env("PUBLIC_URL", ""), "URL pública, ex.: https://avisos.exemplo.com (PUBLIC_URL). Se https: cookie Secure, telas exigem chave, webhook sem redes privadas")
	trustProxy := flag.String("trust-proxy", env("TRUST_PROXY", ""), "CIDRs de proxies confiáveis para X-Forwarded-For/Proto, ex.: 172.16.0.0/12 (TRUST_PROXY)")
	allowKeyless := flag.String("allow-keyless", env("ALLOW_KEYLESS", "auto"), "aceitar telas sem chave (legado LAN): auto|1|0; auto = 1 sem PUBLIC_URL https, 0 com (ALLOW_KEYLESS)")
	whPrivate := flag.String("webhook-allow-private", env("WEBHOOK_ALLOW_PRIVATE", "auto"), "webhook pode chamar IPs privados (10/8, 172.16/12, 192.168/16, 100.64/10, fc00::/7): auto|1|0; auto = 1 no modo LAN, 0 com PUBLIC_URL https (WEBHOOK_ALLOW_PRIVATE)")
	showInfo := flag.String("show-server-info", env("SHOW_SERVER_INFO", "0"), "mostra \"Detalhes técnicos\" (modo, proxies, IP visto) em Configurações, só para admin: 1|0 (SHOW_SERVER_INFO; padrão 0)")
	backupOut := flag.String("out", "", "arquivo de saída do comando backup")
	flag.Usage = usage
	flag.Parse()

	if cmd == "version" {
		fmt.Println("painel-avisos", version)
		return
	}
	if cmd != "" && cmd != "reset-admin" && cmd != "serve" && cmd != "backup" {
		usage()
		os.Exit(2)
	}

	if err := os.MkdirAll(filepath.Join(*dataDir, "uploads"), 0o755); err != nil {
		log.Fatalf("pasta de dados: %v", err)
	}
	db, err := openDB(*dataDir)
	if err != nil {
		log.Fatalf("banco: %v", err)
	}

	if cmd == "reset-admin" {
		pw := *adminPw
		if pw == "" {
			pw = randomPassword()
		}
		h, _ := hashPassword(pw)
		if _, err := db.Exec(`INSERT INTO users(username,name,pass_hash,role,created_at) VALUES('admin','Administrador',?,'admin',?)
			ON CONFLICT(username) DO UPDATE SET pass_hash=excluded.pass_hash, role='admin', active=1`, h, now()); err != nil {
			log.Fatal(err)
		}
		db.Exec(`UPDATE users SET totp_secret='', totp_pending='', totp_last=0 WHERE username='admin'`)
		db.Exec(`DELETE FROM sessions WHERE user_id=(SELECT id FROM users WHERE username='admin')`)
		fmt.Printf("\nUsuário: admin\nSenha:   %s\n2FA do admin: removido (reative em Minha conta)\n\nAnote a senha agora; ela não fica salva em texto em lugar nenhum.\n", pw)
		return
	}
	if cmd == "backup" {
		if *backupOut == "" {
			log.Fatal("informe -out /caminho/arquivo.db")
		}
		if _, err := os.Stat(*backupOut); err == nil {
			log.Fatalf("%s já existe", *backupOut)
		}
		if _, err := db.Exec(`VACUUM INTO ?`, *backupOut); err != nil {
			log.Fatalf("backup: %v", err)
		}
		var ok string
		if b, err := sql.Open("sqlite", "file:"+filepath.ToSlash(*backupOut)+"?mode=ro"); err == nil {
			b.QueryRow(`PRAGMA integrity_check`).Scan(&ok)
			b.Close()
		}
		fmt.Printf("backup gravado em %s (integridade: %s)\n", *backupOut, ok)
		return
	}

	// log em arquivo + console (útil quando roda como tarefa agendada do SYSTEM)
	if lf, err := os.OpenFile(filepath.Join(*dataDir, "painel-avisos.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		log.SetOutput(io.MultiWriter(os.Stdout, lf))
	}

	var users int
	db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users)
	if users == 0 {
		pw := *adminPw
		generated := pw == ""
		if generated {
			pw = randomPassword()
		}
		h, _ := hashPassword(pw)
		db.Exec(`INSERT INTO users(username,name,pass_hash,role,created_at) VALUES('admin','Administrador',?,'admin',?)`, h, now())
		if generated {
			// impresso só no console, nunca no arquivo de log
			fmt.Printf("\n==============================================\n PRIMEIRO USO — usuário: admin  senha: %s\n Anote agora. Para redefinir: painel-avisos reset-admin\n==============================================\n\n", pw)
			log.Printf("usuário admin criado com senha gerada (exibida apenas no console)")
		} else {
			log.Printf("usuário admin criado com a senha informada em PAINEL_ADMIN_PASSWORD/-admin-password")
		}
	}
	if *webhook != "" {
		db.Exec(`UPDATE settings SET value=? WHERE key='webhook_url'`, *webhook)
	}

	trusted, err := parseCIDRs(*trustProxy)
	if err != nil {
		log.Fatal(err)
	}
	pub := strings.TrimRight(strings.TrimSpace(*publicURL), "/")
	pubHTTPS := strings.HasPrefix(strings.ToLower(pub), "https://")
	static, _ := fs.Sub(webFS, "web")
	a := &App{db: db, dataDir: *dataDir, hub: newHub(), wake: make(chan struct{}, 1), lim: newLimiter(), autoRegister: *autoReg,
		static: static, publicURL: pub, publicHTTPS: pubHTTPS, trusted: trusted,
		allowKeyless: triBool(*allowKeyless, !pubHTTPS), webhookAllowPrivate: triBool(*whPrivate, !pubHTTPS), showServerInfo: triBool(*showInfo, false)}
	log.Printf("modo: public_url=%q telas_sem_chave=%v webhook_redes_privadas=%v proxies_confiaveis=%v detalhes_tecnicos_na_ui=%v versao=%s", pub, a.allowKeyless, a.webhookAllowPrivate, *trustProxy, a.showServerInfo, version)
	if pubHTTPS && a.allowKeyless {
		log.Printf("AVISO: PUBLIC_URL é https mas ALLOW_KEYLESS está ligado: telas sem chave ficam acessíveis pela internet")
	}
	go a.scheduler()
	go a.monitor()
	go func() {
		for range time.Tick(time.Hour) {
			db.Exec(`DELETE FROM sessions WHERE expires_at<?`, now())
		}
	}()

	addr := net.JoinHostPort(*bind, strconv.Itoa(*port))
	srv := &http.Server{Addr: addr, Handler: a.securityHeaders(a.routes()), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	log.Printf("Painel de Avisos %s ouvindo em %s (dados: %s)", version, addr, *dataDir)
	if pub != "" {
		log.Printf("  líder:  %s/admin", pub)
		log.Printf("  TV:     %s/tela?screen=<nome>&key=<chave da tela>", pub)
	} else {
		for _, ip := range localIPs() {
			log.Printf("  líder:  http://%s:%d/admin", ip, *port)
			log.Printf("  TV:     http://%s:%d/tela?screen=<nome>&key=<chave da tela>", ip, *port)
		}
	}
	log.Fatal(srv.ListenAndServe())
}

func atoiOr(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func localIPs() []string {
	var out []string
	addrs, _ := net.InterfaceAddrs()
	for _, ad := range addrs {
		if ipn, ok := ad.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() {
			out = append(out, ipn.IP.String())
		}
	}
	if len(out) == 0 {
		out = []string{"localhost"}
	}
	return out
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	static := a.static
	page := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			b, err := fs.ReadFile(static, name)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			w.Write(b)
		}
	}
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/admin", http.StatusFound) })
	mux.HandleFunc("GET /install/tv-kiosk.ps1", a.handleTVInstaller)
	mux.HandleFunc("GET /admin", page("admin.html"))
	mux.HandleFunc("GET /tela", a.handleTela)
	mux.HandleFunc("GET /tela-sw.js", func(w http.ResponseWriter, r *http.Request) {
		b, _ := fs.ReadFile(static, "tela-sw.js")
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Service-Worker-Allowed", "/")
		w.Write(b)
	})
	mux.Handle("GET /static/", http.StripPrefix("/static/", noCache(http.FileServerFS(static))))
	mux.Handle("GET /uploads/", http.StripPrefix("/uploads/", a.uploadsAuth(noDirListing(http.FileServer(http.Dir(filepath.Join(a.dataDir, "uploads")))))))
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "version": version})
	})

	// sessão
	mux.HandleFunc("POST /api/login", a.handleLogin)
	mux.HandleFunc("POST /api/logout", a.handleLogout)
	mux.HandleFunc("GET /api/me", a.handleMe)

	// telas (públicas na LAN, somente leitura + heartbeat)
	mux.HandleFunc("GET /api/screen/state", a.requireScreen(a.handleScreenState))
	mux.HandleFunc("GET /api/screen/events", a.requireScreen(a.handleEvents))
	mux.HandleFunc("POST /api/screen/heartbeat", a.requireScreen(a.handleHeartbeat))

	// painel do líder
	L, A := func(h http.HandlerFunc) http.HandlerFunc { return a.requireUser("leader", h) }, func(h http.HandlerFunc) http.HandlerFunc { return a.requireUser("admin", h) }
	mux.HandleFunc("GET /api/admin/announcements", L(a.handleListAnn))
	mux.HandleFunc("POST /api/admin/announcements", L(a.handleCreateAnn))
	mux.HandleFunc("PUT /api/admin/announcements/{id}", L(a.handleUpdateAnn))
	mux.HandleFunc("POST /api/admin/announcements/{id}/end", L(a.handleEndAnn))
	mux.HandleFunc("POST /api/admin/uploads", L(a.handleUpload))
	mux.HandleFunc("GET /api/admin/screens", L(a.handleListScreens))
	mux.HandleFunc("POST /api/admin/screens", A(a.handleSaveScreen))
	mux.HandleFunc("PUT /api/admin/screens/{id}", A(a.handleSaveScreen))
	mux.HandleFunc("DELETE /api/admin/screens/{id}", A(a.handleDeleteScreen))
	mux.HandleFunc("POST /api/admin/screens/{id}/key", A(a.handleScreenKey))
	mux.HandleFunc("DELETE /api/admin/screens/{id}/key", A(a.handleScreenKeyRevoke))
	mux.HandleFunc("POST /api/admin/me/totp/setup", L(a.handleTOTPSetup))
	mux.HandleFunc("POST /api/admin/me/totp/enable", L(a.handleTOTPEnable))
	mux.HandleFunc("POST /api/admin/me/totp/disable", L(a.handleTOTPDisable))
	mux.HandleFunc("GET /api/admin/info", L(a.handleInfo))
	mux.HandleFunc("GET /api/admin/users", A(a.handleListUsers))
	mux.HandleFunc("POST /api/admin/users", A(a.handleSaveUser))
	mux.HandleFunc("PUT /api/admin/users/{id}", A(a.handleSaveUser))
	mux.HandleFunc("POST /api/admin/me/password", L(a.handleMyPassword))
	mux.HandleFunc("GET /api/admin/settings", L(a.handleGetSettings))
	mux.HandleFunc("PUT /api/admin/settings", A(a.handleSaveSettings))
	mux.HandleFunc("POST /api/admin/webhook-test", A(a.handleWebhookTest))
	mux.HandleFunc("GET /api/admin/tokens", A(a.handleListTokens))
	mux.HandleFunc("POST /api/admin/tokens", A(a.handleCreateToken))
	mux.HandleFunc("DELETE /api/admin/tokens/{id}", A(a.handleDeleteToken))
	mux.HandleFunc("GET /api/admin/audit", A(a.handleAudit))

	// API programática (Bearer token)
	T := a.requireToken
	mux.HandleFunc("GET /api/v1/announcements", T(a.handleListAnn))
	mux.HandleFunc("POST /api/v1/announcements", T(a.handleCreateAnn))
	mux.HandleFunc("POST /api/v1/announcements/{id}/end", T(a.handleEndAnn))
	mux.HandleFunc("GET /api/v1/screens", T(a.handleListScreens))
	return mux
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

func noDirListing(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "" || strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		h.ServeHTTP(w, r)
	})
}
