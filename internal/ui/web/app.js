// qdnder web UI — vanilla JS SPA, Russian/English.
"use strict";

const I18N = {
  ru: {
    login: "Вход", username: "Логин", password: "Пароль", signin: "Войти",
    logout: "Выйти", builds: "Сборки", projects: "Проекты", admin: "Админка",
    build: "Собрать", cancel: "Отменить", rebuild: "Пересобрать", back: "← Назад",
    status: "Статус", project: "Проект", ref: "Ветка/тег", trigger: "Триггер",
    created: "Создана", duration: "Длительность", runner: "Раннер", tags: "Теги образа",
    all: "Все", queued: "в очереди", running: "идёт", success: "успех", failed: "ошибка", canceled: "отменена",
    logs: "Лог сборки", error: "Ошибка", no_builds: "Сборок пока нет",
    name: "Имя", repo_url: "Адрес репозитория", image: "Образ (registry/имя)",
    dockerfile: "Dockerfile", context: "Контекст", default_branch: "Ветка по умолчанию",
    tag_templates: "Шаблоны тегов (через запятую)", build_args: "Build-args (KEY=VALUE по строкам)",
    registry_user: "Логин registry", registry_pass: "Пароль registry",
    git_user: "Логин git", git_pass: "Пароль git", webhook_secret: "Секрет вебхука",
    keep_secret: "пусто — оставить прежний", save: "Сохранить", edit: "Изменить", del: "Удалить",
    new_project: "Новый проект", webhook_url: "Адрес вебхука",
    users: "Пользователи", role: "Роль", add_user: "Добавить пользователя",
    tokens: "API-токены", new_token: "Новый токен", token_shown_once: "Токен показывается один раз — сохраните:",
    runners: "Раннеры", online: "онлайн", offline: "офлайн", last_seen: "Виден",
    settings: "Настройки", runner_token: "Токен раннера", rotate: "Сменить токен",
    queue: "В очереди", version: "Версия", actions: "Действия",
    confirm_delete: "Удалить?", confirm_cancel: "Отменить сборку?",
    build_started: "Сборка поставлена в очередь", required: "Заполните обязательные поля",
    api_hint: "REST API: /api с заголовком Authorization: Bearer <токен>. MCP: POST /mcp.",
    lang_btn: "EN",
  },
  en: {
    login: "Login", username: "Username", password: "Password", signin: "Sign in",
    logout: "Log out", builds: "Builds", projects: "Projects", admin: "Admin",
    build: "Build", cancel: "Cancel", rebuild: "Rebuild", back: "← Back",
    status: "Status", project: "Project", ref: "Ref", trigger: "Trigger",
    created: "Created", duration: "Duration", runner: "Runner", tags: "Image tags",
    all: "All", queued: "queued", running: "running", success: "success", failed: "failed", canceled: "canceled",
    logs: "Build log", error: "Error", no_builds: "No builds yet",
    name: "Name", repo_url: "Repository URL", image: "Image (registry/name)",
    dockerfile: "Dockerfile", context: "Context", default_branch: "Default branch",
    tag_templates: "Tag templates (comma-separated)", build_args: "Build args (KEY=VALUE per line)",
    registry_user: "Registry user", registry_pass: "Registry password",
    git_user: "Git user", git_pass: "Git password", webhook_secret: "Webhook secret",
    keep_secret: "empty — keep previous", save: "Save", edit: "Edit", del: "Delete",
    new_project: "New project", webhook_url: "Webhook URL",
    users: "Users", role: "Role", add_user: "Add user",
    tokens: "API tokens", new_token: "New token", token_shown_once: "The token is shown once — save it:",
    runners: "Runners", online: "online", offline: "offline", last_seen: "Last seen",
    settings: "Settings", runner_token: "Runner token", rotate: "Rotate token",
    queue: "Queued", version: "Version", actions: "Actions",
    confirm_delete: "Delete?", confirm_cancel: "Cancel this build?",
    build_started: "Build queued", required: "Fill in the required fields",
    api_hint: "REST API: /api with Authorization: Bearer <token>. MCP: POST /mcp.",
    lang_btn: "RU",
  },
};

