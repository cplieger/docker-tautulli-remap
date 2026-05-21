package main

import (
	"os"
	"testing"
	"time"
)

// TestWriteAndReadLastRun_roundtrip verifies write then read preserves time.
func TestWriteAndReadLastRun_roundtrip(t *testing.T) {
	original, _ := os.ReadFile(lastRunFile)
	t.Cleanup(func() {
		if original != nil {
			os.WriteFile(lastRunFile, original, 0o600)
		} else {
			os.Remove(lastRunFile)
		}
	})

	writeLastRun()
	got := readLastRun()
	if got.IsZero() {
		t.Fatal("readLastRun() returned zero time after writeLastRun()")
	}
	if time.Since(got) > 5*time.Second {
		t.Errorf("readLastRun() = %v, expected within 5s of now", got)
	}
}

// TestReadLastRun_missing_file returns zero time.
func TestReadLastRun_missing_file(t *testing.T) {
	os.Remove(lastRunFile)
	t.Cleanup(func() { os.Remove(lastRunFile) })
	got := readLastRun()
	if !got.IsZero() {
		t.Errorf("readLastRun() = %v, want zero time for missing file", got)
	}
}

// TestReadLastRun_invalid_content returns zero time.
func TestReadLastRun_invalid_content(t *testing.T) {
	if err := os.WriteFile(lastRunFile, []byte("not-a-timestamp"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	t.Cleanup(func() { os.Remove(lastRunFile) })
	got := readLastRun()
	if !got.IsZero() {
		t.Errorf("readLastRun() = %v, want zero time for invalid content", got)
	}
}
