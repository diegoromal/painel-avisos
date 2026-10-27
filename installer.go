package main

import (
	_ "embed"
	"net"
	"net/http"
	"regexp"
	"strings"
)

//go:embed deploy/windows/install-tv-kiosk.ps1
var tvInstaller []byte

var installerHost = regexp.MustCompile(`^[A-Za-z0-9.-]+(:[0-9]{1,5})?$`)
var installerIPv6 = regexp.MustCompile(`^\[([0-9A-Fa-f:]+)\](:[0-9]{1,5})?$`)

func validInstallerHost(host string) bool {
	if installerHost.MatchString(host) {
		return true
	}
	m := installerIPv6.FindStringSubmatch(host)
	return m != nil && net.ParseIP(m[1]) != nil && strings.Contains(m[1], ":")
}

func (a *App) baseURL(r *http.Request) string {
	if a.publicURL != "" {
		return strings.TrimRight(a.publicURL, "/")
	}
	scheme, host := "http", r.Host
	if r.TLS != nil {
		scheme = "https"
	}
	if a.fromTrustedProxy(r) {
		proto := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])
		if proto == "http" || proto == "https" {
			scheme = proto
		}
		forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0])
		if validInstallerHost(forwarded) {
			host = forwarded
		}
	}
	if !validInstallerHost(host) {
		return ""
	}
	return scheme + "://" + host
}

func (a *App) handleTVInstaller(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="install-tv-kiosk.ps1"`)
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(tvInstaller)
}
