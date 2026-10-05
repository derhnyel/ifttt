package engine

import (
	"strings"

	core "github.com/derhnyel/ifttt/internal"
	"github.com/derhnyel/ifttt/internal/parse"
)

// LINT.IfChange(replaced_block)
// Reconstruct the old source only for files with added opening directives.
// Full comment context is needed: removed fragments can start inside a block
// comment, string or fenced example. Correspondence is one-to-one so a new
// neighbouring block cannot borrow an existing block's change evidence.
func existingGuards(current []byte, dirs []core.LintDirective, change *core.FileChanges, settings *parse.Settings) map[int]bool {
	var old strings.Builder
	anchors := map[int]int{}
	deleted := map[int]bool{}
	oldLine := 1
	lines := strings.SplitAfter(string(current), "\n")
	for index := 0; index <= len(lines); index++ {
		line := ""
		if index < len(lines) {
			line = lines[index]
		}
		newLine := index + 1
		removed := change.RemovedInNew[newLine]
		if removed != "" {
			for _, text := range strings.SplitAfter(removed, "\n") {
				if text == "" {
					continue
				}
				anchors[oldLine], deleted[oldLine] = newLine, true
				old.WriteString(text)
				oldLine++
			}
		}
		if line != "" && !change.AddedLines[newLine] {
			anchors[oldLine] = newLine
			old.WriteString(line)
			oldLine++
		}
	}
	path := change.OldFile
	if path == "" || path == "/dev/null" {
		path = change.File
	}
	provider := parse.Provider{Settings: settings, ReadFile: func(string) ([]byte, error) { return []byte(old.String()), nil }}
	previous, _ := provider.Parse(path)
	oldOpeners, newOpeners := []core.LintDirective{}, []core.LintDirective{}
	oldLabels, newLabels := map[string]int{}, map[string]int{}
	newByLabel := map[string]core.LintDirective{}
	for _, d := range previous {
		if d.Kind == core.IfChange {
			oldOpeners = append(oldOpeners, d)
			oldLabels[d.Label]++
		}
	}
	for _, d := range dirs {
		if d.Kind == core.IfChange {
			newOpeners = append(newOpeners, d)
			newLabels[d.Label]++
			newByLabel[d.Label] = d
		}
	}
	existing, usedOld := map[int]bool{}, map[int]bool{}
	// Unique labels survive insertions and moves. Resolve them before using
	// position to associate renamed or unlabelled opening directives.
	for _, prior := range oldOpeners {
		if prior.Label == "" || oldLabels[prior.Label] != 1 || newLabels[prior.Label] != 1 {
			continue
		}
		now := newByLabel[prior.Label]
		existing[now.Line], usedOld[prior.Line] = true, true
	}
	unmatched := map[int]bool{}
	for _, prior := range oldOpeners {
		if !usedOld[prior.Line] && deleted[prior.Line] {
			unmatched[prior.Line] = true
		}
	}
	if len(unmatched) == 0 {
		return existing
	}
	oldBodies, oldClosers := guardEvidence([]byte(old.String()), previous, unmatched)
	newBodies, newClosers := guardEvidence(current, dirs, change.AddedLines)
	for _, prior := range oldOpeners {
		if usedOld[prior.Line] || !deleted[prior.Line] {
			continue
		}
		anchor := anchors[prior.Line]
		start, end := anchor, anchor
		for start > 1 && change.AddedLines[start-1] {
			start--
		}
		for change.AddedLines[end] {
			end++
		}
		best, distance := 0, int(^uint(0)>>1)
		bestBody, bestCloser := -1.0, false
		for _, now := range newOpeners {
			if existing[now.Line] || !change.AddedLines[now.Line] || now.Line < start || now.Line >= end {
				continue
			}
			delta := now.Line - anchor
			if delta < 0 {
				delta = -delta
			}
			// Git can align an old closing comment with a newly inserted block.
			// Prefer the body that retains the original text before using closing
			// context or distance within the replacement run to break ties.
			body := guardSimilarity(oldBodies[prior.Line], newBodies[now.Line])
			close := oldClosers[prior.Line]
			closer := close > 0 && len(newBodies[now.Line].grams) > 0 && !deleted[close] && anchors[close] == newClosers[now.Line]
			if body > bestBody || (body == bestBody && closer && !bestCloser) ||
				(body == bestBody && closer == bestCloser && delta < distance) {
				best, distance, bestBody, bestCloser = now.Line, delta, body, closer
			}
		}
		if best != 0 {
			existing[best] = true
		}
	}
	return existing
}

// Body fingerprints use character trigrams so comparison is linear in the
// guarded text, independent of language, and tolerant of small value edits.
// They are correspondence evidence only; they do not establish content equality.
type guardFingerprint struct {
	text  string
	grams map[string]bool
}

func guardEvidence(content []byte, dirs []core.LintDirective, selected map[int]bool) (map[int]guardFingerprint, map[int]int) {
	bodies, closers := map[int]guardFingerprint{}, map[int]int{}
	directiveLines := map[int]bool{}
	for _, d := range dirs {
		directiveLines[d.Line] = true
	}
	lines := strings.Split(string(content), "\n")
	var stack []int
	for _, d := range dirs {
		switch d.Kind {
		case core.IfChange:
			stack = append(stack, d.Line)
		case core.ThenChange:
			if len(stack) == 0 {
				continue
			}
			start := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			closers[start] = d.Line
			if !selected[start] {
				continue
			}
			var body strings.Builder
			for line := start + 1; line < d.Line && line <= len(lines); line++ {
				if !directiveLines[line] {
					body.WriteString(lines[line-1])
					body.WriteByte('\n')
				}
			}
			text := strings.Join(strings.Fields(body.String()), " ")
			grams := map[string]bool{}
			if len(text) > 0 && len(text) < 3 {
				grams[text] = true
			}
			for i := 0; i+3 <= len(text); i++ {
				grams[text[i:i+3]] = true
			}
			bodies[start] = guardFingerprint{text: body.String(), grams: grams}
		}
	}
	return bodies, closers
}

func guardSimilarity(prior, current guardFingerprint) float64 {
	// An exact copy can be a newly inserted neighbour; it does not identify
	// which guard contains the edited old body. Leave it to context/position.
	if prior.text == current.text {
		return 0
	}
	a, b := prior.grams, current.grams
	if len(a)+len(b) == 0 {
		return 0
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	shared := 0
	for gram := range a {
		if b[gram] {
			shared++
		}
	}
	return float64(2*shared) / float64(len(a)+len(b))
}

// LINT.ThenChange(//test/integration/default_syntax_test.go:replaced_block)
