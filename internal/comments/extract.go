package comments

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

type Block struct {
	Start int
	Text  string
}

type Extractor func(path string, src string) []Block

var registry = map[string]Extractor{}
var literalExtractors = map[string]bool{}
var pythonExtractors = map[string]bool{}
var registryMu sync.RWMutex

func RegisterLanguage(ext string, e Extractor) {
	registerLanguage(ext, e, false)
}

// Built-in extractors retain literal source text. Custom extractors may decode
// or synthesize comments, so raw-source absence is not sufficient for them.
func registerLanguage(ext string, e Extractor, literal bool) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[strings.ToLower(ext)] = e
	literalExtractors[strings.ToLower(ext)] = literal
	delete(commentFormats, strings.ToLower(ext))
	delete(pythonExtractors, strings.ToLower(ext))
}

// RegisterLineComment registers a simple single-line comment extractor that strips the
// provided prefix (e.g. "//", "#", "--") from each matching line.
func RegisterLineComment(ext string, prefix string) {
	if strings.TrimSpace(prefix) == "" {
		return
	}
	registerLanguage(ext, func(_ string, src string) []Block {
		return linePrefix(prefix, src)
	}, true)
	registerCommentFormat(ext, commentDelimiter{open: prefix})
}

type lineScanner struct {
	src string
	idx int
}

var scannerPool = sync.Pool{
	New: func() any {
		return &lineScanner{}
	},
}

func scanLines(src string, fn func(ln int, line string) bool) {
	ls := scannerPool.Get().(*lineScanner)
	ls.src = src
	ls.idx = 0
	defer func() {
		ls.src = ""
		ls.idx = 0
		scannerPool.Put(ls)
	}()
	ln := 0
	for {
		line, ok := ls.next()
		if !ok {
			return
		}
		ln++
		if !fn(ln, line) {
			return
		}
	}
}

func (ls *lineScanner) next() (string, bool) {
	if ls.idx >= len(ls.src) {
		return "", false
	}
	start := ls.idx
	for ls.idx < len(ls.src) && ls.src[ls.idx] != '\n' {
		ls.idx++
	}
	line := ls.src[start:ls.idx]
	if ls.idx < len(ls.src) && ls.src[ls.idx] == '\n' {
		ls.idx++
	}
	return line, true
}

func init() { registerBuiltinLanguages() }

func Extract(path string) ([]Block, error) {
	return ExtractWithLoader(path, os.ReadFile)
}

func ExtractWithLoader(path string, loader func(string) ([]byte, error)) ([]Block, error) {
	return extractWithLoader(path, "", loader)
}

// ExtractWithNeedle skips built-in extraction when needle is absent from the
// entire source. When present, all comment blocks are returned unchanged.
// Custom language extractors always run because they can transform their input.
func ExtractWithNeedle(path, needle string, loader func(string) ([]byte, error)) ([]Block, error) {
	return extractWithLoader(path, needle, loader)
}

func extractWithLoader(path, needle string, loader func(string) ([]byte, error)) ([]Block, error) {
	return ExtractWithSettings(path, needle, loader, nil)
}

// ExtractWithSettings scopes Python extraction to an immutable snapshot.
// Explicit custom comment extractors retain precedence.
func ExtractWithSettings(path, needle string, loader func(string) ([]byte, error), docstrings *bool) ([]Block, error) {
	if loader == nil {
		loader = os.ReadFile
	}
	b, err := loader(path)
	if err != nil {
		return nil, err
	}
	ext := strings.ToLower(filepath.Ext(path))
	registryMu.RLock()
	e, ok := registry[ext]
	literal := literalExtractors[ext]
	pythonBuiltin := pythonExtractors[ext]
	registryMu.RUnlock()
	if needle != "" && (e == nil || literal) && !bytes.Contains(b, []byte(needle)) {
		return nil, nil
	}
	if docstrings != nil && (pythonBuiltin || (!ok && (filepath.Base(path) == "BUILD" || filepath.Base(path) == "BUILD.bazel" || filepath.Base(path) == "WORKSPACE"))) {
		return pythonWithDocstrings(string(b), *docstrings), nil
	}
	if ok && e != nil {
		return e(path, string(b)), nil
	}
	if extractor := filenameExtractor(path); extractor != nil {
		return extractor(path, string(b)), nil
	}
	return lexComments(path, string(b), grammar{lines: []string{"//", "#"}, blocks: []commentDelimiter{slashBlock}, triple: true, backtick: true, groupLines: true}), nil
}

func cFamily(path, src string) []Block {
	registryMu.RLock()
	extractor := registry[strings.ToLower(filepath.Ext(path))]
	registryMu.RUnlock()
	if extractor != nil {
		return extractor(path, src)
	}
	return lexComments(path, src, grammar{lines: []string{"//"}, blocks: []commentDelimiter{slashBlock}, backtick: true, groupLines: true})
}
func hashOnly(path, src string) []Block { return lexComments(path, src, grammar{lines: []string{"#"}}) }

