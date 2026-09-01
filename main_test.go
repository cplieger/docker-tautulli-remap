package main

import (
	"context"
	"testing"
)

// TestFinishTrigger pins the exit-code contract: a cancelled context means the
// pass did not verifiably complete, so it always exits exitInterrupted
// (never 0 or 1) and never touches the health marker, regardless of Run's
// bool. Only a live context lets Run's bool pick exit 0 vs 1.
func TestFinishTrigger(t *testing.T) {
	tests := []struct {
		wantHealthy *bool // nil => setHealthy must not be called
		name        string
		cancel      bool
		ok          bool
		wantCode    int
	}{
		{name: "interrupted pass returns exitInterrupted without touching marker", cancel: true, ok: false, wantCode: exitInterrupted, wantHealthy: nil},
		{name: "interruption trumps ok=true (completion not verifiable)", cancel: true, ok: true, wantCode: exitInterrupted, wantHealthy: nil},
		{name: "success marks healthy and returns 0", cancel: false, ok: true, wantCode: 0, wantHealthy: new(true)},
		{name: "failure leaves marker untouched and returns 1", cancel: false, ok: false, wantCode: 1, wantHealthy: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Background, not t.Context(): pre-cancelled on purpose to drive
			// the exitInterrupted path.
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
