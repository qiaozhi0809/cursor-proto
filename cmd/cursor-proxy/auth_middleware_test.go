package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// stubOK is the downstream handler the middleware should invoke when auth
// passes. It records whether it was called and returns 204 so we can assert
// the response code was set by the downstream, not by the middleware.
type stubOK struct{ called int }

func (s *stubOK) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	s.called++
	w.WriteHeader(http.StatusNoContent)
}

// decodeErrorBody asserts the response body is a valid OpenAI-shaped error
// envelope and returns the inner error object.
func decodeErrorBody(t *testing.T, body string) map[string]any {
	t.Helper()
	var got struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("response body is not valid JSON: %v\nbody=%q", err, body)
	}
	if got.Error == nil {
		t.Fatalf("response body has no \"error\" object: %q", body)
	}
	return got.Error
}

func TestRequireAPIKeys_MissingHeader(t *testing.T) {
	stub := &stubOK{}
	h := RequireAPIKeys([]string{"sk-good"}, stub)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if stub.called != 0 {
		t.Fatalf("downstream should not run without auth, called=%d", stub.called)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected JSON content-type, got %q", got)
	}
	errObj := decodeErrorBody(t, rec.Body.String())
	if code, _ := errObj["code"].(string); code != "invalid_api_key" {
		t.Fatalf("expected error.code=invalid_api_key, got %q", code)
	}
	if typ, _ := errObj["type"].(string); typ != "invalid_request_error" {
		t.Fatalf("expected error.type=invalid_request_error, got %q", typ)
	}
	if _, present := errObj["param"]; !present {
		t.Fatalf("expected error.param to be present (null), got %v", errObj)
	}
}

func TestRequireAPIKeys_WrongKey(t *testing.T) {
	stub := &stubOK{}
	h := RequireAPIKeys([]string{"sk-good"}, stub)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer sk-bad")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if stub.called != 0 {
		t.Fatalf("downstream should not run with wrong key")
	}
	errObj := decodeErrorBody(t, rec.Body.String())
	if code, _ := errObj["code"].(string); code != "invalid_api_key" {
		t.Fatalf("expected error.code=invalid_api_key, got %q", code)
	}
}

func TestRequireAPIKeys_CorrectKey(t *testing.T) {
	stub := &stubOK{}
	h := RequireAPIKeys([]string{"sk-first", "sk-second"}, stub)

	for _, key := range []string{"sk-first", "sk-second"} {
		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("key %q: expected downstream 204, got %d", key, rec.Code)
		}
	}
	if stub.called != 2 {
		t.Fatalf("expected 2 downstream calls, got %d", stub.called)
	}
}

func TestRequireAPIKeys_CaseInsensitiveScheme(t *testing.T) {
	stub := &stubOK{}
	h := RequireAPIKeys([]string{"sk-good"}, stub)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "bearer sk-good")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if stub.called != 1 {
		t.Fatalf("downstream should have been called once")
	}
}

func TestRequireAPIKeys_EmptyConfigIsPassthrough(t *testing.T) {
	stub := &stubOK{}
	h := RequireAPIKeys(nil, stub)

	// No header at all — with empty config, must still pass through.
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected passthrough 204, got %d", rec.Code)
	}

	// A garbage header should also pass through — empty config means no auth.
	req2 := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req2.Header.Set("Authorization", "Bearer whatever")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("expected passthrough 204, got %d", rec2.Code)
	}

	if stub.called != 2 {
		t.Fatalf("expected 2 downstream calls in passthrough mode, got %d", stub.called)
	}
}

func TestRequireAPIKeys_ConstantTimeCompareStaticInspection(t *testing.T) {
	// Loose sanity check: crypto/subtle.ConstantTimeCompare must appear in the
	// middleware source. This is deliberately a static check rather than a
	// timing benchmark, since real timing measurements are too flaky for CI.
	src, err := os.ReadFile("auth_middleware.go")
	if err != nil {
		t.Fatalf("read auth_middleware.go: %v", err)
	}
	if !strings.Contains(string(src), "subtle.ConstantTimeCompare") {
		t.Fatalf("expected auth_middleware.go to use subtle.ConstantTimeCompare")
	}
	if !strings.Contains(string(src), "\"crypto/subtle\"") {
		t.Fatalf("expected auth_middleware.go to import crypto/subtle")
	}

	// And a very loose runtime check: two wrong keys of very different lengths
	// should both fail without the check short-circuiting one obviously faster
	// than the other. We only check that neither call returns 2xx and that
	// both complete quickly enough to not be pathological.
	stub := &stubOK{}
	h := RequireAPIKeys([]string{strings.Repeat("a", 64)}, stub)

	for _, wrong := range []string{"x", strings.Repeat("z", 4096)} {
		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		req.Header.Set("Authorization", "Bearer "+wrong)
		rec := httptest.NewRecorder()
		start := time.Now()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong key of len %d should be rejected, got %d", len(wrong), rec.Code)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("wrong-key path took too long: %v", elapsed)
		}
	}
	if stub.called != 0 {
		t.Fatalf("downstream should never be called on wrong key")
	}
}

func TestLoadAPIKeys_FlagBeatsEnv(t *testing.T) {
	t.Setenv(apiKeysEnv, "sk-env-1,sk-env-2")

	got := LoadAPIKeys("sk-flag-1 , sk-flag-2 ,, ")
	want := []string{"sk-flag-1", "sk-flag-2"}
	if !stringSliceEqual(got, want) {
		t.Fatalf("flag should win: got %v want %v", got, want)
	}
}

func TestLoadAPIKeys_EnvFallback(t *testing.T) {
	t.Setenv(apiKeysEnv, " sk-a , sk-b ,   ")

	got := LoadAPIKeys("")
	want := []string{"sk-a", "sk-b"}
	if !stringSliceEqual(got, want) {
		t.Fatalf("env fallback: got %v want %v", got, want)
	}
}

func TestLoadAPIKeys_EmptyEverywhere(t *testing.T) {
	t.Setenv(apiKeysEnv, "")
	if got := LoadAPIKeys(""); got != nil {
		t.Fatalf("expected nil with no config, got %v", got)
	}
	// Whitespace-only should also collapse to nil.
	t.Setenv(apiKeysEnv, "  , , ")
	if got := LoadAPIKeys(""); got != nil {
		t.Fatalf("expected nil for whitespace-only config, got %v", got)
	}
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
