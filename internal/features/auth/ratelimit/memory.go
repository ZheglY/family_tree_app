package ratelimit

import (
	"context"
	"errors"
	"sync"
	"time"
)

var errMemoryCapacity = errors.New("in-memory rate limit capacity reached")

type memoryEntry struct {
	count     int64
	expiresAt time.Time
}

type memoryStore struct {
	mu      sync.Mutex
	entries map[string]memoryEntry
	maxKeys int
	now     func() time.Time
}

func newMemoryStore(maxKeys int) *memoryStore {
	return &memoryStore{
		entries: make(map[string]memoryEntry),
		maxKeys: maxKeys,
		now:     time.Now,
	}
}

func (s *memoryStore) Increment(
	_ context.Context,
	key string,
	window time.Duration,
) (int64, time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	entry, exists := s.entries[key]
	if exists && now.Before(entry.expiresAt) {
		entry.count++
		s.entries[key] = entry
		return entry.count, entry.expiresAt.Sub(now), nil
	}

	if !exists && len(s.entries) >= s.maxKeys {
		for storedKey, storedEntry := range s.entries {
			if !now.Before(storedEntry.expiresAt) {
				delete(s.entries, storedKey)
			}
		}
		if len(s.entries) >= s.maxKeys {
			return 0, 0, errMemoryCapacity
		}
	}

	entry = memoryEntry{count: 1, expiresAt: now.Add(window)}
	s.entries[key] = entry
	return entry.count, window, nil
}

func (*memoryStore) Close() error {
	return nil
}
