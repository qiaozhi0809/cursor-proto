package auth

import (
	"testing"
	"time"
)

// TestGenerateChecksumMatchesIDE verifies our implementation reproduces the
// exact byte sequence captured from Cursor 3.10.20 IDE on 2026-07-09.
//
// The captured checksum was:
//
//	kqyuuJOv4071c661bcb367c518becc7b3d4d57cbd69d2291d8b302c558d79080f8fd4f75/df1a4f6be6465f027c4aefa30191f5fa05d1d69d1a791a8504c8d4fe11b674ab
//
// Session start time was around 2026-07-09 22:56:40 local (Asia/Shanghai),
// corresponding to Date.now() ≈ 1783609000000 ms → E = 1783609.
func TestGenerateChecksumMatchesIDE(t *testing.T) {
	// IDE snapshot: Date.now() = 1783609 * 1e6
	snapshot := time.UnixMilli(1_783_609 * 1_000_000)

	machineID := "4071c661bcb367c518becc7b3d4d57cbd69d2291d8b302c558d79080f8fd4f75"
	macMachineID := "df1a4f6be6465f027c4aefa30191f5fa05d1d69d1a791a8504c8d4fe11b674ab"
	want := "kqyuuJOv4071c661bcb367c518becc7b3d4d57cbd69d2291d8b302c558d79080f8fd4f75/df1a4f6be6465f027c4aefa30191f5fa05d1d69d1a791a8504c8d4fe11b674ab"

	got := GenerateChecksum(snapshot, machineID, macMachineID)
	if got != want {
		t.Fatalf("checksum mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestGenerateChecksumNoMac(t *testing.T) {
	// Without macMachineID the trailing "/<mac>" segment is omitted.
	snapshot := time.UnixMilli(1_783_609 * 1_000_000)
	machineID := "4071c661bcb367c518becc7b3d4d57cbd69d2291d8b302c558d79080f8fd4f75"
	got := GenerateChecksum(snapshot, machineID, "")
	want := "kqyuuJOv4071c661bcb367c518becc7b3d4d57cbd69d2291d8b302c558d79080f8fd4f75"
	if got != want {
		t.Fatalf("no-mac checksum mismatch\n got: %s\nwant: %s", got, want)
	}
}
