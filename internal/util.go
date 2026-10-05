package ifttt

import (
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Decode C-style octal sequences like \040
func DecodeCOctal(s string) string {
	var out []byte
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+1 < len(s) {
			j := i + 1
			k := j
			for k < len(s) && k-j < 3 && s[k] >= '0' && s[k] <= '7' {
				k++
			}
			if k > j {
				val := 0
				for t := j; t < k; t++ {
					val = val*8 + int(s[t]-'0')
				}
				out = append(out, byte(val))
				i = k
				continue
			}
		}
		out = append(out, s[i])
		i++
	}
	return string(out)
}

// NormalizePath cleans Git diff paths (strip quotes, a/b prefixes, decode octal).
// Repository paths use forward slashes on every host; this is not a filesystem
// path normalizer because native backslashes can look like Git octal escapes.
func NormalizePath(raw string) string {
	raw = strings.TrimSpace(raw)
	if l := len(raw); l >= 2 && ((raw[0] == '"' && raw[l-1] == '"') || (raw[0] == '\'' && raw[l-1] == '\'')) {
		raw = raw[1 : l-1]
	}
	raw = DecodeCOctal(raw)
	if strings.HasPrefix(raw, "a/") || strings.HasPrefix(raw, "b/") {
		raw = raw[2:]
	}
	raw = path.Clean(raw)
	return raw
}

// IgnorePattern with precompiled regex
type IgnorePattern struct {
	TargetName string
	Label      string
	Rx         *regexp.Regexp
	BaseDir    string
}

// Matches keeps glob syntax unchanged and moves filesystem candidates into
// the configuration's coordinates. Empty BaseDir preserves repository paths.
func (p IgnorePattern) Matches(candidate string) bool {
	if p.BaseDir != "" && !strings.Contains(candidate, "://") {
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			return false
		}
		rel, err := filepath.Rel(p.BaseDir, absolute)
		if err != nil {
			return false
		}
		candidate = filepath.ToSlash(rel)
	}
	return p.Rx.MatchString(candidate) || p.Rx.MatchString(filepath.Base(candidate))
}

// CompileGlob compiles a shell-like glob to regex
func CompileGlob(glob string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	runes := []rune(glob)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch r {
		case '*':
			if i+1 < len(runes) && runes[i+1] == '*' {
				i++
				if i+1 < len(runes) && (runes[i+1] == '/' || runes[i+1] == '\\') {
					i++
					b.WriteString(`(?s:.*[/\\])?`)
				} else {
					b.WriteString(`(?s:.*)`)
				}
			} else {
				b.WriteString(`[^/\\]*`)
			}
		case '?':
			b.WriteString(`[^/\\]`)
		case '/', '\\':
			b.WriteString(`[/\\]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// DefaultParallelism avoids excess filesystem contention in ordinary runs.
const DefaultParallelism = 2
