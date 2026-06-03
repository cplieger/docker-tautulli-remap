package main

import (
	"os"
	"path/filepath"

	"github.com/cplieger/health"
)

// healthMarkerPath is the default marker location.
const healthMarkerPath = health.DefaultPath

// healthMarker wraps the library's Marker to keep the existing internal API.
type healthMarker = health.Marker

// newHealthMarker constructs a marker for path.
func newHealthMarker(path string) *healthMarker {
	return health.NewMarker(path)
}

// runProbe delegates to the library's RunProbe.
func runProbe(path string) {
	health.RunProbe(path)
}

// probeCheck delegates to the library's ProbeCheck (for tests).
func probeCheck(path string) int {
	return health.ProbeCheck(path)
}

// probeHealthDir verifies the marker's parent directory is writable.
// Kept as a local helper for tests that assert directory writability.
func probeHealthDir(path string) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".health-probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return nil
}
