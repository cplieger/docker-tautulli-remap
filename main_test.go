package main

import (
	"context"
	"testing"
)

// TestFinishTrigger pins the trigger subcommand's exit-code contract, in
// particular the graceful-shutdown guard: a cancelled context is never a
// failure (exit 0) regardless of Run's bool, and never touches the health
// marker. Only a live-context run lets Run's bool pick exit 0 vs 1.
func TestFinishTrigger(t *testing.T) {
	tests := []struct {
		wantHealthy *bool // nil => setHealthy must not be called
		name        string
		cancel      bool
		ok          bool
		wantCode    int
	}{
		{name: "graceful shutdown returns 0 without touching marker", cancel: true, ok: false, wantCode: 0, wantHealthy: nil},
		{name: "shutdown with ok=true still returns 0 without touching marker", cancel: true, ok: true, wantCode: 0, wantHealthy: nil},
		{name: "success marks healthy and returns 0", cancel: false, ok: true, wantCode: 0, wantHealthy: new(true)},
		{name: "failure leaves marker untouched and returns 1", cancel: false, ok: false, wantCode: 1, wantHealthy: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.cancel {
				cancel()
			}

			var got *bool
			setHealthy := func(v bool) { got = &v }

			code := finishTrigger(ctx, tt.ok, setHealthy)
			if code != tt.wantCode {
				t.Errorf("finishTrigger code = %d, want %d", code, tt.wantCode)
			}
			switch {
			case tt.wantHealthy == nil && got != nil:
				t.Errorf("setHealthy called with %v, want not called", *got)
			case tt.wantHealthy != nil && got == nil:
				t.Errorf("setHealthy not called, want called with %v", *tt.wantHealthy)
			case tt.wantHealthy != nil && got != nil && *got != *tt.wantHealthy:
				t.Errorf("setHealthy called with %v, want %v", *got, *tt.wantHealthy)
			}
		})
	}
}
