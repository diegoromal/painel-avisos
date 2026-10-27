#!/usr/bin/env python3
"""Teste de fumaça do Painel de Avisos v1.3.0 (somente stdlib).
Sobe dois servidores temporários e testa API, SSE, chaves de tela, uploads, proxy reverso,
cabeçalhos de segurança, SSRF do webhook, 2FA (TOTP), modo LAN legado e download público do instalador Windows.

Uso: python3 tests/smoke_test.py [caminho/do/binario]
"""
import base64, re, hashlib, hmac, http.server, json, os, secrets, shutil, socket, sqlite3, struct, subprocess, sys, tempfile, threading, time, urllib.error, urllib.request, zlib

BIN = sys.argv[1] if len(sys.argv) > 1 else os.path.join(os.path.dirname(__file__), "..", "dist", "painel-avisos-linux-amd64")
results = []

def check(name, cond, detail=""):
    results.append((name, bool(cond), detail))
    print(("PASS " if cond else "FAIL ") + name + (f" — {detail}" if detail else ""), flush=True)

def section(t): print(f"\n## {t}", flush=True)

def free_port():
    s = socket.socket(); s.bind(("127.0.0.1", 0)); p = s.getsockname()[1]; s.close(); return p

def private_ip():
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    try:
        s.connect(("10.255.255.255", 1)); return s.getsockname()[0]
    except Exception: return None
    finally: s.close()

class Server:
    def __init__(self, env):
        self.dir = tempfile.mkdtemp(prefix="painel-test-")
        self.port = free_port(); self.base = f"http://127.0.0.1:{self.port}"
        self.pw = secrets.token_urlsafe(12)
        e = dict(os.environ, PAINEL_ADMIN_PASSWORD=self.pw, **env)
        self.p = subprocess.Popen([BIN, "-port", str(self.port), "-bind", "127.0.0.1", "-data", self.dir], env=e, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for _ in range(50):
            try: urllib.request.urlopen(self.base + "/healthz", timeout=1); break
            except Exception: time.sleep(0.1)
        self.db = os.path.join(self.dir, "painel.db")
    def stop(self):
        self.p.terminate(); self.p.wait(5); shutil.rmtree(self.dir, ignore_errors=True)

def check_installer(srv):
    with urllib.request.urlopen(srv.base + "/install/tv-kiosk.ps1", timeout=10) as r:
        data = r.read()
        with open(os.path.join(os.path.dirname(__file__), "..", "deploy", "windows", "install-tv-kiosk.ps1"), "rb") as f:
            source = f.read()
        check("Instalador público sem sessão: conteúdo idêntico e attachment", r.status == 200 and data == source and r.headers.get("Content-Disposition") == 'attachment; filename="install-tv-kiosk.ps1"')
        check("Instalador identifica monitores e não contém chave", b"-ListMonitors" in data and re.search(rb"tk_[A-Za-z0-9_-]{20,}", data) is None)

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k): return None

class Client:
    """Cliente HTTP com cookie manual (o CookieJar do Python não envia cookie Secure em http)."""
    def __init__(self, srv, headers=None):
        self.srv, self.cookie, self.h = srv, None, dict(headers or {})
        self.op = urllib.request.build_opener(NoRedirect)
    def req(self, method, path, body=None, headers=None, raw=None, ctype=None, csrf=True):
        h = dict(self.h); h.update(headers or {})
        if csrf: h["X-Requested-With"] = "painel"
        if self.cookie: h["Cookie"] = "painel_sess=" + self.cookie
        data = None
        if raw is not None: data = raw; h["Content-Type"] = ctype
        elif body is not None: data = json.dumps(body).encode(); h["Content-Type"] = "application/json"
        r = urllib.request.Request(self.srv.base + path, data=data, method=method, headers=h)
        try:
            resp = self.op.open(r, timeout=10); code, hdrs, txt = resp.status, resp.headers, resp.read()
        except urllib.error.HTTPError as e:
            code, hdrs, txt = e.code, e.headers, e.read()
        for sc in hdrs.get_all("Set-Cookie") or []:
            if sc.startswith("painel_sess="):
                v = sc.split(";")[0].split("=", 1)[1]; self.cookie = v or None; self.last_set_cookie = sc
        try: j = json.loads(txt)
        except Exception: j = txt.decode(errors="replace")
        return code, j, hdrs
    def login(self, user, pw, code=None, headers=None):
        b = {"username": user, "password": pw}
        if code: b["code"] = code
        return self.req("POST", "/api/login", b, headers=headers)

