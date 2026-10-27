package secrets

import "sync"

// Mem is an in-memory Store for tests.
type Mem struct {
	mu sync.Mutex
	m  map[string]string
}

func NewMem() *Mem { return &Mem{m: map[string]string{}} }

func (s *Mem) Get(account string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[account]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (s *Mem) Set(account, secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[account] = secret
	return nil
}

func (s *Mem) Delete(account string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, account)
	return nil
}