let lang = localStorage.getItem("qdnder_lang") || "ru";
let me = null;
let view = { name: "builds" };
let refreshTimer = null;
let eventSource = null;
let projectsCache = [];

const t = (k) => (I18N[lang] && I18N[lang][k]) || I18N.en[k] || k;
const $ = (sel) => document.querySelector(sel);
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

function toggleLang() {
  lang = lang === "ru" ? "en" : "ru";
  localStorage.setItem("qdnder_lang", lang);
  render();
}

async function api(path, method = "GET", body = null) {
  const opts = { method, headers: {} };
  if (body !== null) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const resp = await fetch(path, opts);
  if (resp.status === 401) { me = null; render(); throw new Error("unauthorized"); }
  if (resp.status === 204) return null;
  const data = await resp.json().catch(() => null);
  if (!resp.ok) throw new Error((data && data.error) || ("HTTP " + resp.status));
  return data;
}

function fmtTime(ts) {
  if (!ts) return "—";
  return new Date(ts * 1000).toLocaleString(lang === "ru" ? "ru-RU" : "en-US");
}
function fmtDur(b) {
  if (!b.started_at) return "—";
  const end = b.finished_at || Math.floor(Date.now() / 1000);
  const s = Math.max(0, end - b.started_at);
  return s < 60 ? s + "s" : Math.floor(s / 60) + "m " + (s % 60) + "s";
}
const badge = (s) => `<span class="badge ${esc(s)}">${esc(t(s))}</span>`;

// --- Shell -------------------------------------------------------------------

function render() {
  clearInterval(refreshTimer); refreshTimer = null;
  if (eventSource) { eventSource.close(); eventSource = null; }
  const bar = $("#topbar");
  if (!me) { bar.hidden = true; renderLogin(); return; }
  bar.hidden = false;
  $("#langBtn").textContent = t("lang_btn");
  $("#who").textContent = me.username + " · " + me.role;
  $("#logoutBtn").textContent = t("logout");
  const nav = [["builds", t("builds")], ["projects", t("projects")]];
  if (me.role === "admin") nav.push(["admin", t("admin")]);
  $("#nav").innerHTML = nav.map(([v, label]) =>
    `<button class="${view.name === v || (v === "builds" && view.name === "build") ? "active" : ""}"
      onclick="go('${v}')">${esc(label)}</button>`).join("");
  if (view.name === "builds") renderBuilds();
  else if (view.name === "build") renderBuild(view.id);
  else if (view.name === "projects") renderProjects();
  else if (view.name === "admin") renderAdmin();
}

function go(name, arg) {
  view = name === "build" ? { name, id: arg } : { name };
  render();
}

async function logout() {
  await api("/api/logout", "POST", {});
  me = null; render();
}

function renderLogin() {
  $("#main").innerHTML = `<div class="login-wrap card">
    <h2>qdnder — ${esc(t("login"))}</h2>
    <label>${esc(t("username"))}</label><input id="lu">
    <label>${esc(t("password"))}</label><input id="lp" type="password">
    <div style="margin-top:14px"><button onclick="doLogin()">${esc(t("signin"))}</button></div>
    <div id="lerr" class="error"></div></div>`;
  $("#lp").addEventListener("keydown", (e) => { if (e.key === "Enter") doLogin(); });
}

async function doLogin() {
  try {
    me = await api("/api/login", "POST", { username: $("#lu").value, password: $("#lp").value });
    view = { name: "builds" };
    render();
  } catch (e) { $("#lerr").textContent = e.message; }
}

// --- Builds ------------------------------------------------------------------

