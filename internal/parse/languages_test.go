package parse

import (
	ifttt "github.com/derhnyel/ifttt/internal"
	"strings"
	"testing"
)

func TestLanguageGrammarCoverage(t *testing.T) {
	for _, tc := range []struct{ files, comment string }{
		{"a.c a.h a.cpp a.cc a.cxx a.hpp a.hxx a.hh a.cs a.dart a.go a.groovy a.gradle a.java a.js a.jsx a.mjs a.cjs a.kt a.kts a.m a.mm a.php a.phtml a.proto a.rs a.scala a.sc a.scss a.swift a.ts a.tsx a.mts a.cts", "// SENTRY.IfChange(\"real\")"},
		{"a.cmake CMakeLists.txt Dockerfile Dockerfile.dev a.dockerfile a.ex a.exs a.gn a.gni a.graphql a.gql a.mk a.mak Makefile makefile GNUmakefile a.nix a.pl a.pm a.t a.ps1 a.psm1 a.psd1 a.py a.pyi a.pyw a.r a.R a.rb a.rake a.gemspec Rakefile Gemfile a.sh a.bash a.zsh a.ksh a.bzl BUILD BUILD.bazel WORKSPACE a.tf a.hcl a.tfvars a.toml a.yaml a.yml", "# SENTRY.IfChange(\"real\")"},
		{"a.clj a.cljs a.cljc a.edn a.lisp a.cl a.el a.scm a.rkt", "; SENTRY.IfChange(\"real\")"},
		{"a.hs a.lua a.sql", "-- SENTRY.IfChange(\"real\")"},
		{"a.tex a.sty a.cls a.m a.mm", "% SENTRY.IfChange(\"real\")"},
		{"a.css a.sql a.nix a.tf", "/* SENTRY.IfChange(\"real\") */"},
		{"a.html a.htm a.xhtml a.md a.mdx a.markdown a.xml a.xsd a.xsl a.xslt a.svg a.vue a.svelte", "<!-- SENTRY.IfChange(\"real\") -->"},
		{"a.tpl a.gotmpl a.gohtml a.tmpl", "{{/* SENTRY.IfChange(\"real\") */}}"},
		{"a.hs", "{- SENTRY.IfChange(\"real\") -}"},
		{"a.ps1", "<# SENTRY.IfChange(\"real\") #>"},
	} {
		for _, file := range strings.Fields(tc.files) {
			t.Run(file+tc.comment[:1], func(t *testing.T) {
				ds, e := (Provider{ReadFile: func(string) ([]byte, error) { return []byte(tc.comment), nil }}).Parse(file)
				if e != nil || len(ds) != 1 || ds[0].Kind != ifttt.IfChange || ds[0].Label != "real" {
					t.Fatalf("got %+v, %v", ds, e)
				}
			})
		}
	}
}
func TestLanguageSkipRegions(t *testing.T) {
	for _, tc := range []struct{ file, src string }{
		{"script.sh", "cat <<'EOF'\n# SENTRY.IfChange(\"fake\")\nEOF\n# SENTRY.IfChange(\"real\")"},
		{"script.bash", "cat <<-EOF\n\t# SENTRY.IfChange(\"fake\")\n\tEOF\n# SENTRY.IfChange(\"real\")"},
		{"a.rb", "x = <<~DOC\n# SENTRY.IfChange(\"fake\")\n DOC\n# SENTRY.IfChange(\"real\")"},
		{"a.php", "$x = <<<'DOC'\n# SENTRY.IfChange(\"fake\")\nDOC;\n# SENTRY.IfChange(\"real\")"},
		{"a.tf", "x = <<-DOC\n# SENTRY.IfChange(\"fake\")\n DOC\n# SENTRY.IfChange(\"real\")"},
		{"a.lua", "x = [==[\n-- SENTRY.IfChange(\"fake\")\n]==]\n-- SENTRY.IfChange(\"real\")"},
		{"CMakeLists.txt", "set(x [=[\n# SENTRY.IfChange(\"fake\")\n]=])\n# SENTRY.IfChange(\"real\")"},
		{"a.sql", "SELECT $tag$ quoted ' \n-- SENTRY.IfChange(\"fake\")\n$tag$;\n-- SENTRY.IfChange(\"real\")"},
		{"a.nix", "x = ''quoted \"\n# SENTRY.IfChange(\"fake\")\n'';\n# SENTRY.IfChange(\"real\")"},
		{"a.ps1", "$x = @\"\n# SENTRY.IfChange(\"fake\")\n\"@\n# SENTRY.IfChange(\"real\")"},
		{"a.rb", "x = %q{ nested { inner }\n# SENTRY.IfChange(\"fake\")\n};\n# SENTRY.IfChange(\"real\")"},
		{"a.pl", "$x = qq! quoted '\n# SENTRY.IfChange(\"fake\")\n!;\n# SENTRY.IfChange(\"real\")"},
		{"a.r", "x = r\"--( quoted \"\n# SENTRY.IfChange(\"fake\")\n)--\"\n# SENTRY.IfChange(\"real\")"},
		{"a.groovy", "def x = $/ quoted \"\n// SENTRY.IfChange(\"fake\")\n/$\n// SENTRY.IfChange(\"real\")"},
		{"a.swift", "let x = #\"quoted \"\n// SENTRY.IfChange(\"fake\")\n\"#\n// SENTRY.IfChange(\"real\")"},
		{"a.cs", "var x = @\"quoted \"\" still quoted\n// SENTRY.IfChange(\"fake\")\n\";\n// SENTRY.IfChange(\"real\")"},
		{"a.cs", "var x = \"\"\"\"quoted \"\"\"\n// SENTRY.IfChange(\"fake\")\n\"\"\"\";\n// SENTRY.IfChange(\"real\")"},
		{"a.html", "<script>const x = '<!-- SENTRY.IfChange(\"fake\") -->';</script>\n<!-- SENTRY.IfChange(\"real\") -->"},
		{"a.xml", "<![CDATA[<!-- SENTRY.IfChange(\"fake\") -->]]>\n<!-- SENTRY.IfChange(\"real\") -->"},
		{"a.css", "x = \"/* SENTRY.IfChange('fake') */\";\n/* SENTRY.IfChange(\"real\") */"},
		{"BUILD", "x = \"\"\"\n# SENTRY.IfChange(\"fake\")\n\"\"\"\n# SENTRY.IfChange(\"real\")"},
	} {
		t.Run(tc.file+tc.src[:5], func(t *testing.T) {
			ds, e := (Provider{ReadFile: func(string) ([]byte, error) { return []byte(tc.src), nil }}).Parse(tc.file)
			if e != nil || len(ds) != 1 || ds[0].Label != "real" {
				t.Fatalf("got %+v, %v", ds, e)
			}
		})
	}
}

