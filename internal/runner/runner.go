// Package runner implements the qdnder worker: it claims queued builds from
// the server, clones the repository, builds the image with rootless BuildKit
// (buildctl) and pushes it to the external registry, streaming logs back.
package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/piqab/qdnder/internal/pipeline"
	"github.com/piqab/qdnder/internal/store"
)

// ProjectSettings is the project payload handed to the runner on claim.
type ProjectSettings struct {
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
	RegistryPass  string            `json:"registry_pass"`
	GitUser       string            `json:"git_user"`
	GitPass       string            `json:"git_pass"`
}

// Job is one claimed build with its project settings.
type Job struct {
	Build   store.Build     `json:"build"`
	Project ProjectSettings `json:"project"`
}

// Client talks to the server runner API.
type Client struct {
	Base  string
	Token string
	HTTP  *http.Client
}

// NewClient creates a runner API client.
func NewClient(base, token string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) post(ctx context.Context, path string, body, out any) (int, error) {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", c.Base+path, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode == 200 {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, err
		}
	} else {
		_, _ = io.Copy(io.Discard, resp.Body)
	}
	return resp.StatusCode, nil
}

// Claim asks for the next queued build; nil job means the queue is empty.
func (c *Client) Claim(ctx context.Context, name, version string) (*Job, error) {
	var job Job
	code, err := c.post(ctx, "/api/runner/claim", map[string]string{"name": name, "version": version}, &job)
	if err != nil || code == 204 {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("claim: HTTP %d", code)
	}
	return &job, nil
}

// Log appends log text and reports whether the build was canceled.
func (c *Client) Log(ctx context.Context, buildID, text string) (bool, error) {
	var resp struct {
		Cancel bool `json:"cancel"`
	}
	if _, err := c.post(ctx, "/api/runner/log", map[string]string{"build_id": buildID, "text": text}, &resp); err != nil {
		return false, err
	}
	return resp.Cancel, nil
}

// Finish reports the terminal build state.
func (c *Client) Finish(ctx context.Context, buildID, status string, tags []string, errMsg, runnerName string) error {
	_, err := c.post(ctx, "/api/runner/finish", map[string]any{
		"build_id": buildID, "status": status, "image_tags": tags, "error": errMsg, "runner": runnerName,
	}, nil)
	return err
}

// Heartbeat keeps the runner visible in the admin panel.
func (c *Client) Heartbeat(ctx context.Context, name, version, currentBuild string) error {
	_, err := c.post(ctx, "/api/runner/heartbeat", map[string]string{
		"name": name, "version": version, "current_build": currentBuild,
	}, nil)
	return err
}

// Runner polls the server and executes builds.
type Runner struct {
	Name         string
	Version      string
	Client       *Client
	BuildkitHost string
	WorkDir      string
	PollInterval time.Duration
	FlushEvery   time.Duration
	cur          atomic.Value // current build id for heartbeats
}

// Run polls for builds until ctx is canceled.
func (r *Runner) Run(ctx context.Context) error {
	if r.PollInterval <= 0 {
		r.PollInterval = 2 * time.Second
	}
	if r.FlushEvery <= 0 {
		r.FlushEvery = 500 * time.Millisecond
	}
	r.cur.Store("")
	go r.heartbeatLoop(ctx)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		job, err := r.Client.Claim(ctx, r.Name, r.Version)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			fmt.Fprintf(os.Stderr, "qdnder runner: claim: %v\n", err)
		}
		if job != nil {
			r.execute(ctx, job)
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(r.PollInterval):
		}
	}
}

func (r *Runner) heartbeatLoop(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		cur, _ := r.cur.Load().(string)
		_ = r.Client.Heartbeat(ctx, r.Name, r.Version, cur)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// logSink batches log lines and posts them to the server, watching for the
// cancel flag on every flush.
type logSink struct {
	mu        sync.Mutex
	client    *Client
	buildID   string
	buf       strings.Builder
	lastFlush time.Time
	every     time.Duration
	canceled  bool
}

func (l *logSink) write(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf.WriteString(line)
	l.buf.WriteByte('\n')
	if l.buf.Len() >= 4096 || time.Since(l.lastFlush) >= l.every {
		l.flushLocked()
	}
}

func (l *logSink) flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.flushLocked()
}

func (l *logSink) isCanceled() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.canceled
}

func (l *logSink) flushLocked() {
	// Post even an empty chunk: this is also the cancel poll for quiet
	// processes that produce no output for a while.
	text := l.buf.String()
	l.buf.Reset()
	l.lastFlush = time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if canceled, err := l.client.Log(ctx, l.buildID, text); err == nil && canceled {
		l.canceled = true
	}
}

