package comments

import (
	"path/filepath"
	"strings"
)

// A grammar describes comment delimiters and the literal forms that can contain
// comment-looking text. Extraction never searches inside a recognized literal.
type grammar struct {
	lines                        []string
	blocks                       []commentDelimiter
	kind                         string
	triple, backtick, groupLines bool
}
type commentDelimiter struct {
	open, close string
	nested      bool
}

var slashBlock = commentDelimiter{"/*", "*/", false}
var htmlBlock = commentDelimiter{"<!--", "-->", false}

func registerBuiltinLanguages() {
	register := func(exts string, g grammar) {
		for _, ext := range strings.Fields(exts) {
			spec := g
			registerLanguage(ext, func(path, src string) []Block { return lexComments(path, src, spec) }, true)
			delimiter := commentDelimiter{}
			if len(spec.blocks) > 0 && (len(spec.lines) == 0 || spec.blocks[0].open == "<!--") {
				delimiter = spec.blocks[0]
			} else if len(spec.lines) > 0 {
				delimiter.open = spec.lines[0]
			}
			registerCommentFormat(ext, delimiter)
		}
	}
	slash := grammar{lines: []string{"//"}, blocks: []commentDelimiter{slashBlock}, groupLines: true}
	register(".c .h .cpp .cc .cxx .hpp .hxx .hh", withKind(slash, "cpp"))
	register(".cs", withKind(slash, "csharp"))
	register(".dart", withTriple(slash))
	register(".go .js .jsx .mjs .cjs .ts .tsx .mts .cts", withBacktick(slash))
	register(".groovy .gradle", withKind(withTriple(slash), "groovy"))
	register(".java .scala .sc", withTriple(slash))
	nested := slash
	nested.blocks = []commentDelimiter{{"/*", "*/", true}}
	register(".kt .kts", withTriple(nested))
	register(".rs", withKind(nested, "rust"))
	register(".swift", withKind(withTriple(nested), "swift"))
	register(".m .mm", grammar{lines: []string{"//", "%"}, blocks: []commentDelimiter{slashBlock}, groupLines: true})
	register(".php .phtml", grammar{lines: []string{"//", "#"}, blocks: []commentDelimiter{slashBlock}, kind: "php", groupLines: true})
	register(".proto .scss", slash)
	hash := grammar{lines: []string{"#"}}
	register(".cmake", withKind(hash, "bracket"))
	register(".dockerfile .gn .gni .yaml .yml .mk .mak", hash)
	register(".ex .exs .graphql .gql .toml", withTriple(hash))
	register(".nix", grammar{lines: []string{"#"}, blocks: []commentDelimiter{slashBlock}, kind: "nix"})
	register(".pl .pm .t .rb .rake .gemspec", withKind(hash, "rubyperl"))
	register(".ps1 .psm1 .psd1", grammar{lines: []string{"#"}, blocks: []commentDelimiter{{"<#", "#>", false}}, kind: "powershell"})
	register(".r", withKind(hash, "r"))
	register(".sh .bash .zsh .ksh", withKind(hash, "heredoc"))
	register(".tf .hcl .tfvars", grammar{lines: []string{"#", "//"}, blocks: []commentDelimiter{slashBlock}, kind: "heredoc"})
	register(".clj .cljs .cljc .edn .lisp .cl .el .scm .rkt", grammar{lines: []string{";"}})
	register(".hs", grammar{lines: []string{"--"}, blocks: []commentDelimiter{{"{-", "-}", true}}})
	register(".lua", grammar{lines: []string{"--"}, kind: "bracket"})
	register(".sql", grammar{lines: []string{"--"}, blocks: []commentDelimiter{slashBlock}, kind: "sql"})
	register(".tex .sty .cls", grammar{lines: []string{"%"}})
	register(".css", grammar{blocks: []commentDelimiter{slashBlock}})
	register(".html .htm .xhtml .xml .xsd .xsl .xslt .svg", grammar{blocks: []commentDelimiter{htmlBlock}, kind: "html"})
	register(".vue .svelte", grammar{lines: []string{"//"}, blocks: []commentDelimiter{htmlBlock, slashBlock}, backtick: true, groupLines: true})
	register(".tpl .gotmpl .gohtml .tmpl", grammar{blocks: []commentDelimiter{{"{{/*", "*/}}", false}}})
	for _, ext := range []string{".py", ".pyi", ".pyw", ".bzl"} {
		registerLanguage(ext, python, true)
		pythonExtractors[ext] = true
		registerCommentFormat(ext, commentDelimiter{open: "#"})
	}
	for _, ext := range []string{".md", ".mdx", ".markdown"} {
		registerLanguage(ext, markdown, true)
		registerCommentFormat(ext, htmlBlock)
	}
}
func withKind(g grammar, kind string) grammar { g.kind = kind; return g }
func withTriple(g grammar) grammar            { g.triple = true; return g }
func withBacktick(g grammar) grammar          { g.backtick = true; return g }
func filenameExtractor(path string) Extractor {
	base := filepath.Base(path)
	switch {
	case base == "BUILD" || base == "BUILD.bazel" || base == "WORKSPACE":
		return python
	case base == "CMakeLists.txt":
		return func(path, src string) []Block {
			return lexComments(path, src, grammar{lines: []string{"#"}, kind: "bracket"})
		}
	case base == "Dockerfile" || strings.HasPrefix(base, "Dockerfile.") || base == "Makefile" || base == "makefile" || base == "GNUmakefile":
		return hashOnly
	case base == "Rakefile" || base == "Gemfile":
		return func(path, src string) []Block {
			return lexComments(path, src, grammar{lines: []string{"#"}, kind: "rubyperl"})
		}
	}
	return nil
}

