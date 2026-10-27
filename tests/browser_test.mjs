// Teste no Chrome headless + screenshots 1920x1080 (puppeteer-core).
// Sobe o servidor sozinho (porta própria, pasta temporária), testa: CSP sem violações no admin
// e na TV, imagens com chave, tela não autorizada, revogação ao vivo, service worker offline.
// Uso (na pasta com node_modules/puppeteer-core): BIN=../dist/painel-avisos-linux-amd64 OUT=../screenshots node browser_test.mjs
import puppeteer from "puppeteer-core";
import { spawn } from "node:child_process";
import { mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { randomBytes } from "node:crypto";
import zlib from "node:zlib";

const BIN = process.env.BIN, OUT = process.env.OUT || "screenshots", PORT = 18090;
const BASE = `http://localhost:${PORT}`;   // localhost = contexto seguro => service worker habilitado
const DATA = mkdtempSync(join(tmpdir(), "painel-browser-"));
const PW = randomBytes(12).toString("base64url");
const sleep = (ms) => new Promise(r => setTimeout(r, ms));
const results = [];
const check = (name, ok, detail = "") => { results.push([name, !!ok]); console.log(`${ok ? "PASS" : "FAIL"} ${name}${detail ? " — " + detail : ""}`); };

let srv;
async function startServer(extra = {}) {
  srv = spawn(BIN, ["-port", String(PORT), "-bind", "127.0.0.1", "-data", DATA], { env: { ...process.env, PAINEL_ADMIN_PASSWORD: PW, PAINEL_ALLOW_KEYLESS: "0", ...extra }, stdio: "ignore" });
  for (let i = 0; i < 50; i++) { try { if ((await fetch(BASE + "/healthz")).ok) return; } catch {} await sleep(100); }
  throw new Error("servidor não subiu");
}
async function stopServer() { srv.kill("SIGTERM"); await new Promise(r => srv.once("exit", r)); }

// PNG simples (gradiente) para testar imagem do aviso e logo
function makePng(w, h, f) {
  const raw = Buffer.alloc((w * 3 + 1) * h);
  for (let y = 0; y < h; y++) for (let x = 0; x < w; x++) { const [r, g, b] = f(x, y); raw.set([r, g, b], y * (w * 3 + 1) + 1 + x * 3); }
  const crcT = []; for (let n = 0; n < 256; n++) { let c = n; for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1; crcT[n] = c >>> 0; }
  const crc = (b) => { let c = 0xffffffff; for (const x of b) c = crcT[(c ^ x) & 255] ^ (c >>> 8); return (c ^ 0xffffffff) >>> 0; };
  const chunk = (t, d) => { const l = Buffer.alloc(4); l.writeUInt32BE(d.length); const td = Buffer.concat([Buffer.from(t), d]); const c = Buffer.alloc(4); c.writeUInt32BE(crc(td)); return Buffer.concat([l, td, c]); };
  const ihdr = Buffer.alloc(13); ihdr.writeUInt32BE(w, 0); ihdr.writeUInt32BE(h, 4); ihdr.set([8, 2, 0, 0, 0], 8);
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk("IHDR", ihdr), chunk("IDAT", zlib.deflateSync(raw)), chunk("IEND", Buffer.alloc(0))]);
}
const imgPath = join(DATA, "..", `aviso-${process.pid}.png`), logoPath = join(DATA, "..", `logo-${process.pid}.png`);
writeFileSync(imgPath, makePng(640, 420, (x, y) => [20 + x / 5, 90 + y / 4, 160]));
writeFileSync(logoPath, makePng(240, 80, (x, y) => (x < 70 ? [56, 189, 248] : [240, 244, 250])));

process.on("exit", () => { try { srv && srv.kill("SIGKILL"); } catch {} });
process.on("uncaughtException", (e) => { console.log("ERRO:", e.message); process.exit(1); });
process.on("unhandledRejection", (e) => { console.log("ERRO:", e && e.message); process.exit(1); });
await startServer();
const browser = await puppeteer.launch({ executablePath: process.env.CHROME || "/usr/bin/google-chrome", headless: true,
  args: ["--no-sandbox", "--autoplay-policy=no-user-gesture-required", "--lang=pt-BR"] });
