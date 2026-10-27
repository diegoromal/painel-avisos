// Teste do aviso urgente intercalado (v1.2) no Chrome headless com relógio simulado (Playwright clock):
// roda os tempos reais (urgente 60 s, rodízio 15 s) em segundos. O áudio é substituído por um contador de bipes.
// Uso (na pasta com node_modules/playwright-core): BIN=../dist/painel-avisos-linux-amd64 node urgent_test.mjs
import { chromium } from "playwright-core";
import { spawn } from "node:child_process";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { randomBytes } from "node:crypto";

const BIN = process.env.BIN, PORT = 18095, BASE = `http://localhost:${PORT}`;
const PW = randomBytes(12).toString("base64url"), DATA = mkdtempSync(join(tmpdir(), "painel-urgente-"));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const results = [];
const check = (name, ok, detail = "") => { results.push(!!ok); console.log(`${ok ? "PASS" : "FAIL"} ${name}${detail ? " — " + detail : ""}`); };

const srv = spawn(BIN, ["-port", String(PORT), "-bind", "127.0.0.1", "-data", DATA],
  { env: { ...process.env, PAINEL_ADMIN_PASSWORD: PW, PUBLIC_URL: "https://avisos.exemplo.test", TRUST_PROXY: "127.0.0.1/32" }, stdio: "ignore" });
process.on("exit", () => { try { srv.kill("SIGKILL"); } catch {} });
for (let i = 0; i < 50; i++) { try { if ((await fetch(BASE + "/healthz")).ok) break; } catch {} await sleep(100); }
let cookie = "";
async function call(m, p, b) {
  const r = await fetch(BASE + p, { method: m, headers: { "Content-Type": "application/json", "X-Requested-With": "painel", Cookie: cookie }, body: b ? JSON.stringify(b) : undefined });
  const sc = r.headers.get("set-cookie"); if (sc) cookie = sc.split(";")[0];
  const t = await r.text(); if (!r.ok) throw new Error(`${m} ${p} → ${r.status} ${t}`); return t ? JSON.parse(t) : null;
}
await call("POST", "/api/login", { username: "admin", password: PW });
await call("POST", "/api/admin/screens", { name: "tv-principal", label: "TV" });
const scr = (await call("GET", "/api/admin/screens")).find((s) => s.name === "tv-principal");
const KEY = (await call("POST", `/api/admin/screens/${scr.id}/key`)).key;
const TELA = `${BASE}/tela?screen=tv-principal&key=${KEY}`;
const st0 = await call("GET", "/api/admin/settings");
check("Padrões novos: intercalar, 60 s, repetir alerta", st0.urgent_mode === "interleave" && st0.urgent_seconds === "60" && st0.urgent_beep_repeat === "1", JSON.stringify({ m: st0.urgent_mode, s: st0.urgent_seconds, r: st0.urgent_beep_repeat }));

const browser = await chromium.launch({ executablePath: process.env.CHROME || "/usr/bin/google-chrome", args: ["--no-sandbox"] });
const pageErrors = [];
async function endAll() { for (const a of await call("GET", "/api/admin/announcements?view=active")) await call("POST", `/api/admin/announcements/${a.id}/end`); }
async function setup({ mode = "interleave", urgentSec = "60", repeat = "1", rotate = "15", sound = "1" } = {}) {
  await endAll();
  await call("PUT", "/api/admin/settings", { urgent_mode: mode, urgent_seconds: urgentSec, urgent_beep_repeat: repeat, rotate_seconds: rotate, sound_enabled: sound });
}
const pub = (title, urgent = false) => call("POST", "/api/admin/announcements", { title, body: "Texto.", priority: urgent ? "urgent" : "normal", allScreens: true });

