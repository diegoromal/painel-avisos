#!/usr/bin/env python3
"""Teste ponta a ponta através do Traefik (HTTPS). Uso:
   BASE=https://avisos.exemplo.com ADMIN_PW=... [INSECURE=1] python3 traefik_test.py
INSECURE=1 aceita certificado autoassinado (teste local). Cria uma tela de teste 'tv-teste-traefik' e a remove no fim."""
import json, os, ssl, threading, time, urllib.error, urllib.request

BASE = os.environ["BASE"].rstrip("/"); PW = os.environ["ADMIN_PW"]
ctx = ssl.create_default_context()
if os.environ.get("INSECURE") == "1": ctx.check_hostname = False; ctx.verify_mode = ssl.CERT_NONE
op = urllib.request.build_opener(urllib.request.HTTPSHandler(context=ctx))
results = []; cookie = {"v": None}
def check(n, ok, d=""): results.append(ok); print(("PASS " if ok else "FAIL ") + n + (f" — {d}" if d else ""), flush=True)
def req(m, p, body=None, headers=None, auth=True):
    h = {"X-Requested-With": "painel"}; h.update(headers or {})
    if auth and cookie["v"]: h["Cookie"] = "painel_sess=" + cookie["v"]
    data = json.dumps(body).encode() if body is not None else None
    if data: h["Content-Type"] = "application/json"
    try: r = op.open(urllib.request.Request(BASE + p, data=data, method=m, headers=h), timeout=15); c, hd, t = r.status, r.headers, r.read()
    except urllib.error.HTTPError as e: c, hd, t = e.code, e.headers, e.read()
    for sc in hd.get_all("Set-Cookie") or []:
        if sc.startswith("painel_sess="): cookie["v"] = sc.split(";")[0].split("=", 1)[1]; cookie["raw"] = sc
    try: j = json.loads(t)
    except Exception: j = t.decode(errors="replace")
    return c, j, hd

s, j, h = req("GET", "/healthz", auth=False)
check("GET /healthz via Traefik (HTTPS)", s == 200 and j.get("ok") is True, json.dumps(j))
check("HSTS presente (X-Forwarded-Proto do Traefik reconhecido)", "max-age" in (h.get("Strict-Transport-Security") or ""), h.get("Strict-Transport-Security"))
check("CSP presente", "default-src 'self'" in (h.get("Content-Security-Policy") or ""))
s, _, _ = req("POST", "/api/login", {"username": "admin", "password": PW})
check("Login via Traefik + cookie Secure", s == 200 and "Secure" in cookie.get("raw", ""), cookie.get("raw", "").split(";", 1)[-1].strip())
s, info, _ = req("GET", "/api/admin/info")
sv = info.get("server") or {}   # requer SHOW_SERVER_INFO=1 no container de teste
check("Servidor enxerga HTTPS e o IP do cliente via X-Forwarded-For do Traefik", sv.get("https") is True, f"clientIp={sv.get('clientIp')}")
s, scr, _ = req("POST", "/api/admin/screens", {"name": "tv-teste-traefik", "label": "teste traefik"}); KEY = scr["key"]
check("/tela sem chave via Traefik → 401", req("GET", "/tela?screen=tv-teste-traefik", auth=False)[0] == 401)

events = []; meta = {}
def sse(extra):
    r = op.open(urllib.request.Request(f"{BASE}/api/screen/events?screen=tv-teste-traefik&key={KEY}", headers=extra), timeout=60)
    meta["enc"] = r.headers.get("Content-Encoding"); meta["ct"] = r.headers.get("Content-Type")
    ev = None
    for line in r:
        line = line.decode().rstrip("\n")
        if line.startswith("event:"): ev = line[6:].strip()
        elif line.startswith("data:") and ev == "state": events.append((time.time(), json.loads(line[5:])))
threading.Thread(target=sse, args=({"Accept-Encoding": "gzip, br"},), daemon=True).start()
t_end = time.time() + 5
while not events and time.time() < t_end: time.sleep(0.02)
check("SSE via Traefik: estado inicial chega (sem buffering)", bool(events), f"Content-Type={meta.get('ct')} Content-Encoding={meta.get('enc')}")
check("SSE não é comprimido mesmo com Accept-Encoding: gzip", meta.get("enc") in (None, "identity"))
lat = []
for i in range(3):
    t0 = time.time(); s, a, _ = req("POST", "/api/admin/announcements", {"title": f"teste traefik {i}", "durationMinutes": 1, "allScreens": False, "screens": ["tv-teste-traefik"]})
    while time.time() < t0 + 5 and not any(t >= t0 and any(x["id"] == a["id"] for x in st["announcements"]) for t, st in events): time.sleep(0.005)
    hit = [t for t, st in events if t >= t0 and any(x["id"] == a["id"] for x in st["announcements"])]
    lat.append((hit[0] - t0) * 1000 if hit else None)
    req("POST", f"/api/admin/announcements/{a['id']}/end")
check("Aviso chega pela SSE através do Traefik em < 1 s (3 amostras)", all(l is not None and l < 1000 for l in lat), ", ".join(f"{l:.0f} ms" if l else "falhou" for l in lat))
req("DELETE", f"/api/admin/screens/{scr['id']}")
print(f"\nRESULTADO: {sum(results)}/{len(results)} verificações OK"); raise SystemExit(0 if all(results) else 1)
