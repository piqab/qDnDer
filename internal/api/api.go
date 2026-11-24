// Package api exposes the REST API: auth, users, tokens, projects, builds,
// runners and the runner-facing endpoints. Browser sessions use a cookie;
// scripts and runners use Bearer tokens.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/qdnder/internal/auth"
	"github.com/piqab/qdnder/internal/mcp"
	"github.com/piqab/qdnder/internal/store"
	"github.com/piqab/qdnder/internal/webhook"
)

// Server holds API dependencies.
type Server struct {
	Store       *store.Store
	Sessions    *auth.Sessions
	RunnerToken string
	Version     string
	UI          http.Handler // optional embedded web UI, served at /
}

type ctxKey int

const principalKey ctxKey = 1

type principal struct {
	UserID   string
	Username string
	Role     string // admin | operator | viewer | runner
}

var roleRank = map[string]int{"viewer": 1, "operator": 2, "admin": 3}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	return dec.Decode(v)
}

// Handler builds the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "version": s.Version})
	})
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)

	authed := func(min string, h http.HandlerFunc) http.HandlerFunc {
		return s.withAuth(min, h)
	}

	mux.HandleFunc("GET /api/me", authed("viewer", s.me))

	mux.HandleFunc("GET /api/users", authed("admin", s.listUsers))
	mux.HandleFunc("POST /api/users", authed("admin", s.createUser))
	mux.HandleFunc("DELETE /api/users/{id}", authed("admin", s.deleteUser))
	mux.HandleFunc("PUT /api/users/{id}/password", authed("admin", s.setPassword))

	mux.HandleFunc("GET /api/tokens", authed("admin", s.listTokens))
	mux.HandleFunc("POST /api/tokens", authed("admin", s.createToken))
	mux.HandleFunc("DELETE /api/tokens/{id}", authed("admin", s.deleteToken))

	mux.HandleFunc("GET /api/projects", authed("viewer", s.listProjects))
	mux.HandleFunc("POST /api/projects", authed("operator", s.createProject))
	mux.HandleFunc("GET /api/projects/{id}", authed("viewer", s.getProject))
	mux.HandleFunc("PUT /api/projects/{id}", authed("operator", s.updateProject))
	mux.HandleFunc("DELETE /api/projects/{id}", authed("admin", s.deleteProject))

	mux.HandleFunc("GET /api/builds", authed("viewer", s.listBuilds))
	mux.HandleFunc("POST /api/builds", authed("operator", s.createBuild))
	mux.HandleFunc("GET /api/builds/{id}", authed("viewer", s.getBuild))
	mux.HandleFunc("POST /api/builds/{id}/cancel", authed("operator", s.cancelBuild))
	mux.HandleFunc("GET /api/builds/{id}/logs", authed("viewer", s.buildLogs))
	mux.HandleFunc("GET /api/builds/{id}/events", authed("viewer", s.buildEvents))

	mux.HandleFunc("GET /api/runners", authed("viewer", s.listRunners))
	mux.HandleFunc("GET /api/settings", authed("admin", s.getSettings))
	mux.HandleFunc("POST /api/settings/rotate-runner-token", authed("admin", s.rotateRunnerToken))

	mux.HandleFunc("POST /api/runner/claim", authed("runner", s.runnerClaim))
	mux.HandleFunc("POST /api/runner/log", authed("runner", s.runnerLog))
	mux.HandleFunc("POST /api/runner/finish", authed("runner", s.runnerFinish))
	mux.HandleFunc("POST /api/runner/heartbeat", authed("runner", s.runnerHeartbeat))

	// Webhooks are authenticated by the per-project secret, not by tokens.
	mux.HandleFunc("POST /api/webhook/{project}", s.webhook)

	mcpSrv := &mcp.Server{
		Store:   s.Store,
		Enqueue: s.EnqueueBuild,
		Resolve: func(r *http.Request) string {
			if p := s.resolve(r); p != nil {
				return p.Role
			}
			return ""
		},
		Version: s.Version,
	}
	mux.Handle("POST /mcp", mcpSrv.Handler())

	if s.UI != nil {
		mux.Handle("GET /", s.UI)
	}

	return mux
}