// abre a TV com relógio simulado; mock de AudioContext conta os bipes (cada bipe = 3 osciladores)
async function openTV(ctx) {
  const p = ctx.pages()[0] || await ctx.newPage();
  p.on("pageerror", (e) => pageErrors.push(e.message));
  await p.goto(TELA, { waitUntil: "domcontentloaded" });
  await p.waitForFunction(() => document.body.className !== "mode-idle" && document.getElementById("title").textContent, null, { timeout: 10000 });
  return p;
}
async function newTV() {
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 720 } });
  await ctx.addInitScript(() => {
    const log = JSON.parse(sessionStorage.getItem("__beeps") || "[]");
    window.__beeps = log;
    class FakeCtx {
      constructor() { this.state = "running"; this.currentTime = 0; this.destination = {}; this.n = 0; }
      resume() { return Promise.resolve(); }
      createOscillator() { if (this.n++ % 3 === 0) { log.push(Date.now()); sessionStorage.setItem("__beeps", JSON.stringify(log)); }
        return { type: "", frequency: { value: 0 }, connect: (g) => g, start() {}, stop() {} }; }
      createGain() { return { gain: { setValueAtTime() {}, exponentialRampToValueAtTime() {} }, connect: (d) => d }; }
    }
    window.AudioContext = FakeCtx; window.webkitAudioContext = FakeCtx;
  });
  await ctx.clock.install({ time: Date.now() });
  return ctx;
}
// avança o relógio simulado segundo a segundo e registra o que a TV mostra
async function run(p, ctx, seconds, t0, log) {
  for (let i = 0; i < seconds; i++) {
    await ctx.clock.runFor(1000);
    const v = await p.evaluate(() => ({ t: document.getElementById("title").textContent, m: document.body.className, now: Date.now(),
      bar: document.getElementById("bar").style.transition }));
    log.push({ s: Math.round((v.now - t0) / 1000), t: v.t, m: v.m, bar: v.bar });
  }
}
// segmentos contínuos: [{t, from, to}]
function segments(log) {
  const out = [];
  for (const e of log) { const l = out[out.length - 1]; if (l && l.t === e.t) l.to = e.s; else out.push({ t: e.t, from: e.s, to: e.s }); }
  return out.map((x) => ({ ...x, dur: x.to - x.from + 1 }));
}
const fmtSeg = (sg) => sg.map((x) => `${x.t}(${x.dur}s)`).join(" → ");
const beepsAt = async (p, t0) => (await p.evaluate(() => window.__beeps)).map((b) => Math.round((b - t0) / 1000));
const near = (a, b, tol = 1) => Math.abs(a - b) <= tol;

// ---------- A) 1 urgente + 3 normais, padrões (60 s / 15 s) ----------
{
  await setup();
  await pub("Normal 1"); await pub("Normal 2"); await pub("Normal 3"); await pub("URGENTE A", true);
  const ctx = await newTV(); const p = await openTV(ctx);
  const t0 = await p.evaluate(() => Date.now()); const log = [];
  await run(p, ctx, 215, t0, log);
  const sg = segments(log);
  const seq = sg.map((x) => x.t).slice(0, 9).join(",");
  check("A) Sequência: urgente → ciclo de 3 normais → urgente → normais", seq === "URGENTE A,Normal 1,Normal 2,Normal 3,URGENTE A,Normal 1,Normal 2,Normal 3,URGENTE A", fmtSeg(sg));
  check("A) Urgente fica 60 s e cada normal 15 s", near(sg[0].dur, 60, 2) && sg.slice(1, 4).every((x) => near(x.dur, 15, 1)) && near(sg[4].dur, 60, 1), fmtSeg(sg.slice(0, 5)));
  check("A) Visual: vermelho no urgente, normal nos normais", log.filter((e) => e.t === "URGENTE A").every((e) => e.m === "mode-urgent") && log.filter((e) => e.t.startsWith("Normal")).every((e) => e.m === "mode-normal"));
  const urgBar = log.find((e) => e.t === "URGENTE A" && e.s > 61 && e.bar.includes("width"));
  check("A) Barra do urgente usa o tempo do urgente (~60 s)", log.some((e) => e.t === "URGENTE A" && /width (5\d|60)\d{3}(\.\d+)?ms/.test(e.bar)), (log.find((e) => e.t === "URGENTE A") || {}).bar);
  const b = await beepsAt(p, t0);
  const u2 = sg[4].from;
  const firstWin = b.filter((x) => x < 60), secondWin = b.filter((x) => x >= u2 - 1 && x < u2 + 60);
  check("A) Alerta a cada 10 s durante o urgente (6 + 6), silêncio nos normais", firstWin.length === 6 && secondWin.length === 6 && b.every((x) => x < 60 || (x >= u2 - 1 && x < u2 + 60) || x >= sg[8].from - 1), `bipes em ${b.join(",")} s`);
  await ctx.close();
}

