// Command qdnder is an independent CI for Docker image builds:
// "qdnder server" runs the control plane, "qdnder runner" runs a worker.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/piqab/qdnder/internal/api"
	"github.com/piqab/qdnder/internal/auth"
	"github.com/piqab/qdnder/internal/store"
)

// version is overridden at build time via -ldflags.
var version = "0.1.0-dev"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	mode := "server"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	switch mode {
	case "server":
		runServer()
	case "version", "--version", "-v":
		fmt.Println("qdnder " + version)
	default:
		fmt.Fprintf(os.Stderr, "usage: qdnder server|version\n")
		os.Exit(2)
	}
}

func runServer() {
	addr := env("QDNDER_ADDR", "127.0.0.1:8090")
	dsn := env("QDNDER_DB", filepath.Join(env("QDNDER_DATA", "data"), "qdnder.db"))
	if dsn != ":memory:" && !isPostgres(dsn) {
		if err := os.MkdirAll(filepath.Dir(dsn), 0o700); err != nil {
			log.Fatal(err)
		}
	}
	st, err := store.Open(dsn)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	// Bootstrap: first admin account and the shared runner token.
	if n, err := st.CountUsers(); err == nil && n == 0 {
		pass := auth.RandomSecret(6)
		hash, _ := auth.HashPassword(pass)
		if err := st.CreateUser(&store.User{Username: "admin", PasswordHash: hash, Role: "admin"}); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("initial admin login: admin / %s  (change it after login)\n", pass)
	}
	runnerToken, err := st.Setting("runner_token")
	if err != nil {
		log.Fatal(err)
	}
	if runnerToken == "" {
		runnerToken = "qdnr_" + auth.RandomSecret(24)
		if err := st.SetSetting("runner_token", runnerToken); err != nil {
			log.Fatal(err)
		}
	}

	srv := &api.Server{Store: st, Sessions: auth.NewSessions(), RunnerToken: runnerToken, Version: version}
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	fmt.Printf("qdnder server %s listening on http://%s\n", version, addr)
	log.Fatal(httpSrv.ListenAndServe())
}

func isPostgres(dsn string) bool {
	return len(dsn) > 11 && (dsn[:11] == "postgres://" || (len(dsn) > 13 && dsn[:13] == "postgresql://"))
}
