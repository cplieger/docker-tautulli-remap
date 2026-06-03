package main

import (
	"os"
	"path/filepath"
	"testing"

	"pgregory.net/rapid"
)

// TestHealthMarker_SetCreatesAndRemoves covers the happy path: a writable
// dir, Set(true) creates the marker, Set(false) removes it.
func TestHealthMarker_SetCreatesAndRemoves(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".healthy")
	m := newHealthMarker(path)

	m.Set(true)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("marker should exist after Set(true): %v", err)
	}

	m.Set(false)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("marker should not exist after Set(false): %v", err)
	}
}

// TestHealthMarker_Cleanup confirms Cleanup removes the marker and is
// safe to call when the marker already does not exist.
func TestHealthMarker_Cleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".healthy")
	m := newHealthMarker(path)

	m.Set(true)
	m.Cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("marker should be gone after Cleanup: %v", err)
	}

	// Second cleanup must not error.
	m.Cleanup()
}

// TestHealthMarker_DegradedMode verifies that when the marker directory
// is not writable, the marker enters degraded mode: Set and Cleanup are
// no-ops and no file is ever created.
func TestHealthMarker_DegradedMode(t *testing.T) {
	// Create a read-only directory to simulate a compose misconfiguration.
	dir := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatalf("mkdir ro: %v", err)
	}

	path := filepath.Join(dir, ".healthy")

	// Some environments (root, permissive filesystems like Windows
	// or containers) allow writes through 0500; skip rather than
	// fail in those cases.
	if probeHealthDir(path) == nil {
		t.Skip("test environment bypasses directory mode; skipping")
	}

	m := newHealthMarker(path)

	m.Set(true)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("degraded marker should never create file: %v", err)
	}
	m.Cleanup() // must not panic
}

// TestHealthMarker_Idempotent ensures repeated Set(true) and Set(false)
// calls are safe and converge to the expected file state.
func TestHealthMarker_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".healthy")
	m := newHealthMarker(path)

	for range 3 {
		m.Set(true)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("marker should exist after repeated Set(true): %v", err)
	}

	for range 3 {
		m.Set(false)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("marker should not exist after repeated Set(false): %v", err)
	}
}

// TestHealthMarker_Property exercises arbitrary Set sequences and asserts
// that the file state always matches the last Set argument.
func TestHealthMarker_Property(t *testing.T) {
	dir := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		// A fresh subdir per iteration so markers from earlier iterations
		// don't leak into later ones.
		nonce := rapid.StringMatching(`[a-z0-9]{8}`).Draw(rt, "nonce")
		subdir := filepath.Join(dir, nonce)
		if err := os.Mkdir(subdir, 0o755); err != nil {
			rt.Fatalf("mkdir subdir: %v", err)
		}
		path := filepath.Join(subdir, ".healthy")
		m := newHealthMarker(path)

		calls := rapid.SliceOfN(rapid.Bool(), 1, 30).Draw(rt, "calls")
		for _, ok := range calls {
			m.Set(ok)
		}
		last := calls[len(calls)-1]

		_, err := os.Stat(path)
		exists := err == nil
		if exists != last {
			rt.Fatalf("after Set(%v): exists=%v, want %v",
				last, exists, last)
		}
	})
}

// TestProbeHealthDir_Writable confirms the probe succeeds on a normal
// writable temp dir and leaves no artifact behind.
func TestProbeHealthDir_Writable(t *testing.T) {
	dir := t.TempDir()
	if err := probeHealthDir(filepath.Join(dir, ".healthy")); err != nil {
		t.Fatalf("probeHealthDir on writable dir: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("probe left artifacts behind: %v", entries)
	}
}

// TestProbeHealthDir_NonExistent confirms a missing parent directory is
// reported as an error rather than masked.
func TestProbeHealthDir_NonExistent(t *testing.T) {
	err := probeHealthDir(filepath.Join(t.TempDir(), "nope", ".healthy"))
	if err == nil {
		t.Fatal("expected error for non-existent parent dir")
	}
}

// TestProbeCheck_healthy verifies probeCheck returns 0 when the marker
// file exists in a writable directory.
func TestProbeCheck_healthy(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".healthy")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if got := probeCheck(path); got != 0 {
		t.Errorf("probeCheck(%q) = %d, want 0 (marker exists)", path, got)
	}
}

// TestProbeCheck_unhealthy verifies probeCheck returns 1 when the marker
// is absent from a writable directory.
func TestProbeCheck_unhealthy(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".healthy")
	if got := probeCheck(path); got != 1 {
		t.Errorf("probeCheck(%q) = %d, want 1 (marker absent, dir writable)", path, got)
	}
}

// TestProbeCheck_degraded verifies probeCheck returns 0
// (healthy) when the marker directory is not writable (degraded mode).
func TestProbeCheck_degraded(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, ".healthy")
	// Verify the directory is actually read-only for this test
	if probeHealthDir(path) == nil {
		t.Skip("test environment bypasses directory mode; skipping")
	}
	if got := probeCheck(path); got != 0 {
		t.Errorf("probeCheck(%q) = %d, want 0 (degraded mode)", path, got)
	}
}
