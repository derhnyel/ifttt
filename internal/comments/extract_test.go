package comments

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestExtractGroupsLineCommentsCFamily(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "sample.go", `package main
// first
// second
func main() {}
/* block
body */
`)
	blocks, err := Extract(path)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d (%v)", len(blocks), blocks)
	}
	line := blocks[0]
	if line.Start != 2 {
		t.Fatalf("expected line block start 2, got %d", line.Start)
	}
	if line.Text != " first\n second" {
		t.Fatalf("unexpected line text: %q", line.Text)
	}
	block := blocks[1]
	if block.Start != 5 {
		t.Fatalf("expected block comment start 5, got %d", block.Start)
	}
	if block.Text != " block\nbody " {
		t.Fatalf("unexpected block text: %q", block.Text)
	}
}

func TestExtractHashLanguages(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "script.sh", `echo hi
# first
  # second`)
	blocks, err := Extract(path)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 hash blocks, got %d (%v)", len(blocks), blocks)
	}
	if blocks[0].Start != 2 || !strings.Contains(blocks[0].Text, " first") {
		t.Fatalf("unexpected first hash block: %+v", blocks[0])
	}
	if blocks[1].Start != 3 || !strings.Contains(blocks[1].Text, " second") {
		t.Fatalf("unexpected second hash block: %+v", blocks[1])
	}
}

func TestExtractPythonTripleQuotes(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "module.py", `# top
def fn():
    """doc line
    next"""
    pass
    '''
    alt
    '''
`)
	blocks, err := Extract(path)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d (%v)", len(blocks), blocks)
	}
	if blocks[1].Start != 3 || !strings.Contains(blocks[1].Text, "doc line") {
		t.Fatalf("unexpected triple quote block: %+v", blocks[1])
	}
	if blocks[2].Start != 6 || !strings.Contains(blocks[2].Text, "alt") {
		t.Fatalf("unexpected alt triple block: %+v", blocks[2])
	}
}

func TestExtractHTMLComments(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "page.html", `<body>
<!-- head -->
<div><!-- inline --></div>
<!-- block
more -->
</body>`)
	blocks, err := Extract(path)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(blocks) != 3 {
		t.Fatalf("expected 3 HTML blocks, got %d (%v)", len(blocks), blocks)
	}
	if blocks[0].Text != " head " {
		t.Fatalf("unexpected first HTML block: %q", blocks[0].Text)
	}
	if blocks[1].Start != 3 || blocks[1].Text != " inline " {
		t.Fatalf("unexpected inline HTML block: %+v", blocks[1])
	}
	if !strings.Contains(blocks[2].Text, "block") || !strings.Contains(blocks[2].Text, "more") {
		t.Fatalf("unexpected multi-line HTML block: %q", blocks[2].Text)
	}
}

func TestExtractRustDocComments(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "lib.rs", `/// doc1
fn main() {
    /// doc2
}`)
	blocks, err := Extract(path)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 rust blocks, got %d (%v)", len(blocks), blocks)
	}
	if blocks[0].Start != 1 || !strings.Contains(blocks[0].Text, " doc1") {
		t.Fatalf("unexpected rust block: %+v", blocks[0])
	}
	if blocks[1].Start != 3 || !strings.Contains(blocks[1].Text, " doc2") {
		t.Fatalf("unexpected rust block: %+v", blocks[1])
	}
}

func TestExtractLuaComments(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "file.lua", `-- first
-- second`)
	blocks, err := Extract(path)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected separate lua blocks, got %d (%v)", len(blocks), blocks)
	}
}

func TestExtractFallbackCFamilyThenHash(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "plain.txt", `# hash only`)
	blocks, err := Extract(path)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected fallback hash block, got %d (%v)", len(blocks), blocks)
	}
}

func TestExtractSwiftDefaultsToCFamily(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "app.swift", `// swift line
func foo() {
    /* block */
}`)
	blocks, err := Extract(path)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 swift comment blocks, got %d (%v)", len(blocks), blocks)
	}
	if blocks[0].Start != 1 || !strings.Contains(blocks[0].Text, " swift line") {
		t.Fatalf("unexpected swift line block: %+v", blocks[0])
	}
	if blocks[1].Start != 3 || !strings.Contains(blocks[1].Text, " block ") {
		t.Fatalf("unexpected swift block: %+v", blocks[1])
	}
}

func TestRegisterLineCommentCustomPrefix(t *testing.T) {
	dir := t.TempDir()
	RegisterLineComment(".conf", ";;")
	path := writeTemp(t, dir, "app.conf", `key=value
;; directive
 ;;ignored
normal`)
	blocks, err := Extract(path)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected two custom blocks, got %d (%v)", len(blocks), blocks)
	}
	if blocks[0].Start != 2 || !strings.Contains(blocks[0].Text, " directive") {
		t.Fatalf("unexpected custom block: %+v", blocks[0])
	}
	if blocks[1].Start != 3 || !strings.Contains(blocks[1].Text, "ignored") {
		t.Fatalf("unexpected custom block: %+v", blocks[1])
	}
}
