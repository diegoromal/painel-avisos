// Service worker da tela: permite abrir /tela e exibir o último aviso mesmo sem rede
// (ex.: TV reiniciou e a internet ainda não voltou). Só atua em /tela, /static/tela* e /uploads.
// A chave continua valendo: a página só é guardada depois que o servidor a entregou com
// chave válida (HTTP 200); se o servidor responder 401/403, o cache é apagado.
const CACHE = "painel-tela-v5";
const SHELL = ["/static/tela.css", "/static/tela.js"];

self.addEventListener("install", (e) => {
  e.waitUntil(caches.open(CACHE).then((c) => c.addAll(SHELL)).then(() => self.skipWaiting()));
});
self.addEventListener("activate", (e) => {
  e.waitUntil(caches.keys().then((ks) => Promise.all(ks.filter((k) => k.startsWith("painel-tela") && k !== CACHE).map((k) => caches.delete(k))))
    .then(() => self.clients.claim()));
});

function timeout(ms) { return new Promise((_, rej) => setTimeout(() => rej(new Error("timeout")), ms)); }

async function networkFirst(req, ms) {
  const cache = await caches.open(CACHE);
  try {
    const res = await Promise.race([fetch(req, { cache: "no-store" }), timeout(ms)]);
    if (res.status === 401 || res.status === 403) {
      // chave revogada/inválida: não pode sobrar nada desta tela no cache
      await caches.delete(CACHE);
      return res;
    }
    if (res.ok) await cache.put(req, res.clone());
    return res;
  } catch (err) {
    const hit = await cache.match(req);
    if (hit) return hit;
    throw err;
  }
}

async function cacheFirst(req) {
  const cache = await caches.open(CACHE);
  const hit = await cache.match(req);
  if (hit) return hit;
  const res = await fetch(req);
  if (res.ok) await cache.put(req, res.clone());
  return res;
}

self.addEventListener("fetch", (e) => {
  const url = new URL(e.request.url);
  if (e.request.method !== "GET" || url.origin !== self.location.origin) return;
  if (url.pathname === "/tela") return e.respondWith(networkFirst(e.request, 5000));
  if (url.pathname.startsWith("/static/tela")) return e.respondWith(networkFirst(e.request, 3000));
  if (url.pathname.startsWith("/uploads/")) return e.respondWith(cacheFirst(e.request)); // nomes aleatórios = imutáveis
  // /admin, /api/* (inclusive SSE) passam direto pela rede
});
