package comments

import (
	"strings"
	"testing"
)

func TestCommentLexicalBoundaries(t *testing.T) {
	for _, tc := range []struct {
		path, src string
		count     int
	}{
		{"a.c", "// text /* not a block\n// second", 1},
		{"a.c", "/* first */ code /* second */", 2},
		{"a.rs", "// directive", 1},
		{"a.py", "x = \"# fake\"\n# real", 1},
		{"a.md", "```html\n<!-- fake -->\n```\n<!-- real -->", 1},
		{"a.xml", "<!-- real -->", 1},
	} {
		t.Run(tc.path+tc.src, func(t *testing.T) {
			bs, e := ExtractWithLoader(tc.path, func(string) ([]byte, error) { return []byte(tc.src), nil })
			if e != nil || len(bs) != tc.count {
				t.Fatalf("got %+v, %v", bs, e)
			}
		})
	}
}
func TestAssignedPythonTripleStringIsNotComment(t *testing.T) {
	bs, e := ExtractWithLoader("a.py", func(string) ([]byte, error) {
		return []byte("example = \"\"\"\nSENTRY.IfChange(\"fake\")\n\"\"\"\n# real"), nil
	})
	if e != nil || len(bs) != 1 {
		t.Fatalf("got %+v, %v", bs, e)
	}
}
func FuzzExtractPreservesLineNumbers(f *testing.F) {
	f.Add("/* hi */\n// ok\n\"//fake\"")
	f.Fuzz(func(t *testing.T, src string) {
		for _, path := range []string{"a.go", "a.py", "a.md", "a.rs"} {
			bs, e := ExtractWithLoader(path, func(string) ([]byte, error) { return []byte(src), nil })
			if e != nil {
				t.Fatal(e)
			}
			for _, b := range bs {
				if b.Start < 1 || b.Start > 1+strings.Count(src, "\n") {
					t.Fatalf("invalid line %d", b.Start)
				}
			}
		}
	})
}
func TestHTMLSeparatesCommentsOnSameLine(t *testing.T) {
	bs := html("a.xml", "<!-- one --> text <!-- two -->")
	if len(bs) != 2 || bs[0].Text != " one " || bs[1].Text != " two " {
		t.Fatalf("got %+v", bs)
	}
}
func TestRustLifetimeDoesNotHideComments(t *testing.T) {
	bs := rust("a.rs", "fn f(v: &'a str) {}\n// real")
	if len(bs) != 1 {
		t.Fatalf("got %+v", bs)
	}
}
func TestLinePrefixIgnoresQuotedMarkers(t *testing.T) {
	for _, prefix := range []string{"--", ";;"} {
		bs := linePrefix(prefix, "value = \""+prefix+" fake\"\n"+prefix+" real")
		if len(bs) != 1 || bs[0].Start != 2 {
			t.Fatalf("got %+v", bs)
		}
	}
}
func TestCFamilyRawStringMarkersIgnored(t *testing.T) {
	for _, tc := range []struct{ path, src string }{
		{"a.cpp", "auto example = R\"demo(quoted \"\n// fake\n)demo\";\n// real"},
		{"a.rs", "let example = r##\"quoted \"\n// fake\n\"##;\n// real"},
		{"a.java", "var example = \"\"\"\nquoted \"\n// fake\n\"\"\";\n// real"},
	} {
		bs := cFamily(tc.path, tc.src)
		if len(bs) != 1 || bs[0].Text != " real" {
			t.Fatalf("%s: got %+v", tc.path, bs)
		}
	}
}
func TestMarkdownFenceLength(t *testing.T) {
	bs := markdown("a.md", "````html\n```\n<!-- fake -->\n````\n<!-- real -->")
	if len(bs) != 1 || bs[0].Text != " real " {
		t.Fatalf("got %+v", bs)
	}
}
func TestRustNestedComment(t *testing.T) {
	bs := rust("a.rs", "/* outer /* inner */\n// fake\n*/\n// real")
	if len(bs) != 1 || bs[0].Text != " outer /* inner */\n// fake\n\n real" {
		t.Fatalf("got %+v", bs)
	}
}
func TestPythonDocstringConfiguration(t *testing.T) {
	defer SetPythonDocstrings(true)
	src := "\"\"\"\nSENTRY.IfChange(\"doc\")\n\"\"\"\n# real"
	for _, enabled := range []bool{false, true} {
		SetPythonDocstrings(enabled)
		bs := python("a.py", src)
		want := 1
		if enabled {
			want = 2
		}
		if len(bs) != want {
			t.Fatalf("enabled=%v got %+v", enabled, bs)
		}
	}
}