def totp(secret, t=None, step=0):
    key = base64.b32decode(secret + "=" * (-len(secret) % 8))
    c = int((t or time.time()) // 30) + step
    d = hmac.new(key, struct.pack(">Q", c), hashlib.sha1).digest(); o = d[-1] & 15
    return "%06d" % ((struct.unpack(">I", d[o:o + 4])[0] & 0x7fffffff) % 1000000)

class SSE(threading.Thread):
    def __init__(self, srv, screen, key=None):
        super().__init__(daemon=True); self.events = []; self.lock = threading.Lock(); self.status = None; self.closed_at = None
        q = f"screen={screen}" + (f"&key={key}" if key else "")
        self.url = srv.base + "/api/screen/events?" + q
    def run(self):
        try:
            r = urllib.request.urlopen(self.url, timeout=60); self.status = r.status
        except urllib.error.HTTPError as e:
            self.status = e.code; self.closed_at = time.time(); return
        ev, data = None, []
        try:
            for line in r:
                line = line.decode().rstrip("\n")
                if line.startswith("event:"): ev = line[6:].strip()
                elif line.startswith("data:"): data.append(line[5:].strip())
                elif line == "":
                    if ev == "state":
                        with self.lock: self.events.append((time.time(), json.loads("".join(data))))
                    ev, data = None, []
        except Exception: pass
        self.closed_at = time.time()
    def wait_for(self, pred, since, timeout=15):
        end = time.time() + timeout
        while time.time() < end:
            with self.lock:
                for t, st in self.events:
                    if t >= since and pred(st): return t, st
            time.sleep(0.02)
        return None, None
    def wait_status(self, timeout=5):
        end = time.time() + timeout
        while self.status is None and time.time() < end: time.sleep(0.02)
        return self.status

ids = lambda st: {a["id"] for a in st["announcements"]}

def png(w, h, rgb):
    raw = b"".join(b"\x00" + bytes(rgb) * w for _ in range(h))
    def chunk(t, d): return struct.pack(">I", len(d)) + t + d + struct.pack(">I", zlib.crc32(t + d) & 0xffffffff)
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0)) + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b"")

def multipart(content):
    bnd = "----painel" + secrets.token_hex(4)
    body = (f"--{bnd}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.png\"\r\nContent-Type: image/png\r\n\r\n").encode() + content + f"\r\n--{bnd}--\r\n".encode()
    return body, "multipart/form-data; boundary=" + bnd

