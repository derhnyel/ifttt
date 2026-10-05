package ifttt

import (
	"testing"
)

func TestDecodeCOctal(t *testing.T) {
	cases := map[string]string{
		`foo\040bar`: "foo bar",
		`a\057b`:     "a/b",
		`noesc`:      "noesc",
		`\042`:       `"`,
		`x\061y`:     "x1y",
	}
	for in, want := range cases {
		if got := DecodeCOctal(in); got != want {
			t.Fatalf("%q => %q (want %q)", in, got, want)
		}
	}
}

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		`"a/file\040name.txt"`: "file name.txt",
		"a/foo/bar":            "foo/bar",
		"b/foo/bar":            "foo/bar",
		"./c/../d.txt":         "d.txt",
	}
	for in, want := range cases {
		if got := NormalizePath(in); got != want {
			t.Fatalf("NormalizePath(%q) = %q (want %q)", in, got, want)
		}
	}
}

func TestCompileGlob(t *testing.T) {
	rx := CompileGlob("*.go")
	if !rx.MatchString("x.go") || rx.MatchString("x.py") {
		t.Fatal("glob * mismatch")
	}
	rx = CompileGlob("foo/?ar.txt")
	if !rx.MatchString("foo/bar.txt") || rx.MatchString("foo/barr.txt") {
		t.Fatal("glob ? mismatch")
	}
}

func TestCompileGlobPathBoundaries(t *testing.T) {
	for _, tc := range []struct {
		pattern, path string
		want          bool
	}{
		{"<INTERNAL>/*", "<INTERNAL>/build.sh", true},
		{"<INTERNAL>/*", "<INTERNAL>/release/build.sh", false},
		{"<INTERNAL>/**", "<INTERNAL>/release/build.sh", true},
		{"foo/?ar.txt", "foo//ar.txt", false},
		{"**/*.go", "source.go", true},
		{"**/*.go", "nested/source.go", true},
		{"**/*.go", "nested\nname/source.go", true},
		{"foo/**", "foo/nested\nname/source.go", true},
		{"foo/**/bar.go", "foo/bar.go", true},
		{"foo/**/bar.go", "foo/deep/nested/bar.go", true},
		{"foo/*", `foo\bar.go`, true},
		{"foo/*", `foo\nested\bar.go`, false},
	} {
		t.Run(tc.pattern+tc.path, func(t *testing.T) {
			if got := CompileGlob(tc.pattern).MatchString(tc.path); got != tc.want {
				t.Fatalf("pattern %q against %q: got %v, want %v", tc.pattern, tc.path, got, tc.want)
			}
		})
	}
}