func python(_ string, src string) []Block {
	return pythonWithDocstrings(src, !pythonDocstringsDisabled.Load())
}

func pythonWithDocstrings(src string, docstrings bool) []Block {
	var out []Block
	line := 1
	for i := 0; i < len(src); {
		if src[i] == '\n' {
			line++
			i++
			continue
		}
		if src[i] == '#' {
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				end = len(src)
			} else {
				end += i
			}
			out = append(out, Block{Start: line, Text: src[i+1 : end]})
			i = end
			continue
		}
		if src[i] == '"' || src[i] == '\'' {
			delim := string(src[i])
			triple := strings.HasPrefix(src[i:], strings.Repeat(delim, 3))
			if triple {
				delim = strings.Repeat(delim, 3)
			}
			start := i + len(delim)
			j := start
			for j < len(src) {
				if src[j] == '\\' && j+1 < len(src) {
					j += 2
					continue
				}
				if strings.HasPrefix(src[j:], delim) {
					break
				}
				j++
			}
			if docstrings && triple && strings.TrimSpace(src[strings.LastIndex(src[:i], "\n")+1:i]) == "" {
				out = append(out, Block{Start: line, Text: src[start:j]})
			}
			line += strings.Count(src[start:j], "\n")
			i = j
			if i < len(src) {
				i += len(delim)
			}
			continue
		}
		i++
	}
	return out
}

func rust(path, src string) []Block {
	return lexComments(path, src, grammar{lines: []string{"//"}, blocks: []commentDelimiter{{"/*", "*/", true}}, kind: "rust", groupLines: true})
}
func html(path, src string) []Block {
	return lexComments(path, src, grammar{blocks: []commentDelimiter{htmlBlock}, kind: "html"})
}
func linePrefix(prefix, src string) []Block {
	return lexComments("", src, grammar{lines: []string{prefix}})
}

// Preserve physical lines while excluding examples inside Markdown fences.
func markdown(path string, src string) []Block {
	var lines []string
	fence := ""
	scanLines(src, func(_ int, line string) bool {
		t := strings.TrimSpace(line)
		if fence != "" {
			if ClosesFence(t, fence) {
				fence = ""
			}
			lines = append(lines, "")
			return true
		}
		if opening := FenceDelimiter(t); opening != "" {
			fence = opening
			lines = append(lines, "")
			return true
		}
		lines = append(lines, line)
		return true
	})
	return html(path, strings.Join(lines, "\n"))
}

// FenceDelimiter returns a Markdown fence of at least three matching markers.
func FenceDelimiter(text string) string {
	if len(text) < 3 || (text[0] != '`' && text[0] != '~') {
		return ""
	}
	n := 0
	for n < len(text) && text[n] == text[0] {
		n++
	}
	if n < 3 {
		return ""
	}
	return text[:n]
}
func ClosesFence(text, fence string) bool {
	run := FenceDelimiter(text)
	return len(run) >= len(fence) && run != "" && run[0] == fence[0] && strings.TrimSpace(text[len(run):]) == ""
}
func rawStringEnd(path, src string, i int) (int, bool) {
	tail := src[i:]
	close := ""
	body := 0
	if strings.HasSuffix(path, ".rs") && (strings.HasPrefix(tail, "r\"") || strings.HasPrefix(tail, "r#")) {
		j := 1
		for j < len(tail) && tail[j] == '#' {
			j++
		}
		if j >= len(tail) || tail[j] != '"' {
			return 0, false
		}
		body = j + 1
		close = "\"" + tail[1:j]
	} else if (strings.HasSuffix(path, ".cpp") || strings.HasSuffix(path, ".cc") || strings.HasSuffix(path, ".hpp") || strings.HasSuffix(path, ".h") || strings.HasSuffix(path, ".cxx") || strings.HasSuffix(path, ".hxx") || strings.HasSuffix(path, ".hh") || strings.HasSuffix(path, ".c")) && strings.HasPrefix(tail, "R\"") {
		j := strings.IndexByte(tail[2:], '(')
		if j < 0 || j > 16 {
			return 0, false
		}
		j += 2
		delim := tail[2:j]
		if strings.ContainsAny(delim, " ()\\\t\n\r") {
			return 0, false
		}
		body = j + 1
		close = ")" + delim + "\""
	} else if (strings.HasSuffix(path, ".java") || strings.HasSuffix(path, ".kt") || strings.HasSuffix(path, ".kts") || strings.HasSuffix(path, ".cs") || strings.HasSuffix(path, ".swift")) && strings.HasPrefix(tail, "\"\"\"") {
		body = 3
		close = "\"\"\""
	} else {
		return 0, false
	}
	end := strings.Index(tail[body:], close)
	if end < 0 {
		return len(src), true
	}
	return i + body + end + len(close), true
}

var pythonDocstringsDisabled atomic.Bool

// SetPythonDocstrings controls extraction of standalone Python/Bzl docstrings.
// Quoted code strings are always ignored, and hash comments remain enabled.
func SetPythonDocstrings(enabled bool) { pythonDocstringsDisabled.Store(!enabled) }
