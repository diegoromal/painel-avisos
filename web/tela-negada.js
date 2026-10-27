// Chave inválida: apaga estado e cache offline desta tela para não exibir conteúdo antigo.
(() => {
  const screen = (new URLSearchParams(location.search).get("screen") || "").toLowerCase();
  try { if (screen) localStorage.removeItem("painel:state:" + screen); } catch (e) {}
  if (window.caches) caches.keys().then(ks => ks.forEach(k => k.startsWith("painel-tela") && caches.delete(k))).catch(() => {});
})();
