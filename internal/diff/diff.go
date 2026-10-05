package diff

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"

	godiff "github.com/sourcegraph/go-diff/diff"

	core "github.com/derhnyel/ifttt/internal"
)

var ErrCombinedDiff = errors.New("combined diffs were present and skipped")

// Parser wraps sourcegraph/go-diff to iterate file diffs.
type Parser struct {
	reader   *godiff.MultiFileDiffReader
	detector *combinedDetector
	err      error
}

func New(r io.Reader) *Parser {
	buffered := bufio.NewReader(r)
	var first string
	for {
		line, err := buffered.ReadString('\n')
		if strings.TrimSpace(line) != "" {
			first = line
			break
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return &Parser{err: err}
			}
			break
		}
	}
	if first != "" && !strings.HasPrefix(first, "diff --git ") && !strings.HasPrefix(first, "diff --cc ") && !strings.HasPrefix(first, "diff --combined ") && !strings.HasPrefix(first, "--- ") && !strings.HasPrefix(first, "Index: ") {
		return &Parser{err: errors.New("input is not a unified diff")}
	}
	d := &combinedDetector{r: io.MultiReader(strings.NewReader(first), buffered)}
	return &Parser{
		reader:   godiff.NewMultiFileDiffReader(d),
		detector: d,
	}
}

func (p *Parser) NextFile() (*core.FilePatch, error) {
	if p.err != nil {
		err := p.err
		p.err = nil
		return nil, err
	}
	if p.reader == nil {
		return nil, io.EOF
	}
	for {
		fd, err := p.reader.ReadFile()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if p.detector != nil && p.detector.found {
					return nil, ErrCombinedDiff
				}
				return nil, io.EOF
			}
			return nil, err
		}
		if p.detector != nil && p.detector.found {
			p.err = ErrCombinedDiff
			continue
		}
		if fd == nil {
			continue
		}
		newPath := stripPrefix(fd.NewName)

		patch := &core.FilePatch{
			OldPath: stripPrefix(fd.OrigName),
			NewPath: newPath,
		}
		for _, h := range fd.Hunks {
			if h == nil {
				continue
			}
			chunk := core.Hunk{
				OldStart: int(h.OrigStartLine),
				OldLines: int(h.OrigLines),
				NewStart: int(h.NewStartLine),
				NewLines: int(h.NewLines),
			}
			lines := bytes.Split(h.Body, []byte{'\n'})
			for _, ln := range lines {
				if len(ln) == 0 {
					continue
				}
				if ln[0] != '+' && ln[0] != '-' && ln[0] != ' ' && ln[0] != '\\' {
					continue
				}
				if ln[0] == '\\' {
					continue
				}
				chunk.Lines = append(chunk.Lines, core.HunkLine{
					Kind: ln[0],
					Text: string(ln[1:]),
				})
			}
			patch.Chunks = append(patch.Chunks, chunk)
		}
		if p.err != nil {
			err := p.err
			p.err = nil
			return nil, err
		}
		return patch, nil
	}
}

func stripPrefix(path string) string {
	return core.NormalizePath(path)
}

type combinedDetector struct {
	r     io.Reader
	tail  []byte
	found bool
}

func (d *combinedDetector) Read(p []byte) (n int, err error) {
	n, err = d.r.Read(p)
	if n > 0 {
		segment := append(d.tail, p[:n]...)
		if bytes.Contains(segment, []byte("diff --cc ")) || bytes.Contains(segment, []byte("diff --combined ")) {
			d.found = true
		}
		const maxPattern = len("diff --combined ")
		keep := maxPattern - 1
		if keep < 0 {
			keep = 0
		}
		if len(segment) > keep {
			d.tail = append(d.tail[:0], segment[len(segment)-keep:]...)
		} else {
			d.tail = append(d.tail[:0], segment...)
		}
	}
	if err == io.EOF {
		d.tail = nil
	}
	return n, err
}
