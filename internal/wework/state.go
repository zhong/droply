package wework

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// StateData carries information associated with an OAuth state token.
type StateData struct {
	ProjectID int64
	Subdomain string
	Project   string
	Host      string
	Redirect  string
	IsCustom  bool
	ExpiresAt time.Time
}

// StateStore is a thread-safe in-memory store for OAuth state tokens with TTL.
type StateStore struct {
	mu     sync.Mutex
	states map[string]StateData
	ttl    time.Duration
}

// NewStateStore creates a new state store with the given TTL.
// Expired entries are reclaimed during use; no background worker is needed.
func NewStateStore(ttl time.Duration) *StateStore {
	s := &StateStore{
		states: make(map[string]StateData),
		ttl:    ttl,
	}
	return s
}

// Generate creates a new random state token, stores the associated data, and returns the token.
func (s *StateStore) Generate(data StateData) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)

	data.ExpiresAt = time.Now().Add(s.ttl)

	s.mu.Lock()
	s.cleanupLocked()
	s.states[token] = data
	s.mu.Unlock()

	return token, nil
}

// Consume looks up and removes the state token. Returns (data, true) if found and unexpired.
func (s *StateStore) Consume(token string) (StateData, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, ok := s.states[token]
	if !ok {
		return StateData{}, false
	}
	delete(s.states, token)

	if time.Now().After(data.ExpiresAt) {
		return StateData{}, false
	}
	return data, true
}

func (s *StateStore) cleanupLocked() {
	now := time.Now()
	for token, data := range s.states {
		if now.After(data.ExpiresAt) {
			delete(s.states, token)
		}
	}
}
