// Package store keeps the inventory in memory. Nothing is persisted on
// purpose: the Kubernetes collector re-scrapes and host agents re-push on a
// timer, so the state rebuilds itself within one interval after a restart.
package store

import (
	"sort"
	"sync"
	"time"
)

type Item struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Version   string `json:"version"`
	Available string `json:"available,omitempty"`
	Ref       string `json:"ref,omitempty"`
}

type snapshot struct {
	seen  time.Time
	items []Item
}

type Store struct {
	mu      sync.RWMutex
	sources map[string]snapshot
}

func New() *Store { return &Store{sources: map[string]snapshot{}} }

// Replace swaps the full item set of one source. A report is always the
// complete picture of its host, so removed components disappear by omission.
func (s *Store) Replace(source string, items []Item, now time.Time) {
	cp := append([]Item(nil), items...)
	sort.Slice(cp, func(i, j int) bool {
		if cp[i].Kind != cp[j].Kind {
			return cp[i].Kind < cp[j].Kind
		}
		return cp[i].Name < cp[j].Name
	})
	s.mu.Lock()
	s.sources[source] = snapshot{seen: now, items: cp}
	s.mu.Unlock()
}

// Get returns the last report of a source and when it arrived.
func (s *Store) Get(source string) ([]Item, time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.sources[source]
	return snap.items, snap.seen, ok
}