// webhook verifies the provider signature and queues a build on push.
func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("project")
	p, err := s.Store.ProjectByName(ref)
	if err != nil || p == nil {
		p, err = s.Store.ProjectByID(ref)
	}
	if err != nil || p == nil {
		fail(w, 404, "project not found")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if err != nil {
		fail(w, 400, "bad body")
		return
	}
	ev, err := webhook.Parse(r.Header, body, p.WebhookSecret)
	if err != nil {
		fail(w, 403, err.Error())
		return
	}
	if ev == nil {
		writeJSON(w, 200, map[string]string{"ok": "ignored"})
		return
	}
	b, err := s.EnqueueBuild(p.Name, ev.Ref, ev.SHA, ev.Tag, "webhook")
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, b)
}

// withAuth resolves the principal (Bearer token or session cookie) and
// enforces the minimum role. The runner role only passes "runner" routes.
func (s *Server) withAuth(min string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := s.resolve(r)
		if p == nil {
			fail(w, 401, "authentication required")
			return
		}
		if min == "runner" {
			if p.Role != "runner" && p.Role != "admin" {
				fail(w, 403, "runner token required")
				return
			}
		} else {
			if p.Role == "runner" || roleRank[p.Role] < roleRank[min] {
				fail(w, 403, "insufficient role")
				return
			}
		}
		h(w, r.WithContext(context.WithValue(r.Context(), principalKey, p)))
	}
}

func (s *Server) resolve(r *http.Request) *principal {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok := strings.TrimPrefix(h, "Bearer ")
		if s.RunnerToken != "" && auth.ConstantEqual(tok, s.RunnerToken) {
			return &principal{Username: "runner", Role: "runner"}
		}
		t, err := s.Store.TokenByHash(auth.HashToken(tok))
		if err == nil && t != nil {
			s.Store.TouchToken(t.ID)
			return &principal{UserID: t.UserID, Username: "token:" + t.Name, Role: t.Role}
		}
		return nil
	}
	if c, err := r.Cookie("qdnder_session"); err == nil {
		if sess := s.Sessions.Get(c.Value); sess != nil {
			return &principal{UserID: sess.UserID, Username: sess.Username, Role: sess.Role}
		}
	}
	return nil
}

func getPrincipal(r *http.Request) *principal {
	p, _ := r.Context().Value(principalKey).(*principal)
	return p
}

// --- Auth --------------------------------------------------------------------

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct{ Username, Password string }
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, "bad json")
		return
	}
	u, err := s.Store.UserByName(req.Username)
	if err != nil || u == nil || !auth.CheckPassword(req.Password, u.PasswordHash) {
		fail(w, 401, "wrong username or password")
		return
	}
	id := s.Sessions.Create(u.ID, u.Username, u.Role, 7*24*time.Hour)
	http.SetCookie(w, &http.Cookie{
		Name: "qdnder_session", Value: id, Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: 7 * 24 * 3600,
	})
	writeJSON(w, 200, map[string]string{"username": u.Username, "role": u.Role})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("qdnder_session"); err == nil {
		s.Sessions.Delete(c.Value)
	}
	// Clearing cookie must carry the same attributes as the session cookie,
	// otherwise the browser may keep the original one.
	http.SetCookie(w, &http.Cookie{
		Name: "qdnder_session", Value: "", Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	p := getPrincipal(r)
	writeJSON(w, 200, map[string]string{"username": p.Username, "role": p.Role})
}

// --- Users -------------------------------------------------------------------

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.Store.Users()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, users)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req struct{ Username, Password, Role string }
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, "bad json")
		return
	}
	if len(req.Username) < 2 || len(req.Password) < 8 || !store.ValidRole(req.Role) {
		fail(w, 400, "username >= 2 chars, password >= 8 chars, role admin|operator|viewer")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	u := &store.User{Username: req.Username, PasswordHash: hash, Role: req.Role}
	if err := s.Store.CreateUser(u); err != nil {
		fail(w, 409, "username already exists")
		return
	}
	writeJSON(w, 201, u)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if p := getPrincipal(r); p.UserID == id {
		fail(w, 400, "cannot delete yourself")
		return
	}
	if err := s.Store.DeleteUser(id); err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) setPassword(w http.ResponseWriter, r *http.Request) {
	var req struct{ Password string }
	if err := readJSON(r, &req); err != nil || len(req.Password) < 8 {
		fail(w, 400, "password >= 8 chars")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if err := s.Store.SetPassword(r.PathValue("id"), hash); err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// --- Tokens ------------------------------------------------------------------

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.Store.Tokens()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, tokens)
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name, Role string }
	if err := readJSON(r, &req); err != nil || req.Name == "" || !store.ValidRole(req.Role) {
		fail(w, 400, "name required, role admin|operator|viewer")
		return
	}
	raw := auth.NewAPIToken()
	t := &store.Token{Name: req.Name, Hash: auth.HashToken(raw), Role: req.Role, UserID: getPrincipal(r).UserID}
	if err := s.Store.CreateToken(t); err != nil {
		fail(w, 500, err.Error())
		return
	}
	// The raw token is shown exactly once.
	writeJSON(w, 201, map[string]any{"id": t.ID, "name": t.Name, "role": t.Role, "token": raw})
}

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteToken(r.PathValue("id")); err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// --- Projects ------------------------------------------------------------------

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.Store.Projects()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, projects)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.Store.ProjectByID(r.PathValue("id"))
	if err != nil || p == nil {
		fail(w, 404, "project not found")
		return
	}
	writeJSON(w, 200, p)
}