// execute runs one build end to end and reports the result to the server.
func (r *Runner) execute(ctx context.Context, job *Job) {
	b, p := job.Build, job.Project
	r.cur.Store(b.ID)
	defer r.cur.Store("")
	sink := &logSink{client: r.Client, buildID: b.ID, every: r.FlushEvery, lastFlush: time.Now()}
	sink.write(fmt.Sprintf("=== build %s: %s ref=%s trigger=%s ===", b.ID, p.Name, b.Ref, b.Trigger))

	status, tags, err := r.build(ctx, job, sink)
	sink.flush()
	if err != nil {
		sink.write("ERROR: " + err.Error())
		sink.flush()
	}
	finishCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = r.Client.Finish(finishCtx, b.ID, status, tags, errString(err), r.Name)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// build performs clone → buildctl → push, streaming output into the sink.
func (r *Runner) build(ctx context.Context, job *Job, sink *logSink) (string, []string, error) {
	b, p := job.Build, job.Project
	work, err := os.MkdirTemp(r.WorkDir, "qdnder-"+b.ID+"-")
	if err != nil {
		return store.BuildFailed, nil, err
	}
	defer os.RemoveAll(work)
	repoDir := filepath.Join(work, "src")

	// Clone (credentials are injected into the URL but never logged).
	sink.write("clone " + sanitizeURL(p.RepoURL) + " ref=" + b.Ref)
	if err := r.gitClone(ctx, p, b, repoDir, sink); err != nil {
		return store.BuildFailed, nil, fmt.Errorf("git: %w", err)
	}
	if sink.isCanceled() {
		return store.BuildCanceled, nil, fmt.Errorf("canceled by user")
	}

	// Optional qdnder.yaml overrides project settings and adds steps.
	spec, err := pipeline.Load(repoDir)
	if err != nil {
		return store.BuildFailed, nil, err
	}
	if spec != nil {
		sink.write("pipeline: " + pipeline.FileName + " loaded")
		applySpec(&p, spec)
		if err := r.runSteps(ctx, spec, "before", repoDir, sink); err != nil {
			if sink.isCanceled() {
				return store.BuildCanceled, nil, fmt.Errorf("canceled by user")
			}
			return store.BuildFailed, nil, err
		}
	}

	// Resolve the commit sha for tag templates.
	sha := b.SHA
	if sha == "" {
		sha = gitHead(ctx, repoDir)
	}
	tagCtx := pipeline.TagContext{Tag: b.Tag, Branch: b.Ref, SHA: sha}
	tags := pipeline.ExpandTags(p.Tags, tagCtx)
	if len(tags) == 0 {
		tags = []string{"latest"}
	}
	full := make([]string, len(tags))
	for i, t := range tags {
		full[i] = p.Image + ":" + t
	}

	// Registry credentials for buildctl push (temporary docker config).
	cfgDir := filepath.Join(work, "dockerconfig")
	if err := writeDockerConfig(cfgDir, p.Image, p.RegistryUser, p.RegistryPass); err != nil {
		return store.BuildFailed, nil, err
	}

	ctxDir := filepath.Join(repoDir, p.Context)
	dfPath := filepath.Join(repoDir, p.Dockerfile)
	args := []string{"--addr", r.BuildkitHost, "build",
		"--frontend", "dockerfile.v0",
		"--local", "context=" + ctxDir,
		"--local", "dockerfile=" + filepath.Dir(dfPath),
		"--opt", "filename=" + filepath.Base(dfPath),
	}
	for k, v := range p.BuildArgs {
		args = append(args, "--opt", "build-arg:"+k+"="+pipeline.ExpandValue(v, tagCtx))
	}
	names := strings.Join(full, ",")
	if len(full) > 1 {
		names = "\"" + names + "\""
	}
	args = append(args, "--output", "type=image,name="+names+",push=true")

	sink.write("buildctl build → " + strings.Join(full, ", "))
	cmd := exec.CommandContext(ctx, "buildctl", args...)
	cmd.Env = append(os.Environ(), "DOCKER_CONFIG="+cfgDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := streamCommand(cmd, sink); err != nil {
		if sink.isCanceled() {
			return store.BuildCanceled, nil, fmt.Errorf("canceled by user")
		}
		return store.BuildFailed, nil, fmt.Errorf("buildctl: %w", err)
	}
	if sink.isCanceled() {
		return store.BuildCanceled, nil, fmt.Errorf("canceled by user")
	}
	if err := r.runSteps(ctx, spec, "after", repoDir, sink); err != nil {
		if sink.isCanceled() {
			return store.BuildCanceled, nil, fmt.Errorf("canceled by user")
		}
		return store.BuildFailed, nil, err
	}
	sink.write("pushed: " + strings.Join(full, ", "))
	return store.BuildSuccess, full, nil
}

// applySpec overrides project settings with non-empty qdnder.yaml fields.
func applySpec(p *ProjectSettings, s *pipeline.Spec) {
	if s.Dockerfile != "" {
		p.Dockerfile = s.Dockerfile
	}
	if s.Context != "" {
		p.Context = s.Context
	}
	if s.Image != "" {
		p.Image = s.Image
	}
	if len(s.Tags) > 0 {
		p.Tags = s.Tags
	}
	if len(s.BuildArgs) > 0 {
		p.BuildArgs = s.BuildArgs
	}
}

// runSteps executes pipeline steps of the given phase in the repository
// directory, streaming their output into the build log.
func (r *Runner) runSteps(ctx context.Context, spec *pipeline.Spec, when, dir string, sink *logSink) error {
	if spec == nil {
		return nil
	}
	for _, st := range spec.Steps {
		if st.When != when {
			continue
		}
		if sink.isCanceled() {
			return fmt.Errorf("canceled by user")
		}
		sink.write("=== step: " + st.Name + " ===")
		cmd := exec.CommandContext(ctx, "sh", "-c", st.Run)
		cmd.Dir = dir
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := streamCommand(cmd, sink); err != nil {
			if sink.isCanceled() {
				return fmt.Errorf("canceled by user")
			}
			return fmt.Errorf("step %s: %w", st.Name, err)
		}
	}
	return nil
}

// gitClone clones the ref (and the exact sha when the webhook provided one).
func (r *Runner) gitClone(ctx context.Context, p ProjectSettings, b store.Build, dir string, sink *logSink) error {
	repoURL := authURL(p.RepoURL, p.GitUser, p.GitPass)
	ref := strings.TrimPrefix(strings.TrimPrefix(b.Ref, "refs/heads/"), "refs/tags/")
	if ref == "" {
		ref = p.DefaultBranch
	}
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--branch", ref, repoURL, dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := streamCommand(cmd, sink); err != nil {
		return err
	}
	if b.SHA != "" {
		for _, args := range [][]string{
			{"-C", dir, "fetch", "--depth", "1", "origin", b.SHA},
			{"-C", dir, "checkout", "--detach", b.SHA},
		} {
			cmd := exec.CommandContext(ctx, "git", args...)
			if err := streamCommand(cmd, sink); err != nil {
				return err
			}
		}
	}
	return nil
}

func gitHead(ctx context.Context, dir string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// streamCommand runs a command, forwarding each output line to the sink and
// killing the whole process group when a cancel arrives.
func streamCommand(cmd *exec.Cmd, sink *logSink) error {
	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return err
	}
	pw.Close()
	done := make(chan struct{})
	stop := make(chan struct{})
	// Periodic flush so a quiet process still reports logs and notices
	// cancellation; kill the whole process group when canceled.
	go func() {
		t := time.NewTicker(sink.every)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				sink.flush()
				if sink.isCanceled() && cmd.Process != nil {
					_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				}
			}
		}
	}()
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			sink.write(sc.Text())
			if sink.isCanceled() && cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		}
		pr.Close()
		close(done)
	}()
	err = cmd.Wait()
	close(stop)
	<-done
	sink.flush()
	return err
}

// authURL injects git credentials into an https URL (never logged).
func authURL(raw, user, pass string) string {
	if user == "" || pass == "" || !strings.HasPrefix(raw, "https://") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.User = url.UserPassword(user, pass)
	return u.String()
}

// sanitizeURL strips credentials for log output.
func sanitizeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = nil
	return u.String()
}

// registryHost extracts the registry host from an image reference.
func registryHost(image string) string {
	i := strings.IndexByte(image, '/')
	if i < 0 {
		return "https://index.docker.io/v1/"
	}
	host := image[:i]
	if strings.ContainsAny(host, ".:") || host == "localhost" {
		return host
	}
	return "https://index.docker.io/v1/"
}

// writeDockerConfig writes a temporary docker config with registry auth.
func writeDockerConfig(dir, image, user, pass string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	auths := map[string]any{}
	if user != "" && pass != "" {
		auths[registryHost(image)] = map[string]string{
			"auth": base64.StdEncoding.EncodeToString([]byte(user + ":" + pass)),
		}
	}
	raw, _ := json.Marshal(map[string]any{"auths": auths})
	return os.WriteFile(filepath.Join(dir, "config.json"), raw, 0o600)
}
