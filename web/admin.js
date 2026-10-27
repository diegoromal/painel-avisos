// Painel de Avisos — administração (líder/admin)
(() => {
  "use strict";
  const $ = (s, el = document) => el.querySelector(s);
  const $$ = (s, el = document) => [...el.querySelectorAll(s)];
  const esc = (s) => String(s ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  let me = null, info = {}, screens = [], view = "active", annCache = {};
  const tvUrl = (name, key) => `${location.origin}/tela?screen=${encodeURIComponent(name)}&key=${encodeURIComponent(key)}`;
  function showKey(name, key) {
    const box = $("#key-new"); box.hidden = false;
    box.innerHTML = `<div class="keybox"><b>Endereço da TV "${esc(name)}". Copie agora: ele não será exibido novamente.</b>
      <code id="key-url">${esc(tvUrl(name, key))}</code>Somente a chave (para o instalador da TV):<code id="key-only">${esc(key)}</code>
      <button class="btn sm" type="button" id="copy-url">Copiar endereço</button> <button class="btn sm ghost" type="button" id="close-key">Fechar</button></div>`;
    $("#copy-url").onclick = () => copyText(tvUrl(name, key));
    $("#close-key").onclick = () => { box.hidden = true; box.innerHTML = ""; };
  }
  async function copyText(t) {
    try { await navigator.clipboard.writeText(t); toast("Copiado"); }
    catch (e) { prompt("Copie o texto:", t); }
  }

  async function api(method, url, body) {
    const opt = { method, headers: { "X-Requested-With": "painel" } };
    if (body instanceof FormData) opt.body = body;
    else if (body !== undefined) { opt.headers["Content-Type"] = "application/json"; opt.body = JSON.stringify(body); }
    const r = await fetch(url, opt);
    let data = null;
    try { data = await r.json(); } catch (e) {}
    if (r.status === 401 && url !== "/api/login") { showLogin(); throw new Error("Sua sessão expirou. Entre novamente."); }
    if (!r.ok) throw new Error((data && data.error) || friendlyStatus(r.status));
    return data;
  }
  function friendlyStatus(code) {
    if (code === 403) return "Você não tem permissão para esta ação.";
    if (code === 429) return "Muitas tentativas. Aguarde alguns minutos.";
    if (code >= 500) return "O servidor não conseguiu concluir a operação. Tente novamente em instantes.";
    return "Não foi possível concluir a operação (código " + code + ").";
  }
  function toast(msg) {
    const t = $("#toast"); t.textContent = msg; t.hidden = false;
    clearTimeout(toast.t); toast.t = setTimeout(() => t.hidden = true, 3000);
  }
  const fmt = (ts) => ts ? new Date(ts * 1000).toLocaleString("pt-BR", { day: "2-digit", month: "2-digit", year: "2-digit", hour: "2-digit", minute: "2-digit" }) : "—";
  function ago(ts) {
    if (!ts) return "nunca";
    const s = Math.round(Date.now() / 1000 - ts);
    if (s < 60) return `há ${Math.max(s, 0)} s`;
    if (s < 3600) return `há ${Math.round(s / 60)} min`;
    if (s < 86400) return `há ${Math.round(s / 3600)} h`;
    return fmt(ts);
  }
  // datetime-local <-> unix (hora local do navegador)
  const toLocalInput = (ts) => { if (!ts) return ""; const d = new Date(ts * 1000); d.setMinutes(d.getMinutes() - d.getTimezoneOffset()); return d.toISOString().slice(0, 16); };
  const fromLocalInput = (v) => v ? Math.floor(new Date(v).getTime() / 1000) : 0;

  // ---------- login ----------
  function showLogin() { $("#app").hidden = true; $("#login").hidden = false; }
  $("#login-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = e.target; $("#login-error").textContent = "";
    try {
      const r = await fetch("/api/login", { method: "POST", headers: { "Content-Type": "application/json", "X-Requested-With": "painel" },
        body: JSON.stringify({ username: f.username.value, password: f.password.value, code: f.code.value }) });
      const d = await r.json().catch(() => ({}));
      if (!r.ok) {
        if (d.totpRequired) { $("#code-row").hidden = false; f.code.focus(); }
        throw new Error(d.error || friendlyStatus(r.status));
      }
      f.password.value = ""; f.code.value = ""; $("#code-row").hidden = true; boot();
    } catch (err) { $("#login-error").textContent = err.message; }
  });
  $("#logout").onclick = async () => { await api("POST", "/api/logout").catch(() => {}); showLogin(); };

  async function boot() {
    try { me = await (await fetch("/api/me")).json(); if (!me || !me.id) return showLogin(); }
    catch (e) { return showLogin(); }
    $("#login").hidden = true; $("#app").hidden = false;
    $("#who").textContent = (me.name || me.username) + (me.role === "admin" ? " · admin" : " · líder");
    $$("[data-admin]").forEach(el => el.hidden = me.role !== "admin");
    info = await api("GET", "/api/admin/info").catch(() => ({}));
    $("#app-foot").textContent = info.version ? "Painel de Avisos · v" + info.version : "";
    renderTotp();
    await loadScreens(); loadAnn(); loadCounts();
  }

  // ---------- abas ----------
  $("#tabs").addEventListener("click", (e) => {
    const a = e.target.closest("a[data-tab]"); if (!a) return;
    $$("#tabs a").forEach(x => x.classList.toggle("on", x === a));
    $$("[data-pane]").forEach(p => p.hidden = p.dataset.pane !== a.dataset.tab);
    ({ telas: loadScreens, usuarios: loadUsers, config: loadSettings, api: loadTokens, avisos: loadAnn })[a.dataset.tab]?.();
  });
  $("#views").addEventListener("click", (e) => {
    const a = e.target.closest("a[data-view]"); if (!a) return;
    view = a.dataset.view; $$("#views a").forEach(x => x.classList.toggle("on", x === a)); loadAnn();
  });

  // ---------- avisos ----------
  async function loadCounts() {
    for (const v of ["active", "scheduled"]) {
      const l = await api("GET", "/api/admin/announcements?view=" + v).catch(() => []);
      $("#c-" + v).textContent = l.length || "";
    }
  }
  async function loadAnn() {
    const list = await api("GET", "/api/admin/announcements?view=" + view);
    annCache = Object.fromEntries(list.map(a => [a.id, a]));
    const box = $("#ann-list");
    if (!list.length) { box.innerHTML = `<div class="empty">${{ active: "Nenhum aviso no ar.", scheduled: "Nenhum aviso agendado.", history: "Histórico vazio." }[view]}</div>`; return; }
    box.innerHTML = list.map(a => {
      const telas = a.allScreens ? "todas as telas" : a.screens.join(", ");
      const per = `${fmt(a.startsAt)} → ${a.endsAt ? fmt(a.endsAt) : "até encerrar"}`;
      const live = a.status === "ativo" || a.status === "agendado";
      return `<div class="item ${a.priority}">
        <div><h4><span class="tag ${a.priority}">${a.priority === "urgent" ? "URGENTE" : "NORMAL"}</span><span class="tag ${a.status}">${a.status.toUpperCase()}</span>${esc(a.title)}</h4>
          <div class="snip">${a.html}</div></div>
        <div class="btns">${live ? `<button class="btn sm" data-edit="${a.id}">Editar</button><button class="btn sm danger" data-end="${a.id}">Encerrar</button>` : ""}<button class="btn sm ghost" data-dup="${a.id}">Duplicar</button></div>
        <div class="info"><span>📺 ${esc(telas)}</span><span>🕒 ${per}</span><span>✍️ ${esc(a.author)} em ${fmt(a.createdAt)}</span>
          ${a.updatedBy ? `<span>✏️ editado por ${esc(a.updatedBy)} em ${fmt(a.updatedAt)}</span>` : ""}
          ${a.endedAt ? `<span>⏹ encerrado por ${esc(a.endedBy)} em ${fmt(a.endedAt)}</span>` : ""}
          ${a.image ? `<span>🖼 com imagem</span>` : ""}</div></div>`;
    }).join("");
  }
  $("#ann-list").addEventListener("click", async (e) => {
    const b = e.target.closest("button"); if (!b) return;
    if (b.dataset.end) {
      if (!confirm("Encerrar este aviso? Ele sai das telas imediatamente.")) return;
      await api("POST", `/api/admin/announcements/${b.dataset.end}/end`).then(() => toast("Aviso encerrado")).catch(err => alert(err.message));
      loadAnn(); loadCounts();
    } else if (b.dataset.edit) fillForm(annCache[b.dataset.edit], true);
    else if (b.dataset.dup) fillForm(annCache[b.dataset.dup], false);
  });

  const form = $("#ann-form");
  function resetForm() {
    form.reset(); form.id.value = ""; form.image.value = ""; setImg("");
    $("#form-title").textContent = "Novo comunicado"; $("#submit-btn").textContent = "Publicar agora";
    $("#cancel-edit").hidden = true; $("#form-error").textContent = ""; syncScreens();
  }
  function fillForm(a, edit) {
    form.id.value = edit ? a.id : "";
    form.title.value = a.title; form.body.value = a.body;
    form.querySelector(`[name=priority][value=${a.priority}]`).checked = true;
    form.startsAt.value = edit ? toLocalInput(a.startsAt) : "";
    form.endsAt.value = edit ? toLocalInput(a.endsAt) : "";
    form.allScreens.checked = a.allScreens;
    $$("#screen-checks input").forEach(c => c.checked = a.screens.includes(c.value));
    form.image.value = a.image || ""; setImg(a.image || "");
    $("#form-title").textContent = edit ? `Editando aviso #${a.id}` : "Novo comunicado (cópia)";
    $("#submit-btn").textContent = edit ? "Salvar alterações" : "Publicar";
    $("#cancel-edit").hidden = !edit; syncScreens(); form.title.focus();
    window.scrollTo({ top: 0, behavior: "smooth" });
  }
  $("#cancel-edit").onclick = resetForm;
  function syncScreens() { $("#screen-checks").hidden = form.allScreens.checked; }
  form.allScreens.addEventListener("change", syncScreens);

  // formatação
  $$(".toolbar [data-fmt]").forEach(b => b.onclick = () => {
    const ta = form.body, m = b.dataset.fmt, s = ta.selectionStart, e = ta.selectionEnd, v = ta.value;
    if (m === "- ") {
      const ls = v.lastIndexOf("\n", s - 1) + 1;
      ta.value = v.slice(0, ls) + "- " + v.slice(ls); ta.selectionStart = ta.selectionEnd = e + 2;
    } else {
      const sel = v.slice(s, e) || "texto";
      ta.value = v.slice(0, s) + m + sel + m + v.slice(e);
      ta.selectionStart = s + m.length; ta.selectionEnd = s + m.length + sel.length;
    }
    ta.focus();
  });
  // duração rápida
  $$(".quick [data-dur]").forEach(b => b.onclick = () => {
    const start = fromLocalInput(form.startsAt.value) || Math.floor(Date.now() / 1000);
    const d = b.dataset.dur;
    if (d === "none") { form.endsAt.value = ""; return; }
    let end;
    if (d === "eod") { const x = new Date(start * 1000); x.setHours(23, 59, 0, 0); end = x.getTime() / 1000; }
    else end = start + Number(d) * 60;
    form.endsAt.value = toLocalInput(end);
  });
  // imagem
  function setImg(p) { const i = $("#img-preview"); i.hidden = !p; if (p) i.src = p; $("#img-clear").hidden = !p; if (!p) $("#img-file").value = ""; }
  $("#img-clear").onclick = () => { form.image.value = ""; setImg(""); };
  $("#img-file").onchange = async (e) => {
    const f = e.target.files[0]; if (!f) return;
    const fd = new FormData(); fd.append("file", f);
    try { const r = await api("POST", "/api/admin/uploads", fd); form.image.value = r.path; setImg(r.path); }
    catch (err) { $("#form-error").textContent = err.message; e.target.value = ""; }
  };

  form.addEventListener("submit", async (e) => {
    e.preventDefault(); $("#form-error").textContent = "";
    const all = form.allScreens.checked;
    const body = {
      title: form.title.value, body: form.body.value,
      priority: form.querySelector("[name=priority]:checked").value,
      startsAt: fromLocalInput(form.startsAt.value),
      endsAt: form.endsAt.value ? fromLocalInput(form.endsAt.value) : null,
      allScreens: all, screens: all ? [] : $$("#screen-checks input:checked").map(c => c.value),
      image: form.image.value,
    };
    if (body.priority === "urgent" && !form.id.value && !confirm("Publicar como URGENTE? Ele vai tomar a tela inteira e tocar alerta sonoro.")) return;
    try {
      const id = form.id.value;
      const a = id ? await api("PUT", "/api/admin/announcements/" + id, body) : await api("POST", "/api/admin/announcements", body);
      toast(id ? "Aviso atualizado" : (a.status === "agendado" ? "Aviso agendado para " + fmt(a.startsAt) : "Aviso publicado nas telas"));
      resetForm(); loadAnn(); loadCounts();
    } catch (err) { $("#form-error").textContent = err.message; }
  });

  // ---------- telas ----------
  async function loadScreens() {
    screens = await api("GET", "/api/admin/screens");
    const checked = new Set($$("#screen-checks input:checked").map(c => c.value));
    $("#screen-checks").innerHTML = screens.length ? screens.map(s => `<label class="radio"><input type="checkbox" value="${esc(s.name)}" ${checked.has(s.name) ? "checked" : ""}> ${esc(s.label || s.name)} <span class="dot ${s.online ? "on" : "off"}"></span></label>`).join("") : `<span class="muted small">Nenhuma tela cadastrada ainda.</span>`;
    const adm = me.role === "admin";
    $("#screens-table").innerHTML = `<tr><th>Status</th><th>Tela</th><th>Identificação</th><th>Último sinal</th><th>Chave de acesso</th>${adm ? "<th></th>" : ""}</tr>` +
      (screens.map(s => `<tr><td title="${esc(s.lastIp ? "Último endereço: " + s.lastIp : "")}"><span class="dot ${s.online ? "on" : "off"}"></span>${s.online ? "Online" : "Offline"}</td>
        <td>${esc(s.label)}</td><td><code>${esc(s.name)}</code></td><td title="${fmt(s.lastSeen)}">${ago(s.lastSeen)}</td>
        <td>${s.hasKey ? `<span class="key-ok" title="Início da chave: ${esc(s.keyPrefix)}…">🔑 Configurada</span> <span class="muted small">desde ${fmt(s.keyCreatedAt)}</span>` : `<span class="key-none">Sem chave</span>`}</td>
        ${adm ? `<td><button class="btn sm" data-newkey="${s.id}">${s.hasKey ? "Nova chave" : "Gerar chave"}</button> ${s.hasKey ? `<button class="btn sm ghost" data-revoke="${s.id}">Revogar chave</button>` : ""}
          <button class="btn sm" data-rename="${s.id}">Renomear</button> <button class="btn sm danger" data-del="${s.id}">Remover</button></td>` : ""}</tr>`).join("") ||
        `<tr><td colspan="6" class="empty">Nenhuma tela cadastrada. Adicione a primeira abaixo.</td></tr>`);
  }
  $("#screens-table").addEventListener("click", async (e) => {
    const b = e.target.closest("button"); if (!b) return;
    const s = screens.find(x => x.id == (b.dataset.rename || b.dataset.del || b.dataset.newkey || b.dataset.revoke));
    if (b.dataset.newkey) {
      if (s.hasKey && !confirm(`Gerar nova chave para "${s.label || s.name}"? A chave atual para de funcionar na hora e a TV precisa ser reconfigurada com o novo endereço.`)) return;
      const r = await api("POST", `/api/admin/screens/${s.id}/key`); showKey(r.screen, r.key);
    }
    if (b.dataset.revoke && confirm(`Revogar a chave de "${s.label || s.name}"? A TV deixa de receber avisos.`)) {
      const r = await api("DELETE", `/api/admin/screens/${s.id}/key`); toast(r.note);
    }
    if (b.dataset.rename) { const l = prompt("Nome exibido da tela:", s.label); if (l != null) await api("PUT", "/api/admin/screens/" + s.id, { label: l }); }
    if (b.dataset.del && confirm(`Remover a tela "${s.label || s.name}"? Ela deixa de receber avisos.`)) await api("DELETE", "/api/admin/screens/" + s.id);
    loadScreens();
  });
  $("#screen-form").addEventListener("submit", async (e) => {
    e.preventDefault(); $("#screen-error").textContent = "";
    try { const r = await api("POST", "/api/admin/screens", { name: e.target.name.value, label: e.target.label.value }); e.target.reset(); showKey(r.screen, r.key); loadScreens(); }
    catch (err) { $("#screen-error").textContent = err.message; }
  });
  setInterval(() => { if (!$("#app").hidden) { loadScreens().catch(() => {}); if (view !== "history") loadAnn().catch(() => {}); loadCounts(); } }, 15000);

  // ---------- usuários ----------
  async function loadUsers() {
    const users = await api("GET", "/api/admin/users");
    $("#users-table").innerHTML = `<tr><th>Usuário</th><th>Nome</th><th>Perfil</th><th>Status</th><th>2FA</th><th>Criado</th><th></th></tr>` + users.map(u => `<tr>
      <td><code>${esc(u.username)}</code></td><td>${esc(u.name)}</td><td>${u.role === "admin" ? "Admin" : "Líder"}</td>
      <td>${u.active ? "Ativo" : "<span class='muted'>Desativado</span>"}</td><td>${u.totp ? "<span class='tag ativo'>ATIVO</span>" : "<span class='muted'>—</span>"}</td><td>${fmt(u.createdAt)}</td>
      <td><button class="btn sm" data-pw="${u.id}">Nova senha</button> ${u.totp ? `<button class="btn sm ghost" data-totp="${u.id}">Resetar 2FA</button>` : ""} ${u.id !== me.id ? `<button class="btn sm ${u.active ? "danger" : ""}" data-toggle="${u.id}" data-active="${u.active}">${u.active ? "Desativar" : "Reativar"}</button>
      <button class="btn sm ghost" data-role="${u.id}" data-r="${u.role}">${u.role === "admin" ? "Tornar líder" : "Tornar admin"}</button>` : ""}</td></tr>`).join("");
  }
  $("#users-table").addEventListener("click", async (e) => {
    const b = e.target.closest("button"); if (!b) return;
    try {
      if (b.dataset.pw) { const p = prompt("Nova senha (mín. 8 caracteres):"); if (p) await api("PUT", "/api/admin/users/" + b.dataset.pw, { password: p }); }
      if (b.dataset.totp && confirm("Remover o 2FA deste usuário? Ele poderá entrar só com a senha até ativar de novo.")) await api("PUT", "/api/admin/users/" + b.dataset.totp, { resetTotp: true });
      if (b.dataset.toggle) await api("PUT", "/api/admin/users/" + b.dataset.toggle, { active: b.dataset.active !== "true" });
      if (b.dataset.role) await api("PUT", "/api/admin/users/" + b.dataset.role, { role: b.dataset.r === "admin" ? "leader" : "admin" });
      toast("Usuário atualizado");
    } catch (err) { alert(err.message); }
    loadUsers();
  });
  $("#user-form").addEventListener("submit", async (e) => {
    e.preventDefault(); const f = e.target; $("#user-error").textContent = "";
    try { await api("POST", "/api/admin/users", { username: f.username.value, name: f.name.value, password: f.password.value, role: f.role.value }); f.reset(); toast("Usuário criado"); loadUsers(); }
    catch (err) { $("#user-error").textContent = err.message; }
  });

  // ---------- configurações ----------
  const sf = $("#settings-form");
  function setLogo(p) { const i = $("#logo-preview"); i.hidden = !p; if (p) i.src = p; $("#logo-clear").hidden = !p; }
  async function loadSettings() {
    const s = await api("GET", "/api/admin/settings");
    for (const k of ["company_name", "rotate_seconds", "offline_minutes", "webhook_url", "logo"]) sf[k].value = s[k] || "";
    sf.sound_enabled.checked = s.sound_enabled === "1"; setLogo(s.logo);
    sf.urgent_mode.value = s.urgent_mode === "fixed" ? "fixed" : "interleave";
    sf.urgent_seconds.value = s.urgent_seconds || "60";
    sf.urgent_beep_repeat.checked = s.urgent_beep_repeat !== "0";
    urgentHint();
    renderTech();
  }
  // explicação em linguagem simples, de acordo com as opções escolhidas
  function urgentHint() {
    const t = Number(sf.urgent_seconds.value) || 60, r = Number(sf.rotate_seconds.value) || 15;
    $("#urgent-hint").textContent = sf.urgent_mode.value === "fixed"
      ? `O aviso urgente ocupa a TV até sair do ar; os avisos normais só voltam depois. O alerta repete apenas nos primeiros ${t} segundos.`
      : `Cada aviso urgente fica ${t} segundos na TV. Depois passam os avisos normais uma vez (${r} segundos cada) e o urgente volta. Um urgente novo aparece na hora.`;
    sf.urgent_beep_repeat.disabled = !sf.sound_enabled.checked;
  }
  for (const n of ["urgent_mode", "urgent_seconds", "rotate_seconds", "sound_enabled"]) sf[n].addEventListener("input", urgentHint);
  function renderTech() {
    const box = $("#tech-details");
    const sv = info && info.showServerInfo && info.server;
    box.hidden = !sv || me.role !== "admin";
    if (box.hidden) { $("#tech-info").innerHTML = ""; return; }
    const yn = (v, y, n) => v ? y : n;
    const rows = [
      ["Versão", info.version],
      ["Modo", yn(sv.publicHttps, "público (HTTPS)", "rede local")],
      ["URL pública", sv.publicUrl || "—"],
      ["Telas sem chave", yn(sv.allowKeyless, "aceitas", "bloqueadas")],
      ["Aviso automático para IPs privados", yn(sv.webhookAllowPrivate, "permitido", "bloqueado")],
      ["Proxies confiáveis", (sv.trustProxy || []).join(", ") || "nenhum"],
      ["Seu IP visto pelo servidor", (sv.clientIp || "—") + (sv.https ? " (HTTPS)" : "")],
    ];
    $("#tech-info").innerHTML = "<dl>" + rows.map(([k, v]) => `<dt>${esc(k)}</dt><dd>${esc(v)}</dd>`).join("") + "</dl>" +
      `<p class="small">Definidos no servidor (variáveis de ambiente). Para ocultar esta seção: SHOW_SERVER_INFO=0.</p>`;
  }
  $("#logo-file").onchange = async (e) => {
    const f = e.target.files[0]; if (!f) return;
    const fd = new FormData(); fd.append("file", f);
    try { const r = await api("POST", "/api/admin/uploads", fd); sf.logo.value = r.path; setLogo(r.path); } catch (err) { $("#settings-error").textContent = err.message; }
  };
  $("#logo-clear").onclick = () => { sf.logo.value = ""; setLogo(""); $("#logo-file").value = ""; };
  sf.addEventListener("submit", async (e) => {
    e.preventDefault(); $("#settings-error").textContent = ""; $("#settings-ok").textContent = "";
    const body = { sound_enabled: sf.sound_enabled.checked ? "1" : "0", urgent_beep_repeat: sf.urgent_beep_repeat.checked ? "1" : "0" };
    for (const k of ["company_name", "rotate_seconds", "offline_minutes", "webhook_url", "logo", "urgent_mode", "urgent_seconds"]) body[k] = sf[k].value;
    try { await api("PUT", "/api/admin/settings", body); $("#settings-ok").textContent = "Salvo ✓"; } catch (err) { $("#settings-error").textContent = err.message; }
  });
  $("#webhook-test").onclick = async () => {
    try { const r = await api("POST", "/api/admin/webhook-test"); toast(r.ok ? "Teste enviado com sucesso" : "O endereço recebeu o teste, mas respondeu com erro (código " + r.status + ")"); } catch (err) { $("#settings-error").textContent = err.message; }
  };

  // ---------- tokens ----------
  async function loadTokens() {
    const l = await api("GET", "/api/admin/tokens");
    $("#tokens-table").innerHTML = `<tr><th>Integração</th><th>Código (início)</th><th>Criado por</th><th>Criado</th><th>Último uso</th><th></th></tr>` +
      (l.map(t => `<tr><td>${esc(t.name)}</td><td><code>${esc(t.prefix)}…</code></td><td>${esc(t.createdBy)}</td><td>${fmt(t.createdAt)}</td><td>${t.lastUsed ? fmt(t.lastUsed) : "nunca"}</td>
        <td><button class="btn sm danger" data-revoke="${t.id}">Revogar</button></td></tr>`).join("") || `<tr><td colspan="6" class="empty">Nenhuma integração cadastrada.</td></tr>`);
    $("#api-example").textContent = `curl -X POST ${location.origin}/api/v1/announcements \\
  -H "Authorization: Bearer SEU_CODIGO" -H "Content-Type: application/json" \\
  -d '{"title":"Servidor fora do ar","body":"Equipe de TI já está atuando.","priority":"urgent","durationMinutes":30}'`;
  }
  $("#tokens-table").addEventListener("click", async (e) => {
    const b = e.target.closest("[data-revoke]"); if (!b || !confirm("Revogar este código de acesso? O sistema que o usa deixa de publicar avisos.")) return;
    await api("DELETE", "/api/admin/tokens/" + b.dataset.revoke); loadTokens();
  });
  $("#token-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    try {
      const r = await api("POST", "/api/admin/tokens", { name: e.target.name.value }); e.target.reset();
      const box = $("#token-new"); box.hidden = false;
      box.innerHTML = `<b>Código de acesso. Copie agora: ele não será exibido novamente.</b><br><code>${esc(r.token)}</code>`;
      loadTokens();
    } catch (err) { alert(err.message); }
  });

  // ---------- minha conta ----------
  $("#pw-form").addEventListener("submit", async (e) => {
    e.preventDefault(); const f = e.target; $("#pw-error").textContent = ""; $("#pw-ok").textContent = "";
    try { await api("POST", "/api/admin/me/password", { current: f.current.value, new: f.new.value }); f.reset(); $("#pw-ok").textContent = "Senha alterada ✓"; }
    catch (err) { $("#pw-error").textContent = err.message; }
  });

  // ---------- 2FA ----------
  function renderTotp() { $("#totp-on").hidden = !me.totp; $("#totp-off").hidden = !!me.totp; $("#totp-setup").hidden = true; $("#totp-error").textContent = ""; }
  $("#totp-start").onclick = async () => {
    try {
      const r = await api("POST", "/api/admin/me/totp/setup");
      $("#totp-qr").src = r.qr; $("#totp-secret").textContent = r.secret; $("#totp-setup").hidden = false;
      $("#totp-enable-form").code.focus();
    } catch (err) { $("#totp-error").textContent = err.message; }
  };
  $("#totp-enable-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    try { await api("POST", "/api/admin/me/totp/enable", { code: e.target.code.value }); e.target.reset(); me.totp = true; renderTotp(); toast("2FA ativado"); }
    catch (err) { $("#totp-error").textContent = err.message; }
  });
  $("#totp-disable-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    try { await api("POST", "/api/admin/me/totp/disable", { password: e.target.password.value, code: e.target.code.value }); e.target.reset(); me.totp = false; renderTotp(); toast("2FA desativado"); }
    catch (err) { $("#totp-error").textContent = err.message; }
  });

  boot();
})();
