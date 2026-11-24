// Package webhook parses and verifies push webhooks from Gitea, Forgejo,
// GitHub and GitLab.
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Event is a normalized push event.
type Event struct {
	Provider string
	Ref      string // branch or tag name, without refs/…
	SHA      string
	Tag      string // non-empty when the push created a tag
	RepoURL  string
}

type payload struct {
	Ref          string `json:"ref"`
	After        string `json:"after"`
	CheckoutSHA  string `json:"checkout_sha"`
	Repository   struct {
		CloneURL    string `json:"clone_url"`
		GitHTTPURL  string `json:"git_http_url"`
	} `json:"repository"`
	Project struct {
		GitHTTPURL string `json:"git_http_url"`
	} `json:"project"`
}

// Parse verifies the signature against the project secret and normalizes
// the push. A ping/health event returns (nil, nil) — nothing to build.
func Parse(header http.Header, body []byte, secret string) (*Event, error) {
	if secret == "" {
		return nil, fmt.Errorf("webhook secret is not configured for this project")
	}
	provider := "github"
	switch {
	case header.Get("X-Gitlab-Event") != "":
		provider = "gitlab"
	case header.Get("X-Gitea-Event") != "" || header.Get("X-Gitea-Signature") != "":
		provider = "gitea" // Forgejo uses the Gitea format
	case header.Get("X-GitHub-Event") != "":
		provider = "github"
	}

	switch provider {
	case "gitlab":
		if !constantEqual(header.Get("X-Gitlab-Token"), secret) {
			return nil, fmt.Errorf("bad X-Gitlab-Token")
		}
		ev := header.Get("X-Gitlab-Event")
		if ev != "Push Hook" && ev != "Tag Push Hook" {
			return nil, nil
		}
	default:
		event := header.Get("X-GitHub-Event")
		if event == "" {
			event = header.Get("X-Gitea-Event")
		}
		if event == "ping" {
			return nil, nil
		}
		if event != "" && event != "push" {
			return nil, nil
		}
		if err := verifyHMAC(header, body, secret); err != nil {
			return nil, err
		}
	}

	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("bad webhook payload: %w", err)
	}
	if p.Ref == "" {
		return nil, fmt.Errorf("payload has no ref")
	}
	e := &Event{Provider: provider, SHA: p.After}
	if e.SHA == "" {
		e.SHA = p.CheckoutSHA
	}
	e.RepoURL = p.Repository.CloneURL
	if e.RepoURL == "" {
		e.RepoURL = p.Repository.GitHTTPURL
	}
	if e.RepoURL == "" {
		e.RepoURL = p.Project.GitHTTPURL
	}
	switch {
	case strings.HasPrefix(p.Ref, "refs/heads/"):
		e.Ref = strings.TrimPrefix(p.Ref, "refs/heads/")
	case strings.HasPrefix(p.Ref, "refs/tags/"):
		e.Ref = strings.TrimPrefix(p.Ref, "refs/tags/")
		e.Tag = e.Ref
	default:
		e.Ref = p.Ref
	}
	return e, nil
}

// verifyHMAC accepts GitHub's "sha256=<hex>" (X-Hub-Signature-256) and
// Gitea's plain hex (X-Gitea-Signature).
func verifyHMAC(header http.Header, body []byte, secret string) error {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := mac.Sum(nil)
	if sig := header.Get("X-Hub-Signature-256"); sig != "" {
		got, err := hex.DecodeString(strings.TrimPrefix(sig, "sha256="))
		if err != nil || !hmac.Equal(got, want) {
			return fmt.Errorf("bad X-Hub-Signature-256")
		}
		return nil
	}
	if sig := header.Get("X-Gitea-Signature"); sig != "" {
		got, err := hex.DecodeString(sig)
		if err != nil || !hmac.Equal(got, want) {
			return fmt.Errorf("bad X-Gitea-Signature")
		}
		return nil
	}
	return fmt.Errorf("missing webhook signature")
}

func constantEqual(a, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}