const violations = [], pageErrors = [];
async function newPage(tag) {
  const p = await browser.newPage();
  await p.setViewport({ width: 1920, height: 1080 });
  await p.evaluateOnNewDocument(() => document.addEventListener("securitypolicyviolation", (e) => console.error("CSPVIOLATION " + e.violatedDirective + " " + e.blockedURI)));
  p.on("console", (m) => { const t = m.text(); if (/CSPVIOLATION|Content Security Policy|Refused to/.test(t)) violations.push(`[${tag}] ${t}`); });
  p.on("pageerror", (e) => pageErrors.push(`[${tag}] ${e.message}`));
  return p;
}
const H = { "Content-Type": "application/json", "X-Requested-With": "painel" };

// ---------------- admin ----------------
const adm = await newPage("admin");
await adm.goto(BASE + "/admin", { waitUntil: "networkidle0" });
await adm.type("input[name=username]", "admin");
await adm.type("input[name=password]", PW);
await adm.click("#login-form button");
await adm.waitForSelector("#app:not([hidden])", { polling: 100, timeout: 10000 });
check("Login no painel pelo navegador", true);
const call = (method, path, body) => adm.evaluate(async (m, p, b, h) => { const r = await fetch(p, { method: m, headers: h, body: b ? JSON.stringify(b) : undefined }); return r.json(); }, method, path, body, H);
const tab = async (t) => { await adm.evaluate((t) => document.querySelector(`#tabs a[data-tab="${t}"]`).click(), t); await sleep(700); };

// Configurações: nome + logo via UI
await tab("config");
await adm.$eval("#settings-form [name=company_name]", (el) => { el.value = ""; });
await adm.type("#settings-form [name=company_name]", "Empresa Exemplo Ltda.");
const logoInput = await adm.$("#logo-file"); await logoInput.uploadFile(logoPath);
await adm.waitForFunction(() => !document.getElementById("logo-preview").hidden && document.getElementById("logo-preview").naturalWidth > 0, { polling: 100, timeout: 5000 });
await adm.evaluate(() => document.querySelector("#settings-form button.primary").click());
await adm.waitForFunction(() => document.getElementById("settings-ok").textContent.includes("Salvo"), { polling: 100, timeout: 5000 });
check("Configurações + upload de logo pela UI (preview carregou com a sessão)", true);
await adm.screenshot({ path: `${OUT}/08-admin-configuracoes.png` });
{
  const inf = await call("GET", "/api/admin/info");
  const hidden = await adm.$eval("#tech-details", (e) => e.hidden && e.offsetParent === null);
  check("Padrão: Configurações sem 'Detalhes técnicos' e /api/admin/info só com a versão", hidden && !("server" in inf) && !("trustProxy" in inf) && !("clientIp" in inf), JSON.stringify(inf));
}

// Telas: criar pela UI -> chave exibida uma vez
await tab("telas");
await adm.type("#screen-form [name=name]", "tv-principal");
await adm.type("#screen-form [name=label]", "TV principal — produção");
await adm.evaluate(() => document.querySelector("#screen-form button").click());
await adm.waitForFunction(() => !document.getElementById("key-new").hidden, { polling: 100, timeout: 5000 });
const KEY = await adm.$eval("#key-only", (el) => el.textContent.trim());
check("Criar tela pela UI mostra a chave uma vez", KEY.startsWith("tk_"), KEY.slice(0, 7) + "…");
await call("POST", "/api/admin/screens", { name: "tv-refeitorio", label: "TV do refeitório" });
await adm.evaluate(() => document.getElementById("close-key").click());
{
  // Varredura de jargão técnico no texto visível de todas as abas do admin
  const FORBIDDEN = ["TRUST_PROXY", "SHOW_SERVER_INFO", "PAINEL_", "CIDR", "SSE", "heartbeat", "Proxies confiáveis", "variáveis de ambiente",
    "/tela?screen", "HTTP ", "Bearer", "Nome técnico", "Prefixo", "token", "webhook", "JSON", "POST", "localhost", "127.0.0.1"];
  const found = new Set();
  for (const t of ["avisos", "telas", "usuarios", "config", "api", "conta"]) {
    await tab(t);
    const txt = await adm.evaluate(() => document.getElementById("app").innerText);
    for (const w of FORBIDDEN) if (txt.toLowerCase().includes(w.toLowerCase())) found.add(`${t}:${w}`);
  }
  check("Nenhum jargão técnico visível nas abas do admin (padrão)", found.size === 0, [...found].join(", ") || `${FORBIDDEN.length} termos verificados em 6 abas`);
}
await tab("conta");
await adm.evaluate(() => document.getElementById("totp-start").click());
await adm.waitForFunction(() => document.getElementById("totp-qr").naturalWidth > 0, { polling: 100, timeout: 5000 });
check("QR code do 2FA (data: URL) renderiza sob a CSP", true);
await tab("avisos");

