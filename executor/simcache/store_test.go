package simcache

import (
	"strings"
	"testing"
	"time"
)

func TestLookupOrRecord_MissThenHit(t *testing.T) {
	s := New(10*time.Minute, 100)
	prefix := "system: You are helpful.\nuser: hi\nassistant: hello"

	hit1, tokens1, ent1 := s.LookupOrRecord(prefix)
	if hit1 {
		t.Fatalf("first call: expected miss, got hit")
	}
	if tokens1 != 0 {
		t.Fatalf("first call: cachedTokens=%d want 0", tokens1)
	}
	if ent1 == nil || ent1.HitCount != 0 {
		t.Fatalf("first call: entry=%+v want HitCount=0", ent1)
	}
	if ent1.TokenCount != estTokens(prefix) {
		t.Fatalf("first call: TokenCount=%d want %d", ent1.TokenCount, estTokens(prefix))
	}

	hit2, tokens2, ent2 := s.LookupOrRecord(prefix)
	if !hit2 {
		t.Fatalf("second call: expected hit, got miss")
	}
	if tokens2 != estTokens(prefix) {
		t.Fatalf("second call: cachedTokens=%d want %d", tokens2, estTokens(prefix))
	}
	if ent2 == nil || ent2.HitCount != 1 {
		t.Fatalf("second call: HitCount=%d want 1", ent2.HitCount)
	}
}

func TestLookupOrRecord_DifferentPrefixesDoNotCollide(t *testing.T) {
	s := New(10*time.Minute, 100)
	a := "prefix-A"
	b := "prefix-B"
	s.LookupOrRecord(a)
	s.LookupOrRecord(b)
	if hit, _, _ := s.LookupOrRecord(a); !hit {
		t.Fatalf("prefix A should hit after being recorded")
	}
	if hit, _, _ := s.LookupOrRecord(b); !hit {
		t.Fatalf("prefix B should hit after being recorded")
	}
	if s.Len() != 2 {
		t.Fatalf("Len=%d want 2", s.Len())
	}
}

func TestTTLEviction(t *testing.T) {
	s := New(100*time.Millisecond, 100)
	// Fake clock so we don't sleep.
	base := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return base }

	s.LookupOrRecord("p")
	if hit, _, _ := s.LookupOrRecord("p"); !hit {
		t.Fatalf("immediate re-lookup should hit")
	}

	// Advance past TTL.
	s.now = func() time.Time { return base.Add(200 * time.Millisecond) }
	hit, tokens, ent := s.LookupOrRecord("p")
	if hit {
		t.Fatalf("post-TTL should be miss, got hit")
	}
	if tokens != 0 {
		t.Fatalf("post-TTL miss should have cachedTokens=0, got %d", tokens)
	}
	if ent.HitCount != 0 {
		t.Fatalf("post-TTL re-record HitCount=%d want 0", ent.HitCount)
	}
}

func TestLRUEviction(t *testing.T) {
	s := New(10*time.Minute, 2)
	s.LookupOrRecord("a")
	s.LookupOrRecord("b")
	// Touch a so b becomes LRU.
	if hit, _, _ := s.LookupOrRecord("a"); !hit {
		t.Fatalf("a should still be live")
	}
	// Insert c — b must be evicted.
	s.LookupOrRecord("c")
	if s.Len() != 2 {
		t.Fatalf("Len=%d want 2", s.Len())
	}
	if hit, _, _ := s.LookupOrRecord("b"); hit {
		t.Fatalf("b should have been evicted")
	}
	// b is now a fresh miss so len jumps to 3 briefly, then evicts LRU (c or a).
	// We don't test which — just that the bound is respected.
	if s.Len() > 2 {
		t.Fatalf("Len=%d exceeds max=2", s.Len())
	}
}

func TestEstTokens(t *testing.T) {
	// Rough ASCII: 8 chars → 2 tokens.
	if got := estTokens("abcdefgh"); got != 2 {
		t.Fatalf("ascii: got %d want 2", got)
	}
	// Empty.
	if got := estTokens(""); got != 0 {
		t.Fatalf("empty: got %d want 0", got)
	}
	// CJK: 3 chars → int(3/1.5) = 2.
	cjk := "你好嗎"
	if got := estTokens(cjk); got != 2 {
		t.Fatalf("cjk: got %d want 2 (chars=%d)", got, len([]rune(cjk)))
	}
	// Mixed: "abcd你好" → 4 ascii + 2 cjk → 1 + 1 = 2.
	if got := estTokens("abcd你好"); got != 2 {
		t.Fatalf("mixed: got %d want 2", got)
	}
	// Realistic: ~13000 tokens for a ~52000-char ASCII prompt.
	big := strings.Repeat("word ", 10400) // 52000 chars
	if got := estTokens(big); got < 12000 || got > 14000 {
		t.Fatalf("large prompt estimate off: got %d, want ~13000", got)
	}
}

func TestEmptyPrefixIsRecorded(t *testing.T) {
	s := New(time.Minute, 10)
	if hit, tokens, _ := s.LookupOrRecord(""); hit || tokens != 0 {
		t.Fatalf("first empty: hit=%v tokens=%d want (false, 0)", hit, tokens)
	}
	// Second call should hit — but with 0 tokens (empty prefix estimator = 0).
	hit, tokens, ent := s.LookupOrRecord("")
	if !hit {
		t.Fatalf("second empty: expected hit")
	}
	if tokens != 0 {
		t.Fatalf("second empty: tokens=%d want 0", tokens)
	}
	if ent.HitCount != 1 {
		t.Fatalf("second empty: HitCount=%d want 1", ent.HitCount)
	}
}