async function renderBuilds() {
  const status = view.status || "";
  $("#main").innerHTML = `<div class="toolbar">
      <h2 style="margin:0">${esc(t("builds"))}</h2>
      <select id="fStatus" onchange="view.status=this.value;renderBuilds()">
        <option value="">${esc(t("all"))}</option>
        ${["queued", "running", "success", "failed", "canceled"].map((s) =>
          `<option value="${s}" ${s === status ? "selected" : ""}>${esc(t(s))}</option>`).join("")}
      </select>
      <button onclick="buildForm()">${esc(t("build"))}</button>
    </div><div id="buildList"></div><div id="buildFormBox"></div>`;
  await loadBuildList();
  refreshTimer = setInterval(loadBuildList, 3000);
}

async function loadBuildList() {
  let builds;
  try { builds = await api("/api/builds?limit=100&status=" + (view.status || "")); }
  catch (e) { return; }
  const box = $("#buildList");
  if (!box) return;
  if (!builds.length) { box.innerHTML = `<p class="muted">${esc(t("no_builds"))}</p>`; return; }
  box.innerHTML = `<table><tr><th>ID</th><th>${esc(t("project"))}</th><th>${esc(t("ref"))}</th>
    <th>${esc(t("status"))}</th><th>${esc(t("trigger"))}</th><th>${esc(t("created"))}</th><th>${esc(t("duration"))}</th></tr>
    ${builds.map((b) => `<tr class="click" onclick="go('build','${esc(b.id)}')">
      <td class="mono">${esc(b.id)}</td><td>${esc(b.project_name)}</td><td>${esc(b.ref)}${b.tag ? " 🏷 " + esc(b.tag) : ""}</td>
      <td>${badge(b.status)}</td><td>${esc(b.trigger)}</td><td>${esc(fmtTime(b.created_at))}</td><td>${esc(fmtDur(b))}</td></tr>`).join("")}
    </table>`;
}

async function buildForm() {
  projectsCache = await api("/api/projects");
  $("#buildFormBox").innerHTML = `<div class="form" style="margin-top:14px">
    <label>${esc(t("project"))}</label>
    <select id="bfProject">${projectsCache.map((p) => `<option value="${esc(p.name)}">${esc(p.name)}</option>`).join("")}</select>
    <label>${esc(t("ref"))}</label><input id="bfRef" placeholder="main">
    <label>Tag</label><input id="bfTag" placeholder="v1.2.3">
    <div style="margin-top:12px"><button onclick="submitBuild()">${esc(t("build"))}</button></div>
    <div id="bfErr" class="error"></div></div>`;
}

async function submitBuild() {
  try {
    const b = await api("/api/builds", "POST", {
      project: $("#bfProject").value, ref: $("#bfRef").value, tag: $("#bfTag").value,
    });
    go("build", b.id);
  } catch (e) { $("#bfErr").textContent = e.message; }
}

