// Painel de Avisos — tela (quiosque)
(() => {
  "use strict";
  const qs = new URLSearchParams(location.search);
  const screen = (qs.get("screen") || "tv-principal").toLowerCase();
  const skey = qs.get("key") || "";
  const KEY = "painel:state:" + screen;
  const ROT = "painel:rot:" + screen; // posição do rodízio: sobrevive a recarga/reabertura da página
  const auth = "screen=" + encodeURIComponent(screen) + (skey ? "&key=" + encodeURIComponent(skey) : "");
  // imagens protegidas: /uploads exige a chave da tela
  const withKey = (p) => p ? p + (p.includes("?") ? "&" : "?") + auth : p;
  let denied = false;
  const $ = (id) => document.getElementById(id);

  let state = null;          // último estado recebido do servidor
  let offset = 0;            // relógio do servidor - relógio local (ms)
  let shownKey = "";         // slide exibido (nº do slide:updatedAt:tamanho do grupo)
  // rodízio medido com relógio monotônico: não trava se o relógio do Windows for ajustado
  const mono = () => performance.now();
  // Sequência: fase "u" (urgentes) ou "n" (normais); idx/curId = aviso atual dentro do grupo da fase
  let phase = "", curId = null, idx = 0, slideStart = mono(), slideNo = 0;
  let alertStart = null, beeps = 0; // período de alerta sonoro do urgente (relógio monotônico) e bipes já tocados
  let seenU = null;                 // urgentes já conhecidos (id:updatedAt): um novo ou editado entra na hora
  let inited = false;
  const BEEP_EVERY = 10000;         // repetição do alerta: a cada 10 s
  let es = null, lastMsg = 0, connected = false;
  const loadedAt = Date.now();

  // ---------- estado ----------
  let srvVersion = "";
  function applyState(s, fromCache) {
    // servidor atualizado (versão nova): recarrega para pegar a tela nova (a posição do rodízio é mantida)
    if (!fromCache && s.version) {
      if (!srvVersion) srvVersion = s.version;
      else if (s.version !== srvVersion) { location.reload(); return; }
    }
    state = s;
    if (!fromCache) {
      offset = s.serverTime - Date.now();
      try { localStorage.setItem(KEY, JSON.stringify({ s, offset })); } catch (e) {}
    }
    tick();
  }

  const srvNow = () => Date.now() + offset;
  function active() {
    if (!state) return [];
    const t = srvNow() / 1000;
    return state.announcements.filter(a => a.startsAt <= t && (a.endsAt == null || a.endsAt > t));
  }

  // ---------- relógio ----------
  const fmtTime = new Intl.DateTimeFormat("pt-BR", { hour: "2-digit", minute: "2-digit" });
  const fmtDate = new Intl.DateTimeFormat("pt-BR", { weekday: "long", day: "numeric", month: "long", year: "numeric" });
  const fmtShort = new Intl.DateTimeFormat("pt-BR", { weekday: "long", day: "2-digit", month: "2-digit" });
  function clocks() {
    const d = new Date(srvNow());
    const t = fmtTime.format(d);
    $("idle-clock").textContent = t;
    $("top-clock").textContent = t;
    const cap = (x) => x.charAt(0).toUpperCase() + x.slice(1);
    $("idle-date").textContent = cap(fmtDate.format(d));
    $("top-date").textContent = cap(fmtShort.format(d));
  }

  function brand() {
    const name = (state && state.companyName) || "";
    const logo = (state && state.logo) || "";
    for (const [img, txt] of [["idle-logo", "idle-company"], ["top-logo", "top-company"]]) {
      const el = $(img);
      const src = withKey(logo);
      if (logo) { if (el.getAttribute("src") !== src) el.src = src; el.hidden = false; } else el.hidden = true;
      $(txt).textContent = name;
    }
  }

  // ---------- render ----------
  function setMode(m) {
    document.body.className = "mode-" + m;
  }

  function fitText() {
    const box = document.querySelector(".text");
    let fs = 1;
    box.style.setProperty("--fs", fs);
    while (box.scrollHeight > box.clientHeight + 2 && fs > 0.45) {
      fs -= 0.05;
      box.style.setProperty("--fs", fs.toFixed(2));
    }
  }

  function showNotice(a, list, urgent, durMs, showBar) {
    setMode(urgent ? "urgent" : "normal");
    $("badge-text").textContent = urgent ? "URGENTE" : "COMUNICADO";
    $("title").textContent = a.title;
    $("body").innerHTML = a.html; // HTML já sanitizado no servidor
    if (a.image) { $("image").src = withKey(a.image); $("image-wrap").hidden = false; } else { $("image-wrap").hidden = true; }
    const hm = fmtTime.format(new Date(a.startsAt * 1000));
    let meta = (a.author ? a.author + " · " : "") + "publicado às " + hm;
    if (a.endsAt) meta += " · válido até " + new Intl.DateTimeFormat("pt-BR", { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" }).format(new Date(a.endsAt * 1000));
    $("meta").textContent = meta;
    // pager: só o grupo atual (urgentes ou normais), mais claro de longe
    $("pager").innerHTML = list.length > 1 ? list.map((_, i) => `<i class="${i === idx ? "on" : ""}"></i>`).join("") : "";
    const card = $("card");
    card.classList.remove("enter"); void card.offsetWidth; card.classList.add("enter");
    fitText();
    if (a.image) $("image").onload = fitText;
    // barra de progresso do rodízio
    const bar = $("bar");
    bar.style.transition = "none"; bar.style.width = "0";
    if (showBar) {
      void bar.offsetWidth;
      const left = Math.max(0, durMs - (mono() - slideStart));
      bar.style.transition = `width ${left}ms linear`; bar.style.width = "100%";
    }
  }

  const rotateMs = () => Math.max(5, (state && state.rotateSeconds) || 15) * 1000;

  const urgentMs = () => Math.min(600, Math.max(10, (state && state.urgentSeconds) || 60)) * 1000;
  // modo do urgente: "interleave" (padrão: intercala com os normais) ou "fixed" (fica fixo até sair do ar)
  const interleave = () => !(state && state.urgentMode === "fixed");
  // identifica a versão do urgente: id + edição + conteúdo (uma edição no mesmo segundo também conta)
  const hash = (t) => { let h = 5381; for (let i = 0; i < t.length; i++) h = ((h * 33) ^ t.charCodeAt(i)) >>> 0; return h.toString(36); };
  const ukey = (a) => a.id + ":" + a.updatedAt + ":" + hash(a.title + "\u0000" + a.html + "\u0000" + (a.image || ""));
  const slideMs = (ph) => (ph === "u" && interleave()) ? urgentMs() : rotateMs();

  // Posição da sequência, guardada para sobreviver a recarga/reabertura da página (ex.: o Edge reabriu a aba):
  // não volta ao começo, não apita de novo do zero e não pula o urgente.
  function save() {
    try {
      const n = Date.now(), m = mono();
      localStorage.setItem(ROT, JSON.stringify({ v: 2, phase, id: curId, at: n - (m - slideStart),
        alertAt: alertStart === null ? null : n - (m - alertStart), beeps, seenU: seenU ? [...seenU] : [] }));
    } catch (e) {}
  }
  function init(urg, norm) {
    inited = true;
    let r = null;
    try { r = JSON.parse(localStorage.getItem(ROT) || "null"); } catch (e) {}
    if (!r || r.v !== 2) { seenU = new Set(urg.map(ukey)); return; }  // 1ª abertura: começa pelos urgentes
    seenU = new Set(r.seenU || []);
    const g = r.phase === "u" ? urg : r.phase === "n" ? norm : [];
    const i = g.findIndex(a => a.id === r.id);
    const el = Date.now() - r.at, dur = slideMs(r.phase);
    const fixedU = r.phase === "u" && !interleave();
    if (i < 0 || !(el >= 0 && (el < dur * 4 || fixedU))) return;   // muito antigo ou relógio mudou: recomeça
    phase = r.phase; idx = i; curId = r.id; slideStart = mono() - Math.min(el, dur); slideNo++;
    if (phase === "u") {
      const ael = r.alertAt == null ? -1 : Date.now() - r.alertAt;
      if (ael >= 0) { alertStart = mono() - ael; beeps = r.beeps || 0; } else { alertStart = mono(); beeps = 0; }
    }
  }
  // entra no aviso i da fase ph; alert = começa um novo período de alerta sonoro
  function enter(ph, i, urg, norm, alert) {
    const g = ph === "u" ? urg : norm;
    phase = ph; idx = i; curId = g[i].id; slideStart = mono(); slideNo++;
    if (ph === "u") { if (alert || interleave() || alertStart === null) { alertStart = mono(); beeps = 0; } }
    else alertStart = null;
    save();
  }
  // próximo depois da posição `from` do grupo atual: segue no grupo; no fim, troca de grupo (intercalar) ou recomeça
  function next(from, urg, norm) {
    const g = phase === "u" ? urg : norm;
    if (from + 1 < g.length) return enter(phase, from + 1, urg, norm, false);
    if (interleave()) {
      if (phase === "u" && norm.length) return enter("n", 0, urg, norm, false);
      if (phase === "n" && urg.length) return enter("u", 0, urg, norm, true);
    }
    enter(phase, 0, urg, norm, false);
  }

  function tick() {
    clocks();
    brand();
    const list = active();
    const urg = list.filter(a => a.priority === "urgent");
    const norm = list.filter(a => a.priority !== "urgent");
    if (!list.length) {
      if (shownKey !== "idle") { setMode("idle"); shownKey = "idle"; phase = ""; curId = null; alertStart = null; }
      if (seenU && seenU.size) { seenU = new Set(); save(); }
      return;
    }
    if (!inited) init(urg, norm);
    // urgente novo ou editado: entra na hora (interrompe o que estiver na tela)
    const fresh = urg.findIndex(a => !seenU.has(ukey(a)));
    const keys = urg.map(ukey);
    const seenChanged = keys.length !== seenU.size || keys.some(k => !seenU.has(k));
    seenU = new Set(keys);
    if (fresh >= 0) enter("u", fresh, urg, norm, true);
    else if (seenChanged) save();
    // garante uma fase válida
    if (phase === "u" && !urg.length) enter("n", 0, urg, norm, false);
    else if (phase === "n" && !norm.length) enter("u", 0, urg, norm, true);
    else if (phase === "") enter(urg.length ? "u" : "n", 0, urg, norm, true);
    else if (phase === "n" && urg.length && !interleave()) enter("u", 0, urg, norm, true); // modo fixo: urgente manda
    let g = phase === "u" ? urg : norm;
    const i = g.findIndex(a => a.id === curId);
    if (i >= 0) idx = i;
    else next(Math.min(idx, g.length) - 1, urg, norm);          // o aviso atual saiu do ar: segue para o próximo
    // tempo do slide esgotado?
    const canAdvance = () => interleave() ? (phase === "u" || norm.length > 1 || urg.length > 0) : (phase === "u" ? urg : norm).length > 1;
    if (canAdvance() && mono() - slideStart >= slideMs(phase)) next(idx, urg, norm);
    g = phase === "u" ? urg : norm;
    const a = g[idx];
    const k = slideNo + ":" + ukey(a) + ":" + g.length;
    if (k !== shownKey) {
      shownKey = k;
      showNotice(a, g, phase === "u", slideMs(phase), canAdvance());
    }
    // alerta sonoro: no início do período e, se ligado, a cada 10 s enquanto durar o tempo do urgente
    if (phase === "u" && alertStart !== null && state && state.sound) {
      const el = mono() - alertStart;
      const due = state.urgentBeepRepeat === false ? 1 : Math.min(Math.floor(el / BEEP_EVERY) + 1, Math.ceil(urgentMs() / BEEP_EVERY));
      if (beeps < due) { beeps = due; beep(); save(); }
    }
  }

  // ---------- som (Web Audio, sem arquivo) ----------
  let ctx = null;
  function beep() {
    try {
      ctx = ctx || new (window.AudioContext || window.webkitAudioContext)();
      if (ctx.state === "suspended") ctx.resume();
      setTimeout(() => { $("sound").hidden = ctx.state === "running"; }, 300);
      const t0 = ctx.currentTime + 0.05;
      [0, 0.35, 0.7].forEach((d, i) => {
        const o = ctx.createOscillator(), g = ctx.createGain();
        o.type = "sine"; o.frequency.value = i === 2 ? 1175 : 880;
        g.gain.setValueAtTime(0.0001, t0 + d);
        g.gain.exponentialRampToValueAtTime(0.5, t0 + d + 0.02);
        g.gain.exponentialRampToValueAtTime(0.0001, t0 + d + 0.28);
        o.connect(g).connect(ctx.destination);
        o.start(t0 + d); o.stop(t0 + d + 0.3);
      });
    } catch (e) { $("sound").hidden = false; }
  }
  // um clique/tecla destrava o áudio caso o navegador bloqueie
  ["click", "keydown", "touchstart"].forEach(ev => addEventListener(ev, () => {
    ctx = ctx || new (window.AudioContext || window.webkitAudioContext)();
    ctx.resume().then(() => { $("sound").hidden = true; });
  }));

  // estado em cache: continua exibindo mesmo se o servidor estiver fora
  try {
    const c = JSON.parse(localStorage.getItem(KEY) || "null");
    if (c && c.s) { offset = c.offset || 0; applyState(c.s, true); }
  } catch (e) {}

  // ---------- conexão em tempo real (SSE) ----------
  function setConn(ok) {
    connected = ok;
    $("conn").hidden = ok || denied;
  }
  // chave inválida/revogada: para tudo, apaga o cache local e mostra o aviso
  function deny(msg) {
    if (denied) return;
    denied = true;
    if (es) es.close();
    try { localStorage.removeItem(KEY); localStorage.removeItem(ROT); } catch (e) {}
    state = null;
    if (window.caches) caches.keys().then(ks => ks.forEach(k => k.startsWith("painel-tela") && caches.delete(k))).catch(() => {});
    $("denied-msg").textContent = "O acesso desta TV foi removido ou o endereço está incompleto.";
    $("denied").hidden = false;
    $("conn").hidden = true;
  }
  async function checkAuth() {
    try {
      const r = await fetch("/api/screen/heartbeat?screen=" + encodeURIComponent(screen), { method: "POST", headers: skey ? { "X-Screen-Key": skey } : {}, cache: "no-store" });
      if (r.status === 401 || r.status === 403) {
        let m = ""; try { m = (await r.json()).error; } catch (e) {}
        deny(m);
      }
      return r.ok;
    } catch (e) { return false; }
  }
  function connect() {
    if (denied) return;
    if (es) es.close();
    es = new EventSource("/api/screen/events?" + auth);
    lastMsg = Date.now();
    es.addEventListener("state", (e) => {
      lastMsg = Date.now(); setConn(true);
      try { applyState(JSON.parse(e.data), false); } catch (err) { console.error(err); }
    });
    es.addEventListener("ping", (e) => {
      lastMsg = Date.now(); setConn(true);
      const t = Number(e.data); if (t) offset = t - Date.now();
    });
    // o EventSource reconecta sozinho (retry 3s); um heartbeat descobre se o erro é de autorização
    es.onerror = () => { setConn(false); checkAuth(); };
  }
  connect();
  // vigia: sem mensagens por 40s => reconecta à força
  setInterval(() => {
    if (!denied && Date.now() - lastMsg > 40000) { setConn(false); connect(); }
  }, 5000);

  // heartbeat explícito (status online no painel do líder)
  function heartbeat() { if (!denied) checkAuth(); }
  heartbeat();
  setInterval(heartbeat, 30000);

  // relógio / rodízio
  setInterval(tick, 1000);
  tick();
  if (!state) setConn(false);

  // service worker: abre a tela mesmo sem rede após reiniciar (só em contexto seguro: https ou localhost)
  if ("serviceWorker" in navigator && window.isSecureContext) {
    navigator.serviceWorker.register("/tela-sw.js", { scope: "/" }).catch((e) => console.warn("SW:", e));
  }

  // recarrega 1x por dia de madrugada (higiene do quiosque), só se o servidor estiver no ar
  setInterval(() => {
    const h = new Date().getHours();
    if (connected && !denied && h === 4 && Date.now() - loadedAt > 6 * 3600e3) location.reload();
  }, 600000);
})();
