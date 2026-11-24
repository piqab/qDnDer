package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/piqab/qdnder/internal/auth"
	"github.com/piqab/qdnder/internal/store"
)

func setup(t *testing.T) (*Server, *httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	opTok := auth.NewAPIToken()
	viewTok := auth.NewAPIToken()
	_ = st.CreateToken(&store.Token{Name: "op", Hash: auth.HashToken(opTok), Role: "operator", UserID: "u"})
	_ = st.CreateToken(&store.Token{Name: "view", Hash: auth.HashToken(viewTok), Role: "viewer", UserID: "u"})
	t.Setenv("MCP_OP_TOKEN", opTok)
	t.Setenv("MCP_VIEW_TOKEN", viewTok)

	s := &Server{Store: st, Version: "test"}
	s.Enqueue = func(projectRef, ref, sha, tag, trigger string) (*store.Build, error) {
		p, err := st.ProjectByName(projectRef)
		if err != nil || p == nil {
			return nil, err
		}
		b := &store.Build{ProjectID: p.ID, Ref: ref, Tag: tag, Trigger: trigger}
		if err := st.CreateBuild(b); err != nil {
			return nil, err
		}
		return b, nil
	}
	s.Resolve = func(r *http.Request) string {
		h := r.Header.Get("Authorization")
		switch h {
		case "Bearer " + opTok:
			return "operator"
		case "Bearer " + viewTok:
			return "viewer"
		}
		return ""
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts, st
}

func call(t *testing.T, url, token string, req map[string]any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(req)
	r, err := http.NewRequest("POST", url, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out
}

func TestInitializeAndTools(t *testing.T) {
	_, ts, _ := setup(t)
	tok := testEnv("MCP_OP_TOKEN")
	out := call(t, ts.URL, tok, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	res := out["result"].(map[string]any)
	if res["protocolVersion"] != "2024-11-05" {
		t.Fatalf("initialize: %v", out)
	}
	out = call(t, ts.URL, tok, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	tools := out["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 4 {
		t.Fatalf("tools: %d", len(tools))
	}
}

func testEnv(k string) string {
	return os.Getenv(k)
}

func TestBuildAndLogsViaMCP(t *testing.T) {
	_, ts, st := setup(t)
	_ = st.CreateProject(&store.Project{Name: "app", RepoURL: "https://x/app.git", Image: "reg/app"})
	tok := testEnv("MCP_OP_TOKEN")

	out := call(t, ts.URL, tok, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "qdnder_build", "arguments": map[string]any{"project": "app", "ref": "main"}}})
	res := out["result"].(map[string]any)
	if res["isError"] == true {
		t.Fatalf("build tool error: %v", res)
	}
	builds, _ := st.Builds("", "", 10)
	if len(builds) != 1 || builds[0].Trigger != "mcp" {
		t.Fatalf("builds: %+v", builds)
	}
	_, _ = st.AppendLog(builds[0].ID, "line1\nline2\nline3\n")

	out = call(t, ts.URL, tok, map[string]any{"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{"name": "qdnder_logs", "arguments": map[string]any{"build_id": builds[0].ID, "tail": 2}}})
	text := out["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	var payload struct{ Log string }
	_ = json.Unmarshal([]byte(text), &payload)
	if payload.Log != "line2\nline3\n" {
		t.Fatalf("tail: %q", payload.Log)
	}
}

func TestViewerCannotBuild(t *testing.T) {
	_, ts, st := setup(t)
	_ = st.CreateProject(&store.Project{Name: "app", RepoURL: "https://x/app.git", Image: "reg/app"})
	out := call(t, ts.URL, testEnv("MCP_VIEW_TOKEN"), map[string]any{"jsonrpc": "2.0", "id": 5, "method": "tools/call",
		"params": map[string]any{"name": "qdnder_build", "arguments": map[string]any{"project": "app"}}})
	res := out["result"].(map[string]any)
	if res["isError"] != true {
		t.Fatalf("viewer must be rejected: %v", res)
	}
	// Read tool works for viewer.
	out = call(t, ts.URL, testEnv("MCP_VIEW_TOKEN"), map[string]any{"jsonrpc": "2.0", "id": 6, "method": "tools/call",
		"params": map[string]any{"name": "qdnder_builds", "arguments": map[string]any{}}})
	if out["result"].(map[string]any)["isError"] == true {
		t.Fatal("viewer reads builds")
	}
}

func TestUnauthenticated(t *testing.T) {
	_, ts, _ := setup(t)
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	resp, err := http.Post(ts.URL, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("no auth: %d", resp.StatusCode)
	}
}
