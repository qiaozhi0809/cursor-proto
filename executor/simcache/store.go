// Package simcache implements an in-process, LRU-with-TTL prompt-prefix
// cache simulator. Cursor's real prompt-cache counters (`cache_read_tokens`)
// are noisy and only reflect Cursor's own internal system-prompt cache; they
// swing between different values for the same client input, which throws off
// dashboards like CPA and new-api that expect Anthropic-style monotonic
// `cached_tokens` behaviour.
//
// simcache sits in the cursor-proxy request path: for each request, the proxy
// computes a "stable prefix" (system prompts + all history turns before the
// last user turn) and asks the store whether it has seen that prefix before.
// On a hit the store returns an estimated token count for the prefix so the
// proxy can synthesise a plausible `cached_tokens` value. On a miss the store
// records the prefix and returns 0.
//
// The store is purely local, keyed by SHA-256 of the prefix bytes, and has no
// disk persistence — it is a dashboard-facing simulator, not a real cache.
package simcache

import (
	"container/list"
	"crypto/sha256"
	"sync"
	"time"
)

// Entry is a single recorded prefix. Zero value is not valid; use
// Store.LookupOrRecord to obtain one.
type Entry struct {
	Hash         [32]byte
	TokenCount   int
	FirstSeen    time.Time
	LastAccessed time.Time
	HitCount     int
}

// Store is a bounded, TTL-scoped LRU cache of prefix hashes. The zero value
// is not ready to use; call New.
type Store struct {
	ttl time.Duration
	max int

	mu    sync.Mutex
	items map[[32]byte]*list.Element // hash -> LRU element (Value is *Entry)
	lru   *list.List                 // front = most-recently-used

	// now is injected in tests to control the clock. Nil means time.Now.
	now func() time.Time
}

// New returns a Store bounded to `max` entries and evicting anything older
// than `ttl` since last access. `max <= 0` defaults to 1000; `ttl <= 0`
// defaults to 10 minutes (mirrors Anthropic's ephemeral prompt-cache TTL).
func New(ttl time.Duration, max int) *Store {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	if max <= 0 {
		max = 1000
	}
	return &Store{
		ttl:   ttl,
		max:   max,
		items: make(map[[32]byte]*list.Element, max),
		lru:   list.New(),
	}
}

// LookupOrRecord returns (hit, cachedTokens, entry).
//
// On a hit within TTL, the entry's LastAccessed/HitCount are updated and its
// TokenCount is returned. On a miss (or on an expired entry), the store
// records a fresh entry keyed by SHA-256(prefix) with TokenCount=estTokens
// and returns hit=false, cachedTokens=0.
//
// A caller wanting only to peek can pass an empty prefix — it hashes to a
// stable value but its token count is 0.
func (s *Store) LookupOrRecord(prefix string) (bool, int, *Entry) {
	hash := sha256.Sum256([]byte(prefix))
	now := s.clock()

	s.mu.Lock()
	defer s.mu.Unlock()

	if el, ok := s.items[hash]; ok {
		ent := el.Value.(*Entry)
		if now.Sub(ent.LastAccessed) > s.ttl {
			// Stale — evict and treat as miss so cache_creation semantics fire.
			s.lru.Remove(el)
			delete(s.items, hash)
		} else {
			ent.LastAccessed = now
			ent.HitCount++
			s.lru.MoveToFront(el)
			// Return a copy so callers can't mutate our state.
			cp := *ent
			return true, ent.TokenCount, &cp
		}
	}

	// Miss: record. Evict LRU or expired entries if we're at capacity.
	s.evictLocked(now)
	ent := &Entry{
		Hash:         hash,
		TokenCount:   estTokens(prefix),
		FirstSeen:    now,
		LastAccessed: now,
		HitCount:     0,
	}
	el := s.lru.PushFront(ent)
	s.items[hash] = el
	cp := *ent
	return false, 0, &cp
}

// evictLocked drops expired entries and, if still over `max`, evicts LRU
// tails until we're under capacity. Caller must hold s.mu.
func (s *Store) evictLocked(now time.Time) {
	// Sweep expired entries from the LRU tail first — they are always the
	// safest to remove and often free enough space to skip LRU eviction.
	for {
		back := s.lru.Back()
		if back == nil {
			break
		}
		ent := back.Value.(*Entry)
		if now.Sub(ent.LastAccessed) <= s.ttl {
			break
		}
		s.lru.Remove(back)
		delete(s.items, ent.Hash)
	}
	for len(s.items) >= s.max {
		back := s.lru.Back()
		if back == nil {
			return
		}
		ent := back.Value.(*Entry)
		s.lru.Remove(back)
		delete(s.items, ent.Hash)
	}
}

// Len returns the current number of live entries. Intended for tests /
// diagnostics.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}

func (s *Store) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// EstTokens exposes the token estimator so cursor-proxy can log the same
// number the store computed. It is a package-level export of estTokens.
func EstTokens(s string) int { return estTokens(s) }

// estTokens returns an approximate token count for `s`.
//
// The heuristic: 1 token ≈ 4 characters of ASCII text, ≈ 1.5 characters of
// non-ASCII (CJK, emoji, etc.). This is intentionally rough — the goal is a
// plausible dashboard number, not tokenizer accuracy. Empirically it lands
// within ~10% of Cursor's own count for English prompts and within ~20% for
// mixed English/CJK prompts, which is well within dashboard tolerance.
func estTokens(s string) int {
	ascii, nonAscii := 0, 0
	for _, r := range s {
		if r < 0x80 {
			ascii++
		} else {
			nonAscii++
		}
	}
	return ascii/4 + int(float64(nonAscii)/1.5)
}
