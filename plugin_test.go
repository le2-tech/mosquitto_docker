package main

import (
	"context"
	"testing"
	"time"
)

func TestParseBoolOption(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		input  string
		want   bool
		wantOK bool
	}{
		{"true lowercase", "true", true, true},
		{"true numeric", "1", true, true},
		{"false uppercase", "FALSE", false, true},
		{"no trim", "  yes  ", true, true},
		{"invalid", "maybe", false, false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseBoolOption(tc.input)
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("parseBoolOption(%q) = (%v, %v), want (%v, %v)", tc.input, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestParseTimeoutMS(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input  string
		want   time.Duration
		wantOK bool
	}{
		{"1000", 1000 * time.Millisecond, true},
		{" 250 ", 250 * time.Millisecond, true},
		{"0", 0, false},
		{"-10", 0, false},
		{"abc", 0, false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got, ok := parseTimeoutMS(tc.input)
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("parseTimeoutMS(%q) = (%v, %v), want (%v, %v)", tc.input, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestSafeDSN(t *testing.T) {
	t.Parallel()
	if got := safeDSN("postgres://user:secret@example.com/db"); got != "postgres://user:xxxxx@example.com/db" {
		t.Fatalf("safeDSN masked password = %q", got)
	}
	if raw := "%%%"; safeDSN(raw) != raw {
		t.Fatalf("safeDSN should return original string when parsing fails")
	}
}

func TestSha256PwdSalt(t *testing.T) {
	t.Parallel()
	const want = "7a37b85c8918eac19a9089c0fa5a2ab4dce3f90528dcdeec108b23ddf3607b99"
	if got := sha256PwdSalt("password", "salt"); got != want {
		t.Fatalf("sha256PwdSalt mismatch: got %q want %q", got, want)
	}
}

func TestEnvBool(t *testing.T) {
	t.Setenv("TEST_BOOL_TRUE", "YeS")
	if !envBool("TEST_BOOL_TRUE") {
		t.Fatal("envBool expected true for yes")
	}
	t.Setenv("TEST_BOOL_FALSE", "0")
	if envBool("TEST_BOOL_FALSE") {
		t.Fatal("envBool expected false for 0")
	}
}

func TestCtxTimeout(t *testing.T) {
	oldTimeout := timeout
	t.Cleanup(func() { timeout = oldTimeout })

	timeout = 100 * time.Millisecond
	ctx, cancel := ctxTimeout()
	defer cancel()
	if deadline, ok := ctx.Deadline(); !ok {
		t.Fatal("ctxTimeout expected deadline to be set")
	} else if remaining := time.Until(deadline); remaining < 40*time.Millisecond || remaining > 120*time.Millisecond {
		t.Fatalf("ctxTimeout deadline remaining %v outside expected range", remaining)
	}

	timeout = 0
	ctx, cancel = ctxTimeout()
	cancel()
	if ctx != context.Background() {
		t.Fatalf("ctxTimeout with timeout<=0 should return Background context")
	}
}