// ---------------- TV ----------------
const TELA = `${BASE}/tela?screen=tv-principal&key=${encodeURIComponent(KEY)}`;
const tv = await newPage("tela");
await tv.goto(TELA, { waitUntil: "domcontentloaded" });
await sleep(1500);
await tv.screenshot({ path: `${OUT}/01-tela-ociosa.png` });
check("Logo protegido carrega na TV (URL com chave)", await tv.$eval("#idle-logo", (i) => !i.hidden && i.naturalWidth > 0));

// publicar pela UI com imagem
const now = Math.floor(Date.now() / 1000);
await adm.type("#ann-form [name=title]", "Reunião geral hoje às 15h");
await adm.type("#ann-form [name=body]", "Todos os colaboradores devem comparecer ao **refeitório**.\n\nPauta:\n- Resultados do mês\n- Novas metas de produção\n- Escala de férias de dezembro");
const imgInput = await adm.$("#img-file"); await imgInput.uploadFile(imgPath);
await adm.waitForFunction(() => !document.getElementById("img-preview").hidden, { polling: 100, timeout: 5000 });
const t0 = Date.now();
await adm.evaluate(() => document.getElementById("submit-btn").click());
await tv.waitForFunction(() => document.getElementById("title").textContent === "Reunião geral hoje às 15h", { polling: 100, timeout: 15000 });
check("Aviso publicado pela UI aparece na TV", true, `${Date.now() - t0} ms (inclui o clique)`);
await tv.waitForFunction(() => document.getElementById("image").naturalWidth > 0, { polling: 100, timeout: 5000 });
check("Imagem do aviso carrega na TV via chave", true);
await call("POST", "/api/admin/announcements", { title: "Manutenção do ar-condicionado", body: "Quinta-feira, das 8h às 10h, no setor administrativo.", endsAt: now + 3 * 86400 });
await sleep(1500);
await tv.screenshot({ path: `${OUT}/02-tela-aviso-normal.png` });

// rodízio: 2 avisos normais precisam trocar sozinhos (v1.1.2)
await call("PUT", "/api/admin/settings", { rotate_seconds: "5" });
await sleep(800);
{
  const first = await tv.$eval("#title", (e) => e.textContent);
  const tr0 = Date.now();
  let ok = true;
  try { await tv.waitForFunction((f) => document.getElementById("title").textContent !== f, { polling: 200, timeout: 9000 }, first); } catch { ok = false; }
  check("Rodízio: 2 avisos normais trocam sozinhos no tempo configurado (5 s)", ok, ok ? `${Date.now() - tr0} ms` : "não trocou");
  // TV reabrindo a página a cada 3 s (Edge reaberto pelo lançador) não pode prender no 1º aviso
  const seenT = new Set();
  for (let i = 0; i < 5; i++) { await tv.goto(TELA, { waitUntil: "domcontentloaded" }); await sleep(3000); seenT.add(await tv.$eval("#title", (e) => e.textContent)); }
  check("Rodízio continua mesmo com a página reaberta a cada 3 s", seenT.size > 1, JSON.stringify([...seenT]));
}
await call("PUT", "/api/admin/settings", { rotate_seconds: "15" });

