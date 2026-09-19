package rline

import (
	"testing"
	"time"
)

func TestCompletionRows(t *testing.T) {
	tests := []struct {
		env  string
		want int
	}{
		{"", defaultCompletionRows},
		{"3", 3},
		{"25", 25},
		{"1", minCompletionRows},     // clamped up
		{"10000", maxCompletionRows}, // clamped down
		{"bogus", defaultCompletionRows},
	}
	for _, test := range tests {
		t.Setenv("USQL_COMPLETION_ROWS", test.env)
		if got := completionRows(); got != test.want {
			t.Errorf("completionRows(%q) = %d, want %d", test.env, got, test.want)
		}
	}
}

func TestCompletionDelay(t *testing.T) {
	tests := []struct {
		env  string
		want time.Duration
	}{
		{"", defaultCompletionDelay},
		{"400", 400 * time.Millisecond},
		{"1", minCompletionDelay},      // clamped up
		{"999999", maxCompletionDelay}, // clamped down
		{"bogus", defaultCompletionDelay},
	}
	for _, test := range tests {
		t.Setenv("USQL_COMPLETION_DELAY", test.env)
		if got := completionDelay(); got != test.want {
			t.Errorf("completionDelay(%q) = %v, want %v", test.env, got, test.want)
		}
	}
}
