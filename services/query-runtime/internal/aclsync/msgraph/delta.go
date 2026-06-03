package msgraph

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// DeltaTokenStore persists the Microsoft Graph delta token between sync runs so the
// connector only fetches changes since the last poll.
type DeltaTokenStore interface {
	Load(ctx context.Context, key string) (string, error)
	Save(ctx context.Context, key, token string) error
}

// MemoryDeltaTokenStore keeps the token in memory (dev/test; lost on restart).
type MemoryDeltaTokenStore struct {
	mu sync.Mutex
	m  map[string]string
}

func NewMemoryDeltaTokenStore() *MemoryDeltaTokenStore {
	return &MemoryDeltaTokenStore{m: map[string]string{}}
}

func (s *MemoryDeltaTokenStore) Load(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[key], nil
}

func (s *MemoryDeltaTokenStore) Save(_ context.Context, key, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = token
	return nil
}

// FileDeltaTokenStore is a simple durable token store (one file per key in a directory).
// For production at scale, back this with a database or object store; the interface keeps
// that swap local to this package.
type FileDeltaTokenStore struct {
	dir string
}

func NewFileDeltaTokenStore(dir string) *FileDeltaTokenStore {
	return &FileDeltaTokenStore{dir: dir}
}

func (s *FileDeltaTokenStore) path(key string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, key)
	return filepath.Join(s.dir, "delta_"+safe+".token")
}

func (s *FileDeltaTokenStore) Load(_ context.Context, key string) (string, error) {
	data, err := os.ReadFile(s.path(key))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (s *FileDeltaTokenStore) Save(_ context.Context, key, token string) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.path(key), []byte(token), 0o600)
}
