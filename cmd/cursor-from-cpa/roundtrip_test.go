package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRoundTrip covers cursor-batch-import → cursor-to-cpa →
// cursor-from-cpa. The final cursor-proto Account files must byte-match
// what cursor-batch-import wrote.
//
// The test builds all three binaries into a scratch dir before running
// them so it is self-contained (no assumption about PATH). It skips if
// `go build` is unavailable (should never happen in CI).
func TestRoundTrip(t *testing.T) {
	if os.Getenv("SKIP_CURSOR_INTEGRATION") == "1" {
		t.Skip("SKIP_CURSOR_INTEGRATION=1")
	}
	root := repoRoot(t)
	binDir := t.TempDir()

	build := func(pkg, name string) string {
		p := filepath.Join(binDir, name)
		cmd := exec.Command("go", "build", "-o", p, "./cmd/"+pkg)
		cmd.Dir = root
		cmd.Env = os.Environ()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", pkg, err, out)
		}
		return p
	}
	batchImport := build("cursor-batch-import", "cursor-batch-import")
	toCPA := build("cursor-to-cpa", "cursor-to-cpa")
	fromCPA := build("cursor-from-cpa", "cursor-from-cpa")

	dir := t.TempDir()
	poolA := filepath.Join(dir, "pool-a")
	cpaDir := filepath.Join(dir, "cpa-pool")
	poolB := filepath.Join(dir, "pool-b")

	// Craft a synthetic CSV. Tokens are JWT-shaped so IssuedAt/ExpiresAt
	// resolve deterministically.
	tokA := fakeJWT(t, 1_700_000_000, 1_700_003_600)
	tokB := fakeJWT(t, 1_700_100_000, 1_700_103_600)
	csv := "email,access_token,refresh_token\n" +
		"alice@example.com," + tokA + ",r1\n" +
		"bob@example.com," + tokB + ",r2\n"
	csvPath := filepath.Join(dir, "tokens.csv")
	if err := os.WriteFile(csvPath, []byte(csv), 0o600); err != nil {
		t.Fatalf("write csv: %v", err)
	}

	run := func(bin string, args ...string) {
		cmd := exec.Command(bin, args...)
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s %s: %v", filepath.Base(bin), strings.Join(args, " "), err)
		}
	}

	run(batchImport, "--csv", csvPath, "--out", poolA, "--skip-validate", "--force")
	run(toCPA, "--dir-in", poolA, "--dir", cpaDir)
	run(fromCPA, "--dir-in", cpaDir, "--out", poolB)

	// Compare every *.json in poolA with poolB. They must match byte-for-byte
	// (cursor-proto Account JSON is deterministic once IssuedAt/ExpiresAt are
	// pinned from the JWT).
	names := listJSON(t, poolA)
	got := listJSON(t, poolB)
	if len(names) != 2 || len(got) != 2 {
		t.Fatalf("expected 2 files each, got %d / %d", len(names), len(got))
	}
	for _, name := range names {
		orig, err := os.ReadFile(filepath.Join(poolA, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		round, err := os.ReadFile(filepath.Join(poolB, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !bytes.Equal(orig, round) {
			t.Errorf("round-trip mismatch for %s\n--- original ---\n%s\n--- round-trip ---\n%s",
				name, string(orig), string(round))
		}
	}
}

func fakeJWT(t *testing.T, iat, exp int64) string {
	t.Helper()
	body, err := json.Marshal(struct {
		Iat int64 `json:"iat,omitempty"`
		Exp int64 `json:"exp,omitempty"`
	}{Iat: iat, Exp: exp})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	head := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	pl := base64.RawURLEncoding.EncodeToString(body)
	sig := base64.RawURLEncoding.EncodeToString([]byte("sig"))
	return head + "." + pl + "." + sig
}

func listJSON(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		out = append(out, e.Name())
	}
	return out
}

func repoRoot(t *testing.T) string {
	t.Helper()
	// This test file lives at <root>/cmd/cursor-from-cpa/roundtrip_test.go
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}