// ---------- B) 2 urgentes + 2 normais ----------
{
  await setup();
  await pub("Normal 1"); await pub("Normal 2"); await pub("URGENTE A", true); await pub("URGENTE B", true);
  const ctx = await newTV(); const p = await openTV(ctx);
  const t0 = await p.evaluate(() => Date.now()); const log = [];
  await run(p, ctx, 165, t0, log);
  const sg = segments(log);
  check("B) Vários urgentes: um depois do outro (60 s cada), depois os normais, e volta", sg.map((x) => x.t).slice(0, 5).join(",") === "URGENTE A,URGENTE B,Normal 1,Normal 2,URGENTE A" && near(sg[1].dur, 60, 1), fmtSeg(sg));
  const b = await beepsAt(p, t0);
  check("B) Cada urgente apita no seu período (12 bipes em 120 s)", b.filter((x) => x < 120).length === 12, `bipes em ${b.join(",")} s`);
  await ctx.close();
}

// ---------- C) só urgente, sem normais ----------
{
  await setup();
  await pub("URGENTE SOZINHO", true);
  const ctx = await newTV(); const p = await openTV(ctx);
  const t0 = await p.evaluate(() => Date.now()); const log = [];
  await run(p, ctx, 125, t0, log);
  const b = await beepsAt(p, t0);
  check("C) Só urgente: fica direto na tela", log.every((e) => e.t === "URGENTE SOZINHO" && e.m === "mode-urgent"));
  check("C) Reinicia o tempo/alerta a cada 60 s (bipes 0..50, 60..110, 120)", b.filter((x) => x < 120).length === 12 && b.some((x) => near(x, 60)) && b.some((x) => near(x, 120)), `bipes em ${b.join(",")} s`);
  await ctx.close();
}

// ---------- D) modo fixo (comportamento antigo) ----------
{
  await setup({ mode: "fixed" });
  await pub("Normal 1"); await pub("Normal 2"); await pub("URGENTE FIXO", true);
  const ctx = await newTV(); const p = await openTV(ctx);
  const t0 = await p.evaluate(() => Date.now()); const log = [];
  await run(p, ctx, 150, t0, log);
  const b = await beepsAt(p, t0);
  check("D) Fixo: urgente ocupa a TV o tempo todo (150 s)", log.every((e) => e.t === "URGENTE FIXO"));
  check("D) Fixo: alerta repete só nos primeiros 60 s", b.length === 6 && b.every((x) => x < 60), `bipes em ${b.join(",")} s`);
  await ctx.close();
  await setup({ mode: "fixed", repeat: "0" });
  await pub("Normal 1"); await pub("URGENTE FIXO", true);
  const ctx2 = await newTV(); const p2 = await openTV(ctx2);
  const t2 = await p2.evaluate(() => Date.now()); await run(p2, ctx2, 70, t2, []);
  const b2 = await beepsAt(p2, t2);
  check("D) Fixo sem repetição: um bipe só (como antes)", b2.length === 1, `bipes em ${b2.join(",")} s`);
  await ctx2.close();
}