func validateProject(p *store.Project) string {
	if p.Name == "" || p.RepoURL == "" || p.Image == "" {
		return "name, repo_url and image are required"
	}
	if !strings.HasPrefix(p.RepoURL, "https://") && !strings.HasPrefix(p.RepoURL, "http://") &&
		!strings.HasPrefix(p.RepoURL, "git@") && !strings.HasPrefix(p.RepoURL, "ssh://") {
		return "repo_url must be an http(s), git@ or ssh:// URL"
	}
	if p.DefaultBranch == "" {
		p.DefaultBranch = "main"
	}
	if p.Dockerfile == "" {
		p.Dockerfile = "Dockerfile"
	}
	if p.Context == "" {
		p.Context = "."
	}
	return ""
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var p store.Project
	if err := readJSON(r, &p); err != nil {
		fail(w, 400, "bad json")
		return
	}
	if msg := validateProject(&p); msg != "" {
		fail(w, 400, msg)
		return
	}
	if err := s.Store.CreateProject(&p); err != nil {
		fail(w, 409, "project name already exists")
		return
	}
	writeJSON(w, 201, p)
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	var p store.Project
	if err := readJSON(r, &p); err != nil {
		fail(w, 400, "bad json")
		return
	}
	p.ID = r.PathValue("id")
	if msg := validateProject(&p); msg != "" {
		fail(w, 400, msg)
		return
	}
	if err := s.Store.UpdateProject(&p); err != nil {
		fail(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteProject(r.PathValue("id")); err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// --- Builds --------------------------------------------------------------------

func (s *Server) listBuilds(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	builds, err := s.Store.Builds(r.URL.Query().Get("project_id"), r.URL.Query().Get("status"), limit)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, builds)
}

// EnqueueBuild resolves the project and queues a build; shared with webhook
// and MCP handlers.
func (s *Server) EnqueueBuild(projectRef, ref, sha, tag, trigger string) (*store.Build, error) {
	p, err := s.Store.ProjectByName(projectRef)
	if err != nil {
		return nil, err
	}
	if p == nil {
		p, err = s.Store.ProjectByID(projectRef)
		if err != nil {
			return nil, err
		}
	}
	if p == nil {
		return nil, fmt.Errorf("project %q not found", projectRef)
	}
	if ref == "" {
		ref = p.DefaultBranch
	}
	b := &store.Build{ProjectID: p.ID, Ref: ref, SHA: sha, Tag: tag, Trigger: trigger}
	if err := s.Store.CreateBuild(b); err != nil {
		return nil, err
	}
	b.ProjectName = p.Name
	return b, nil
}

func (s *Server) createBuild(w http.ResponseWriter, r *http.Request) {
	var req struct{ Project, Ref, Tag string }
	if err := readJSON(r, &req); err != nil || req.Project == "" {
		fail(w, 400, "project is required")
		return
	}
	b, err := s.EnqueueBuild(req.Project, req.Ref, "", req.Tag, "api")
	if err != nil {
		fail(w, 404, err.Error())
		return
	}
	writeJSON(w, 201, b)
}

func (s *Server) getBuild(w http.ResponseWriter, r *http.Request) {
	b, err := s.Store.BuildByID(r.PathValue("id"))
	if err != nil || b == nil {
		fail(w, 404, "build not found")
		return
	}
	writeJSON(w, 200, b)
}

func (s *Server) cancelBuild(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.RequestCancel(r.PathValue("id")); err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) buildLogs(w http.ResponseWriter, r *http.Request) {
	b, err := s.Store.BuildByID(r.PathValue("id"))
	if err != nil || b == nil {
		fail(w, 404, "build not found")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, b.Log)
}

// buildEvents streams log deltas and status changes as SSE until the build
// finishes.
func (s *Server) buildEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, "streaming unsupported")
		return
	}
	id := r.PathValue("id")
	b, err := s.Store.BuildByID(id)
	if err != nil || b == nil {
		fail(w, 404, "build not found")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	sent := 0
	lastStatus := ""
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		b, err := s.Store.BuildByID(id)
		if err != nil || b == nil {
			return
		}
		if len(b.Log) > sent {
			data, _ := json.Marshal(b.Log[sent:])
			fmt.Fprintf(w, "event: log\ndata: %s\n\n", data)
			sent = len(b.Log)
		}
		if b.Status != lastStatus {
			data, _ := json.Marshal(map[string]any{"status": b.Status, "image_tags": b.ImageTags, "error": b.Error})
			fmt.Fprintf(w, "event: status\ndata: %s\n\n", data)
			lastStatus = b.Status
		}
		flusher.Flush()
		if b.Done() {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

// --- Runners and settings --------------------------------------------------------

func (s *Server) listRunners(w http.ResponseWriter, r *http.Request) {
	runners, err := s.Store.Runners()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, runners)
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	n, _ := s.Store.QueueSize()
	writeJSON(w, 200, map[string]any{
		"runner_token": s.RunnerToken,
		"queue_size":   n,
		"version":      s.Version,
	})
}

func (s *Server) rotateRunnerToken(w http.ResponseWriter, r *http.Request) {
	tok := "qdnr_" + auth.RandomSecret(24)
	if err := s.Store.SetSetting("runner_token", tok); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.RunnerToken = tok
	writeJSON(w, 200, map[string]string{"runner_token": tok})
}

// --- Runner-facing endpoints -------------------------------------------------------

// runnerClaim hands the oldest queued build to a runner together with the
// full project settings (including secrets — the runner role is trusted).
func (s *Server) runnerClaim(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := readJSON(r, &req); err != nil || req.Name == "" {
		fail(w, 400, "runner name required")
		return
	}
	b, err := s.Store.ClaimBuild(req.Name)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	_ = s.Store.TouchRunner(req.Name, req.Version, "")
	if b == nil {
		w.WriteHeader(204)
		return
	}
	p, err := s.Store.ProjectByID(b.ProjectID)
	if err != nil || p == nil {
		_ = s.Store.FinishBuild(b.ID, store.BuildFailed, nil, "project deleted")
		w.WriteHeader(204)
		return
	}
	_ = s.Store.TouchRunner(req.Name, req.Version, b.ID)
	writeJSON(w, 200, map[string]any{"build": b, "project": map[string]any{
		"id": p.ID, "name": p.Name, "repo_url": p.RepoURL, "default_branch": p.DefaultBranch,
		"dockerfile": p.Dockerfile, "context": p.Context, "image": p.Image,
		"tags": p.Tags, "build_args": p.BuildArgs,
		"registry_user": p.RegistryUser, "registry_pass": p.RegistryPass,
		"git_user": p.GitUser, "git_pass": p.GitPass,
	}})
}

func (s *Server) runnerLog(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BuildID string `json:"build_id"`
		Text    string `json:"text"`
	}
	if err := readJSON(r, &req); err != nil || req.BuildID == "" {
		fail(w, 400, "build_id required")
		return
	}
	cancel, err := s.Store.AppendLog(req.BuildID, req.Text)
	if err != nil {
		fail(w, 404, "build not found")
		return
	}
	writeJSON(w, 200, map[string]bool{"cancel": cancel})
}

func (s *Server) runnerFinish(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BuildID   string   `json:"build_id"`
		Status    string   `json:"status"`
		ImageTags []string `json:"image_tags"`
		Error     string   `json:"error"`
		Runner    string   `json:"runner"`
	}
	if err := readJSON(r, &req); err != nil || req.BuildID == "" {
		fail(w, 400, "build_id required")
		return
	}
	if err := s.Store.FinishBuild(req.BuildID, req.Status, req.ImageTags, req.Error); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if req.Runner != "" {
		_ = s.Store.TouchRunner(req.Runner, "", "")
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) runnerHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string `json:"name"`
		Version      string `json:"version"`
		CurrentBuild string `json:"current_build"`
	}
	if err := readJSON(r, &req); err != nil || req.Name == "" {
		fail(w, 400, "runner name required")
		return
	}
	if err := s.Store.TouchRunner(req.Name, req.Version, req.CurrentBuild); err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
