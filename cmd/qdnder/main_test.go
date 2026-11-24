package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/qdnder/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestRunnerTokenFromEnv(t *testing.T) {
	st := openStore(t)
	t.Setenv("QDNDER_RUNNER_TOKEN", "qdnr_fixed_token")
	if got := resolveRunnerToken(st); got != "qdnr_fixed_token" {
		t.Fatalf("env token ignored: %q", got)
	}
	// Persisted so the admin panel shows the same token.
	if v, _ := st.Setting("runner_token"); v != "qdnr_fixed_token" {
		t.Fatalf("env token not persisted: %q", v)
	}
}

func TestRunnerTokenGeneratedOnce(t *testing.T) {
	st := openStore(t)
	t.Setenv("QDNDER_RUNNER_TOKEN", "")
	a := resolveRunnerToken(st)
	if !strings.HasPrefix(a, "qdnr_") || len(a) < 40 {
		t.Fatalf("bad generated token: %q", a)
	}
	if b := resolveRunnerToken(st); a != b {
		t.Fatalf("token must be stable: %q vs %q", a, b)
	}
}