async function renderBuild(id) {
  let b;
  try { b = await api("/api/builds/" + id); }
  catch (e) { $("#main").innerHTML = `<p class="error">${esc(e.message)}</p>`; return; }
  const active = b.status === "queued" || b.status === "running";
  $("#main").innerHTML = `
    <div class="toolbar"><button class="ghost" onclick="go('builds')">${esc(t("back"))}</button>
      <h2 style="margin:0">${esc(b.project_name)} <span class="mono muted">${esc(b.id)}</span></h2>
      ${badge(b.status)}
      ${active ? `<button class="danger" onclick="cancelBuild('${esc(b.id)}')">${esc(t("cancel"))}</button>` : ""}
      <button class="ghost" onclick="rebuild('${esc(b.project_name)}','${esc(b.ref)}','${esc(b.tag)}')">${esc(t("rebuild"))}</button>
    </div>
    <div class="card">
      <div class="row muted">
        <span>${esc(t("ref"))}: <b>${esc(b.ref)}</b></span>
        ${b.sha ? `<span>sha: <span class="mono">${esc(b.sha.slice(0, 12))}</span></span>` : ""}
        <span>${esc(t("trigger"))}: ${esc(b.trigger)}</span>
        <span>${esc(t("runner"))}: ${esc(b.runner || "—")}</span>
        <span>${esc(t("created"))}: ${esc(fmtTime(b.created_at))}</span>
        <span>${esc(t("duration"))}: ${esc(fmtDur(b))}</span>
      </div>
      ${b.image_tags && b.image_tags.length ? `<div style="margin-top:8px">${esc(t("tags"))}:
        ${b.image_tags.map((x) => `<code>${esc(x)}</code>`).join(" ")}</div>` : ""}
      ${b.error ? `<div class="error">${esc(t("error"))}: ${esc(b.error)}</div>` : ""}
    </div>
    <h3>${esc(t("logs"))}</h3><pre class="log" id="logBox">${esc(b.log)}</pre>`;
  const box = $("#logBox");
  box.scrollTop = box.scrollHeight;
  if (active) {
    eventSource = new EventSource("/api/builds/" + id + "/events");
    eventSource.addEventListener("log", (ev) => {
      box.textContent += JSON.parse(ev.data);
      box.scrollTop = box.scrollHeight;
    });
    eventSource.addEventListener("status", (ev) => {
      const s = JSON.parse(ev.data);
      if (["success", "failed", "canceled"].includes(s.status)) {
        eventSource.close(); eventSource = null;
        renderBuild(id);
      }
    });
  }
}

async function cancelBuild(id) {
  if (!confirm(t("confirm_cancel"))) return;
  await api("/api/builds/" + id + "/cancel", "POST", {});
  renderBuild(id);
}

async function rebuild(project, ref, tag) {
  const b = await api("/api/builds", "POST", { project, ref, tag });
  go("build", b.id);
}

// --- Projects ------------------------------------------------------------------

async function renderProjects() {
  projectsCache = await api("/api/projects");
  const canEdit = me.role !== "viewer";
  $("#main").innerHTML = `<div class="toolbar"><h2 style="margin:0">${esc(t("projects"))}</h2>
      ${canEdit ? `<button onclick="projectForm()">${esc(t("new_project"))}</button>` : ""}</div>
    <table><tr><th>${esc(t("name"))}</th><th>${esc(t("repo_url"))}</th><th>${esc(t("image"))}</th><th>${esc(t("actions"))}</th></tr>
    ${projectsCache.map((p) => `<tr><td>${esc(p.name)}</td><td class="mono">${esc(p.repo_url)}</td><td class="mono">${esc(p.image)}</td>
      <td>${canEdit ? `<button class="small ghost" onclick="projectForm('${esc(p.id)}')">${esc(t("edit"))}</button>
        <button class="small ghost" onclick="rebuild('${esc(p.name)}','','')">${esc(t("build"))}</button>` : ""}
      ${me.role === "admin" ? `<button class="small danger" onclick="delProject('${esc(p.id)}')">${esc(t("del"))}</button>` : ""}</td></tr>`).join("")}
    </table><div id="projectFormBox"></div>`;
}

async function delProject(id) {
  if (!confirm(t("confirm_delete"))) return;
  await api("/api/projects/" + id, "DELETE");
  renderProjects();
}