class Hook:
    def __init__(self, host):
        self.hits = []; outer = self
        class H(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                outer.hits.append(json.loads(self.rfile.read(int(self.headers["Content-Length"])))); self.send_response(204); self.end_headers()
            def log_message(self, *a): pass
        self.srv = http.server.HTTPServer((host, 0), H); self.url = f"http://{host}:{self.srv.server_port}/hook"
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()

PROXY = {"X-Forwarded-For": "203.0.113.50", "X-Forwarded-Proto": "https"}   # simula o Traefik
loop_hook = Hook("127.0.0.1")

# =====================================================================================
# Servidor PÚBLICO: PUBLIC_URL https + TRUST_PROXY (127.0.0.1 faz o papel do Traefik)
# =====================================================================================
P = Server({"PAINEL_PUBLIC_URL": "https://avisos.teste.local", "PAINEL_TRUST_PROXY": "127.0.0.1/32", "PAINEL_WEBHOOK_URL": loop_hook.url, "PAINEL_SHOW_SERVER_INFO": "1"})
try:
    section("Autenticação, cookie Secure, HSTS e proxy reverso")
    adm = Client(P, PROXY); anon = Client(P)
    s, _, _ = anon.req("GET", "/api/admin/announcements"); check("API admin sem login → 401", s == 401, f"HTTP {s}")
    s, _, _ = adm.login("admin", "errada"); check("Senha errada → 401", s == 401)
    s, _, h = adm.login("admin", P.pw); check("Login correto atrás do proxy", s == 200)
    sc = adm.last_set_cookie
    check("Cookie de sessão com Secure + HttpOnly + SameSite=Strict", "Secure" in sc and "HttpOnly" in sc and "SameSite=Strict" in sc, sc.split(";", 1)[1].strip())
    check("HSTS enviado quando X-Forwarded-Proto=https vem do proxy confiável", "max-age" in (h.get("Strict-Transport-Security") or ""), h.get("Strict-Transport-Security"))
    _, _, h = anon.req("GET", "/healthz"); check("Sem X-Forwarded-Proto: sem HSTS", h.get("Strict-Transport-Security") is None)
    check_installer(P)
    s, public_info, _ = adm.req("GET", "/api/admin/info")
    check("URL base pública no info", s == 200 and public_info.get("baseUrl") == "https://avisos.teste.local")
    s, info, _ = adm.req("GET", "/api/admin/info"); sv = info.get("server") or {}
    check("SHOW_SERVER_INFO=1: admin recebe detalhes técnicos", info.get("showServerInfo") is True and info.get("version") and "trustProxy" in sv, f"versão={info.get('version')}")
    check("IP real do cliente lido do X-Forwarded-For (proxy confiável)", sv.get("clientIp") == "203.0.113.50" and sv.get("https") is True, f"clientIp={sv.get('clientIp')} https={sv.get('https')}")
    check("Modo público: telas sem chave bloqueadas e webhook sem redes privadas", sv["allowKeyless"] is False and sv["webhookAllowPrivate"] is False)
    s, aud, _ = adm.req("GET", "/api/admin/audit")
    check("Auditoria registra o IP real do login", any(e["action"] == "login" and e["detail"] == "203.0.113.50" for e in aud))
    r = urllib.request.Request(P.base + "/api/admin/announcements", data=b'{"title":"x"}', method="POST", headers={"Content-Type": "application/json", "Cookie": "painel_sess=" + adm.cookie})
    try: urllib.request.urlopen(r); code = 200
    except urllib.error.HTTPError as e: code = e.code
    check("POST sem cabeçalho anti-CSRF → 403", code == 403)

    section("Cabeçalhos de segurança")
    for path in ["/admin", "/tela?screen=x&key=y", "/healthz", "/static/admin.js"]:
        _, _, h = anon.req("GET", path, csrf=False)
        csp = h.get("Content-Security-Policy") or ""
        ok = "script-src 'self'" in csp and "'unsafe-inline'" not in csp and "frame-ancestors 'none'" in csp and "object-src 'none'" in csp
        ok = ok and h.get("X-Frame-Options") == "DENY" and h.get("X-Content-Type-Options") == "nosniff" and h.get("Referrer-Policy") == "no-referrer" and "camera=()" in (h.get("Permissions-Policy") or "")
        check(f"CSP estrita + XFO DENY + nosniff + Referrer/Permissions-Policy em {path}", ok)

    section("Chaves por tela")
    s, scr, _ = adm.req("POST", "/api/admin/screens", {"name": "tv-principal", "label": "TV principal"})
    K1 = scr.get("key", ""); check("Criar tela devolve chave uma única vez", s == 200 and K1.startswith("tk_") and len(K1) > 30, K1[:7] + "…")
    s, scr2, _ = adm.req("POST", "/api/admin/screens", {"name": "tv-recepcao", "label": "Recepção"}); K2 = scr2["key"]
    c = sqlite3.connect(P.db); row = c.execute("SELECT key_hash FROM screens WHERE name='tv-principal'").fetchone(); c.close()
    check("Banco guarda só o hash SHA-256 da chave", row[0] == hashlib.sha256(K1.encode()).hexdigest() and K1 not in row[0])
    lst = {x["name"]: x for x in adm.req("GET", "/api/admin/screens")[1]}
    check("Listagem não expõe a chave (só prefixo)", "key" not in lst["tv-principal"] and lst["tv-principal"]["keyPrefix"] == K1[:7])
    for name, q, want in [("sem chave", "screen=tv-principal", 401), ("chave errada", "screen=tv-principal&key=tk_errada", 403), ("chave de outra tela", f"screen=tv-principal&key={K2}", 403)]:
        s1 = anon.req("GET", "/api/screen/state?" + q, csrf=False)[0]
        s2 = anon.req("POST", "/api/screen/heartbeat?" + q, csrf=False)[0]
        st = SSE(P, "x"); st.url = P.base + "/api/screen/events?" + q; st.start()
        s3 = st.wait_status()
        s4, body, _ = anon.req("GET", "/tela?" + q, csrf=False)
        check(f"{name}: state/heartbeat/events/tela → {want}", s1 == s2 == s3 == s4 == want and "Tela não autorizada" in body, f"{s1}/{s2}/{s3}/{s4}")
    s, body, _ = anon.req("GET", f"/tela?screen=tv-principal&key={K1}", csrf=False)
    check("/tela com chave válida → 200 (página da TV)", s == 200 and 'id="notice"' in body)
    s, _, _ = anon.req("POST", "/api/screen/heartbeat?screen=tv-principal", headers={"X-Screen-Key": K1}, csrf=False)
    check("Heartbeat aceita a chave no cabeçalho X-Screen-Key", s == 200)
    s = anon.req("GET", "/api/screen/state?screen=tv-intrusa", csrf=False)[0]
    c = sqlite3.connect(P.db); n = c.execute("SELECT COUNT(*) FROM screens WHERE name='tv-intrusa'").fetchone()[0]; c.close()
    check("Modo público: tela desconhecida sem chave é recusada e NÃO é cadastrada", s == 401 and n == 0, f"HTTP {s}")

    section("Tempo real (SSE) com chave")
    sse = SSE(P, "tv-principal", K1); sse.start(); sse2 = SSE(P, "tv-recepcao", K2); sse2.start()
    check("SSE com chave válida conecta", sse.wait_status() == 200 and sse.wait_for(lambda s: True, 0, 5)[0] is not None)
    t0 = time.time(); s, a1, _ = adm.req("POST", "/api/admin/announcements", {"title": "Reunião geral às 15h", "body": "Todos no **refeitório**.\n- Pauta: metas", "priority": "normal"})
    t, st = sse.wait_for(lambda st: a1["id"] in ids(st), t0)
    check("Aviso normal chega à TV em < 1 s", t and t - t0 < 1, f"{(t - t0) * 1000:.0f} ms" if t else "não chegou")
    t0 = time.time(); s, a2, _ = adm.req("POST", "/api/admin/announcements", {"title": "Evacuação — simulado", "priority": "urgent", "durationMinutes": 60})
    t, st = sse.wait_for(lambda st: a2["id"] in ids(st), t0)
    check("Aviso URGENTE chega em < 1 s e vem primeiro", t and t - t0 < 1 and st["announcements"][0]["id"] == a2["id"], f"{(t - t0) * 1000:.0f} ms" if t else "")
    t0 = time.time(); s, a3, _ = adm.req("POST", "/api/admin/announcements", {"title": "Só na recepção", "allScreens": False, "screens": ["tv-recepcao"]})
    t, _ = sse2.wait_for(lambda st: a3["id"] in ids(st), t0); time.sleep(0.4)
    with sse.lock: last = sse.events[-1][1]
    check("Aviso segmentado: chega na tela alvo e não na outra", t is not None and a3["id"] not in ids(last))
    start = int(time.time()) + 3; end = start + 3
    s, a4, _ = adm.req("POST", "/api/admin/announcements", {"title": "Agendado", "startsAt": start, "endsAt": end})
    t, _ = sse.wait_for(lambda st: True, start, 8)
    check("Agendado: servidor empurra no horário de início", t and t - start < 1.5, f"{(t - start) * 1000:.0f} ms após o início" if t else "")
    t, _ = sse.wait_for(lambda st: a4["id"] not in ids(st), end, 8)
    check("Expiração: servidor empurra estado sem o aviso", t and t - end < 1.5, f"{(t - end) * 1000:.0f} ms após o fim" if t else "")
    t0 = time.time(); adm.req("POST", f"/api/admin/announcements/{a2['id']}/end")
    t, _ = sse.wait_for(lambda st: a2["id"] not in ids(st), t0)
    check("Encerrar remove o aviso da TV em < 1 s", t and t - t0 < 1)

    section("Uploads protegidos")
    body, ct = multipart(png(8, 8, (0, 120, 200)))
    s, up, _ = adm.req("POST", "/api/admin/uploads", raw=body, ctype=ct); path = up["path"]
    check("Upload de imagem (sessão)", s == 200 and path.startswith("/uploads/"))
    tests = [("anônimo", anon, path, 401), ("sessão logada", adm, path, 200), ("chave de tela válida", anon, f"{path}?screen=tv-principal&key={K1}", 200),
             ("chave inválida", anon, f"{path}?screen=tv-principal&key=tk_x", 403), ("screen sem chave", anon, f"{path}?screen=tv-principal", 401)]
    for name, cl, p, want in tests:
        s = cl.req("GET", p, csrf=False)[0]; check(f"GET /uploads ({name}) → {want}", s == want, f"HTTP {s}")
    body2, ct2 = multipart(b"<?php echo 1; ?>")
    check("Upload de não-imagem recusado", adm.req("POST", "/api/admin/uploads", raw=body2, ctype=ct2)[0] == 400)

    section("Regenerar e revogar chave")
    t0 = time.time(); s, nk, _ = adm.req("POST", f"/api/admin/screens/{scr['id']}/key"); K1b = nk["key"]
    end_ = time.time() + 3
    while sse.closed_at is None and time.time() < end_: time.sleep(0.02)
    check("Nova chave derruba na hora a conexão SSE com a chave antiga", sse.closed_at is not None and sse.closed_at - t0 < 1, f"{(sse.closed_at - t0) * 1000:.0f} ms" if sse.closed_at else "continua conectada")
    check("Chave antiga → 403 / nova → 200", anon.req("GET", f"/api/screen/state?screen=tv-principal&key={K1}", csrf=False)[0] == 403 and anon.req("GET", f"/api/screen/state?screen=tv-principal&key={K1b}", csrf=False)[0] == 200)
    check("Chave antiga também perde acesso às imagens", anon.req("GET", f"{path}?screen=tv-principal&key={K1}", csrf=False)[0] == 403)
    sse3 = SSE(P, "tv-principal", K1b); sse3.start(); sse3.wait_status()
    t0 = time.time(); s, rv, _ = adm.req("DELETE", f"/api/admin/screens/{scr['id']}/key")
    end_ = time.time() + 3
    while sse3.closed_at is None and time.time() < end_: time.sleep(0.02)
    s1 = anon.req("GET", f"/api/screen/state?screen=tv-principal&key={K1b}", csrf=False)[0]
    s2 = anon.req("GET", "/api/screen/state?screen=tv-principal", csrf=False)[0]
    check("Chave revogada: conexão derrubada e tela rejeitada com/sem chave", sse3.closed_at is not None and s1 in (401, 403) and s2 == 401, f"com chave={s1} sem chave={s2}")

    section("Webhook: proteção contra SSRF (modo público)")
    blocked = ["http://127.0.0.1:9999/x", "http://localhost:9999/x", "http://10.0.0.10/x", "http://172.17.0.1/x", "http://192.168.0.50/x",
               "http://169.254.169.254/latest/meta-data/", "http://100.64.0.1/x", "http://0.0.0.0/x", "http://[::1]:9999/x", "http://[fd00:ec2::254]/x",
               "http://[::ffff:127.0.0.1]/x", "http://[fe80::1]/x", "ftp://example.com/x", "file:///etc/passwd", "http://user:pw@example.com/x"]
    bad = [u for u in blocked if adm.req("PUT", "/api/admin/settings", {"webhook_url": u})[0] != 400]
    check(f"{len(blocked)} destinos internos/metadados/esquemas inválidos recusados ao salvar", not bad, ", ".join(bad) or "todos 400")
    n0 = len(loop_hook.hits)
    s, r, _ = adm.req("POST", "/api/admin/webhook-test")   # URL loopback injetada por env (pula a validação de salvar)
    check("Checagem no momento da conexão: webhook já gravado apontando p/ loopback é bloqueado e nada é entregue", s == 502 and "bloqueado" in r.get("error", "") and len(loop_hook.hits) == n0, r.get("error", ""))

    section("2FA (TOTP)")
    adm.req("POST", "/api/admin/users", {"username": "lider1", "name": "Líder 1", "password": "senhaLider123", "role": "leader"})
    ld = Client(P, PROXY); ld.login("lider1", "senhaLider123")
    s, li, _ = ld.req("GET", "/api/admin/info")
    check("SHOW_SERVER_INFO=1: líder (não admin) NÃO recebe detalhes técnicos", s == 200 and "server" not in li and li.get("showServerInfo") is False, json.dumps(li))
    s, setup, _ = ld.req("POST", "/api/admin/me/totp/setup")
    check("Setup 2FA devolve segredo, URI otpauth e QR (PNG)", s == 200 and setup["uri"].startswith("otpauth://totp/") and setup["qr"].startswith("data:image/png;base64,"))
    check("Código errado não ativa o 2FA", ld.req("POST", "/api/admin/me/totp/enable", {"code": "000000"})[0] == 400)
    s, _, _ = ld.req("POST", "/api/admin/me/totp/enable", {"code": totp(setup["secret"])}); check("Código correto ativa o 2FA", s == 200)
    time.sleep(0.2)
    c2 = Client(P, PROXY); s, j, _ = c2.login("lider1", "senhaLider123")
    check("Login só com senha → 401 pedindo código", s == 401 and j.get("totpRequired") is True and c2.cookie is None)
    s, j, _ = c2.login("lider1", "senhaLider123", "123456" if totp(setup["secret"]) != "123456" else "654321"); check("Código errado → 401", s == 401)
    s, j, _ = c2.login("lider1", "senhaLider123", "000000"); 
    # espera virar a janela para não colidir com o código usado na ativação
    time.sleep(30 - time.time() % 30 + 0.5)
    code = totp(setup["secret"])
    s, j, _ = c2.login("lider1", "senhaLider123", code); check("Senha + código correto → 200", s == 200 and c2.cookie)
    s, j, _ = Client(P, PROXY).login("lider1", "senhaLider123", code); check("Mesmo código reutilizado → recusado (anti-replay)", s == 401)
    s = adm.req("PUT", f"/api/admin/users/{[u for u in adm.req('GET', '/api/admin/users')[1] if u['username'] == 'lider1'][0]['id']}", {"resetTotp": True})[0]
    s2, _, _ = Client(P, PROXY).login("lider1", "senhaLider123")
    check("Admin reseta o 2FA (recuperação) → login volta a ser só com senha", s == 200 and s2 == 200)
    s, _, _ = c2.req("GET", "/api/admin/users"); check("Líder não acessa gestão de usuários (403)", s == 403)

    section("Rate limit usa o IP real (X-Forwarded-For)")
    atk = Client(P, {"X-Forwarded-For": "198.51.100.7"})
    for _ in range(8): atk.login("admin", "chute")
    s1 = atk.login("admin", P.pw)[0]; s2 = Client(P, {"X-Forwarded-For": "198.51.100.8"}).login("admin", P.pw)[0]
    check("IP atacante bloqueado (429) sem bloquear outro IP atrás do mesmo proxy (200)", s1 == 429 and s2 == 200, f"{s1}/{s2}")

    section("API com token")
    s, tk, _ = adm.req("POST", "/api/admin/tokens", {"name": "zabbix"})
    s, a5, _ = anon.req("POST", "/api/v1/announcements", {"title": "Via API", "durationMinutes": 5}, headers={"Authorization": "Bearer " + tk["token"]}, csrf=False)
    check("POST /api/v1/announcements com Bearer → 201", s == 201)
    check("Sem token → 401", anon.req("POST", "/api/v1/announcements", {"title": "x"}, csrf=False)[0] == 401)
    c = sqlite3.connect(P.db); bad = c.execute("SELECT COUNT(*) FROM users WHERE pass_hash NOT LIKE '$2a$%'").fetchone()[0]
    leaks = c.execute("SELECT COUNT(*) FROM api_tokens WHERE token_hash=?", (tk["token"],)).fetchone()[0]; c.close()
    check("Senhas em bcrypt e tokens só como hash", bad == 0 and leaks == 0)
finally:
    P.stop()

# =====================================================================================
# Servidor LAN (padrão Windows): sem PUBLIC_URL, sem proxy confiável
# =====================================================================================
L = Server({})
try:
    section("Modo LAN (compatibilidade)")
    adm = Client(L); adm.login("admin", L.pw)
    s, info, _ = adm.req("GET", "/api/admin/info")
    check_installer(L)
    check("Padrão (SHOW_SERVER_INFO=0): /api/admin/info expõe versão e URL base — sem modo, proxies ou IP", s == 200 and set(info) == {"version", "baseUrl", "showServerInfo"} and info["showServerInfo"] is False, json.dumps(info))
    leg = SSE(L, "tv-legado"); leg.start()
    check("Tela legada SEM chave conecta e é cadastrada automaticamente", leg.wait_status() == 200 and any(x["name"] == "tv-legado" for x in adm.req("GET", "/api/admin/screens")[1]))
    s, scr, _ = adm.req("POST", "/api/admin/screens", {"name": "tv-nova"})
    check("Tela COM chave exige a chave mesmo no modo LAN", Client(L).req("GET", "/api/screen/state?screen=tv-nova", csrf=False)[0] == 401
          and Client(L).req("GET", f"/api/screen/state?screen=tv-nova&key={scr['key']}", csrf=False)[0] == 200)
    sp = Client(L, {"X-Forwarded-For": "1.2.3.4", "X-Forwarded-Proto": "https"})
    s, _, h = sp.login("admin", L.pw); s, aud, _ = sp.req("GET", "/api/admin/audit")
    lip = next((e["detail"] for e in aud if e["action"] == "login"), None)
    check("Sem TRUST_PROXY: X-Forwarded-For/Proto forjados são ignorados (IP=127.0.0.1, sem HSTS, cookie sem Secure)",
          lip == "127.0.0.1" and h.get("Strict-Transport-Security") is None and "Secure" not in sp.last_set_cookie, f"IP auditado={lip}")
    pip = private_ip()
    if pip:
        hook = Hook(pip)
        s, _, _ = adm.req("PUT", "/api/admin/settings", {"webhook_url": hook.url, "offline_minutes": "1"})
        s2, r, _ = adm.req("POST", "/api/admin/webhook-test")
        check(f"Modo LAN: webhook para IP privado ({pip}) permitido e entregue", s == 200 and s2 == 200 and hook.hits and hook.hits[-1]["event"] == "test")
        check("Modo LAN: loopback continua bloqueado", adm.req("PUT", "/api/admin/settings", {"webhook_url": "http://127.0.0.1:1/x"})[0] == 400)
        c = sqlite3.connect(L.db, timeout=10); c.execute("UPDATE screens SET last_seen=?, offline_notified=0 WHERE name='tv-nova'", (int(time.time()) - 200,)); c.commit(); c.close()
        end = time.time() + 25
        while time.time() < end and not any(h.get("event") == "screen_offline" for h in hook.hits): time.sleep(0.3)
        off = [h for h in hook.hits if h.get("event") == "screen_offline"]
        check("Webhook screen_offline disparado para tela sem sinal", off and off[0]["screen"] == "tv-nova", json.dumps(off[0], ensure_ascii=False) if off else "")
    else:
        check("IP privado disponível para teste do webhook LAN", False)
finally:
    L.stop()

ok = sum(1 for r in results if r[1])
print(f"\nRESULTADO: {ok}/{len(results)} verificações OK")
raise SystemExit(0 if ok == len(results) else 1)