// ---------- E) urgente novo e urgente editado interrompem na hora ----------
{
  await setup();
  await pub("Normal 1"); await pub("Normal 2"); await pub("Normal 3");
  const ctx = await newTV(); const p = await openTV(ctx);
  const t0 = await p.evaluate(() => Date.now()); const log = [];
  await run(p, ctx, 20, t0, log);
  const before = log[log.length - 1].t;
  const u = await pub("URGENTE NOVO", true);
  await p.waitForFunction(() => document.getElementById("title").textContent === "URGENTE NOVO", null, { timeout: 5000 }).catch(() => {});
  await run(p, ctx, 2, t0, log);
  check("E) Urgente novo entra na hora (interrompe o normal)", log[log.length - 1].t === "URGENTE NOVO" && before.startsWith("Normal"), `antes: ${before}`);
  await run(p, ctx, 75, t0, log);
  const sg = segments(log);
  check("E) Depois de 60 s volta aos normais do começo do ciclo", sg.map((x) => x.t).join(",").includes("URGENTE NOVO,Normal 1,Normal 2"), fmtSeg(sg));
  const nb = (await beepsAt(p, t0)).length;
  await call("PUT", `/api/admin/announcements/${u.id}`, { title: "URGENTE EDITADO", body: "Novo texto.", priority: "urgent", allScreens: true });
  await p.waitForFunction(() => document.getElementById("title").textContent === "URGENTE EDITADO", null, { timeout: 5000 }).catch(() => {});
  await run(p, ctx, 2, t0, log);
  const nb2 = (await beepsAt(p, t0)).length;
  check("E) Urgente editado entra na hora e apita de novo", log[log.length - 1].t === "URGENTE EDITADO" && nb2 > nb, `bipes ${nb} → ${nb2}`);
  await ctx.close();
}

// ---------- F) reabrir a página no meio do urgente e no meio dos normais ----------
{
  await setup();
  await pub("Normal 1"); await pub("Normal 2"); await pub("Normal 3"); await pub("URGENTE A", true);
  const ctx = await newTV(); let p = await openTV(ctx);
  const t0 = await p.evaluate(() => Date.now()); const log = [];
  await run(p, ctx, 32, t0, log);
  const b1 = (await beepsAt(p, t0)).length;
  await p.reload({ waitUntil: "domcontentloaded" });
  await run(p, ctx, 1, t0, log);
  const b1r = (await beepsAt(p, t0)).length;
  check("F) Reabrir no meio do urgente: continua o urgente e não apita do zero", log[log.length - 1].t === "URGENTE A" && b1 === 4 && b1r === 4, `bipes antes ${b1}, logo depois ${b1r}`);
  await run(p, ctx, 50, t0, log);   // até ~83 s: Normal 2
  const sgA = segments(log);
  const uSeg = sgA.find((x) => x.t === "URGENTE A");
  const bAll = await beepsAt(p, t0);
  check("F) O urgente não reinicia: total ~60 s e 6 bipes", near(uSeg.dur, 60, 2) && bAll.filter((x) => x < 60).length === 6, `${fmtSeg(sgA)} | bipes ${bAll.join(",")}`);
  const cur = log[log.length - 1].t;
  await p.reload({ waitUntil: "domcontentloaded" });
  await run(p, ctx, 1, t0, log);
  check("F) Reabrir no meio dos normais: continua no mesmo normal (não pula para o urgente)", log[log.length - 1].t === cur && cur.startsWith("Normal"), `${cur} → ${log[log.length - 1].t}`);
  await run(p, ctx, 40, t0, log);
  const sgB = segments(log);
  check("F) E o urgente volta no fim do ciclo (não é pulado)", sgB.map((x) => x.t).join(",").endsWith("Normal 3,URGENTE A") || sgB.map((x) => x.t).join(",").includes("Normal 3,URGENTE A"), fmtSeg(sgB));
  await ctx.close();
}

// ---------- G) som desligado ----------
{
  await setup({ sound: "0" });
  await pub("Normal 1"); await pub("URGENTE MUDO", true);
  const ctx = await newTV(); const p = await openTV(ctx);
  const t0 = await p.evaluate(() => Date.now()); await run(p, ctx, 70, t0, []);
  check("G) Som desligado no painel: nenhum bipe", (await beepsAt(p, t0)).length === 0);
  await ctx.close();
}

// ---------- H) servidor envia os campos novos no estado da TV ----------
{
  await setup({ mode: "fixed", urgentSec: "45", repeat: "0" });
  const s = await (await fetch(`${BASE}/api/screen/state?screen=tv-principal`, { headers: { "X-Screen-Key": KEY } })).json();
  check("H) Estado da TV traz modo, tempo, repetição e versão", s.urgentMode === "fixed" && s.urgentSeconds === 45 && s.urgentBeepRepeat === false && !!s.version, JSON.stringify({ m: s.urgentMode, s: s.urgentSeconds, r: s.urgentBeepRepeat, v: s.version }));
}