const t1 = Date.now();
const u = await call("POST", "/api/admin/announcements", { title: "Vazamento na linha 3 — afaste-se da área", priority: "urgent", body: "Equipe de manutenção a caminho.\nUse a **saída lateral** até nova orientação.", durationMinutes: 30 });
await tv.waitForFunction(() => document.body.className === "mode-urgent", { polling: 100, timeout: 15000 });
check("TV entra em modo URGENTE", true, `${Date.now() - t1} ms`);
await sleep(1000);
check("Áudio do alerta liberado (flag de autoplay)", await tv.evaluate(() => document.getElementById("sound").hidden));
await tv.screenshot({ path: `${OUT}/03-tela-urgente.png` });
// v1.2: urgente intercalado em tempo real (tempos curtos): urgente 10 s → normais (5 s cada) → urgente de novo
await call("PUT", "/api/admin/settings", { urgent_seconds: "10", rotate_seconds: "5" });
{
  const tl = []; const ts = Date.now();
  while (Date.now() - ts < 34000) { tl.push([Math.round((Date.now() - ts) / 1000), await tv.evaluate(() => document.body.className)]); await sleep(500); }
  const modes = tl.map((x) => x[1]).filter((m, i, arr) => i === 0 || m !== arr[i - 1]);
  check("Urgente intercalado (tempo real): urgente → avisos normais → urgente de novo", modes.join(",").includes("mode-urgent,mode-normal,mode-urgent"), modes.join(" → "));
}
await call("PUT", "/api/admin/settings", { urgent_seconds: "60", rotate_seconds: "15" });
await call("POST", "/api/admin/announcements", { title: "Inventário semestral", body: "Sábado, 8h. Setores A e B.", startsAt: now + 2 * 86400, endsAt: now + 2 * 86400 + 8 * 3600, allScreens: false, screens: ["tv-principal"] });
await adm.reload({ waitUntil: "domcontentloaded" }); await sleep(1500);
await adm.screenshot({ path: `${OUT}/04-admin-avisos.png` });
await call("POST", `/api/admin/announcements/${u.id}/end`);
await tv.waitForFunction(() => document.body.className === "mode-normal", { polling: 100, timeout: 15000 });
await tab("telas");
await adm.screenshot({ path: `${OUT}/05-admin-telas.png` });

// ---------------- service worker / offline ----------------
await tv.evaluate(async () => { await navigator.serviceWorker.ready; });
await tv.reload({ waitUntil: "domcontentloaded" });   // segunda carga: página controlada pelo SW e guardada no cache
await tv.waitForFunction(() => !!navigator.serviceWorker.controller, { polling: 100, timeout: 10000 });
await sleep(1500);
const cached = await tv.evaluate(async () => { const ks = await caches.keys(); let n = 0; for (const k of ks) n += (await (await caches.open(k)).keys()).length; return { ks, n }; });
check("Service worker ativo e com a tela em cache", cached.n >= 3, JSON.stringify(cached));
await stopServer();
await sleep(500);
const off = await newPage("tela-offline");
let offOk = false;
try {
  await off.goto(TELA, { waitUntil: "domcontentloaded", timeout: 15000 });
  await off.waitForFunction(() => document.getElementById("title") && document.getElementById("title").textContent.length > 0, { polling: 100, timeout: 10000 });
  offOk = true;
} catch (e) { console.log("offline:", e.message); }
const offTitle = offOk ? await off.$eval("#title", (e) => e.textContent) : "";
check("SERVIDOR DESLIGADO: nova aba abre a tela pelo cache e mostra o último aviso", offOk && offTitle.length > 0, offTitle);
await sleep(4000);
check("Indicador 'reconectando…' visível offline", offOk && await off.$eval("#conn", (e) => !e.hidden));
await off.screenshot({ path: `${OUT}/06-tela-offline-service-worker.png` });
await off.close();