func lexComments(path, src string, g grammar) []Block {
	var out []Block
	line := 1
	for i := 0; i < len(src); {
		if src[i] == '\n' {
			line++
			i++
			continue
		}
		// Lua and CMake bracket comments use the same variable delimiter as their
		// long strings, with the line-comment marker immediately before it.
		handled := false
		if g.kind == "bracket" {
			for _, prefix := range g.lines {
				if strings.HasPrefix(src[i:], prefix) {
					if open, close, ok := longBracket(src[i+len(prefix):]); ok {
						start := i + len(prefix) + open
						end := literalEnd(src, start, close)
						out = append(out, Block{Start: line, Text: src[start:end]})
						line += strings.Count(src[i:end], "\n")
						i = end
						if i < len(src) {
							i += len(close)
						}
						handled = true
						break
					}
				}
			}
		}
		if handled {
			continue
		}
		for _, delim := range g.blocks {
			if !strings.HasPrefix(src[i:], delim.open) {
				continue
			}
			start := i + len(delim.open)
			end := commentEnd(src, start, delim)
			out = append(out, Block{Start: line, Text: src[start:end]})
			line += strings.Count(src[i:end], "\n")
			i = end
			if i < len(src) {
				i += len(delim.close)
			}
			handled = true
			break
		}
		if handled {
			continue
		}
		for _, prefix := range g.lines {
			if !strings.HasPrefix(src[i:], prefix) {
				continue
			}
			start := i + len(prefix)
			if prefix == ";" {
				for start < len(src) && src[start] == ';' {
					start++
				}
			}
			if g.kind == "rust" && start < len(src) && (src[start] == '/' || src[start] == '!') {
				start++
			}
			end := strings.IndexByte(src[start:], '\n')
			if end < 0 {
				end = len(src)
			} else {
				end += start
			}
			text := src[start:end]
			if g.groupLines && len(out) > 0 && out[len(out)-1].Start+strings.Count(out[len(out)-1].Text, "\n")+1 == line && strings.TrimSpace(src[strings.LastIndex(src[:i], "\n")+1:i]) == "" {
				out[len(out)-1].Text += "\n" + text
			} else {
				out = append(out, Block{Start: line, Text: text})
			}
			i = end
			handled = true
			break
		}
		if handled {
			continue
		}
		if end, ok := skipLiteral(path, src, i, g); ok {
			line += strings.Count(src[i:end], "\n")
			i = end
			continue
		}
		i++
	}
	return out
}
func commentEnd(src string, start int, d commentDelimiter) int {
	depth := 1
	for i := start; i < len(src); i++ {
		if strings.HasPrefix(src[i:], d.close) {
			depth--
			if depth == 0 {
				return i
			}
			i += len(d.close) - 1
			continue
		}
		if d.nested && strings.HasPrefix(src[i:], d.open) {
			depth++
			i += len(d.open) - 1
		}
	}
	return len(src)
}
func literalEnd(src string, start int, close string) int {
	end := strings.Index(src[start:], close)
	if end < 0 {
		return len(src)
	}
	return start + end
}
func fixedLiteralEnd(src string, start int, close string) int {
	end := literalEnd(src, start, close)
	if end < len(src) {
		end += len(close)
	}
	return end
}
func longBracket(tail string) (int, string, bool) {
	if len(tail) < 2 || tail[0] != '[' {
		return 0, "", false
	}
	j := 1
	for j < len(tail) && tail[j] == '=' {
		j++
	}
	if j >= len(tail) || tail[j] != '[' {
		return 0, "", false
	}
	return j + 1, "]" + tail[1:j] + "]", true
}

