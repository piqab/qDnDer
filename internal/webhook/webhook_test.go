package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
)

const secret = "whsecret"

func sign(body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestGitHubPush(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main","after":"abc123","repository":{"clone_url":"https://git.example.com/app.git"}}`)
	h := http.Header{}
	h.Set("X-GitHub-Event", "push")
	h.Set("X-Hub-Signature-256", "sha256="+sign(body))
	ev, err := Parse(h, body, secret)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Provider != "github" || ev.Ref != "main" || ev.SHA != "abc123" || ev.Tag != "" ||
		ev.RepoURL != "https://git.example.com/app.git" {
		t.Fatalf("event: %+v", ev)
	}
}

func TestGitHubTagPush(t *testing.T) {
	body := []byte(`{"ref":"refs/tags/v1.2.3","after":"def456","repository":{"clone_url":"https://x/app.git"}}`)
	h := http.Header{}
	h.Set("X-GitHub-Event", "push")
	h.Set("X-Hub-Signature-256", "sha256="+sign(body))
	ev, err := Parse(h, body, secret)
	if err != nil || ev.Tag != "v1.2.3" || ev.Ref != "v1.2.3" {
		t.Fatalf("tag event: %+v %v", ev, err)
	}
}

func TestGiteaSignature(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main","after":"abc","repository":{"clone_url":"https://x/app.git"}}`)
	h := http.Header{}
	h.Set("X-Gitea-Event", "push")
	h.Set("X-Gitea-Signature", sign(body))
	ev, err := Parse(h, body, secret)
	if err != nil || ev.Provider != "gitea" || ev.Ref != "main" {
		t.Fatalf("gitea: %+v %v", ev, err)
	}
}

func TestGitLabToken(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main","checkout_sha":"abc","project":{"git_http_url":"https://x/app.git"}}`)
	h := http.Header{}
	h.Set("X-Gitlab-Event", "Push Hook")
	h.Set("X-Gitlab-Token", secret)
	ev, err := Parse(h, body, secret)
	if err != nil || ev.Provider != "gitlab" || ev.SHA != "abc" || ev.RepoURL != "https://x/app.git" {
		t.Fatalf("gitlab: %+v %v", ev, err)
	}
	h.Set("X-Gitlab-Token", "wrong")
	if _, err := Parse(h, body, secret); err == nil {
		t.Fatal("wrong gitlab token accepted")
	}
}

func TestBadSignature(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	h := http.Header{}
	h.Set("X-GitHub-Event", "push")
	h.Set("X-Hub-Signature-256", "sha256=deadbeef")
	if _, err := Parse(h, body, secret); err == nil {
		t.Fatal("bad signature accepted")
	}
	h.Del("X-Hub-Signature-256")
	if _, err := Parse(h, body, secret); err == nil {
		t.Fatal("missing signature accepted")
	}
	if _, err := Parse(h, body, ""); err == nil {
		t.Fatal("empty project secret accepted")
	}
}

func TestPingIgnored(t *testing.T) {
	h := http.Header{}
	h.Set("X-GitHub-Event", "ping")
	ev, err := Parse(h, []byte(`{}`), secret)
	if ev != nil || err != nil {
		t.Fatalf("ping must be ignored: %+v %v", ev, err)
	}
}