// ---------- I) TV com a tela antiga (1.1.x) continua funcionando até recarregar ----------
if (process.env.OLD_TELA_JS) {
  const { readFileSync } = await import("node:fs");
  const oldJs = readFileSync(process.env.OLD_TELA_JS, "utf8");
  await setup();
  await pub("Normal 1"); await pub("Normal 2");
  const ctx = await newTV();
  await ctx.route("**/static/tela.js", (r) => r.fulfill({ status: 200, contentType: "text/javascript", body: oldJs }));
  const p = await openTV(ctx);
  const t0 = await p.evaluate(() => Date.now()); const log = [];
  await run(p, ctx, 32, t0, log);
  const u = await pub("URGENTE (TV ANTIGA)", true);
  await p.waitForFunction(() => document.body.className === "mode-urgent", null, { timeout: 5000 }).catch(() => {});
  await run(p, ctx, 3, t0, log);
  const okU = log[log.length - 1].m === "mode-urgent";
  await call("POST", `/api/admin/announcements/${u.id}/end`);
  await p.waitForFunction(() => document.body.className === "mode-normal", null, { timeout: 5000 }).catch(() => {});
  await run(p, ctx, 35, t0, log);
  const titles = new Set(log.filter((e) => e.m === "mode-normal").map((e) => e.t));
  check("I) TV com a tela antiga: normais rodam, urgente aparece (fixo, como antes) e some ao encerrar", okU && titles.size === 2 && log[log.length - 1].m === "mode-normal", fmtSeg(segments(log)));
  await ctx.close();
}

// ---------- J) servidor atualizado: a TV recarrega sozinha e mantém a posição ----------
if (process.env.BIN_NEXT) {
  await setup();
  await pub("Normal 1"); await pub("Normal 2"); await pub("Normal 3");
  const ctx = await newTV(); const p = await openTV(ctx);
  const t0 = await p.evaluate(() => Date.now()); const log = [];
  await run(p, ctx, 20, t0, log);
  const cur = log[log.length - 1].t;
  let navigated = 0; p.on("framenavigated", (f) => { if (f === p.mainFrame()) navigated++; });
  srv.kill("SIGTERM"); await new Promise((r) => srv.once("exit", r));
  const srv2 = spawn(process.env.BIN_NEXT, ["-port", String(PORT), "-bind", "127.0.0.1", "-data", DATA],
    { env: { ...process.env, PAINEL_ADMIN_PASSWORD: PW, PUBLIC_URL: "https://avisos.exemplo.test", TRUST_PROXY: "127.0.0.1/32" }, stdio: "ignore" });
  process.on("exit", () => { try { srv2.kill("SIGKILL"); } catch {} });
  const v2 = await (async () => { for (let i = 0; i < 50; i++) { try { const r = await fetch(BASE + "/healthz"); if (r.ok) return (await r.json()).version; } catch {} await sleep(100); } })();
  for (let i = 0; i < 40 && !navigated; i++) { await ctx.clock.runFor(1000); await sleep(150); }
  await p.waitForFunction(() => document.getElementById("title").textContent, null, { timeout: 10000 }).catch(() => {});
  await run(p, ctx, 1, t0, log);
  check("J) Servidor atualizado (" + v2 + "): a TV recarrega sozinha e continua no mesmo ponto", navigated > 0 && log[log.length - 1].t.startsWith("Normal"), `recargas: ${navigated}; antes: ${cur}, depois: ${log[log.length - 1].t}`);
  await ctx.close();
  srv2.kill();
}

check("Nenhum erro de JavaScript na TV", pageErrors.length === 0, pageErrors.join(" | "));
await browser.close();
srv.kill();
console.log(`\nRESULTADO: ${results.filter(Boolean).length}/${results.length} verificações OK`);
process.exit(results.every(Boolean) ? 0 : 1);