func skipLiteral(path, src string, i int, g grammar) (int, bool) {
	tail := src[i:]
	switch g.kind {
	case "cpp", "rust":
		if end, ok := rawStringEnd(path, src, i); ok {
			return end, true
		}
	case "swift":
		if tail[0] == '#' {
			j := 0
			for j < len(tail) && tail[j] == '#' {
				j++
			}
			hashes := tail[:j]
			quotes := 1
			if strings.HasPrefix(tail[j:], "\"\"\"") {
				quotes = 3
			}
			if j < len(tail) && tail[j] == '"' {
				return fixedLiteralEnd(src, i+j+quotes, strings.Repeat("\"", quotes)+hashes), true
			}
		}
	case "csharp":
		if strings.HasPrefix(tail, "@\"") {
			return verbatimEnd(src, i+2), true
		}
		if strings.HasPrefix(tail, "\"\"\"") {
			n := 0
			for n < len(tail) && tail[n] == '"' {
				n++
			}
			return fixedLiteralEnd(src, i+n, strings.Repeat("\"", n)), true
		}
	case "sql":
		if tail[0] == '$' {
			j := 1
			if j < len(tail) && tail[j] != '$' && !identifierStart(tail[j]) {
				break
			}
			for j < len(tail) && identifierByte(tail[j]) {
				j++
			}
			if j < len(tail) && tail[j] == '$' {
				delimiter := tail[:j+1]
				return fixedLiteralEnd(src, i+j+1, delimiter), true
			}
		}
	case "bracket":
		if open, close, ok := longBracket(tail); ok {
			return fixedLiteralEnd(src, i+open, close), true
		}
	case "groovy":
		if strings.HasPrefix(tail, "$/") {
			return fixedLiteralEnd(src, i+2, "/$"), true
		}
	case "nix":
		if strings.HasPrefix(tail, "''") && !strings.HasPrefix(tail, "'''") {
			return nixStringEnd(src, i+2), true
		}
	case "rubyperl":
		if end, ok := percentLiteralEnd(src, i); ok {
			return end, true
		}
		fallthrough
	case "heredoc", "php":
		if end, ok := heredocEnd(src, i, g.kind == "php"); ok {
			return end, true
		}
	case "powershell":
		if len(tail) >= 2 && tail[0] == '@' && (tail[1] == '"' || tail[1] == '\'') {
			lineEnd := strings.IndexByte(tail, '\n')
			if lineEnd >= 0 && strings.TrimSpace(tail[2:lineEnd]) == "" {
				return lineTerminatorEnd(src, i+lineEnd+1, string(tail[1])+"@", false), true
			}
		}
	case "r":
		if len(tail) >= 3 && (tail[0] == 'r' || tail[0] == 'R') && tail[1] == '"' {
			j := 2
			for j < len(tail) && tail[j] == '-' {
				j++
			}
			if j < len(tail) {
				close := pairedClose(tail[j])
				if close != 0 {
					return fixedLiteralEnd(src, i+j+1, string(close)+tail[2:j]+"\""), true
				}
			}
		}
	case "html":
		if len(tail) >= 9 && strings.EqualFold(tail[:9], "<![cdata[") {
			return fixedLiteralEnd(src, i+9, "]]>"), true
		}
		for _, tag := range []string{"script", "style"} {
			open := "<" + tag
			if len(tail) > len(open) && strings.EqualFold(tail[:len(open)], open) && (tail[len(open)] == '>' || asciiSpace(tail[len(open)])) {
				close := "</" + tag + ">"
				end := indexFold(tail[len(open):], close)
				if end < 0 {
					return len(src), true
				}
				return i + len(open) + end + len(close), true
			}
		}
	}
	if g.triple && (strings.HasPrefix(tail, "\"\"\"") || strings.HasPrefix(tail, "'''")) {
		return fixedLiteralEnd(src, i+3, tail[:3]), true
	}
	ch := tail[0]
	if ch != '"' && ch != '\'' && !(ch == '`' && g.backtick) {
		return 0, false
	}
	if ch == '\'' && g.kind == "rust" {
		j := 1
		for j < len(tail) && identifierByte(tail[j]) {
			j++
		}
		if j > 1 && (j == len(tail) || tail[j] != '\'') {
			return 0, false
		}
	}
	for j := i + 1; j < len(src); j++ {
		if src[j] == '\\' && !(ch == '`' && strings.HasSuffix(path, ".go")) {
			if j+1 < len(src) {
				j++
			}
			continue
		}
		if src[j] == ch {
			if g.kind == "sql" && j+1 < len(src) && src[j+1] == ch {
				j++
				continue
			}
			return j + 1, true
		}
		if src[j] == '\n' && ch == '\'' && g.kind != "heredoc" && g.kind != "rubyperl" && g.kind != "php" && g.kind != "sql" {
			return j, true
		}
	}
	return len(src), true
}
func identifierStart(ch byte) bool {
	return ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}
