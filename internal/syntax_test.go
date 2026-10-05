package ifttt

import "testing"

func TestInitialDirectivePrefix(t *testing.T) {
	if got := CurrentDirectiveSyntax(); got.Prefix != "LINT" || got.TokenIfChange != "LINT.IfChange" || got.TokenThenChange != "LINT.ThenChange" {
		t.Fatalf("initial syntax = %+v; want standard LINT", got)
	}
}

func TestSetDirectivePrefixDefaultAndOverrides(t *testing.T) {
	previous := CurrentDirectiveSyntax().Prefix
	t.Cleanup(func() { SetDirectivePrefix(previous) })
	for _, tc := range []struct{ prefix, want string }{
		{"CUSTOM", "CUSTOM"}, {"", "LINT"}, {"SENTRY", "SENTRY"},
		{" \t\n", "LINT"}, {" LINT ", "LINT"},
	} {
		SetDirectivePrefix(tc.prefix)
		if got := CurrentDirectiveSyntax(); got.Prefix != tc.want || got.PrefixDot != tc.want+"." {
			t.Fatalf("prefix %q: got %+v, want %q", tc.prefix, got, tc.want)
		}
	}
}
