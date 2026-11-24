package auth

import (
	"testing"
	"time"
)

func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword("correct horse", h) {
		t.Fatal("correct password rejected")
	}
	if CheckPassword("wrong", h) {
		t.Fatal("wrong password accepted")
	}
	if CheckPassword("x", "garbage") {
		t.Fatal("garbage hash accepted")
	}
}

func TestTokensUnique(t *testing.T) {
	a, b := NewAPIToken(), NewAPIToken()
	if a == b || len(a) < 40 {
		t.Fatal("tokens must be unique and long")
	}
	if HashToken(a) == HashToken(b) || len(HashToken(a)) != 64 {
		t.Fatal("hashes wrong")
	}
}

func TestSessions(t *testing.T) {
	s := NewSessions()
	id := s.Create("u1", "alice", "admin", time.Hour)
	if got := s.Get(id); got == nil || got.Username != "alice" {
		t.Fatal("session missing")
	}
	s.Delete(id)
	if s.Get(id) != nil {
		t.Fatal("session must be gone")
	}
	expired := s.Create("u2", "bob", "viewer", -time.Second)
	if s.Get(expired) != nil {
		t.Fatal("expired session accepted")
	}
}
