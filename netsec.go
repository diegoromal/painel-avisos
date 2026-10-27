package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ---------- proxy reverso confiável ----------

func parseCIDRs(s string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if !strings.Contains(p, "/") {
			if ip := net.ParseIP(p); ip != nil && ip.To4() != nil {
				p += "/32"
			} else {
				p += "/128"
			}
		}
		_, n, err := net.ParseCIDR(p)
		if err != nil {
			return nil, fmt.Errorf("TRUST_PROXY inválido %q: %w", p, err)
		}
		out = append(out, n)
	}
	return out, nil
}

func inNets(ip net.IP, nets []*net.IPNet) bool {
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func remoteIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}

func (a *App) fromTrustedProxy(r *http.Request) bool {
	return len(a.trusted) > 0 && inNets(remoteIP(r), a.trusted)
}

// clientIP devolve o IP real do cliente. X-Forwarded-For só é considerado quando a
// conexão vem de um proxy confiável (TRUST_PROXY); a lista é lida da direita para a
// esquerda, pulando proxies confiáveis, para não aceitar valores forjados pelo cliente.
func (a *App) clientIP(r *http.Request) string {
	rip := remoteIP(r)
	if rip == nil {
		return r.RemoteAddr
	}
	if !a.fromTrustedProxy(r) {
		return rip.String()
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				hops = append(hops, p)
			}
		}
	}
	for i := len(hops) - 1; i >= 0; i-- {
		ip := net.ParseIP(hops[i])
		if ip == nil {
			break
		}
		if !inNets(ip, a.trusted) || i == 0 {
			return ip.String()
		}
	}
	return rip.String()
}

// isHTTPS: TLS direto ou X-Forwarded-Proto=https vindo de proxy confiável.
func (a *App) isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return a.fromTrustedProxy(r) && strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https")
}

func (a *App) secureCookie(r *http.Request) bool { return a.publicHTTPS || a.isHTTPS(r) }

// ---------- cabeçalhos de segurança ----------

const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; connect-src 'self'; " +
	"font-src 'self'; media-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; " +
	"worker-src 'self'; manifest-src 'self'"

func (a *App) securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", csp)
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=(), serial=(), bluetooth=(), interest-cohort=(), autoplay=(self), fullscreen=(self)")
		hd.Set("Cross-Origin-Opener-Policy", "same-origin")
		hd.Set("Cross-Origin-Resource-Policy", "same-origin")
		if a.isHTTPS(r) {
			hd.Set("Strict-Transport-Security", "max-age=31536000")
		}
		h.ServeHTTP(w, r)
	})
}

// ---------- webhook com proteção contra SSRF ----------

var errBadWebhookURL = errors.New("endereço inválido")

var errBlockedTarget = errors.New("destino bloqueado (rede interna/loopback/metadados)")

var alwaysBlocked = mustNets(
	"0.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15",
	"198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "255.255.255.255/32",
	"::/128", "::1/128", "fe80::/10", "ff00::/8", "64:ff9b::/96", "64:ff9b:1::/48", "2001:db8::/32", "2002::/16", "100::/64",
	"fd00:ec2::254/128",
)

// privadas: liberáveis só no modo LAN (PAINEL_WEBHOOK_ALLOW_PRIVATE)
var privateNets = mustNets("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "fc00::/7")

func mustNets(cidrs ...string) []*net.IPNet {
	var out []*net.IPNet
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(err)
		}
		out = append(out, n)
	}
	return out
}

func ipAllowed(ip net.IP, allowPrivate bool) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4 // trata ::ffff:a.b.c.d como IPv4
	}
	if inNets(ip, alwaysBlocked) || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	if inNets(ip, privateNets) || ip.IsPrivate() {
		return allowPrivate
	}
	return true
}

func validateWebhookURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, fmt.Errorf("%w: use um endereço que comece com http:// ou https://", errBadWebhookURL)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: o endereço não pode conter usuário e senha", errBadWebhookURL)
	}
	return u, nil
}

// checkWebhookTarget resolve o host agora (feedback ao salvar). A garantia real
// é feita no momento da conexão (safeClient), que derrota DNS rebinding.
func (a *App) checkWebhookTarget(raw string) error {
	u, err := validateWebhookURL(raw)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if err != nil {
		return fmt.Errorf("não foi possível resolver %s", u.Hostname())
	}
	for _, ip := range ips {
		if !ipAllowed(ip.IP, a.webhookAllowPrivate) {
			return fmt.Errorf("%w: %s", errBlockedTarget, ip.IP)
		}
	}
	return nil
}

func (a *App) safeClient() *http.Client {
	allowPrivate := a.webhookAllowPrivate
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		// Control roda depois da resolução DNS, com o IP que será de fato usado.
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if !ipAllowed(net.ParseIP(host), allowPrivate) {
				return fmt.Errorf("%w: %s", errBlockedTarget, host)
			}
			return nil
		},
	}
	tr := &http.Transport{
		Proxy:                 nil, // nunca usa proxy do ambiente (contornaria a checagem)
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
		MaxIdleConns:          2,
		IdleConnTimeout:       30 * time.Second,
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("redirecionamentos demais")
			}
			if _, err := validateWebhookURL(req.URL.String()); err != nil {
				return err
			}
			return nil // o IP do novo destino é verificado no dial
		},
	}
}
