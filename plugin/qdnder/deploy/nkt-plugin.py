"""Плагин хаба nkt для независимого CI qdnder (протокол плагинов nkt v1).

Разделы: «Сборки» (таблица с логами и отменой), «Сводка» (находки: упавшие
сборки, офлайн-раннеры). Операции MCP: builds, build_logs, build_trigger,
build_cancel. Команда сценария: qdnder builds | build | logs | cancel.
Только стандартная библиотека Python; тексты на русском и английском.

Настройки — окружение службы (хаб пишет их в /etc/nkt-plugins/qdnder.env):
QDNDER_URL, QDNDER_TOKEN (секрет). Плагин запускается и без них — их задают
после установки («Настройки» на странице плагина).
"""
import hashlib
import hmac
import json
import os
import re
import time
import urllib.error
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

# Версия — та же, что «version» в nkt-plugin.json (проверяет lint).
VERSION = "0.1.0"
SECRET = os.environ.get("NKT_PLUGIN_SECRET", "")
PORT = int(os.environ.get("NKT_PLUGIN_PORT", "9290") or 9290)
LISTEN = os.environ.get("NKT_PLUGIN_LISTEN", "0.0.0.0").strip() or "0.0.0.0"
API = (os.environ.get("QDNDER_URL", "").strip() or "http://127.0.0.1:8090").rstrip("/")
TOKEN = os.environ.get("QDNDER_TOKEN", "").strip()

T = {
    "ru": {
        "title": "qdnder CI",
        "desc": "Сборки Docker-образов в независимом CI qdnder: список, логи, запуск, отмена, сводка.",
        "builds": "Сборки", "summary": "Сводка", "logs": "Лог сборки",
        "id": "ID", "project": "Проект", "ref": "Ветка/тег", "status": "Статус",
        "trigger": "Триггер", "created": "Создана", "runner": "Раннер", "error": "Ошибка",
        "queued": "в очереди", "running": "идёт", "success": "успех", "failed": "ошибка", "canceled": "отменена",
        "build": "Собрать", "cancel": "Отменить", "open_logs": "Лог",
        "cancel_confirm": "Отменить сборку {id}?",
        "project_field": "Проект", "ref_field": "Ветка или тег (пусто — по умолчанию)", "tag_field": "Тег для шаблона {tag}",
        "triggered": "сборка {id} поставлена в очередь",
        "canceled_msg": "отмена сборки {id} запрошена",
        "would": "(сухой прогон) {what}",
        "would_build": "поставить в очередь сборку проекта {project}",
        "would_cancel": "отменить сборку {id}",
        "no_token": "не задан QDNDER_TOKEN — «Настройки» на странице плагина",
        "api_failed": "API {method} {path}: {err}",
        "bad_project": "проект {name}: латиница, цифры, - и _ (до 64)",
        "bad_id": "неверный ID сборки: {id}",
        "bad_ref": "неверная ветка/тег: {ref}",
        "no_builds": "Сборок пока нет.", "no_findings": "Находок нет: сборки успешны, раннеры на связи.",
        "f_failed": "Сборка {id} ({project}) упала", "f_failed_fix": "откройте лог и исправьте причину",
        "f_offline": "Раннер {name} не на связи", "f_offline_fix": "проверьте службу раннера и токен",
        "x_total": "Всего сборок", "x_failed": "Упало", "x_running": "Идёт", "x_runners_offline": "Раннеров офлайн",
        "usage": "qdnder builds | qdnder build ПРОЕКТ [ВЕТКА] | qdnder logs ID | qdnder cancel ID",
        "cmd_builds_done": "сборок: {n}", "cmd_logs_empty": "лог пуст",
        "unknown_word": "неизвестная команда: {word}",
        "connect_unsupported": "_connect не поддерживается: плагину не нужны действия хаба",
    },
    "en": {
        "title": "qdnder CI",
        "desc": "Docker image builds in the independent qdnder CI: list, logs, trigger, cancel, summary.",
        "builds": "Builds", "summary": "Summary", "logs": "Build log",
        "id": "ID", "project": "Project", "ref": "Ref", "status": "Status",
        "trigger": "Trigger", "created": "Created", "runner": "Runner", "error": "Error",
        "queued": "queued", "running": "running", "success": "success", "failed": "failed", "canceled": "canceled",
        "build": "Build", "cancel": "Cancel", "open_logs": "Log",
        "cancel_confirm": "Cancel build {id}?",
        "project_field": "Project", "ref_field": "Branch or tag (empty — default)", "tag_field": "Tag for the {tag} template",
        "triggered": "build {id} queued",
        "canceled_msg": "cancellation of build {id} requested",
        "would": "(dry run) {what}",
        "would_build": "queue a build of project {project}",
        "would_cancel": "cancel build {id}",
        "no_token": "QDNDER_TOKEN is not set — Settings on the plugin page",
        "api_failed": "API {method} {path}: {err}",
        "bad_project": "project {name}: latin letters, digits, - and _ (up to 64)",
        "bad_id": "bad build id: {id}",
        "bad_ref": "bad ref: {ref}",
        "no_builds": "No builds yet.", "no_findings": "No findings: builds succeed, runners are online.",
        "f_failed": "Build {id} ({project}) failed", "f_failed_fix": "open the log and fix the cause",
        "f_offline": "Runner {name} is offline", "f_offline_fix": "check the runner service and token",
        "x_total": "Total builds", "x_failed": "Failed", "x_running": "Running", "x_runners_offline": "Runners offline",
        "usage": "qdnder builds | qdnder build PROJECT [REF] | qdnder logs ID | qdnder cancel ID",
        "cmd_builds_done": "builds: {n}", "cmd_logs_empty": "log is empty",
        "unknown_word": "unknown command: {word}",
        "connect_unsupported": "_connect is not supported: the plugin needs no hub actions",
    },
}


