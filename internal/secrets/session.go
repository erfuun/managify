package secrets

import "sync"

// Session caches secrets in process memory for the life of one Managify run.
type Session struct {
	mu sync.Mutex
	m  map[string]string
}

func NewSession() *Session { return &Session{m: map[string]string{}} }

func (s *Session) Get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	return v, ok
}

func (s *Session) Set(key, val string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = val
}

func (s *Session) Forget(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
}
