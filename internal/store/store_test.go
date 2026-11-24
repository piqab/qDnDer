package store

import (
	"path/filepath"
	"testing"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUsersAndTokens(t *testing.T) {
	s := openTest(t)
	u := &User{Username: "alice", PasswordHash: "x", Role: "admin"}
	if err := s.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateUser(&User{Username: "alice", PasswordHash: "x", Role: "viewer"}); err == nil {
		t.Fatal("duplicate username must fail")
	}
	got, err := s.UserByName("alice")
	if err != nil || got == nil || got.ID != u.ID {
		t.Fatalf("UserByName: %v %v", got, err)
	}
	if n, _ := s.CountUsers(); n != 1 {
		t.Fatalf("CountUsers = %d", n)
	}
	tok := &Token{Name: "ci", Hash: "h1", Role: "operator", UserID: u.ID}
	if err := s.CreateToken(tok); err != nil {
		t.Fatal(err)
	}
	gotTok, err := s.TokenByHash("h1")
	if err != nil || gotTok == nil || gotTok.Role != "operator" {
		t.Fatalf("TokenByHash: %v %v", gotTok, err)
	}
	if err := s.DeleteToken(tok.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.TokenByHash("h1"); got != nil {
		t.Fatal("token must be gone")
	}
}

func TestProjectSecretsKeptOnUpdate(t *testing.T) {
	s := openTest(t)
	p := &Project{Name: "app", RepoURL: "https://git.example.com/app.git", Image: "reg.example.com/app",
		RegistryUser: "robot", RegistryPass: "s3cret", WebhookSecret: "wh"}
	if err := s.CreateProject(p); err != nil {
		t.Fatal(err)
	}
	// Update without secrets must keep the old ones.
	up := &Project{ID: p.ID, Name: "app", RepoURL: p.RepoURL, Image: "reg.example.com/app2"}
	if err := s.UpdateProject(up); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ProjectByID(p.ID)
	if got.RegistryPass != "s3cret" || got.WebhookSecret != "wh" || got.Image != "reg.example.com/app2" {
		t.Fatalf("secrets lost or image not updated: %+v", got)
	}
	if !got.HasRegistryPass || !got.HasWebhookSecret {
		t.Fatal("derived flags wrong")
	}
}

func TestBuildLifecycle(t *testing.T) {
	s := openTest(t)
	p := &Project{Name: "app", RepoURL: "https://x/app.git", Image: "reg/app"}
	if err := s.CreateProject(p); err != nil {
		t.Fatal(err)
	}
	b := &Build{ProjectID: p.ID, Ref: "main", Trigger: "api"}
	if err := s.CreateBuild(b); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.QueueSize(); n != 1 {
		t.Fatalf("queue = %d", n)
	}
	claimed, err := s.ClaimBuild("runner-1")
	if err != nil || claimed == nil || claimed.ID != b.ID || claimed.Status != BuildRunning {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	// Second claim gets nothing.
	if again, _ := s.ClaimBuild("runner-2"); again != nil {
		t.Fatal("queue must be empty")
	}
	cancel, err := s.AppendLog(b.ID, "line 1\n")
	if err != nil || cancel {
		t.Fatalf("append: %v %v", cancel, err)
	}
	if err := s.RequestCancel(b.ID); err != nil {
		t.Fatal(err)
	}
	if cancel, _ := s.AppendLog(b.ID, "line 2\n"); !cancel {
		t.Fatal("cancel flag must be reported")
	}
	if err := s.FinishBuild(b.ID, BuildCanceled, nil, "canceled by user"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.BuildByID(b.ID)
	if got.Status != BuildCanceled || got.Log != "line 1\nline 2\n" || !got.Done() {
		t.Fatalf("final build: %+v", got)
	}
	if got.ProjectName != "app" {
		t.Fatal("project name join missing")
	}
}

func TestCancelQueuedBuild(t *testing.T) {
	s := openTest(t)
	p := &Project{Name: "app", RepoURL: "https://x/app.git", Image: "reg/app"}
	_ = s.CreateProject(p)
	b := &Build{ProjectID: p.ID}
	_ = s.CreateBuild(b)
	if err := s.RequestCancel(b.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.BuildByID(b.ID)
	if got.Status != BuildCanceled {
		t.Fatalf("queued cancel: %s", got.Status)
	}
	if err := s.RequestCancel(b.ID); err == nil {
		t.Fatal("cancel of finished build must fail")
	}
}

func TestRunnersAndSettings(t *testing.T) {
	s := openTest(t)
	if err := s.TouchRunner("r1", "0.1.0", "b1"); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchRunner("r1", "0.1.1", ""); err != nil {
		t.Fatal(err)
	}
	rs, err := s.Runners()
	if err != nil || len(rs) != 1 || rs[0].Version != "0.1.1" || !rs[0].Online {
		t.Fatalf("runners: %+v %v", rs, err)
	}
	if v, _ := s.Setting("k"); v != "" {
		t.Fatal("absent setting must be empty")
	}
	_ = s.SetSetting("k", "v1")
	_ = s.SetSetting("k", "v2")
	if v, _ := s.Setting("k"); v != "v2" {
		t.Fatalf("setting = %q", v)
	}
}
