package main

import "github.com/cplieger/health"

// healthMarkerPath is the marker location, sourced from the library.
const healthMarkerPath = health.DefaultPath

// healthMarker aliases the library's Marker to keep the existing internal API.
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
