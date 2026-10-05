package parse

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	ifttt "github.com/derhnyel/ifttt/internal"
	"github.com/derhnyel/ifttt/internal/comments"
)

// Settings belongs to one source snapshot; nil settings preserve CLI defaults.
type Settings struct {
	Syntax           ifttt.DirectiveSyntax
	PythonDocstrings *bool
	UnknownPolicy    string
}

type Provider struct {
	Settings *Settings
	ReadFile func(string) ([]byte, error)
}

func (p Provider) readFile() func(string) ([]byte, error) {
	if p.ReadFile != nil {
		return p.ReadFile
	}
	return os.ReadFile
}

func (p Provider) Parse(path string) ([]ifttt.LintDirective, error) {
	var syn ifttt.DirectiveSyntax
	var docstrings *bool
	if p.Settings != nil {
		syn = p.Settings.Syntax
		docstrings = p.Settings.PythonDocstrings
	} else {
		syn = ifttt.CurrentDirectiveSyntax()
	}
	blocks, err := comments.ExtractWithSettings(path, syn.PrefixDot, p.readFile(), docstrings)
	if err != nil {
		return nil, err
	}
	{
		var merged []comments.Block
		for _, block := range blocks {
			if len(merged) > 0 && merged[len(merged)-1].Start+strings.Count(merged[len(merged)-1].Text, "\n")+1 == block.Start {
				merged[len(merged)-1].Text += "\n" + block.Text
			} else {
				merged = append(merged, block)
			}
		}
		blocks = merged
	}
	var dirs []ifttt.LintDirective
	for _, b := range blocks {
		lines := strings.Split(b.Text, "\n")
		fence := ""
		for i := 0; i < len(lines); i++ {
			t := lines[i]
			line := b.Start + i
			t = strings.TrimSpace(t)
			t = strings.TrimSpace(strings.TrimPrefix(t, "*"))
			if fence != "" {
				if comments.ClosesFence(t, fence) {
					fence = ""
				}
				continue
			}
			if opening := comments.FenceDelimiter(t); opening != "" {
				fence = opening
				continue
			}
			if !strings.HasPrefix(t, syn.PrefixDot) {
				continue
			}
			if syn.Prefix == "LINT" && (strings.HasPrefix(t, "LINT.IfChange") || strings.HasPrefix(t, "LINT.ThenChange")) {
				raw := t
				consumed := 0
				if strings.HasPrefix(t, "LINT.ThenChange(") {
					for googleDirectiveEnd(raw) < 0 && i+consumed+1 < len(lines) {
						next := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i+consumed+1]), "*"))
						if strings.HasPrefix(next, syn.PrefixDot) || comments.FenceDelimiter(next) != "" {
							break
						}
						consumed++
						// A trailing backslash continues the directive, rather than
						// becoming part of the next target's filesystem path.
						raw = strings.TrimSuffix(strings.TrimSpace(raw), `\`)
						raw += " " + next
					}
				}
				if end := googleDirectiveEnd(raw); end >= 0 && strings.TrimSpace(raw[end:]) != "" {
					i += consumed
					continue
				}
				d := parseGoogle(raw, line)
				dirs = append(dirs, d)
				i += consumed
				continue
			}
			raw := t
			consumed := 0
			if open := strings.IndexByte(t, '('); open >= 0 && strings.HasPrefix(strings.TrimSpace(t[open+1:]), "[") {
				for !arrayClosed(raw) && i+consumed+1 < len(lines) {
					next := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i+consumed+1]), "*"))
					if strings.HasPrefix(next, syn.PrefixDot) {
						break
					}
					consumed++
					raw += " " + next
				}
			}
			dirs = append(dirs, parseSentry(raw, line, syn.PrefixDot))
			i += consumed

		}
	}
	// basic structural validation: labels balanced
	var stack []struct {
		name string
		line int
	}
	for _, d := range dirs {
		if d.Kind == ifttt.Label {
			stack = append(stack, struct {
				name string
				line int
			}{d.Name, d.Line})
		}
		if d.Kind == ifttt.EndLabel {
			if len(stack) == 0 {
				return dirs, fmt.Errorf("unmatched EndLabel at %s:%d", filepath.Base(path), d.Line)
			}
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) > 0 {
		return dirs, fmt.Errorf("unclosed Label '%s' at %s:%d", stack[len(stack)-1].name, filepath.Base(path), stack[len(stack)-1].line)
	}
	return dirs, nil
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

var googleLabel = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*$`)

// Find the outer closing parenthesis, allowing parentheses inside target paths.
// A completed directive followed by text is a prose mention; an unclosed call
// remains a malformed directive and receives the configured diagnostic policy.
func googleDirectiveEnd(text string) int {
	for _, name := range []string{"LINT.IfChange", "LINT.ThenChange"} {
		if !strings.HasPrefix(text, name) {
			continue
		}
		rest := text[len(name):]
		if rest == "" || rest[0] == ' ' || rest[0] == '\t' {
			return len(name)
		}
		if rest[0] != '(' {
			return -1
		}
		depth := 0
		for i, ch := range rest {
			switch ch {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					return len(name) + i + 1
				}
			}
		}
	}
	return -1
}

func unknown(text string, line int, reason string) ifttt.LintDirective {
	return ifttt.LintDirective{Kind: ifttt.Unknown, Line: line, Name: text, Label: reason}
}
func parseGoogle(text string, line int) ifttt.LintDirective {
	bad := func(reason string) ifttt.LintDirective { return unknown(text, line, reason) }
	if text == "LINT.IfChange" || text == "LINT.IfChange()" {
		return ifttt.LintDirective{Kind: ifttt.IfChange, Line: line}
	}
	if strings.HasPrefix(text, "LINT.IfChange(") && strings.HasSuffix(text, ")") {
		label := text[len("LINT.IfChange(") : len(text)-1]
		if !googleLabel.MatchString(label) {
			return bad("malformed label: must start with a letter and contain only letters, digits, _, ., or -")
		}
		return ifttt.LintDirective{Kind: ifttt.IfChange, Line: line, Label: label}
	}
	if strings.HasPrefix(text, "LINT.ThenChange(") && strings.HasSuffix(text, ")") {
		raw := strings.TrimSpace(text[len("LINT.ThenChange(") : len(text)-1])
		d := ifttt.LintDirective{Kind: ifttt.ThenChange, Line: line}
		if raw == "" {
			return d
		}
		for _, item := range splitCSV(raw) {
			if item == "" {
				continue
			}
			if strings.ContainsAny(item, "[]\"'\t\n") {
				return bad("malformed target")
			}
			if strings.HasPrefix(item, ":") && !googleLabel.MatchString(item[1:]) {
				return bad("malformed target label")
			}
			if idx := strings.LastIndex(item, ":"); idx > strings.LastIndexAny(item, "/\\") && idx > 0 && !googleLabel.MatchString(item[idx+1:]) {
				return bad("malformed target label")
			}
			d.List = append(d.List, item)
		}
		if len(d.List) == 0 {
			return bad("empty target list")
		}
		return d
	}
	return bad("malformed directive")
}

func parseSentry(text string, line int, prefix string) ifttt.LintDirective {
	bad := func() ifttt.LintDirective { return unknown(text, line, "malformed or unknown directive") }
	body := strings.TrimPrefix(text, prefix)
	// LINT.IfChange(match_dispatch)
	if strings.TrimSpace(strings.SplitN(body, "(", 2)[0]) == "Match" {
		return parseMatch(body, line)
	}
	// LINT.ThenChange(//internal/parse/match.go:match_contract, //internal/parse/match_test.go:match_parser_tests)
	if body == "IfChange" {
		return ifttt.LintDirective{Kind: ifttt.IfChange, Line: line}
	}
	if body == "EndLabel" {
		return ifttt.LintDirective{Kind: ifttt.EndLabel, Line: line}
	}
	open := strings.IndexByte(body, '(')
	if open < 0 || !strings.HasSuffix(body, ")") {
		return bad()
	}
	name := strings.TrimSpace(body[:open])
	arg := strings.TrimSpace(body[open+1 : len(body)-1])
	d := ifttt.LintDirective{Line: line}
	if name == "ThenChange" || name == "RequireAny" || name == "RequireAll" {
		if strings.HasPrefix(arg, "[") && strings.HasSuffix(arg, "]") {
			list, ok := quotedList(arg[1 : len(arg)-1])
			if !ok {
				return bad()
			}
			d.List = list
			switch name {
			case "ThenChange":
				d.Kind = ifttt.ThenChange
			case "RequireAny":
				d.Kind = ifttt.RequireAny
			case "RequireAll":
				d.Kind = ifttt.RequireAll
			}
			return d
		}
		if name != "ThenChange" {
			return bad()
		}
	}
	value, ok := quotedValue(arg)
	if !ok {
		return bad()
	}
	switch name {
	case "IfChange":
		d.Kind = ifttt.IfChange
		d.Label = value
	case "ThenChange":
		d.Kind = ifttt.ThenChange
		d.Target = value
	case "Label":
		d.Kind = ifttt.Label
		d.Name = value
	case "ForbidChange":
		d.Kind = ifttt.Forbid
		d.Target = value
	case "Disable":
		d.Kind = ifttt.Disable
		d.Label = value
	case "Enable":
		d.Kind = ifttt.Enable
		d.Label = value
	case "Ignore":
		d.Kind = ifttt.Ignore
		d.Label = strings.ToLower(strings.TrimSpace(value))
	default:
		return bad()
	}
	return d
}
func quotedValue(arg string) (string, bool) {
	if len(arg) < 3 || (arg[0] != '\'' && arg[0] != '"') || arg[len(arg)-1] != arg[0] {
		return "", false
	}
	if strings.ContainsRune(arg[1:len(arg)-1], rune(arg[0])) {
		return "", false
	}
	return arg[1 : len(arg)-1], true
}
func quotedList(raw string) ([]string, bool) {
	var result []string
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return result, true
	}
	for raw != "" {
		if raw[0] != '\'' && raw[0] != '"' {
			return nil, false
		}
		end := strings.IndexByte(raw[1:], raw[0])
		if end < 0 {
			return nil, false
		}
		end++
		value, ok := quotedValue(raw[:end+1])
		if !ok {
			return nil, false
		}
		result = append(result, value)
		raw = strings.TrimSpace(raw[end+1:])
		if raw == "" {
			break
		}
		if raw[0] != ',' {
			return nil, false
		}
		raw = strings.TrimSpace(raw[1:])
	}
	return result, true
}

// Find the array terminator outside quoted target names.
func arrayClosed(text string) bool {
	var quote byte
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		if ch == ']' {
			return true
		}
	}
	return false
}