function projectForm(id) {
  const p = id ? projectsCache.find((x) => x.id === id) : null;
  const args = p && p.build_args ? Object.entries(p.build_args).map(([k, v]) => k + "=" + v).join("\n") : "";
  $("#projectFormBox").innerHTML = `<div class="form" style="margin-top:14px">
    <label>${esc(t("name"))} *</label><input id="pfName" value="${esc(p ? p.name : "")}">
    <label>${esc(t("repo_url"))} *</label><input id="pfRepo" value="${esc(p ? p.repo_url : "")}">
    <label>${esc(t("image"))} *</label><input id="pfImage" value="${esc(p ? p.image : "")}" placeholder="registry.example.com/team/app">
    <label>${esc(t("default_branch"))}</label><input id="pfBranch" value="${esc(p ? p.default_branch : "main")}">
    <label>${esc(t("dockerfile"))}</label><input id="pfDockerfile" value="${esc(p ? p.dockerfile : "Dockerfile")}">
    <label>${esc(t("context"))}</label><input id="pfContext" value="${esc(p ? p.context : ".")}">
    <label>${esc(t("tag_templates"))}</label><input id="pfTags" value="${esc(p && p.tags ? p.tags.join(", ") : "latest")}">
    <label>${esc(t("build_args"))}</label><textarea id="pfArgs" rows="3">${esc(args)}</textarea>
    <label>${esc(t("registry_user"))}</label><input id="pfRegUser" value="${esc(p ? p.registry_user : "")}">
    <label>${esc(t("registry_pass"))}</label><input id="pfRegPass" type="password" placeholder="${esc(t("keep_secret"))}">
    <label>${esc(t("git_user"))}</label><input id="pfGitUser" value="${esc(p ? p.git_user : "")}">
    <label>${esc(t("git_pass"))}</label><input id="pfGitPass" type="password" placeholder="${esc(t("keep_secret"))}">
    <label>${esc(t("webhook_secret"))}</label><input id="pfWh" type="password" placeholder="${esc(t("keep_secret"))}">
    ${p ? `<p class="muted">${esc(t("webhook_url"))}: <code>${esc(location.origin + "/api/webhook/" + p.name)}</code></p>` : ""}
    <div style="margin-top:12px"><button onclick="saveProject('${esc(id || "")}')">${esc(t("save"))}</button></div>
    <div id="pfErr" class="error"></div></div>`;
}

async function saveProject(id) {
  const args = {};
  for (const line of $("#pfArgs").value.split("\n")) {
    const i = line.indexOf("=");
    if (i > 0) args[line.slice(0, i).trim()] = line.slice(i + 1).trim();
  }
  const body = {
    name: $("#pfName").value.trim(), repo_url: $("#pfRepo").value.trim(), image: $("#pfImage").value.trim(),
    default_branch: $("#pfBranch").value.trim(), dockerfile: $("#pfDockerfile").value.trim(),
    context: $("#pfContext").value.trim(),
    tags: $("#pfTags").value.split(",").map((s) => s.trim()).filter(Boolean),
    build_args: args,
    registry_user: $("#pfRegUser").value, registry_pass: $("#pfRegPass").value,
    git_user: $("#pfGitUser").value, git_pass: $("#pfGitPass").value,
    webhook_secret: $("#pfWh").value,
  };
  try {
    if (id) await api("/api/projects/" + id, "PUT", body);
    else await api("/api/projects", "POST", body);
    renderProjects();
  } catch (e) { $("#pfErr").textContent = e.message; }
}

// --- Admin -----------------------------------------------------------------------

