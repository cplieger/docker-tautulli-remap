package remap

import (
	"encoding/json"
	"testing"
)

func TestFlexIntUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name string
		json string
		want FlexInt
	}{
		{"float", "42.0", 42},
		{"string", `"123"`, 123},
		{"empty string", `""`, 0},
		{"null", "null", 0},
		{"invalid string", `"abc"`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f FlexInt
			if err := json.Unmarshal([]byte(tt.json), &f); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}
			if f != tt.want {
				t.Errorf("got %d, want %d", f, tt.want)
			}
		})
	}
}

func TestMediaType(t *testing.T) {
	if Movie.String() != "movie" {
		t.Errorf("Movie.String() = %q", Movie.String())
	}
	var m MediaType
	if err := m.UnmarshalText([]byte("show")); err != nil {
		t.Fatal(err)
	}
	if m != Show {
		t.Errorf("got %q", m)
	}
	if err := m.UnmarshalText([]byte("invalid")); err == nil {
		t.Error("expected error for invalid media type")
	}
}

// TestParseMediaType pins the public string-to-MediaType mapping, including the
// default branch where an unrecognized type yields the empty MediaType (which
// causes such items to be dropped from the index downstream).
func TestParseMediaType(t *testing.T) {
	tests := []struct {
		in   string
		want MediaType
	}{
		{"movie", Movie},
		{"show", Show},
		{"episode", Episode},
		{"artist", ""},
		{"track", ""},
		{"", ""},
		{"MOVIE", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := ParseMediaType(tt.in); got != tt.want {
				t.Errorf("ParseMediaType(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestMatchMethod(t *testing.T) {
	if MethodGUID.String() != "guid" {
		t.Errorf("MethodGUID.String() = %q", MethodGUID.String())
	}
}

func TestRatingKeyIsValid(t *testing.T) {
	tests := []struct {
		input RatingKey
		want  bool
	}{
		{"42", true},
		{"0", true},
		{"", false},
		{"-1", false},
		{"abc", false},
	}
	for _, tt := range tests {
		if got := tt.input.IsValid(); got != tt.want {
			t.Errorf("RatingKey(%q).IsValid() = %v, want %v", tt.input, got, tt.want)
		}
	}
}
