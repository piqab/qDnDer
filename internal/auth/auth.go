// Package auth implements password hashing, API tokens and browser sessions.
// Passwords use PBKDF2-HMAC-SHA256 from the standard library; tokens are
// random secrets stored only as SHA-256 hashes.
package auth

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const pbkdf2Iterations = 120_000

// HashPassword returns a self-describing PBKDF2 hash string.
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2$%d$%s$%s", pbkdf2Iterations, hex.EncodeToString(salt), hex.EncodeToString(key)), nil
}

// CheckPassword verifies a password against a stored hash in constant time.
func CheckPassword(password, stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1000 {
		return false
	}
	salt, err := hex.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(parts[3])
	if err != nil {
		return false
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	return hmac.Equal(key, want)
}

// RandomSecret returns a random hex secret of n bytes.
func RandomSecret(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// NewAPIToken returns a fresh API token string.
func NewAPIToken() string { return "qdn_" + RandomSecret(24) }

// HashToken returns the SHA-256 hex hash used to store/lookup tokens.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ConstantEqual compares two strings in constant time.
func ConstantEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// Session is an in-memory browser session.
type Session struct {
	UserID   string
	Username string
	Role     string
	Expires  time.Time
}

// Sessions keeps browser sessions in memory (single server process).
type Sessions struct {
	mu sync.Mutex
	m  map[string]Session
}

// NewSessions creates an empty session store.
func NewSessions() *Sessions { return &Sessions{m: map[string]Session{}} }

// Create stores a new session and returns its cookie value.
func (s *Sessions) Create(userID, username, role string, ttl time.Duration) string {
	id := RandomSecret(24)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[id] = Session{UserID: userID, Username: username, Role: role, Expires: time.Now().Add(ttl)}
	return id
}

// Get returns a live session or nil.
func (s *Sessions) Get(id string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.m[id]
	if !ok || time.Now().After(sess.Expires) {
		delete(s.m, id)
		return nil
	}
	return &sess
}

// Delete removes a session (logout).
func (s *Sessions) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
}
