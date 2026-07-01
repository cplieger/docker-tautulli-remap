package plex

import (
	"testing"

	"github.com/cplieger/tautulli-remap/internal/remap"
)

// FuzzParseGrandparentRatingKey pins the security invariant of the episode-GUID
// resolution parser: any non-empty rating key it returns is numeric, so it is
// safe to interpolate into a Plex /library/metadata/<key> URL. The numeric
// guard inside parseGrandparentRatingKey is the path-traversal defense; this
// target fails if a change ever lets a non-numeric grandparent key through.
func FuzzParseGrandparentRatingKey(f *testing.F) {
	f.Add([]byte(`{"MediaContainer":{"Metadata":[{"grandparentRatingKey":"647130"}]}}`))
	f.Add([]byte(`{"MediaContainer":{"Metadata":[{"grandparentRatingKey":"5"},{"grandparentRatingKey":"5"}]}}`))
	f.Add([]byte(`{"MediaContainer":{"Metadata":[{"grandparentRatingKey":"1"},{"grandparentRatingKey":"2"}]}}`))
	f.Add([]byte(`{"MediaContainer":{"Metadata":[{"grandparentRatingKey":""}]}}`))
	f.Add([]byte(`{"MediaContainer":{"size":0}}`))
	f.Add([]byte(`not json`))
	f.Fuzz(func(t *testing.T, body []byte) {
		got, err := parseGrandparentRatingKey(body)
		if err != nil {
			return
		}
		if got != "" && !remap.RatingKey(got).IsValid() {
			t.Errorf("parseGrandparentRatingKey(%q) = %q, a non-empty result must be a numeric rating key safe for URL interpolation", body, got)
		}
	})
}
