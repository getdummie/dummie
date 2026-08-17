package main

import (
	"testing"
	"time"
)

func TestParseTTLSeconds(t *testing.T) {
	tests := []struct {
		name    string
		secs    int64
		want    time.Duration
		wantErr bool
	}{
		// Zero is the ordinary case, not an omission to complain about: most VMs and
		// most allowances are permanent.
		{name: "absent", secs: 0, want: 0},
		{name: "floor", secs: 10, want: 10 * time.Second},
		{name: "an hour", secs: 3600, want: time.Hour},
		{name: "ceiling", secs: 30 * 24 * 3600, want: 30 * 24 * time.Hour},
		// Below the floor the deadline can land inside the time it takes to apply,
		// which is a promise the pipeline cannot keep.
		{name: "under the floor", secs: 5, wantErr: true},
		{name: "over the ceiling", secs: 31 * 24 * 3600, wantErr: true},
		{name: "negative", secs: -60, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTTLSeconds(tc.secs)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseTTLSeconds(%d) = %v, want an error", tc.secs, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseTTLSeconds(%d): %v", tc.secs, err)
			}
			if got != tc.want {
				t.Errorf("parseTTLSeconds(%d) = %v, want %v", tc.secs, got, tc.want)
			}
		})
	}
}

func TestFormatTTL(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{45 * time.Second, "45s"},
		{90 * time.Second, "90s"},
		{30 * time.Minute, "30m"},
		{2 * time.Hour, "2h"},
		{90 * time.Minute, "90m"},
		{3 * 24 * time.Hour, "3d"},
	}
	for _, tc := range tests {
		if got := formatTTL(tc.in); got != tc.want {
			t.Errorf("formatTTL(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The backoff is only used for retries a handler had no expectation about, so
// what matters is that it grows and then stops growing -- an unbounded doubling
// would turn a transient fault into a task that effectively never runs again.
func TestTaskBackoffGrowsAndIsCapped(t *testing.T) {
	prev := time.Duration(0)
	for attempts := int32(0); attempts < 6; attempts++ {
		d := taskBackoff(attempts)
		if d <= prev {
			t.Errorf("taskBackoff(%d) = %v, want more than the previous %v", attempts, d, prev)
		}
		prev = d
	}
	for _, attempts := range []int32{6, 20, 1000} {
		if d := taskBackoff(attempts); d > 2*time.Minute {
			t.Errorf("taskBackoff(%d) = %v, want no more than 2m", attempts, d)
		}
	}
}
