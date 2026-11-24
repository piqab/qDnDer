package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/piqab/qdnder/internal/auth"
	"github.com/piqab/qdnder/internal/store"
)

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hash, _ := auth.HashPassword("adminpass123")
	if err := st.CreateUser(&store.User{Username: "admin", PasswordHash: hash, Role: "admin"}); err != nil {
		t.Fatal(err)
	}
	s := &Server{Store: st, Sessions: auth.NewSessions(), RunnerToken: "qdnr_testtoken", Version: "test"}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

func do(t *testing.T, method, url, token string, body any, out any) int {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func adminToken(t *testing.T, s *Server, ts *httptest.Server) string {
	t.Helper()
	raw := auth.NewAPIToken()
	err := s.Store.CreateToken(&store.Token{Name: "test", Hash: auth.HashToken(raw), Role: "admin", UserID: "u"})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAuthRequired(t *testing.T) {
	_, ts := newTestServer(t)
	if code := do(t, "GET", ts.URL+"/api/builds", "", nil, nil); code != 401 {
		t.Fatalf("no auth: %d", code)
	}
	if code := do(t, "GET", ts.URL+"/api/builds", "bad", nil, nil); code != 401 {
		t.Fatalf("bad token: %d", code)
	}
	if code := do(t, "GET", ts.URL+"/api/health", "", nil, nil); code != 200 {
		t.Fatalf("health: %d", code)
	}
}

func TestFullBuildFlow(t *testing.T) {
	s, ts := newTestServer(t)
	tok := adminToken(t, s, ts)

	// Create project.
	var p store.Project
	code := do(t, "POST", ts.URL+"/api/projects", tok, map[string]any{
		"name": "app", "repo_url": "https://git.example.com/app.git",
		"image": "reg.example.com/app", "registry_user": "robot", "registry_pass": "secret",
	}, &p)
	if code != 201 {
		t.Fatalf("create project: %d", code)
	}
	if p.HasRegistryPass != true || p.RegistryPass != "" {
		t.Fatal("registry pass must be hidden but flagged")
	}

	// Enqueue build.
	var b store.Build
	if code := do(t, "POST", ts.URL+"/api/builds", tok, map[string]string{"project": "app"}, &b); code != 201 {
		t.Fatalf("create build: %d", code)
	}
	if b.Status != "queued" || b.Ref != "main" {
		t.Fatalf("build: %+v", b)
	}

	// Runner claims with the runner token.
	var claim struct {
		Build   store.Build    `json:"build"`
		Project map[string]any `json:"project"`
	}
	if code := do(t, "POST", ts.URL+"/api/runner/claim", "qdnr_testtoken",
		map[string]string{"name": "r1", "version": "test"}, &claim); code != 200 {
		t.Fatalf("claim: %d", code)
	}
	if claim.Build.ID != b.ID || claim.Project["registry_pass"] != "secret" {
		t.Fatalf("claim payload: %+v", claim)
	}

	// Runner posts logs and finishes.
	var logResp struct{ Cancel bool }
	if code := do(t, "POST", ts.URL+"/api/runner/log", "qdnr_testtoken",
		map[string]string{"build_id": b.ID, "text": "building...\n"}, &logResp); code != 200 || logResp.Cancel {
		t.Fatalf("log: %d %+v", code, logResp)
	}
	if code := do(t, "POST", ts.URL+"/api/runner/finish", "qdnr_testtoken", map[string]any{
		"build_id": b.ID, "status": "success", "image_tags": []string{"reg.example.com/app:latest"},
	}, nil); code != 200 {
		t.Fatalf("finish: %d", code)
	}

	var got store.Build
	if code := do(t, "GET", ts.URL+"/api/builds/"+b.ID, tok, nil, &got); code != 200 {
		t.Fatalf("get build: %d", code)
	}
	if got.Status != "success" || got.Log != "building...\n" || len(got.ImageTags) != 1 {
		t.Fatalf("final: %+v", got)
	}

	// Empty claim → 204.
	if code := do(t, "POST", ts.URL+"/api/runner/claim", "qdnr_testtoken",
		map[string]string{"name": "r1"}, nil); code != 204 {
		t.Fatalf("empty claim: %d", code)
	}
}

func TestRoleEnforcement(t *testing.T) {
	s, ts := newTestServer(t)
	viewer := auth.NewAPIToken()
	_ = s.Store.CreateToken(&store.Token{Name: "v", Hash: auth.HashToken(viewer), Role: "viewer", UserID: "u"})
	operator := auth.NewAPIToken()
	_ = s.Store.CreateToken(&store.Token{Name: "o", Hash: auth.HashToken(operator), Role: "operator", UserID: "u"})

	if code := do(t, "GET", ts.URL+"/api/users", viewer, nil, nil); code != 403 {
		t.Fatalf("viewer reads users: %d", code)
	}
	if code := do(t, "POST", ts.URL+"/api/builds", viewer, map[string]string{"project": "x"}, nil); code != 403 {
		t.Fatalf("viewer builds: %d", code)
	}
	if code := do(t, "GET", ts.URL+"/api/builds", viewer, nil, nil); code != 200 {
		t.Fatalf("viewer lists builds: %d", code)
	}
	if code := do(t, "POST", ts.URL+"/api/users", operator,
		map[string]string{"username": "x", "password": "longenough1", "role": "viewer"}, nil); code != 403 {
		t.Fatalf("operator creates users: %d", code)
	}
	// API token cannot use runner endpoints.
	if code := do(t, "POST", ts.URL+"/api/runner/claim", operator, map[string]string{"name": "r"}, nil); code != 403 {
		t.Fatalf("operator claims: %d", code)
	}
}

func TestLoginSession(t *testing.T) {
	_, ts := newTestServer(t)
	resp, err := http.Post(ts.URL+"/api/login", "application/json",
		bytes.NewBufferString(`{"username":"admin","password":"adminpass123"}`))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("login: %v %d", err, resp.StatusCode)
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "qdnder_session" {
			cookie = c
		}
	}
	resp.Body.Close()
	if cookie == nil {
		t.Fatal("session cookie missing")
	}
	if !cookie.Secure || !cookie.HttpOnly {
		t.Fatal("session cookie must be Secure and HttpOnly")
	}
	req, _ := http.NewRequest("GET", ts.URL+"/api/me", nil)
	req.AddCookie(cookie)
	r2, err := http.DefaultClient.Do(req)
	if err != nil || r2.StatusCode != 200 {
		t.Fatalf("me: %v %d", err, r2.StatusCode)
	}
	r2.Body.Close()
}

func TestCancelRunningBuildSignalsRunner(t *testing.T) {
	s, ts := newTestServer(t)
	tok := adminToken(t, s, ts)
	var p store.Project
	do(t, "POST", ts.URL+"/api/projects", tok, map[string]any{
		"name": "app", "repo_url": "https://x/app.git", "image": "reg/app"}, &p)
	var b store.Build
	do(t, "POST", ts.URL+"/api/builds", tok, map[string]string{"project": "app"}, &b)
	do(t, "POST", ts.URL+"/api/runner/claim", "qdnr_testtoken", map[string]string{"name": "r1"}, nil)
	if code := do(t, "POST", ts.URL+"/api/builds/"+b.ID+"/cancel", tok, nil, nil); code != 200 {
		t.Fatalf("cancel: %d", code)
	}
	var logResp struct{ Cancel bool }
	do(t, "POST", ts.URL+"/api/runner/log", "qdnr_testtoken",
		map[string]string{"build_id": b.ID, "text": "x"}, &logResp)
	if !logResp.Cancel {
		t.Fatal("runner must see cancel flag")
	}
}