func TestLanguageLiteralAndCommentEdges(t *testing.T) {
	for _, tc := range []struct {
		file, src string
		line      int
	}{
		{"a.lua", "--[=[\nSENTRY.IfChange(\"real\")\n]=]", 2},
		{"CMakeLists.txt", "#[==[\nSENTRY.IfChange(\"real\")\n]==]", 2},
		{"a.hs", "{- outer {- nested -}\nSENTRY.IfChange(\"real\")\n-}", 2},
		{"a.sql", "SELECT $$-- fake$$; -- SENTRY.IfChange(\"real\")", 1},
		{"a.sql", "SELECT 'it''s -- fake'; /* SENTRY.IfChange(\"real\") */", 1},
		{"a.rkt", ";; SENTRY.IfChange(\"real\")", 1},
		{"unknown.custom", "// irrelevant\n# SENTRY.IfChange(\"real\")", 2},
		{"Dockerfile", "RUN echo 'fake # SENTRY.IfChange(\"fake\")'\n# SENTRY.IfChange(\"real\")", 2},
		{"a.dart", "var x = '''\n// SENTRY.IfChange(\"fake\")\n'''; // SENTRY.IfChange(\"real\")", 3},
		{"a.scala", "val x = \"\"\" quoted \"\n// SENTRY.IfChange(\"fake\")\n\"\"\"\n// SENTRY.IfChange(\"real\")", 4},
		{"a.ex", "x = \"\"\"quoted \"\n# SENTRY.IfChange(\"fake\")\n\"\"\"\n# SENTRY.IfChange(\"real\")", 4},
		{"a.graphql", "x = \"\"\"quoted \"\n# SENTRY.IfChange(\"fake\")\n\"\"\"\n# SENTRY.IfChange(\"real\")", 4},
		{"a.vue", "const x = `<!-- SENTRY.IfChange('fake') -->`;\n<!-- SENTRY.IfChange(\"real\") -->", 2},
	} {
		t.Run(tc.file+tc.src[:2], func(t *testing.T) {
			ds, e := (Provider{ReadFile: func(string) ([]byte, error) { return []byte(tc.src), nil }}).Parse(tc.file)
			if e != nil || len(ds) != 1 || ds[0].Label != "real" || ds[0].Line != tc.line {
				t.Fatalf("got %+v, %v", ds, e)
			}
		})
	}
}