func identifierByte(ch byte) bool { return identifierStart(ch) || (ch >= '0' && ch <= '9') }
func asciiSpace(ch byte) bool     { return ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' }
func pairedClose(open byte) byte {
	switch open {
	case '(':
		return ')'
	case '[':
		return ']'
	case '{':
		return '}'
	case '<':
		return '>'
	}
	return 0
}
func verbatimEnd(src string, start int) int {
	for i := start; i < len(src); i++ {
		if src[i] == '"' {
			if i+1 < len(src) && src[i+1] == '"' {
				i++
				continue
			}
			return i + 1
		}
	}
	return len(src)
}
func nixStringEnd(src string, start int) int {
	for i := start; i+1 < len(src); i++ {
		if src[i] == '\'' && src[i+1] == '\'' {
			if i+2 < len(src) && (src[i+2] == '\'' || src[i+2] == '$' || src[i+2] == '\\') {
				i += 2
				continue
			}
			return i + 2
		}
	}
	return len(src)
}
func percentLiteralEnd(src string, start int) (int, bool) {
	if start > 0 && identifierByte(src[start-1]) {
		return 0, false
	}
	tail := src[start:]
	j := 0
	if tail[0] == '%' {
		j = 1
		if j < len(tail) && strings.ContainsRune("qQwWiIrxs", rune(tail[j])) {
			j++
		}
	} else if strings.HasPrefix(tail, "qq") {
		j = 2
	} else if tail[0] == 'q' {
		j = 1
	} else {
		return 0, false
	}
	if j >= len(tail) || identifierByte(tail[j]) || asciiSpace(tail[j]) {
		return 0, false
	}
	open := tail[j]
	close := pairedClose(open)
	nested := close != 0
	if !nested {
		close = open
	}
	depth := 1
	for i := start + j + 1; i < len(src); i++ {
		if src[i] == '\\' && i+1 < len(src) {
			i++
			continue
		}
		if nested && src[i] == open {
			depth++
		}
		if src[i] == close {
			depth--
			if depth == 0 {
				return i + 1, true
			}
		}
	}
	return len(src), true
}
func heredocEnd(src string, start int, php bool) (int, bool) {
	tail := src[start:]
	opener := "<<"
	if php {
		opener = "<<<"
	}
	if !strings.HasPrefix(tail, opener) {
		return 0, false
	}
	j := len(opener)
	indent := false
	if !php && j < len(tail) && (tail[j] == '-' || tail[j] == '~') {
		indent = true
		j++
	}
	for j < len(tail) && (tail[j] == ' ' || tail[j] == '\t') {
		j++
	}
	var quote byte
	if j < len(tail) && (tail[j] == '\'' || tail[j] == '"') {
		quote = tail[j]
		j++
	}
	if j >= len(tail) || !identifierStart(tail[j]) {
		return 0, false
	}
	begin := j
	for j < len(tail) && identifierByte(tail[j]) {
		j++
	}
	ident := tail[begin:j]
	if quote != 0 {
		if j >= len(tail) || tail[j] != quote {
			return 0, false
		}
		j++
	}
	newline := strings.IndexByte(tail[j:], '\n')
	if newline < 0 {
		return len(src), true
	}
	return lineTerminatorEnd(src, start+j+newline+1, ident, indent || php), true
}
func lineTerminatorEnd(src string, start int, ident string, indent bool) int {
	for start < len(src) {
		end := strings.IndexByte(src[start:], '\n')
		if end < 0 {
			end = len(src)
		} else {
			end += start
		}
		text := strings.TrimSuffix(src[start:end], "\r")
		if indent {
			text = strings.TrimSpace(text)
		}
		text = strings.TrimSuffix(text, ";")
		if text == ident {
			if end < len(src) {
				return end + 1
			}
			return end
		}
		start = end + 1
	}
	return len(src)
}

// indexFold searches tag boundaries without copying the remaining document.
func indexFold(text, needle string) int {
	for offset := 0; offset < len(text); {
		next := strings.IndexByte(text[offset:], needle[0])
		if next < 0 {
			return -1
		}
		offset += next
		if len(text)-offset >= len(needle) && strings.EqualFold(text[offset:offset+len(needle)], needle) {
			return offset
		}
		offset++
	}
	return -1
}
