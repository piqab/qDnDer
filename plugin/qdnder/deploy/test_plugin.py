"""Тесты плагина qdnder на подделках: API подменяется, хаб имитируется
подписанными запросами. Запуск: python3 test_plugin.py"""
import hashlib
import hmac
import importlib.util
import json
import os
import threading
import time
import unittest
import urllib.error
import urllib.request
from http.server import ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location("qdnder_plugin", os.path.join(HERE, "nkt-plugin.py"))
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)

SECRET = "test-secret"
mod.SECRET = SECRET
mod.TOKEN = "test-token"
mod.API = "http://fake.invalid"

BUILDS = [
    {"id": "a1b2c3d4e5f60718", "project_name": "app", "ref": "main", "tag": "",
     "status": "success", "trigger": "api", "created_at": 1700000000, "log": "ok\n", "image_tags": ["reg/app:latest"]},
    {"id": "b2c3d4e5f6071829", "project_name": "app", "ref": "main", "tag": "",
     "status": "failed", "trigger": "webhook", "created_at": 1700000100, "log": "boom\n", "error": "exit 1"},
    {"id": "c3d4e5f607182930", "project_name": "web", "ref": "dev", "tag": "",
     "status": "running", "trigger": "api", "created_at": 1700000200, "log": ""},
]
RUNNERS = [{"name": "r1", "version": "0.1.0", "online": True, "last_seen": 1700000000},
           {"name": "r2", "version": "0.1.0", "online": False, "last_seen": 1699999000}]
PROJECTS = [{"name": "app"}, {"name": "web"}]

CALLS = []


def fake_api(c, method, path, body=None):
    CALLS.append((method, path, body))
    if path.startswith("/api/builds?"):
        return BUILDS
    if path.startswith("/api/builds/") and method == "GET":
        bid = path.rsplit("/", 1)[1]
        for b in BUILDS:
            if b["id"] == bid:
                return b
        return None
    if path == "/api/builds" and method == "POST":
        return {"id": "d4e5f60718293031", "status": "queued"}
    if path.endswith("/cancel"):
        return {"ok": True}
    if path == "/api/projects":
        return PROJECTS
    if path == "/api/runners":
        return RUNNERS
    raise AssertionError("unexpected api call: " + path)


mod.api = fake_api


