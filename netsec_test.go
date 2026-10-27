package main

import (
	"encoding/base32"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIPAllowed(t *testing.T) {
	cases := []struct {
		ip           string
		allowPrivate bool
		want         bool
	}{
		{"8.8.8.8", false, true},
		{"2606:4700:4700::1111", false, true},
		{"127.0.0.1", true, false},
		{"127.10.0.1", true, false},
		{"::1", true, false},
		{"0.0.0.0", true, false},
		{"::", true, false},
		{"169.254.169.254", true, false}, // metadados de nuvem
		{"169.254.1.1", true, false},
		{"fe80::1", true, false},
		{"fd00:ec2::254", true, false}, // metadados AWS IPv6
		{"::ffff:127.0.0.1", true, false},
		{"::ffff:169.254.169.254", true, false},
		{"64:ff9b::7f00:1", true, false}, // NAT64 -> 127.0.0.1
		{"224.0.0.1", true, false},
		{"255.255.255.255", true, false},
		{"10.60.20.10", false, false},
		{"10.60.20.10", true, true},
		{"172.16.5.4", false, false},
		{"192.168.1.195", false, false},
		{"192.168.1.195", true, true},
		{"100.64.1.1", false, false}, // CGNAT
		{"100.64.1.1", true, true},
		{"fc00::1", false, false},
		{"fd12:3456::1", false, false},
		{"::ffff:10.0.0.1", false, false},
	}
	for _, c := range cases {
		if got := ipAllowed(net.ParseIP(c.ip), c.allowPrivate); got != c.want {
			t.Errorf("ipAllowed(%s, private=%v) = %v, want %v", c.ip, c.allowPrivate, got, c.want)
		}
	}
}

func TestSafeClientBlocksAtDial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer srv.Close()
	a := &App{webhookAllowPrivate: true} // mesmo liberando privadas, loopback é sempre bloqueado
	for _, u := range []string{srv.URL, strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)} {
		_, err := a.safeClient().Post(u, "application/json", strings.NewReader("{}"))
		if err == nil || !errors.Is(err, errBlockedTarget) && !strings.Contains(err.Error(), "bloqueado") {
			t.Errorf("POST %s deveria ser bloqueado no dial, err=%v", u, err)
		}
	}
	if err := a.checkWebhookTarget(strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)); err == nil {
		t.Errorf("checkWebhookTarget(localhost) deveria falhar")
	}
	for _, bad := range []string{"ftp://example.com/x", "file:///etc/passwd", "gopher://x", "http://user:pw@example.com/"} {
		if err := a.checkWebhookTarget(bad); err == nil {
			t.Errorf("URL %q deveria ser recusada", bad)
		}
	}
}

// Um servidor "permitido" (IP privado liberado no modo LAN) que redireciona para loopback
// e para o endereço de metadados: o redirecionamento não pode chegar ao destino.
func TestSafeClientRedirectToBlocked(t *testing.T) {
	ip := firstPrivateIP()
	if ip == "" {
		t.Skip("sem IP privado não-loopback na máquina")
	}
	loop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("loopback foi alcançado!") }))
	defer loop.Close()
	ln, err := net.Listen("tcp", ip+":0")
	if err != nil {
		t.Skip(err)
	}
	red := &httptest.Server{Listener: ln, Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/meta" {
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
			return
		}
		http.Redirect(w, r, loop.URL+"/segredo", http.StatusTemporaryRedirect)
	})}}
	red.Start()
	defer red.Close()
	a := &App{webhookAllowPrivate: true}
	for _, p := range []string{"/", "/meta"} {
		_, err = a.safeClient().Post(red.URL+p, "application/json", strings.NewReader("{}"))
		if err == nil || !strings.Contains(err.Error(), "bloqueado") {
			t.Errorf("redirecionamento %s deveria ser bloqueado, err=%v", p, err)
		}
	}
	// no modo público, o próprio IP privado é bloqueado
	b := &App{webhookAllowPrivate: false}
	if _, err := b.safeClient().Post(red.URL, "application/json", strings.NewReader("{}")); err == nil {
		t.Errorf("IP privado deveria ser bloqueado no modo público")
	}
}

func firstPrivateIP() string {
	addrs, _ := net.InterfaceAddrs()
	for _, ad := range addrs {
		if n, ok := ad.(*net.IPNet); ok && n.IP.To4() != nil && n.IP.IsPrivate() {
			return n.IP.String()
		}
	}
	return ""
}

func TestClientIPTrustProxy(t *testing.T) {
	nets, _ := parseCIDRs("172.16.0.0/12")
	a := &App{trusted: nets}
	mk := func(remote, xff, proto string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		if proto != "" {
			r.Header.Set("X-Forwarded-Proto", proto)
		}
		return r
	}
	// do proxy confiável: pega o IP real (mais à direita não confiável)
	if ip := a.clientIP(mk("172.18.0.2:5555", "1.1.1.1, 200.10.20.30", "https")); ip != "200.10.20.30" {
		t.Errorf("clientIP = %s", ip)
	}
	if !a.isHTTPS(mk("172.18.0.2:5555", "", "https")) {
		t.Errorf("isHTTPS deveria ser true via proxy confiável")
	}
	// cliente direto tentando forjar cabeçalhos: ignorados
	if ip := a.clientIP(mk("8.8.8.8:5555", "127.0.0.1", "https")); ip != "8.8.8.8" {
		t.Errorf("XFF forjado aceito: %s", ip)
	}
	if a.isHTTPS(mk("8.8.8.8:5555", "", "https")) {
		t.Errorf("X-Forwarded-Proto forjado aceito")
	}
}

func TestTOTPRFC6238(t *testing.T) {
	sec := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	for ts, want := range map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"} {
		got, _ := totpCode(sec, ts/30)
		if got != want {
			t.Errorf("T=%d got %s want %s", ts, got, want)
		}
	}
	now := time.Unix(1234567890, 0)
	c := verifyTOTP(sec, "005924", 0, now)
	if c < 0 {
		t.Fatal("código válido recusado")
	}
	if verifyTOTP(sec, "005924", c, now) >= 0 {
		t.Error("reuso do mesmo código deveria ser recusado")
	}
	if verifyTOTP(sec, "000000", 0, now) >= 0 {
		t.Error("código errado aceito")
	}
}