async function renderAdmin() {
  const [users, tokens, runners, settings] = await Promise.all([
    api("/api/users"), api("/api/tokens"), api("/api/runners"), api("/api/settings"),
  ]);
  $("#main").innerHTML = `
    <h2>${esc(t("admin"))}</h2>
    <h3>${esc(t("settings"))}</h3>
    <div class="card">
      <div>${esc(t("runner_token"))}: <code id="rtBox">${esc(settings.runner_token)}</code>
        <button class="small ghost" onclick="rotateToken()">${esc(t("rotate"))}</button></div>
      <div class="muted" style="margin-top:6px">${esc(t("queue"))}: ${settings.queue_size} · ${esc(t("version"))}: ${esc(settings.version)}</div>
      <div class="muted" style="margin-top:6px">${esc(t("api_hint"))}</div>
    </div>
    <h3>${esc(t("runners"))}</h3>
    <table><tr><th>${esc(t("name"))}</th><th>${esc(t("version"))}</th><th>${esc(t("status"))}</th><th>${esc(t("last_seen"))}</th></tr>
    ${runners.map((r) => `<tr><td>${esc(r.name)}</td><td>${esc(r.version)}</td>
      <td><span class="badge ${r.online ? "success" : "failed"}">${esc(t(r.online ? "online" : "offline"))}</span></td>
      <td>${esc(fmtTime(r.last_seen))}</td></tr>`).join("") || `<tr><td colspan="4" class="muted">—</td></tr>`}</table>
    <h3>${esc(t("users"))}</h3>
    <table><tr><th>${esc(t("username"))}</th><th>${esc(t("role"))}</th><th>${esc(t("actions"))}</th></tr>
    ${users.map((u) => `<tr><td>${esc(u.username)}</td><td>${esc(u.role)}</td>
      <td><button class="small ghost" onclick="resetPass('${esc(u.id)}')">${esc(t("password"))}</button>
      ${u.username !== me.username ? `<button class="small danger" onclick="delUser('${esc(u.id)}')">${esc(t("del"))}</button>` : ""}</td></tr>`).join("")}</table>
    <div class="form" style="margin-top:12px">
      <div class="row"><input id="nuName" placeholder="${esc(t("username"))}" style="width:160px">
      <input id="nuPass" type="password" placeholder="${esc(t("password"))}" style="width:160px">
      <select id="nuRole" style="width:120px"><option>viewer</option><option>operator</option><option>admin</option></select>
      <button onclick="addUser()">${esc(t("add_user"))}</button></div><div id="nuErr" class="error"></div>
    </div>
    <h3>${esc(t("tokens"))}</h3>
    <table><tr><th>${esc(t("name"))}</th><th>${esc(t("role"))}</th><th>${esc(t("created"))}</th><th>${esc(t("actions"))}</th></tr>
    ${tokens.map((x) => `<tr><td>${esc(x.name)}</td><td>${esc(x.role)}</td><td>${esc(fmtTime(x.created_at))}</td>
      <td><button class="small danger" onclick="delToken('${esc(x.id)}')">${esc(t("del"))}</button></td></tr>`).join("")}</table>
    <div class="form" style="margin-top:12px">
      <div class="row"><input id="ntName" placeholder="${esc(t("name"))}" style="width:180px">
      <select id="ntRole" style="width:120px"><option>viewer</option><option>operator</option><option>admin</option></select>
      <button onclick="addToken()">${esc(t("new_token"))}</button></div>
      <div id="ntOut"></div><div id="ntErr" class="error"></div>
    </div>`;
}

async function rotateToken() {
  const r = await api("/api/settings/rotate-runner-token", "POST", {});
  $("#rtBox").textContent = r.runner_token;
}
async function addUser() {
  try {
    await api("/api/users", "POST", { username: $("#nuName").value, password: $("#nuPass").value, role: $("#nuRole").value });
    renderAdmin();
  } catch (e) { $("#nuErr").textContent = e.message; }
}
async function delUser(id) {
  if (!confirm(t("confirm_delete"))) return;
  await api("/api/users/" + id, "DELETE");
  renderAdmin();
}
async function resetPass(id) {
  const p = prompt(t("password") + " (>= 8):");
  if (!p) return;
  await api("/api/users/" + id + "/password", "PUT", { password: p });
}
async function addToken() {
  try {
    const r = await api("/api/tokens", "POST", { name: $("#ntName").value, role: $("#ntRole").value });
    $("#ntOut").innerHTML = `<p class="ok">${esc(t("token_shown_once"))}</p><code>${esc(r.token)}</code>`;
  } catch (e) { $("#ntErr").textContent = e.message; }
}
async function delToken(id) {
  if (!confirm(t("confirm_delete"))) return;
  await api("/api/tokens/" + id, "DELETE");
  renderAdmin();
}

// --- Boot ------------------------------------------------------------------------

(async function boot() {
  try { me = await api("/api/me"); } catch (e) { me = null; }
  render();
})();
