package comments

import (
	"strings"
	"testing"
)

func TestLargeHTMLAndCaseInsensitiveRawText(t *testing.T) {
	text := strings.Repeat("<div>Content</div>\n", 10000) + "<ScRiPt>\"<!-- LINT.IfChange(fake) -->\"</sCrIpT>\n<!-- LINT.IfChange(real) -->"
	blocks, err := ExtractWithLoader("page.html", func(string) ([]byte, error) { return []byte(text), nil })
	if err != nil || len(blocks) != 1 || !strings.Contains(blocks[0].Text, "real") {
		t.Fatalf("unexpected comments: %v %v", blocks, err)
	}
}

func BenchmarkHTMLExtraction(b *testing.B) {
	text := strings.Repeat("<div>Content</div>\n", 10000)
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := ExtractWithLoader("page.html", func(string) ([]byte, error) { return []byte(text), nil })
		if err != nil {
			b.Fatal(err)
		}
	}
}
