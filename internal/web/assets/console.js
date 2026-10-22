const $ = (s) => document.querySelector(s);
const screen = $("#screen");
const errorBox = $("#connection-error");
const feedback = $("#feedback");
const names = {
  "user:ines": "Inés Roldán",
  "user:mateo": "Mateo Vidal",
  "agent:diagnostico": "Diagnóstico",
};
const titles = {
  "analytics-read": "Consultar analítica",
  "payments-read": "Revisar pagos",
  "logs-investigate": "Investigar logs",
};
const states = {
  requested: "Solicitada",
  pending_approval: "Por aprobar",
  approved: "Aprobada",
  granting: "Concediendo",
  active: "Activa",
  revoking: "Revocación pendiente",
  revoked: "Revocada",
  denied: "Denegada",
};
const events = {
  created: "Solicitud registrada",
  needs_approval: "Enviada a aprobación",
  approve: "Aprobación registrada",
  deny: "Solicitud denegada",
  start_grant: "Intento de concesión guardado",
  granted: "Acceso concedido",
  fail: "Resultado desconocido; limpieza requerida",
  expire: "Ventana vencida",
  revoke: "Revocación solicitada",
  revoked: "Ausencia confirmada en el destino",
};
let data,
  head,
  inFlight = false,
  busy = false,
  filter = "all";
let view = ["requests", "catalog", "evidence"].includes(location.hash.slice(1))
  ? location.hash.slice(1)
  : "requests";
let serverOffset = 0;
const esc = (value) =>
  String(value ?? "").replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
const actor = () => $("#actor").value;
const person = (id) => names[id] || id;
const now = () => Date.now() + serverOffset;
const date = (value) =>
  new Intl.DateTimeFormat("es-AR", {
    dateStyle: "short",
    timeStyle: "medium",
  }).format(new Date(value));
const remaining = (g) => {
  if (g.state !== "active")
    return g.state === "revoked" || g.state === "denied"
      ? "Finalizado"
      : date(g.expires_at);
  const secs = Math.ceil((Date.parse(g.expires_at) - now()) / 1000);
  if (secs <= 0) return "Vencido; pendiente";
  return `${String(Math.floor(secs / 60)).padStart(2, "0")}:${String(secs % 60).padStart(2, "0")}`;
};
const empty = (title, message, button = "") =>
  `<div class="empty"><h2>${title}</h2><p>${message}</p>${button}</div>`;
const requestButton = () =>
  `<button class="primary" data-action="request">Solicitar acceso <span aria-hidden="true">＋</span></button>`;
