package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestBaseURL(t *testing.T) {
	trusted, err := parseCIDRs("192.0.2.0/24")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, public, remote, host, proto, forwarded, want string
		tls                                                bool
	}{
		{name: "PUBLIC_URL", public: "https://avisos.exemplo/", host: "interno", want: "https://avisos.exemplo"},
		{name: "proxy confiável", remote: "192.0.2.1:10", host: "interno", proto: "https", forwarded: "avisos.exemplo:443, outro", want: "https://avisos.exemplo:443"},
		{name: "proxy não confiável", remote: "198.51.100.1:10", host: "interno:8080", proto: "https", forwarded: "forjado", want: "http://interno:8080"},
		{name: "TLS", host: "avisos.exemplo", tls: true, want: "https://avisos.exemplo"},
		{name: "LAN", host: "192.168.1.1:8080", want: "http://192.168.1.1:8080"},
		{name: "IPv6", host: "[::1]:8080", want: "http://[::1]:8080"},
		{name: "IPv6 encaminhado", remote: "192.0.2.1:10", host: "interno", proto: "https", forwarded: "[2001:db8::1]", want: "https://[2001:db8::1]"},
		{name: "headers inválidos", remote: "192.0.2.1:10", host: "interno", proto: "javascript", forwarded: "host;comando", want: "http://interno"},
		{name: "espaço", host: "host comando"}, {name: "aspas simples", host: "host'"}, {name: "ponto e vírgula", host: "host;"}, {name: "aspas duplas", host: `host"`},
		{name: "IPv6 inválido", host: "[::::]"}, {name: "porta longa", host: "host:123456"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &App{publicURL: c.public, trusted: trusted}
			r := httptest.NewRequest("GET", "http://interno/", nil)
			r.Host = c.host
			r.RemoteAddr = c.remote
			r.Header.Set("X-Forwarded-Proto", c.proto)
			r.Header.Set("X-Forwarded-Host", c.forwarded)
			if c.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if got := a.baseURL(r); got != c.want {
				t.Fatalf("baseURL = %q; esperado %q", got, c.want)
			}
		})
	}
}

func TestPublicTVInstaller(t *testing.T) {
	source, err := os.ReadFile("deploy/windows/install-tv-kiosk.ps1")
	if err != nil {
		t.Fatal(err)
	}
	a := testApp(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/install/tv-kiosk.ps1", nil)
	a.securityHeaders(a.routes()).ServeHTTP(w, r)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), source) {
		t.Fatal("download deve funcionar sem sessão e preservar todos os bytes")
	}
	for k, v := range map[string]string{"Content-Type": "text/plain; charset=utf-8", "Content-Disposition": `attachment; filename="install-tv-kiosk.ps1"`, "Cache-Control": "no-cache"} {
		if w.Header().Get(k) != v {
			t.Errorf("%s = %q", k, w.Header().Get(k))
		}
	}
	if w.Header().Get("Content-Security-Policy") == "" {
		t.Error("faltam cabeçalhos de segurança")
	}
	if regexp.MustCompile(`tk_[A-Za-z0-9_-]{20,}`).Match(source) || regexp.MustCompile(`(?i)\bpassword\s*=\s*["'][^"']+`).Match(source) {
		t.Error("instalador contém segredo")
	}
	if !strings.Contains(string(source), "-ListMonitors") {
		t.Error("falta identificação dos monitores")
	}
}

// A URL pública é útil também para líderes; detalhes de rede continuam privados.
func TestInfoBaseURLForLoggedUsers(t *testing.T) {
	for _, role := range []string{"admin", "leader"} {
		t.Run(role, func(t *testing.T) {
			a := testApp(t)
			a.publicURL = "https://avisos.exemplo"
			a.showServerInfo = true
			r := httptest.NewRequest("GET", "/api/admin/info", nil)
			r = r.WithContext(context.WithValue(r.Context(), userKey, &User{Role: role}))
			w := httptest.NewRecorder()
			a.handleInfo(w, r)
			var info map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
				t.Fatal(err)
			}
			if info["baseUrl"] != a.publicURL {
				t.Fatalf("URL base ausente: %v", info)
			}
			if role == "leader" && info["server"] != nil {
				t.Fatal("detalhes técnicos expostos ao líder")
			}
		})
	}
}
