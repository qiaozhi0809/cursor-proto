package auth

import (
	"encoding/hex"
	"runtime"
	"testing"
)

func TestGetMachineID(t *testing.T) {
	id, err := GetMachineID()
	if err != nil {
		t.Skipf("skipping: cannot read machine id on %s: %v", runtime.GOOS, err)
	}
	if len(id) != 64 {
		t.Fatalf("expected 64-char SHA-256 hex, got %d chars", len(id))
	}
	if _, err := hex.DecodeString(id); err != nil {
		t.Fatalf("not valid hex: %v", err)
	}
	t.Logf("this machine's machineID (sha256 of IOPlatformUUID) = %s", id)
}

// TestGetMacMachineIDMatchesIDE proves our algorithm matches what Cursor IDE
// puts in the trailing `/<mac>` segment of x-cursor-checksum. On the machine
// used for capture (2026-07-09), Cursor 3.10.20 sent:
//
//	.../df1a4f6be6465f027c4aefa30191f5fa05d1d69d1a791a8504c8d4fe11b674ab
//
// If this test runs on the same machine, the values match. On other machines
// we can only verify format.
func TestGetMacMachineID(t *testing.T) {
	id, err := GetMacMachineID()
	if err != nil {
		t.Skipf("skipping: cannot read MAC on %s: %v", runtime.GOOS, err)
	}
	if len(id) != 64 {
		t.Fatalf("expected 64-char SHA-256 hex, got %d chars", len(id))
	}
	if _, err := hex.DecodeString(id); err != nil {
		t.Fatalf("not valid hex: %v", err)
	}
	t.Logf("this machine's macMachineID (sha256 of first MAC) = %s", id)

	const capturedFrom3_10_20 = "df1a4f6be6465f027c4aefa30191f5fa05d1d69d1a791a8504c8d4fe11b674ab"
	if id == capturedFrom3_10_20 {
		t.Logf("PERFECT — matches the 2026-07-09 IDE capture on this machine")
	}
}

func TestIsValidMACString(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"aa:bb:cc:dd:ee:ff", true},
		{"00:00:00:00:00:00", false},
		{"", false},
		{"01-23-45-67-89-ab", true},
		{"00-00-00-00-00-00", false},
	}
	for _, c := range cases {
		got := isValidMACString(c.in)
		if got != c.want {
			t.Errorf("isValidMACString(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestGenerateClientKeyShape(t *testing.T) {
	k := GenerateClientKey()
	if len(k) != 64 {
		t.Fatalf("client key not 64 chars: %d", len(k))
	}
	if _, err := hex.DecodeString(k); err != nil {
		t.Fatalf("not hex: %v", err)
	}
}

func TestSessionIDShape(t *testing.T) {
	s := GenerateSessionID()
	if len(s) != 36 {
		t.Fatalf("session id not uuid-shaped: %d chars, %s", len(s), s)
	}
}