function policy(id) {
  return data.policy.find((e) => e.id === id);
}
function allowedRequests() {
  return data.policy.filter((e) => e.requesters.includes(actor()));
}
function canApprove(g) {
  return (
    policy(g.entitlement_id)?.approvers.includes(actor()) &&
    actor() !== g.key.Principal
  );
}
function canRevoke(g) {
  return (
    actor() === g.key.Principal ||
    policy(g.entitlement_id)?.approvers.includes(actor())
  );
}
function heading(title, description, action = "") {
  return `<div class="page-heading"><div><p class="muted">Litoral / Acceso</p><h1 tabindex="-1">${title}</h1>${description ? `<p class="description">${description}</p>` : ""}</div>${action}</div>`;
}
function rowActions(g) {
  let result = "";
  if (["requested", "pending_approval"].includes(g.state) && canApprove(g))
    result += `<button class="primary" data-action="approve" data-id="${esc(g.id)}">Aprobar</button><button class="quiet" data-action="deny" data-id="${esc(g.id)}">Denegar</button>`;
  if (
    ["active", "granting", "revoking", "approved"].includes(g.state) &&
    canRevoke(g)
  )
    result += `<button class="quiet" data-action="revoke" data-id="${esc(g.id)}">${g.state === "revoking" ? "Reintentar" : "Revocar"}</button>`;
  return result || '<span class="muted">Sin acciones</span>';
}
function requests() {
  let list = [...data.snapshot.grants].sort((a, b) =>
    b.created_at.localeCompare(a.created_at),
  );
  if (filter === "pending")
    list = list.filter((g) => g.state === "pending_approval");
  if (filter === "active") list = list.filter((g) => g.state === "active");
  if (filter === "mine") list = list.filter((g) => g.key.Principal === actor());
  const all = data.snapshot.grants;
  return (
    heading(
      "El acceso, con principio y fin.",
      "Ventanas acotadas. Aprobaciones separadas. La salida también se verifica.",
      allowedRequests().length ? requestButton() : "",
    ) +
    `<div class="metrics"><div class="metric"><span>Accesos activos</span><strong id="active-count">${all.filter((g) => g.state === "active" && Date.parse(g.expires_at) > now()).length}</strong></div><div class="metric"><span>Por aprobar</span><strong>${all.filter((g) => g.state === "pending_approval").length}</strong></div><div class="metric"><span>Por revocar</span><strong id="revoke-count">${all.filter((g) => g.state === "revoking" || (g.state === "active" && Date.parse(g.expires_at) <= now())).length}</strong></div></div>` +
    `<div class="toolbar"><label for="filter">Mostrar</label><select id="filter"><option value="all">Todas las solicitudes</option><option value="pending">Por aprobar</option><option value="active">Activas</option><option value="mine">Mis solicitudes</option></select><span class="muted">${esc(person(actor()))} / Rol simulado</span></div>` +
    (list.length
      ? `<div class="table-shell"><table><thead><tr><th scope="col">Persona o agente</th><th scope="col">Permiso</th><th scope="col">Estado</th><th scope="col">Ventana</th><th scope="col">Acciones</th></tr></thead><tbody>${list.map((g) => `<tr data-grant="${esc(g.id)}"><td><div class="person-cell"><span class="avatar" aria-hidden="true">${g.key.Principal.startsWith("agent:") ? "AG" : "IR"}</span><span class="person">${esc(person(g.key.Principal))}<small>${g.key.Principal.startsWith("agent:") ? "Agente de diagnóstico" : "Equipo de operaciones"}</small></span></div></td><td><span class="mobile-label">Permiso</span><button class="link-button" data-action="detail" data-id="${esc(g.id)}">${esc(titles[g.entitlement_id] || g.entitlement_id)}</button><small>${esc(g.key.Target)} / ${esc(g.key.Role)}</small></td><td><span class="mobile-label">Estado</span><span class="status ${g.state === "active" ? "active" : ""}">${esc(states[g.state] || g.state)}</span></td><td><span class="mobile-label">Ventana</span><span class="time-left" data-expiry="${esc(g.id)}" title="${esc(date(g.expires_at))}">${esc(remaining(g))}</span></td><td><div class="row-actions">${rowActions(g)}</div></td></tr>`).join("")}</tbody></table></div>`
      : empty(
          "No hay solicitudes en esta vista.",
          "Podés cambiar el filtro o pedir un permiso del catálogo.",
          allowedRequests().length ? requestButton() : "",
        )) +
    `<div class="table-footer"><span>${list.length} solicitudes en esta vista</span><span>Destino simulado persistente / worker cada 1 s</span></div>`
  );
}
function catalog() {
  return (
    heading(
      "Sólo el acceso que hace falta.",
      "Cada permiso define un destino, un alcance y su duración máxima.",
    ) +
    `<div class="catalog">${data.policy.map((e) => `<article class="entitlement"><div><h2>${esc(titles[e.id] || e.id)}</h2><p>${esc(e.target)} / ${esc(e.role)}</p></div><dl><div><dt>Ventana máxima</dt><dd>${esc(e.max_ttl)}</dd></div><div><dt>Aprobación</dt><dd>${e.approvers.map((p) => esc(person(p))).join(", ")}</dd></div></dl>${e.requesters.includes(actor()) ? `<button class="quiet" data-action="request" data-entitlement="${esc(e.id)}">Solicitar acceso ↗</button>` : '<span class="muted">Fuera de tu alcance</span>'}</article>`).join("")}</div>`
  );
}
function eventList(list) {
  return `<ol class="event-list">${list.map((e) => `<li class="event"><span class="sequence">${e.sequence}</span><div><h3>${esc(events[e.body.event] || e.body.event)}</h3><p>${esc(person(e.body.actor))} / ${esc(e.grant_id.slice(0, 16))}</p></div><time datetime="${esc(e.body.at)}">${esc(date(e.body.at))}</time></li>`).join("")}</ol>`;
}
function evidence() {
  return (
    heading(
      "La historia de cada permiso.",
      "Transiciones y eventos se guardan juntos. Podés inspeccionar el registro completo.",
      '<button class="quiet" data-action="export">Exportar evidencia ↓</button>',
    ) +
    `<div class="audit-head"><strong>Consistencia local verificada por el servidor</strong><p>${data.snapshot.events.length} eventos encadenados. Hash de la cabecera:</p><code>${esc(data.snapshot.head)}</code></div>` +
    (data.snapshot.events.length
      ? eventList([...data.snapshot.events].reverse())
      : empty(
          "Todavía no hay evidencia.",
          "Solicitá un permiso para iniciar su recorrido.",
        )) +
    '<p class="audit-note">Un administrador que controla la base puede reescribir el registro y recalcular los hashes. No es una firma ni un anclaje externo.</p>'
  );
}
function render(force = false) {
  if (!data) return;
  if (!force && head === data.snapshot.head) return;
  const focused = document.activeElement?.dataset;
  const focusAction = focused?.action,
    focusID = focused?.id;
  head = data.snapshot.head;
  screen.innerHTML =
    view === "catalog"
      ? catalog()
      : view === "evidence"
        ? evidence()
        : requests();
  screen.setAttribute("aria-busy", "false");
  if ($("#filter")) $("#filter").value = filter;
  document.querySelectorAll("[data-view]").forEach((a) => {
    if (a.dataset.view === view) a.setAttribute("aria-current", "page");
    else a.removeAttribute("aria-current");
  });
  if (focusAction) {
    const next = [...screen.querySelectorAll("[data-action]")].find(
      (el) => el.dataset.action === focusAction && el.dataset.id === focusID,
    );
    (next || screen.querySelector("h1")).focus();
  }
  updateClock();
}
function updateClock() {
  if (!data) return;
  document.querySelectorAll("[data-expiry]").forEach((el) => {
    const g = data.snapshot.grants.find((g) => g.id === el.dataset.expiry);
    if (g) el.textContent = remaining(g);
  });
  if ($("#active-count"))
    $("#active-count").textContent = data.snapshot.grants.filter(
      (g) => g.state === "active" && Date.parse(g.expires_at) > now(),
    ).length;
  if ($("#revoke-count"))
    $("#revoke-count").textContent = data.snapshot.grants.filter(
      (g) =>
        g.state === "revoking" ||
        (g.state === "active" && Date.parse(g.expires_at) <= now()),
    ).length;
}
async function refresh(force = false) {
  if (inFlight) return;
  inFlight = true;
  try {
    const response = await fetch("/api/state", {
      cache: "no-store",
      signal: AbortSignal.timeout(8000),
    });
    if (!response.ok) throw new Error("unavailable");
    data = await response.json();
    serverOffset = Date.parse(data.now) - Date.now();
    errorBox.hidden = !data.worker_error && !data.missing;
    errorBox.textContent = data.worker_error
      ? "La reconciliación necesita atención. Hay operaciones pendientes; el worker seguirá reintentando."
      : data.missing
        ? `${data.missing} permisos activos no se encontraron completos en el destino. Revisá su evidencia; no se vuelven a conceder automáticamente.`
        : "";
    render(force);
  } catch {
    errorBox.hidden = false;
    errorBox.innerHTML =
      'No pudimos conectar con el laboratorio. Si abriste la web estática, iniciá <code>lease demo</code>. <button class="quiet" data-action="retry">Reintentar</button>';
    screen.setAttribute("aria-busy", "false");
    if (!data)
      screen.innerHTML =
        heading(
          "El laboratorio está desconectado.",
          "La presentación funciona como sitio estático. Para operar permisos, iniciá el backend local siguiendo la guía.",
        ) + '<a class="button" href="/guide/">Ver instalación ↗</a>';
  } finally {
    inFlight = false;
  }
}
async function mutate(action, body) {
  if (busy) return;
  busy = true;
  document.querySelectorAll("button").forEach((b) => (b.disabled = true));
  try {
    const response = await fetch(`/api/${action}`, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Lease-Demo": "1" },
      body: JSON.stringify({ ...body, actor: actor() }),
      signal: AbortSignal.timeout(10000),
    });
    const result = await response.json();
    if (!response.ok)
      throw new Error(result.error || "No se pudo completar la operación.");
    feedback.textContent =
      action === "request"
        ? "Solicitud registrada. Cambiá al rol de Mateo para revisarla."
        : action === "approve"
          ? "Acceso concedido. La ventana ya está corriendo."
          : action === "deny"
            ? "Solicitud denegada. No se concedió acceso."
            : "Revocación confirmada en el destino simulado.";
    const acknowledgement = feedback.textContent;
    setTimeout(() => {
      if (feedback.textContent === acknowledgement) feedback.textContent = "";
    }, 8000);
    return result;
  } finally {
    busy = false;
    document.querySelectorAll("button").forEach((b) => (b.disabled = false));
  }
}
function openRequest(id) {
  if (!data) return;
  const list = allowedRequests();
  if (!list.length) return;
  $("#entitlement").innerHTML = list
    .map(
      (e) =>
        `<option value="${esc(e.id)}">${esc(titles[e.id] || e.id)}</option>`,
    )
    .join("");
  if (id && list.some((e) => e.id === id)) $("#entitlement").value = id;
  $("#reason").value = "";
  $("#form-error").hidden = true;
  updateDurations();
  $("#request-dialog").showModal();
  $("#entitlement").focus();
}
function updateDurations() {
  const e = policy($("#entitlement").value);
  if (!e) return;
  const ttl =
    data.snapshot.entitlements.find((record) => record.ID === e.id)?.MaxTTL /
    1e9;
  $("#entitlement-hint").textContent =
    `${e.target} / ${e.role}. Aprueba ${e.approvers.map(person).join(", ")}.`;
  const options = [
    [10, "10 segundos (vencimiento rápido)"],
    [60, "1 minuto"],
    [300, "5 minutos"],
    [600, "10 minutos"],
    [900, "15 minutos"],
    [1800, "30 minutos"],
  ].filter(([value]) => value <= ttl);
  $("#duration").innerHTML = options
    .map(
      ([value, label]) =>
        `<option value="${value}"${value === 300 ? " selected" : ""}>${label}</option>`,
    )
    .join("");
}
function showDetail(id) {
  const g = data.snapshot.grants.find((g) => g.id === id);
  if (!g) return;
  $("#detail-title").textContent = titles[g.entitlement_id] || g.entitlement_id;
  $("#detail-content").innerHTML =
    `<dl class="detail-summary"><div><dt>Solicitante</dt><dd>${esc(person(g.key.Principal))}</dd></div><div><dt>Estado</dt><dd>${esc(states[g.state])}</dd></div><div><dt>Destino y rol</dt><dd>${esc(g.key.Target)} / ${esc(g.key.Role)}</dd></div><div><dt>Vence</dt><dd>${esc(date(g.expires_at))}</dd></div></dl><p class="detail-reason">${esc(g.reason)}</p><h3>Recorrido registrado</h3>${eventList(data.snapshot.events.filter((e) => e.grant_id === g.id))}`;
  $("#detail-dialog").showModal();
}
function exportEvidence() {
  const blob = new Blob([JSON.stringify(data.snapshot, null, 2)], {
    type: "application/json",
  });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = "lease-lab-evidence.json";
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
document.addEventListener("click", async (event) => {
  const close = event.target.closest("[data-close]");
  if (close) {
    $("#" + close.dataset.close).close();
    return;
  }
  const button = event.target.closest("[data-action]");
  if (!button) return;
  const { action, id, entitlement } = button.dataset;
  if (action === "request") {
    openRequest(entitlement);
    return;
  }
  if (action === "detail") {
    showDetail(id);
    return;
  }
  if (action === "export") {
    exportEvidence();
    return;
  }
  if (action === "retry") {
    await refresh(true);
    return;
  }
  try {
    await mutate(action, { id });
    await refresh(true);
  } catch (err) {
    errorBox.textContent = err.message;
    errorBox.hidden = false;
  }
});
$("#request-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  if (!event.currentTarget.reportValidity()) return;
  try {
    await mutate("request", {
      entitlement: $("#entitlement").value,
      reason: $("#reason").value.trim(),
      seconds: Number($("#duration").value),
    });
    $("#request-dialog").close();
    view = "requests";
    location.hash = "requests";
    filter = "all";
    await refresh(true);
  } catch (err) {
    $("#form-error").textContent = err.message;
    $("#form-error").hidden = false;
  }
});
$("#entitlement").addEventListener("change", updateDurations);
$("#actor").addEventListener("change", () => {
  feedback.textContent = "";
  render(true);
});
screen.addEventListener("change", (event) => {
  if (event.target.id === "filter") {
    filter = event.target.value;
    render(true);
    $("#filter").focus();
  }
});
window.addEventListener("hashchange", () => {
  const next = location.hash.slice(1);
  if (["requests", "catalog", "evidence"].includes(next)) {
    if (view !== next) feedback.textContent = "";
    view = next;
    render(true);
    screen.querySelector("h1")?.focus();
  }
});
await refresh(true);
setInterval(() => {
  if (!document.hidden && !busy) refresh();
}, 2000);
setInterval(updateClock, 1000);

for (const dialog of document.querySelectorAll("dialog"))
  dialog.addEventListener("close", () => {
    if (document.activeElement === document.body)
      screen.querySelector("h1")?.focus();
  });
