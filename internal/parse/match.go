package parse

import (
	"strconv"
	"strings"

	core "github.com/derhnyel/ifttt/internal"
)

// LINT.IfChange(match_contract)
// The caller strips the owning repository’s prefix before parsing arguments.
// Double quotes use JSON/Go escapes; single quotes preserve regex backslashes.
// References stay raw so the engine resolves them with the source grammar.
// Discovery ignores do not change selector parsing: an explicit endpoint can
// name a Git-ignored file and must still be compared by the engine.
func parseMatch(body string, line int) core.LintDirective {
	d := core.LintDirective{Kind: core.Match, Line: line}
	bad := func() core.LintDirective {
		d.Error = "Match requires two quoted labelled references and an optional nonempty quoted regex"
		return d
	}
	open := strings.IndexByte(body, '(')
	if open < 0 || !strings.HasSuffix(body, ")") {
		return bad()
	}
	raw := strings.TrimSpace(body[open+1 : len(body)-1])
	var args []string
	for raw != "" {
		quote := raw[0]
		if quote != '\'' && quote != '"' {
			return bad()
		}
		end := 1
		for end < len(raw) {
			if raw[end] == quote {
				break
			}
			if quote == '"' && raw[end] == '\\' {
				end++
			}
			end++
		}
		if end >= len(raw) {
			return bad()
		}
		value := raw[1:end]
		if quote == '"' {
			var err error
			value, err = strconv.Unquote(raw[:end+1])
			if err != nil {
				return bad()
			}
		}
		if value == "" {
			return bad()
		}
		args = append(args, value)
		raw = strings.TrimSpace(raw[end+1:])
		if raw == "" {
			break
		}
		if raw[0] != ',' {
			return bad()
		}
		raw = strings.TrimSpace(raw[1:])
		if raw == "" {
			return bad()
		}
	}
	if len(args) != 2 && len(args) != 3 {
		return bad()
	}
	d.List = args[:2]
	if len(args) == 3 {
		d.Pattern = args[2]
	}
	return d
}

// LINT.ThenChange(//internal/engine/match.go:match_contract, //test/integration/match_test.go:match_contract, //README.md:match_contract)