// ---------------- revogação ao vivo ----------------
await startServer();
await adm.reload({ waitUntil: "domcontentloaded" }); await adm.waitForSelector("#app:not([hidden])", { polling: 100, timeout: 10000 });
await tv.waitForFunction(() => document.getElementById("conn").hidden, { polling: 100, timeout: 20000 });
const scr = (await call("GET", "/api/admin/screens")).find((s) => s.name === "tv-principal");
const tr = Date.now();
await call("DELETE", `/api/admin/screens/${scr.id}/key`);
await tv.waitForFunction(() => !document.getElementById("denied").hidden, { polling: 100, timeout: 15000 });
check("Chave revogada com a TV aberta: TV mostra 'Tela não autorizada'", true, `${Date.now() - tr} ms`);
const leftover = await tv.evaluate(async () => ({ ls: Object.keys(localStorage).filter((k) => k.startsWith("painel:state")).length, caches: (await caches.keys()).length }));
check("Revogação apaga o estado local e o cache offline da TV", leftover.ls === 0 && leftover.caches === 0, JSON.stringify(leftover));
await tv.screenshot({ path: `${OUT}/07-tela-nao-autorizada.png` });
const resp = await tv.reload({ waitUntil: "domcontentloaded" });
check("Recarregar com chave revogada → HTTP 403 + página 'Tela não autorizada'", resp.status() === 403 && (await tv.content()).includes("Tela não autorizada"), `HTTP ${resp.status()}`);
await stopServer();
const off2 = await newPage("tela-revogada-offline");
let shown = false;
try { await off2.goto(TELA, { waitUntil: "domcontentloaded", timeout: 8000 }); shown = await off2.evaluate(() => !!document.getElementById("title") && document.getElementById("title").textContent.length > 0); } catch {}
check("Chave revogada + servidor desligado: NÃO abre conteúdo do cache", !shown);
await startServer();
const bad = await newPage("tela-chave-errada");
const r2 = await bad.goto(`${BASE}/tela?screen=tv-refeitorio&key=tk_errada`, { waitUntil: "domcontentloaded" });
check("Chave errada → 403 'Tela não autorizada'", r2.status() === 403 && (await bad.content()).includes("Tela não autorizada"));

// ---------------- detalhes técnicos (opt-in) ----------------
await stopServer();
await startServer({ PAINEL_SHOW_SERVER_INFO: "1" });
await adm.reload({ waitUntil: "domcontentloaded" }); await adm.waitForSelector("#app:not([hidden])", { polling: 100, timeout: 10000 });
await tab("config");
const techVisible = await adm.$eval("#tech-details", (e) => !e.hidden && !e.open);
await adm.evaluate(() => { const d = document.getElementById("tech-details"); d.open = true; d.scrollIntoView(); });
await sleep(300);
const techTxt = await adm.$eval("#tech-info", (e) => e.innerText);
check("SHOW_SERVER_INFO=1: admin vê 'Detalhes técnicos' recolhido, com modo/proxies/IP", techVisible && techTxt.includes("Proxies confiáveis") && techTxt.includes("Seu IP"), techTxt.split("\n").slice(0, 2).join(" "));
await adm.bringToFront();
await adm.screenshot({ path: `${OUT}/09-admin-detalhes-tecnicos-opcional.png` });

// ---------------- CSP ----------------
const real = violations.slice();
// controle positivo: injetar script inline DEVE gerar violação (prova que o detector funciona)
const sanity = await newPage("csp-sanity");
await sanity.goto(`${BASE}/admin`, { waitUntil: "domcontentloaded" });
await sanity.evaluate(() => { const s = document.createElement("script"); s.textContent = "window.__inline = 1"; document.body.appendChild(s); });
await sleep(500);
const inlineRan = await sanity.evaluate(() => window.__inline === 1);
check("Controle: script inline injetado é bloqueado pela CSP e detectado", !inlineRan && violations.some((v) => v.startsWith("[csp-sanity]")));
check("Nenhuma violação de CSP no console (admin, TV, offline, negada)", real.length === 0, real.slice(0, 5).join(" | "));
check("Nenhum erro de JavaScript nas páginas", pageErrors.length === 0, pageErrors.slice(0, 5).join(" | "));

await browser.close();
await stopServer();
rmSync(DATA, { recursive: true, force: true }); rmSync(imgPath, { force: true }); rmSync(logoPath, { force: true });
const ok = results.filter((r) => r[1]).length;
console.log(`\nRESULTADO: ${ok}/${results.length} verificações OK`);
process.exit(ok === results.length ? 0 : 1);