class Server:
    def __init__(self):
        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), mod.Handler)
        self.port = self.httpd.server_address[1]
        self.thread = threading.Thread(target=self.httpd.serve_forever, daemon=True)
        self.thread.start()

    def stop(self):
        self.httpd.shutdown()

    def request(self, path, body=None, lang="ru", ts=None, sig=None):
        raw = json.dumps(body).encode() if body is not None else b""
        ts = str(int(time.time())) if ts is None else str(ts)
        if sig is None:
            sig = hmac.new(SECRET.encode(), f"{ts}.{path}.".encode() + raw, hashlib.sha256).hexdigest()
        req = urllib.request.Request("http://127.0.0.1:%d%s" % (self.port, path),
                                     data=raw if body is not None else None,
                                     method="POST" if body is not None else "GET",
                                     headers={"X-NKT-Timestamp": ts, "X-NKT-Signature": sig,
                                              "X-NKT-Lang": lang, "Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=10) as r:
                return r.status, json.loads(r.read())
        except urllib.error.HTTPError as e:
            return e.code, json.loads(e.read())

    def call(self, op, args=None, dry=False, lang="ru"):
        return self.request("/nkt/call", {"op": op, "args": args or {}, "dry_run": dry}, lang=lang)


class PluginTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.srv = Server()

    @classmethod
    def tearDownClass(cls):
        cls.srv.stop()

    def setUp(self):
        CALLS.clear()

    def test_version_matches_json(self):
        with open(os.path.join(HERE, "nkt-plugin.json"), encoding="utf-8") as f:
            manifest = json.load(f)
        self.assertEqual(manifest["version"], mod.VERSION)
        self.assertEqual(manifest["protocol"], 1)
        self.assertEqual(manifest["name"], "qdnder")

    def test_manifest_ru_en(self):
        for lang, word in (("ru", "Сборки"), ("en", "Builds")):
            code, m = self.srv.request("/nkt/manifest", lang=lang)
            self.assertEqual(code, 200)
            self.assertEqual(m["protocol"], 1)
            self.assertEqual(m["version"], mod.VERSION)
            titles = [p["title"] for p in m["pages"]]
            self.assertIn(word, titles)
            self.assertEqual(m["hub_actions"], [])
            words = [c["word"] for c in m["commands"]]
            self.assertEqual(words, ["qdnder"])
            ops = {o["name"]: o for o in m["ops"]}
            self.assertTrue(ops["builds"]["read_only"])
            self.assertTrue(ops["build_cancel"]["destructive"])

    def test_bad_signature(self):
        code, _ = self.srv.request("/nkt/manifest", sig="deadbeef")
        self.assertEqual(code, 401)

    def test_expired_timestamp(self):
        code, _ = self.srv.request("/nkt/manifest", ts=int(time.time()) - 600)
        self.assertEqual(code, 401)

    def test_builds_table(self):
        code, r = self.srv.call("builds")
        self.assertEqual(code, 200)
        self.assertTrue(r["ok"])
        v = r["view"]
        self.assertEqual(v["type"], "table")
        self.assertEqual(len(v["rows"]), 3)
        # running build has a destructive cancel action with confirm
        running = v["rows"][2]["actions"]
        cancel = [a for a in running if a.get("op") == "build_cancel"][0]
        self.assertTrue(cancel["danger"])
        self.assertIn("c3d4e5f607182930", cancel["confirm"])
        # finished build has no cancel
        self.assertFalse([a for a in v["rows"][0]["actions"] if a.get("op") == "build_cancel"])
        # top action opens the trigger form with project select
        form = v["actions"][0]
        self.assertEqual(form["op"], "build_trigger")
        self.assertEqual(form["fields"][0]["type"], "select")

    def test_logs_text_view(self):
        code, r = self.srv.call("build_logs", {"build_id": "b2c3d4e5f6071829"})
        self.assertTrue(r["ok"])
        self.assertEqual(r["view"]["type"], "text")
        self.assertIn("boom", r["view"]["text"])
        self.assertIn("exit 1", r["view"]["text"])

    def test_trigger_dry_run(self):
        code, r = self.srv.call("build_trigger", {"project": "app"}, dry=True)
        self.assertTrue(r["ok"])
        self.assertIn("сухой прогон", r["text"])
        self.assertFalse([c for c in CALLS if c[0] == "POST"])

    def test_trigger_real(self):
        code, r = self.srv.call("build_trigger", {"project": "app", "ref": "main", "tag": "v1.0.0"})
        self.assertTrue(r["ok"])
        posts = [c for c in CALLS if c[0] == "POST"]
        self.assertEqual(len(posts), 1)
        self.assertEqual(posts[0][1], "/api/builds")
        self.assertEqual(posts[0][2]["tag"], "v1.0.0")
        self.assertIn("d4e5f60718293031", r["text"])

    def test_trigger_validation(self):
        code, r = self.srv.call("build_trigger", {"project": "bad name!"})
        self.assertFalse(r["ok"])
        self.assertIn("проект", r["error"])

    def test_cancel(self):
        code, r = self.srv.call("build_cancel", {"build_id": "c3d4e5f607182930"})
        self.assertTrue(r["ok"])
        self.assertTrue(any(c[1].endswith("/cancel") for c in CALLS))
        code, r = self.srv.call("build_cancel", {"build_id": "zzz"})
        self.assertFalse(r["ok"])

    def test_summary_findings(self):
        code, r = self.srv.call("summary")
        self.assertTrue(r["ok"])
        self.assertEqual(r["result"]["failed"], 1)
        self.assertEqual(r["result"]["runners_offline"], 1)
        items = r["view"]["items"]
        self.assertEqual(items[0]["type"], "text")
        findings = items[1]["rows"]
        self.assertEqual(len(findings), 2)

    def test_command_check_and_dry(self):
        code, r = self.srv.call("_check", {"argv": ["qdnder", "build", "app"]})
        self.assertTrue(r["ok"])
        code, r = self.srv.call("_check", {"argv": ["qdnder", "build"]})
        self.assertFalse(r["ok"])
        code, r = self.srv.call("_command", {"argv": ["qdnder", "build", "app"]}, dry=True)
        self.assertTrue(r["ok"])
        self.assertFalse([c for c in CALLS if c[0] == "POST"])
        code, r = self.srv.call("_command", {"argv": ["qdnder", "logs", "a1b2c3d4e5f60718"]})
        self.assertTrue(r["ok"])
        self.assertIn("ok", r["text"])

    def test_english_texts(self):
        code, r = self.srv.call("build_trigger", {"project": "app"}, dry=True, lang="en")
        self.assertIn("dry run", r["text"])
        code, r = self.srv.call("build_trigger", {"project": "bad name!"}, lang="en")
        self.assertIn("project", r["error"])

    def test_connect_unsupported(self):
        code, r = self.srv.call("_connect", {"name": "qdnder"})
        self.assertFalse(r["ok"])
        self.assertIn("_connect", r["error"])


if __name__ == "__main__":
    unittest.main(verbosity=2)
