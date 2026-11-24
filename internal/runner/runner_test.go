package runner

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piqab/qdnder/internal/api"
	"github.com/piqab/qdnder/internal/auth"
	"github.com/piqab/qdnder/internal/store"
)

const fakeGit = `#!/bin/sh
case "$1" in
  clone)
    for last; do :; done
    mkdir -p "$last"
    printf 'FROM scratch\n' > "$last/Dockerfile"
    echo "Cloning into '$last'..."
    ;;
  -C)
    case "$3" in
      rev-parse) echo "0123456789abcdef0123456789abcdef01234567" ;;
      *) echo "git $3 done" ;;
    esac
    ;;
esac
exit 0
`

const fakeBuildctl = `#!/bin/sh
echo "#1 resolving dockerfile"
case "${FAKE_BUILDCTL_MODE:-ok}" in
  fail) echo "error: build failed"; exit 1 ;;
  sleep) echo "#2 building"; sleep 30 ;;
esac
echo "#3 pushing image"
exit 0
`

// setup creates a server with one queued build and fake git/buildctl on PATH.
func setup(t *testing.T, mode string) (*store.Store, *Runner, context.CancelFunc) {
	t.Helper()
	return setupGit(t, mode, fakeGit)
}

func setupGit(t *testing.T, mode, gitScript string) (*store.Store, *Runner, context.CancelFunc) {
	t.Helper()
	bin := t.TempDir()
	for name, script := range map[string]string{"git": gitScript, "buildctl": fakeBuildctl} {
		p := filepath.Join(bin, name)
		if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_BUILDCTL_MODE", mode)

	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := &api.Server{Store: st, Sessions: auth.NewSessions(), RunnerToken: "qdnr_test", Version: "test"}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	p := &store.Project{Name: "app", RepoURL: "https://git.example.com/app.git", Image: "reg.example.com/app",
		Tags: []string{"latest", "{sha:12}"}}
	if err := st.CreateProject(p); err != nil {
		t.Fatal(err)
	}
	b := &store.Build{ProjectID: p.ID, Ref: "main", Trigger: "api"}
	if err := st.CreateBuild(b); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	r := &Runner{
		Name: "test-runner", Version: "test",
		Client:       NewClient(ts.URL, "qdnr_test"),
		BuildkitHost: "tcp://127.0.0.1:1",
		WorkDir:      t.TempDir(),
		PollInterval: 20 * time.Millisecond,
		FlushEvery:   30 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = r.Run(ctx) }()
	return st, r, cancel
}

func waitBuild(t *testing.T, st *store.Store, id string, timeout time.Duration) *store.Build {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		b, err := st.BuildByID(id)
		if err != nil {
			t.Fatal(err)
		}
		if b != nil && b.Done() {
			return b
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("build did not finish in time")
	return nil
}

func TestRunnerSuccess(t *testing.T) {
	st, _, _ := setup(t, "ok")
	builds, _ := st.Builds("", "", 10)
	b := waitBuild(t, st, builds[0].ID, 15*time.Second)
	if b.Status != store.BuildSuccess {
		t.Fatalf("status=%s error=%s log:\n%s", b.Status, b.Error, b.Log)
	}
	want := []string{"reg.example.com/app:latest", "reg.example.com/app:0123456789ab"}
	if len(b.ImageTags) != 2 || b.ImageTags[0] != want[0] || b.ImageTags[1] != want[1] {
		t.Fatalf("image tags: %v", b.ImageTags)
	}
	if !strings.Contains(b.Log, "buildctl") || !strings.Contains(b.Log, "#3 pushing image") {
		t.Fatalf("log missing build output:\n%s", b.Log)
	}
}

func TestRunnerFailure(t *testing.T) {
	st, _, _ := setup(t, "fail")
	builds, _ := st.Builds("", "", 10)
	b := waitBuild(t, st, builds[0].ID, 15*time.Second)
	if b.Status != store.BuildFailed || !strings.Contains(b.Error, "buildctl") {
		t.Fatalf("status=%s error=%s", b.Status, b.Error)
	}
}

func TestRunnerCancel(t *testing.T) {
	st, _, _ := setup(t, "sleep")
	builds, _ := st.Builds("", "", 10)
	id := builds[0].ID
	// Wait until the build is running and producing output.
	deadline := time.Now().Add(10 * time.Second)
	for {
		b, _ := st.BuildByID(id)
		if b.Status == store.BuildRunning && strings.Contains(b.Log, "#2 building") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("build never started")
		}
		time.Sleep(30 * time.Millisecond)
	}
	if err := st.RequestCancel(id); err != nil {
		t.Fatal(err)
	}
	b := waitBuild(t, st, id, 10*time.Second)
	if b.Status != store.BuildCanceled {
		t.Fatalf("status=%s error=%s", b.Status, b.Error)
	}
}

func TestRunnerPipeline(t *testing.T) {
	// Fake git also writes a qdnder.yaml overriding image/tags and adding steps.
	gitScript := `#!/bin/sh
case "$1" in
  clone)
    for last; do :; done
    mkdir -p "$last"
    printf 'FROM scratch\n' > "$last/Dockerfile"
    cat > "$last/qdnder.yaml" <<'EOF'
image: reg2.example.com/override
tags: ["v-test"]
steps:
  - name: pre
    run: echo hello-step
  - name: post
    run: echo after-step
    when: after
EOF
    echo "Cloning into '$last'..."
    ;;
  -C)
    case "$3" in
      rev-parse) echo "0123456789abcdef0123456789abcdef01234567" ;;
      *) echo "git $3 done" ;;
    esac
    ;;
esac
exit 0
`
	st, _, _ := setupGit(t, "ok", gitScript)
	builds, _ := st.Builds("", "", 10)
	b := waitBuild(t, st, builds[0].ID, 15*time.Second)
	if b.Status != store.BuildSuccess {
		t.Fatalf("status=%s error=%s log:\n%s", b.Status, b.Error, b.Log)
	}
	if len(b.ImageTags) != 1 || b.ImageTags[0] != "reg2.example.com/override:v-test" {
		t.Fatalf("pipeline image/tags not applied: %v", b.ImageTags)
	}
	if !strings.Contains(b.Log, "hello-step") || !strings.Contains(b.Log, "after-step") {
		t.Fatalf("steps output missing:\n%s", b.Log)
	}
}

func TestRegistryHost(t *testing.T) {
	cases := map[string]string{
		"reg.example.com/app":     "reg.example.com",
		"localhost:5000/app":      "localhost:5000",
		"library/nginx":           "https://index.docker.io/v1/",
		"nginx":                   "https://index.docker.io/v1/",
		"localhost/app":           "localhost",
	}
	for image, want := range cases {
		if got := registryHost(image); got != want {
			t.Errorf("%s: got %q want %q", image, got, want)
		}
	}
}

func TestAuthURLSanitize(t *testing.T) {
	raw := "https://git.example.com/app.git"
	authed := authURL(raw, "robot", "p@ss")
	if !strings.Contains(authed, "robot") {
		t.Fatal("credentials not injected")
	}
	if strings.Contains(sanitizeURL(authed), "robot") || strings.Contains(sanitizeURL(authed), "p@ss") {
		t.Fatal("sanitize leaked credentials")
	}
}
