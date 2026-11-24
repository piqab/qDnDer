// Package store persists users, tokens, projects, builds and runners in
// SQLite (single host) or PostgreSQL (VPS / cluster). All IDs are random
// hex strings so the same schema works on both engines.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

// Store wraps the SQL database and its dialect.
type Store struct {
	DB       *sql.DB
	postgres bool
}

// Open opens the database by DSN. A DSN starting with "postgres://" or
// "postgresql://" selects PostgreSQL, anything else is a SQLite file path.
func Open(dsn string) (*Store, error) {
	driver, pg := "sqlite", false
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		driver, pg = "postgres", true
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	if !pg {
		// SQLite: single writer avoids SQLITE_BUSY under concurrent load.
		db.SetMaxOpenConns(1)
		if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
			return nil, err
		}
	}
	s := &Store{DB: db, postgres: pg}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.DB.Close() }

// rebind converts ? placeholders to $n for PostgreSQL.
func (s *Store) rebind(q string) string {
	if !s.postgres {
		return q
	}
	var b strings.Builder
	n := 0
	for _, r := range q {
		if r == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *Store) exec(q string, args ...any) (sql.Result, error) {
	return s.DB.Exec(s.rebind(q), args...)
}

func (s *Store) query(q string, args ...any) (*sql.Rows, error) {
	return s.DB.Query(s.rebind(q), args...)
}

func (s *Store) queryRow(q string, args ...any) *sql.Row {
	return s.DB.QueryRow(s.rebind(q), args...)
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS tokens (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			hash TEXT NOT NULL UNIQUE,
			role TEXT NOT NULL,
			user_id TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			last_used INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS projects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			repo_url TEXT NOT NULL,
			default_branch TEXT NOT NULL DEFAULT 'main',
			dockerfile TEXT NOT NULL DEFAULT 'Dockerfile',
			context TEXT NOT NULL DEFAULT '.',
			image TEXT NOT NULL,
			tags TEXT NOT NULL DEFAULT '["latest"]',
			build_args TEXT NOT NULL DEFAULT '{}',
			registry_user TEXT NOT NULL DEFAULT '',
			registry_pass TEXT NOT NULL DEFAULT '',
			git_user TEXT NOT NULL DEFAULT '',
			git_pass TEXT NOT NULL DEFAULT '',
			webhook_secret TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS builds (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			ref TEXT NOT NULL DEFAULT '',
			sha TEXT NOT NULL DEFAULT '',
			tag TEXT NOT NULL DEFAULT '',
			trigger TEXT NOT NULL DEFAULT 'manual',
			status TEXT NOT NULL DEFAULT 'queued',
			cancel_requested INTEGER NOT NULL DEFAULT 0,
			runner TEXT NOT NULL DEFAULT '',
			log TEXT NOT NULL DEFAULT '',
			image_tags TEXT NOT NULL DEFAULT '[]',
			error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			started_at INTEGER NOT NULL DEFAULT 0,
			finished_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS builds_status ON builds (status, created_at)`,
		`CREATE INDEX IF NOT EXISTS builds_project ON builds (project_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS runners (
			name TEXT PRIMARY KEY,
			version TEXT NOT NULL DEFAULT '',
			last_seen INTEGER NOT NULL,
			current_build TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	}
	for _, q := range stmts {
		if _, err := s.DB.Exec(q); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

// NewID returns a random 16-char hex identifier.
func NewID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// Now returns the current unix time in seconds.
func Now() int64 { return time.Now().Unix() }

// --- Users -----------------------------------------------------------------

// User is a local account with a role: admin, operator or viewer.
type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"`
	CreatedAt    int64  `json:"created_at"`
}

// ValidRole reports whether r is a known role.
func ValidRole(r string) bool { return r == "admin" || r == "operator" || r == "viewer" }

// CreateUser inserts a user.
func (s *Store) CreateUser(u *User) error {
	if u.ID == "" {
		u.ID = NewID()
	}
	if u.CreatedAt == 0 {
		u.CreatedAt = Now()
	}
	_, err := s.exec(`INSERT INTO users (id, username, password_hash, role, created_at) VALUES (?, ?, ?, ?, ?)`,
		u.ID, u.Username, u.PasswordHash, u.Role, u.CreatedAt)
	return err
}

// UserByName looks a user up by username; returns nil when absent.
func (s *Store) UserByName(name string) (*User, error) {
	u := &User{}
	err := s.queryRow(`SELECT id, username, password_hash, role, created_at FROM users WHERE username = ?`, name).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

// UserByID looks a user up by ID; returns nil when absent.
func (s *Store) UserByID(id string) (*User, error) {
	u := &User{}
	err := s.queryRow(`SELECT id, username, password_hash, role, created_at FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

// Users lists all users ordered by username.
func (s *Store) Users() ([]User, error) {
	rows, err := s.query(`SELECT id, username, password_hash, role, created_at FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CountUsers returns the number of accounts.
func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.queryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// SetPassword updates a user's password hash.
func (s *Store) SetPassword(id, hash string) error {
	_, err := s.exec(`UPDATE users SET password_hash = ? WHERE id = ?`, hash, id)
	return err
}

// DeleteUser removes a user.
func (s *Store) DeleteUser(id string) error {
	_, err := s.exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}

// --- API tokens --------------------------------------------------------------

// Token is a hashed API token with its own role.
type Token struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Hash      string `json:"-"`
	Role      string `json:"role"`
	UserID    string `json:"user_id"`
	CreatedAt int64  `json:"created_at"`
	LastUsed  int64  `json:"last_used"`
}

// CreateToken inserts a token record.
func (s *Store) CreateToken(t *Token) error {
	if t.ID == "" {
		t.ID = NewID()
	}
	if t.CreatedAt == 0 {
		t.CreatedAt = Now()
	}
	_, err := s.exec(`INSERT INTO tokens (id, name, hash, role, user_id, created_at, last_used) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		t.ID, t.Name, t.Hash, t.Role, t.UserID, t.CreatedAt)
	return err
}

// TokenByHash finds a token by SHA-256 hash; nil when absent.
func (s *Store) TokenByHash(hash string) (*Token, error) {
	t := &Token{}
	err := s.queryRow(`SELECT id, name, hash, role, user_id, created_at, last_used FROM tokens WHERE hash = ?`, hash).
		Scan(&t.ID, &t.Name, &t.Hash, &t.Role, &t.UserID, &t.CreatedAt, &t.LastUsed)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

// TouchToken records token usage time.
func (s *Store) TouchToken(id string) {
	_, _ = s.exec(`UPDATE tokens SET last_used = ? WHERE id = ?`, Now(), id)
}

// Tokens lists all tokens.
func (s *Store) Tokens() ([]Token, error) {
	rows, err := s.query(`SELECT id, name, hash, role, user_id, created_at, last_used FROM tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Token{}
	for rows.Next() {
		var t Token
		if err := rows.Scan(&t.ID, &t.Name, &t.Hash, &t.Role, &t.UserID, &t.CreatedAt, &t.LastUsed); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteToken removes a token.
func (s *Store) DeleteToken(id string) error {
	_, err := s.exec(`DELETE FROM tokens WHERE id = ?`, id)
	return err
}

// --- Projects ----------------------------------------------------------------

// Project describes what to build and where to push it.
type Project struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	RepoURL       string            `json:"repo_url"`
	DefaultBranch string            `json:"default_branch"`
	Dockerfile    string            `json:"dockerfile"`
	Context       string            `json:"context"`
	Image         string            `json:"image"`
	Tags          []string          `json:"tags"`
	BuildArgs     map[string]string `json:"build_args"`
	RegistryUser  string            `json:"registry_user"`
	RegistryPass  string            `json:"registry_pass,omitempty"`
	GitUser       string            `json:"git_user"`
	GitPass       string            `json:"git_pass,omitempty"`
	WebhookSecret string            `json:"webhook_secret,omitempty"`
	CreatedAt     int64             `json:"created_at"`
	// Derived flags for the UI; secrets themselves are never returned.
	HasRegistryPass  bool `json:"has_registry_pass"`
	HasGitPass       bool `json:"has_git_pass"`
	HasWebhookSecret bool `json:"has_webhook_secret"`
}

func (p *Project) flags() {
	p.HasRegistryPass = p.RegistryPass != ""
	p.HasGitPass = p.GitPass != ""
	p.HasWebhookSecret = p.WebhookSecret != ""
}

// MarshalJSON hides secrets in API responses; input decoding still accepts
// them (the UI sends them once and only sees the Has* flags back).
func (p Project) MarshalJSON() ([]byte, error) {
	type alias Project
	a := alias(p)
	a.RegistryPass = ""
	a.GitPass = ""
	a.WebhookSecret = ""
	return json.Marshal(a)
}

func marshalJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// CreateProject inserts a project.
func (s *Store) CreateProject(p *Project) error {
	if p.ID == "" {
		p.ID = NewID()
	}
	if p.CreatedAt == 0 {
		p.CreatedAt = Now()
	}
	if p.Tags == nil {
		p.Tags = []string{"latest"}
	}
	if p.BuildArgs == nil {
		p.BuildArgs = map[string]string{}
	}
	_, err := s.exec(`INSERT INTO projects
		(id, name, repo_url, default_branch, dockerfile, context, image, tags, build_args,
		 registry_user, registry_pass, git_user, git_pass, webhook_secret, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.RepoURL, p.DefaultBranch, p.Dockerfile, p.Context, p.Image,
		marshalJSON(p.Tags), marshalJSON(p.BuildArgs),
		p.RegistryUser, p.RegistryPass, p.GitUser, p.GitPass, p.WebhookSecret, p.CreatedAt)
	if err == nil {
		p.flags()
	}
	return err
}

const projectCols = `id, name, repo_url, default_branch, dockerfile, context, image, tags, build_args,
	registry_user, registry_pass, git_user, git_pass, webhook_secret, created_at`

func scanProject(scan func(...any) error) (*Project, error) {
	p := &Project{}
	var tags, args string
	err := scan(&p.ID, &p.Name, &p.RepoURL, &p.DefaultBranch, &p.Dockerfile, &p.Context, &p.Image,
		&tags, &args, &p.RegistryUser, &p.RegistryPass, &p.GitUser, &p.GitPass, &p.WebhookSecret, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(tags), &p.Tags)
	_ = json.Unmarshal([]byte(args), &p.BuildArgs)
	p.flags()
	return p, nil
}

// ProjectByID finds a project by ID; nil when absent.
func (s *Store) ProjectByID(id string) (*Project, error) {
	row := s.queryRow(`SELECT `+projectCols+` FROM projects WHERE id = ?`, id)
	p, err := scanProject(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

// ProjectByName finds a project by name; nil when absent.
func (s *Store) ProjectByName(name string) (*Project, error) {
	row := s.queryRow(`SELECT `+projectCols+` FROM projects WHERE name = ?`, name)
	p, err := scanProject(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

// Projects lists all projects ordered by name.
func (s *Store) Projects() ([]Project, error) {
	rows, err := s.query(`SELECT ` + projectCols + ` FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		p, err := scanProject(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// UpdateProject updates mutable project fields. Empty secret fields keep
// the previous values (the UI never receives secrets back).
func (s *Store) UpdateProject(p *Project) error {
	old, err := s.ProjectByID(p.ID)
	if err != nil || old == nil {
		return fmt.Errorf("project not found")
	}
	if p.RegistryPass == "" {
		p.RegistryPass = old.RegistryPass
	}
	if p.GitPass == "" {
		p.GitPass = old.GitPass
	}
	if p.WebhookSecret == "" {
		p.WebhookSecret = old.WebhookSecret
	}
	if p.Tags == nil {
		p.Tags = []string{"latest"}
	}
	if p.BuildArgs == nil {
		p.BuildArgs = map[string]string{}
	}
	_, err = s.exec(`UPDATE projects SET name=?, repo_url=?, default_branch=?, dockerfile=?, context=?,
		image=?, tags=?, build_args=?, registry_user=?, registry_pass=?, git_user=?, git_pass=?, webhook_secret=? WHERE id=?`,
		p.Name, p.RepoURL, p.DefaultBranch, p.Dockerfile, p.Context, p.Image,
		marshalJSON(p.Tags), marshalJSON(p.BuildArgs),
		p.RegistryUser, p.RegistryPass, p.GitUser, p.GitPass, p.WebhookSecret, p.ID)
	return err
}

// DeleteProject removes a project (build history stays).
func (s *Store) DeleteProject(id string) error {
	_, err := s.exec(`DELETE FROM projects WHERE id = ?`, id)
	return err
}

// --- Builds ------------------------------------------------------------------

// Build statuses.
const (
	BuildQueued   = "queued"
	BuildRunning  = "running"
	BuildSuccess  = "success"
	BuildFailed   = "failed"
	BuildCanceled = "canceled"
)

// Build is one image build job.
type Build struct {
	ID              string   `json:"id"`
	ProjectID       string   `json:"project_id"`
	ProjectName     string   `json:"project_name,omitempty"`
	Ref             string   `json:"ref"`
	SHA             string   `json:"sha"`
	Tag             string   `json:"tag"`
	Trigger         string   `json:"trigger"`
	Status          string   `json:"status"`
	CancelRequested bool     `json:"cancel_requested"`
	Runner          string   `json:"runner"`
	Log             string   `json:"log,omitempty"`
	ImageTags       []string `json:"image_tags"`
	Error           string   `json:"error,omitempty"`
	CreatedAt       int64    `json:"created_at"`
	StartedAt       int64    `json:"started_at"`
	FinishedAt      int64    `json:"finished_at"`
}

// Done reports whether the build reached a terminal state.
func (b *Build) Done() bool {
	return b.Status == BuildSuccess || b.Status == BuildFailed || b.Status == BuildCanceled
}

// CreateBuild enqueues a build.
func (s *Store) CreateBuild(b *Build) error {
	if b.ID == "" {
		b.ID = NewID()
	}
	if b.CreatedAt == 0 {
		b.CreatedAt = Now()
	}
	b.Status = BuildQueued
	if b.ImageTags == nil {
		b.ImageTags = []string{}
	}
	_, err := s.exec(`INSERT INTO builds
		(id, project_id, ref, sha, tag, trigger, status, cancel_requested, runner, log, image_tags, error, created_at, started_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, '', '', '[]', '', ?, 0, 0)`,
		b.ID, b.ProjectID, b.Ref, b.SHA, b.Tag, b.Trigger, b.Status, b.CreatedAt)
	return err
}

const buildCols = `b.id, b.project_id, COALESCE(p.name, ''), b.ref, b.sha, b.tag, b.trigger, b.status,
	b.cancel_requested, b.runner, b.image_tags, b.error, b.created_at, b.started_at, b.finished_at`

func scanBuild(scan func(...any) error, withLog bool, log *string) (*Build, error) {
	b := &Build{}
	var cancel int
	var tags string
	dst := []any{&b.ID, &b.ProjectID, &b.ProjectName, &b.Ref, &b.SHA, &b.Tag, &b.Trigger, &b.Status,
		&cancel, &b.Runner, &tags, &b.Error, &b.CreatedAt, &b.StartedAt, &b.FinishedAt}
	if withLog {
		dst = append(dst, log)
	}
	if err := scan(dst...); err != nil {
		return nil, err
	}
	b.CancelRequested = cancel != 0
	_ = json.Unmarshal([]byte(tags), &b.ImageTags)
	return b, nil
}

// BuildByID loads a build including its log; nil when absent.
func (s *Store) BuildByID(id string) (*Build, error) {
	row := s.queryRow(`SELECT `+buildCols+`, b.log FROM builds b LEFT JOIN projects p ON p.id = b.project_id WHERE b.id = ?`, id)
	var log string
	b, err := scanBuild(row.Scan, true, &log)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if b != nil {
		b.Log = log
	}
	return b, err
}

// Builds lists builds (without logs), newest first, optionally filtered.
func (s *Store) Builds(projectID, status string, limit int) ([]Build, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT ` + buildCols + ` FROM builds b LEFT JOIN projects p ON p.id = b.project_id WHERE 1=1`
	args := []any{}
	if projectID != "" {
		q += ` AND b.project_id = ?`
		args = append(args, projectID)
	}
	if status != "" {
		q += ` AND b.status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY b.created_at DESC, b.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Build{}
	for rows.Next() {
		b, err := scanBuild(rows.Scan, false, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// ClaimBuild atomically takes the oldest queued build for a runner.
// Returns nil when the queue is empty.
func (s *Store) ClaimBuild(runner string) (*Build, error) {
	for i := 0; i < 5; i++ {
		var id string
		err := s.queryRow(`SELECT id FROM builds WHERE status = ? ORDER BY created_at, id LIMIT 1`, BuildQueued).Scan(&id)
		if err == sql.ErrNoRows {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		res, err := s.exec(`UPDATE builds SET status = ?, runner = ?, started_at = ? WHERE id = ? AND status = ?`,
			BuildRunning, runner, Now(), id, BuildQueued)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			return s.BuildByID(id)
		}
	}
	return nil, nil
}

// AppendLog appends text to the build log and reports whether cancellation
// was requested.
func (s *Store) AppendLog(id, text string) (bool, error) {
	_, err := s.exec(`UPDATE builds SET log = log || ? WHERE id = ?`, text, id)
	if err != nil {
		return false, err
	}
	var cancel int
	var status string
	err = s.queryRow(`SELECT cancel_requested, status FROM builds WHERE id = ?`, id).Scan(&cancel, &status)
	if err != nil {
		return false, err
	}
	return cancel != 0 || status == BuildCanceled, nil
}

// RequestCancel marks a build for cancellation. Queued builds are canceled
// immediately; running ones are stopped by the runner on its next log post.
func (s *Store) RequestCancel(id string) error {
	b, err := s.BuildByID(id)
	if err != nil || b == nil {
		return fmt.Errorf("build not found")
	}
	if b.Done() {
		return fmt.Errorf("build already finished")
	}
	if b.Status == BuildQueued {
		_, err = s.exec(`UPDATE builds SET status = ?, finished_at = ?, error = 'canceled before start' WHERE id = ?`,
			BuildCanceled, Now(), id)
		return err
	}
	_, err = s.exec(`UPDATE builds SET cancel_requested = 1 WHERE id = ?`, id)
	return err
}

// FinishBuild sets the terminal state of a build.
func (s *Store) FinishBuild(id, status string, imageTags []string, errMsg string) error {
	if status != BuildSuccess && status != BuildFailed && status != BuildCanceled {
		return fmt.Errorf("bad status %q", status)
	}
	if imageTags == nil {
		imageTags = []string{}
	}
	_, err := s.exec(`UPDATE builds SET status = ?, image_tags = ?, error = ?, finished_at = ? WHERE id = ?`,
		status, marshalJSON(imageTags), errMsg, Now(), id)
	return err
}

// QueueSize returns the number of queued builds.
func (s *Store) QueueSize() (int, error) {
	var n int
	err := s.queryRow(`SELECT COUNT(*) FROM builds WHERE status = ?`, BuildQueued).Scan(&n)
	return n, err
}

// --- Runners -----------------------------------------------------------------

// Runner is a registered worker heartbeat.
type Runner struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	LastSeen     int64  `json:"last_seen"`
	CurrentBuild string `json:"current_build"`
	Online       bool   `json:"online"`
}

// TouchRunner upserts a runner heartbeat.
func (s *Store) TouchRunner(name, version, currentBuild string) error {
	_, err := s.exec(`INSERT INTO runners (name, version, last_seen, current_build) VALUES (?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET version = excluded.version, last_seen = excluded.last_seen, current_build = excluded.current_build`,
		name, version, Now(), currentBuild)
	return err
}

// Runners lists runners, marking ones seen in the last 60 seconds online.
func (s *Store) Runners() ([]Runner, error) {
	rows, err := s.query(`SELECT name, version, last_seen, current_build FROM runners ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Runner{}
	cutoff := Now() - 60
	for rows.Next() {
		var r Runner
		if err := rows.Scan(&r.Name, &r.Version, &r.LastSeen, &r.CurrentBuild); err != nil {
			return nil, err
		}
		r.Online = r.LastSeen >= cutoff
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- Settings ----------------------------------------------------------------

// Setting gets a settings value ("" when absent).
func (s *Store) Setting(key string) (string, error) {
	var v string
	err := s.queryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// SetSetting upserts a settings value.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
