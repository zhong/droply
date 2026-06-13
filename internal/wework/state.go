package wework

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// StateData carries information associated with an OAuth state token.
type StateData struct {
	Subdomain  string
	Project    string
	Host       string
	Redirect   string
	IsCustom   bool
	ExpiresAt  time.Time
}

// StateStore is a thread-safe in-memory store for OAuth state tokens with TTL.
type StateStore struct {
	mu     sync.Mutex
	states map[string]StateData
	ttl    time.Duration
}

// NewStateStore creates a new state store with the given TTL.
// A background goroutine periodically cleans up expired entries.
func NewStateStore(ttl time.Duration) *StateStore {
	s := &StateStore{
		states: make(map[string]StateData),
		ttl:    ttl,
	}
	go s.cleanup()
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

func (s *StateStore) cleanup() {
	for {
		time.Sleep(5 * time.Minute)
		s.mu.Lock()
		now := time.Now()
		for token, data := range s.states {
			if now.After(data.ExpiresAt) {
				delete(s.states, token)
			}
		}
		s.mu.Unlock()
	}
}