class Fail(Exception):
    """Ошибка для человека: уходит хабу как {ok: false, error}."""


class Ctx:
    def __init__(self, lang):
        self.lang = lang if lang in T else "ru"

    def t(self, key, **kw):
        s = T[self.lang][key]
        return s.format(**kw) if kw else s

    def fail(self, key, **kw):
        return Fail(self.t(key, **kw))


def log(msg):
    """Журнал службы. Секретов здесь нет."""
    print(f"nkt-plugin-qdnder: {msg}", flush=True)


# --- API -----------------------------------------------------------------------

PROJECT_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$")
ID_RE = re.compile(r"^[a-f0-9]{8,32}$")
REF_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$")


def check_project(c, name):
    name = str(name or "").strip()
    if not PROJECT_RE.match(name):
        raise c.fail("bad_project", name=name or "—")
    return name


def check_id(c, value):
    value = str(value or "").strip()
    if not ID_RE.match(value):
        raise c.fail("bad_id", id=value or "—")
    return value


def check_ref(c, value):
    value = str(value or "").strip()
    if value and not REF_RE.match(value):
        raise c.fail("bad_ref", ref=value)
    return value


def api(c, method, path, body=None):
    if not TOKEN:
        raise c.fail("no_token")
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(API + path, data=data, method=method,
                                 headers={"Authorization": "Bearer " + TOKEN,
                                          "Content-Type": "application/json", "Accept": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            raw = r.read()
    except urllib.error.HTTPError as e:
        raise c.fail("api_failed", method=method, path=path, err=f"HTTP {e.code}")
    except urllib.error.URLError as e:
        raise c.fail("api_failed", method=method, path=path, err=str(e.reason))
    return json.loads(raw or b"null")


def fmt_ts(c, ts):
    if not ts:
        return "—"
    return time.strftime("%Y-%m-%d %H:%M", time.localtime(ts))


def status_text(c, s):
    return c.t(s) if s in ("queued", "running", "success", "failed", "canceled") else str(s)


# --- Operations ------------------------------------------------------------------

def op_builds(c, args):
    builds = api(c, "GET", "/api/builds?limit=50")
    builds = builds if isinstance(builds, list) else []
    projects = api(c, "GET", "/api/projects")
    projects = projects if isinstance(projects, list) else []
    rows = []
    for b in builds:
        bid = str(b.get("id", ""))
        actions = [{"title": c.t("open_logs"), "open": "logs", "args": {"build_id": bid}}]
        if b.get("status") in ("queued", "running"):
            actions.append({"title": c.t("cancel"), "op": "build_cancel", "args": {"build_id": bid},
                            "confirm": c.t("cancel_confirm", id=bid), "danger": True})
        rows.append({"cells": {
            "id": bid, "project": str(b.get("project_name", "")),
            "ref": str(b.get("ref", "")) + ((" 🏷 " + b["tag"]) if b.get("tag") else ""),
            "status": status_text(c, b.get("status", "")),
            "trigger": str(b.get("trigger", "")), "created": fmt_ts(c, b.get("created_at")),
        }, "actions": actions})
    view = {"type": "table", "title": c.t("builds"),
            "note": c.t("no_builds") if not rows else "",
            "columns": [{"key": k, "title": c.t(k)} for k in ("id", "project", "ref", "status", "trigger", "created")],
            "rows": rows,
            "actions": [{"title": c.t("build"), "op": "build_trigger", "fields": [
                {"name": "project", "title": c.t("project_field"), "type": "select", "required": True,
                 "options": [{"value": p.get("name", ""), "title": p.get("name", "")} for p in projects]},
                {"name": "ref", "title": c.t("ref_field")},
                {"name": "tag", "title": c.t("tag_field")},
            ]}]}
    return {"ok": True, "view": view, "result": {"count": len(rows)}}


def op_build_logs(c, args):
    bid = check_id(c, args.get("build_id"))
    b = api(c, "GET", "/api/builds/" + urllib.parse.quote(bid))
    if not isinstance(b, dict):
        raise c.fail("bad_id", id=bid)
    head = f"{b.get('project_name', '')} · {bid} · {status_text(c, b.get('status', ''))}"
    if b.get("error"):
        head += "\n" + c.t("error") + ": " + str(b["error"])
    tags = b.get("image_tags") or []
    if tags:
        head += "\n" + ", ".join(str(x) for x in tags)
    text = head + "\n\n" + (b.get("log") or c.t("cmd_logs_empty"))
    return {"ok": True, "view": {"type": "text", "title": c.t("logs"), "text": text},
            "result": {"status": b.get("status"), "log": b.get("log", "")}}


def op_build_trigger(c, args, dry_run=False):
    project = check_project(c, args.get("project"))
    ref = check_ref(c, args.get("ref"))
    tag = check_ref(c, args.get("tag"))
    if dry_run:
        return {"ok": True, "text": c.t("would", what=c.t("would_build", project=project))}
    body = {"project": project}
    if ref:
        body["ref"] = ref
    if tag:
        body["tag"] = tag
    b = api(c, "POST", "/api/builds", body)
    bid = str(b.get("id", "")) if isinstance(b, dict) else ""
    return {"ok": True, "text": c.t("triggered", id=bid), "result": {"build_id": bid}}


def op_build_cancel(c, args, dry_run=False):
    bid = check_id(c, args.get("build_id"))
    if dry_run:
        return {"ok": True, "text": c.t("would", what=c.t("would_cancel", id=bid))}
    api(c, "POST", "/api/builds/" + urllib.parse.quote(bid) + "/cancel", {})
    return {"ok": True, "text": c.t("canceled_msg", id=bid)}


def op_summary(c, args):
    builds = api(c, "GET", "/api/builds?limit=100")
    builds = builds if isinstance(builds, list) else []
    runners = api(c, "GET", "/api/runners")
    runners = runners if isinstance(runners, list) else []
    failed = [b for b in builds if b.get("status") == "failed"]
    running = [b for b in builds if b.get("status") in ("queued", "running")]
    offline = [r for r in runners if not r.get("online")]
    stats = (f"{c.t('x_total')}: {len(builds)} · {c.t('x_failed')}: {len(failed)} · "
             f"{c.t('x_running')}: {len(running)} · {c.t('x_runners_offline')}: {len(offline)}")
    findings = []
    for b in failed[:20]:
        findings.append({"cells": {
            "finding": c.t("f_failed", id=b.get("id", ""), project=b.get("project_name", "")),
            "fix": c.t("f_failed_fix")},
            "actions": [{"title": c.t("open_logs"), "open": "logs", "args": {"build_id": str(b.get("id", ""))}}]})
    for r in offline:
        findings.append({"cells": {"finding": c.t("f_offline", name=r.get("name", "")), "fix": c.t("f_offline_fix")}})
    items = [{"type": "text", "title": c.t("summary"), "text": stats}]
    if findings:
        items.append({"type": "table", "title": c.t("summary"),
                      "columns": [{"key": "finding", "title": c.t("error")}, {"key": "fix", "title": "→"}],
                      "rows": findings})
    else:
        items.append({"type": "text", "text": c.t("no_findings")})
    return {"ok": True, "view": {"type": "sections", "items": items},
            "result": {"total": len(builds), "failed": len(failed), "runners_offline": len(offline)}}


OPS = {
    "builds": op_builds,
    "build_logs": op_build_logs,
    "build_trigger": op_build_trigger,
    "build_cancel": op_build_cancel,
    "summary": op_summary,
}
WRITE_OPS = {"build_trigger", "build_cancel"}


def manifest(lang):
    c = Ctx(lang)
    return {
        "protocol": 1, "name": "qdnder", "title": c.t("title"), "version": VERSION,
        "description": c.t("desc"),
        "ops": [
            {"name": "builds", "title": c.t("builds"), "read_only": True, "mcp": True,
             "description": "List qdnder image builds (newest first)",
             "input": {"type": "object", "properties": {}}},
            {"name": "build_logs", "title": c.t("logs"), "read_only": True, "mcp": True,
             "description": "Get the log and status of a build",
             "input": {"type": "object", "properties": {"build_id": {"type": "string"}}, "required": ["build_id"]}},
            {"name": "build_trigger", "title": c.t("build"), "mcp": True, "destructive": False,
             "description": "Queue an image build for a project",
             "input": {"type": "object", "properties": {
                 "project": {"type": "string"}, "ref": {"type": "string"}, "tag": {"type": "string"}},
                 "required": ["project"]}},
            {"name": "build_cancel", "title": c.t("cancel"), "mcp": True, "destructive": True,
             "description": "Cancel a queued or running build",
             "input": {"type": "object", "properties": {"build_id": {"type": "string"}}, "required": ["build_id"]}},
            {"name": "summary", "title": c.t("summary"), "read_only": True, "mcp": True,
             "description": "Build statistics and findings: failed builds, offline runners",
             "input": {"type": "object", "properties": {}}},
        ],
        "pages": [
            {"id": "builds", "title": c.t("builds"), "op": "builds"},
            {"id": "summary", "title": c.t("summary"), "op": "summary"},
            {"id": "logs", "title": c.t("logs"), "op": "build_logs", "hidden": True},
        ],
        "commands": [{"word": "qdnder", "usage": c.t("usage")}],
        "provides": [], "hub_actions": [],
    }


# --- Script command ----------------------------------------------------------------

def command(c, argv, dry_run):
    if not argv or argv[0] != "qdnder":
        raise c.fail("unknown_word", word=(argv[0] if argv else "—"))
    word = argv[1] if len(argv) > 1 else "builds"
    if word == "builds":
        res = op_builds(c, {})
        return {"ok": True, "text": c.t("cmd_builds_done", n=res["result"]["count"])}
    if word == "build":
        if len(argv) < 3:
            raise Fail(c.t("usage"))
        args = {"project": argv[2]}
        if len(argv) > 3:
            args["ref"] = argv[3]
        return op_build_trigger(c, args, dry_run)
    if word == "logs":
        if len(argv) != 3:
            raise Fail(c.t("usage"))
        res = op_build_logs(c, {"build_id": argv[2]})
        return {"ok": True, "text": res["view"]["text"]}
    if word == "cancel":
        if len(argv) != 3:
            raise Fail(c.t("usage"))
        return op_build_cancel(c, {"build_id": argv[2]}, dry_run)
    raise c.fail("unknown_word", word=word)


def check_command(c, argv):
    if not argv or argv[0] != "qdnder":
        raise c.fail("unknown_word", word=(argv[0] if argv else "—"))
    word = argv[1] if len(argv) > 1 else "builds"
    if word == "builds":
        return {"ok": True, "text": c.t("usage")}
    if word == "build":
        if len(argv) < 3:
            raise Fail(c.t("usage"))
        check_project(c, argv[2])
        if len(argv) > 3:
            check_ref(c, argv[3])
        return {"ok": True, "text": c.t("usage")}
    if word in ("logs", "cancel"):
        if len(argv) != 3:
            raise Fail(c.t("usage"))
        check_id(c, argv[2])
        return {"ok": True, "text": c.t("usage")}
    raise c.fail("unknown_word", word=word)


# --- HTTP --------------------------------------------------------------------------

def signed(headers, path, body):
    if not SECRET:
        return False
    ts = headers.get("X-NKT-Timestamp", "0")
    try:
        if abs(time.time() - int(ts)) >= 300:
            return False
    except ValueError:
        return False
    want = hmac.new(SECRET.encode(), f"{ts}.{path}.".encode() + body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(want, headers.get("X-NKT-Signature", ""))


class Handler(BaseHTTPRequestHandler):
    def reply(self, obj, code=200):
        raw = json.dumps(obj, ensure_ascii=False).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self):
        if self.path != "/nkt/manifest":
            return self.reply({"error": "not found"}, 404)
        if not signed(self.headers, self.path, b""):
            return self.reply({"error": "signature"}, 401)
        self.reply(manifest(self.headers.get("X-NKT-Lang", "ru")))

    def do_POST(self):
        if self.path != "/nkt/call":
            return self.reply({"error": "not found"}, 404)
        body = self.rfile.read(int(self.headers.get("Content-Length", 0) or 0))
        if not signed(self.headers, self.path, body):
            return self.reply({"error": "signature"}, 401)
        try:
            req = json.loads(body or b"{}")
        except ValueError:
            return self.reply({"ok": False, "error": "bad json"})
        c = Ctx(self.headers.get("X-NKT-Lang", "ru"))
        op = str(req.get("op", ""))
        args = req.get("args") or {}
        dry = bool(req.get("dry_run"))
        try:
            if op == "_connect":
                raise Fail(c.t("connect_unsupported"))
            if op == "_check":
                return self.reply(check_command(c, args.get("argv") or []))
            if op == "_command":
                return self.reply(command(c, args.get("argv") or [], dry))
            if op not in OPS:
                raise Fail(f"no op {op}")
            if op in WRITE_OPS:
                return self.reply(OPS[op](c, args, dry))
            return self.reply(OPS[op](c, args))
        except Fail as e:
            return self.reply({"ok": False, "error": str(e)})
        except Exception as e:  # noqa: BLE001 — ошибка для человека, без стектрейса
            log(f"op {op}: {type(e).__name__}")
            return self.reply({"ok": False, "error": str(e)})

    def log_message(self, fmt, *args):
        pass


if __name__ == "__main__":
    log(f"listening on {LISTEN}:{PORT}, api={API}")
    ThreadingHTTPServer((LISTEN, PORT), Handler).serve_forever()
