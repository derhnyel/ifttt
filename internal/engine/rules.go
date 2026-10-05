package engine

import (
	"fmt"
	"sort"
	"strings"

	core "github.com/derhnyel/ifttt/internal"
)

type conditionalRule struct {
	src              string
	directive        core.LintDirective
	ifLine, thenLine int
}

func evaluateConditionalRule(rule conditionalRule, changes map[string]*core.FileChanges, labels map[string]map[string]core.LineRange, files FileProvider, factories []FileProviderFactory, codeOnly bool, ignored func(core.TargetRef) bool, emit func(core.Finding), lookup func(string) (*core.FileChanges, error), syn core.DirectiveSyntax, suppress ...bool) {
	// LINT.IfChange(conditional_source_trigger)
	// Configured path/label exclusions are a separate policy, not change evidence.
	source := changes[rule.src]
	triggered := source != nil && !source.Deleted && !(len(suppress) > 0 && suppress[0])
	if triggered {
		if rule.ifLine > 0 {
			triggered = pairTriggered(pairInfo{ifLine: rule.ifLine, thenLine: rule.thenLine}, source, codeOnly)
		} else {
			triggered = len(source.AddedLines)+len(source.RemovedLines) > 0 && (!codeOnly || !onlyCommentChanges(source, 1, int(^uint(0)>>1)-1))
		}
	}
	// LINT.ThenChange(//test/integration/change_set_test.go:conditional_target_structure, //internal/engine/conditional_structure_test.go:conditional_target_structure)
	var missing []string
	satisfied, total := 0, 0
	for _, raw := range targetsOf(rule.directive) {
		target := resolveTarget(rule.src, raw, syn)
		if ignored(target) {
			continue
		}
		// LINT.IfChange(conditional_target_structure)
		// Snapshot configuration can invalidate a selector without changing its
		// source or target body. Validate structure even when edit checks are inactive.
		// Editor/build targets are eligible unless the caller explicitly skips them.
		// The ignored callback resolves config-relative target exclusions first.
		provider, actual, readErr := fileProviderForPath(files, factories, target.Path)
		if readErr == nil {
			_, readErr = provider.ReadFile(actual)
		}
		if readErr != nil {
			// Labelled targets already report read errors during directive loading.
			if target.Label == "" {
				f := errFinding(rule.src, rule.directive.Line, readErr)
				f.TargetPath = target.Path
				emit(f)
			}
		} else if target.Label != "" {
			if _, ok := labels[target.Path][target.Label]; !ok {
				f := finding("label_missing", rule.src, rule.directive.Line, fmt.Sprintf("label '%s' not found in '%s'", target.Label, target.Path))
				f.TargetPath, f.TargetLabel = target.Path, target.Label
				emit(f)
			}
		}
		if !triggered {
			continue
		}
		// LINT.ThenChange(//internal/engine/engine.go:conditional_target_structure, //test/integration/change_set_test.go:conditional_target_structure, //internal/engine/conditional_structure_test.go:conditional_target_structure, //docs/directives.md:conditional_target_structure)
		total++
		changed, lookupErr := lookup(target.Path)
		if lookupErr != nil {
			f := finding("change_evidence_error", rule.src, rule.directive.Line, lookupErr.Error())
			f.TargetPath, f.TargetLabel = target.Path, target.Label
			emit(f)
			continue
		}
		touched := changed != nil && (changed.ContentChanged || changed.TypeChanged || changed.Deleted || changed.Renamed || len(changed.AddedLines)+len(changed.RemovedLines) > 0)
		if target.Label != "" {
			if changed != nil && changed.Opaque {
				f := finding("change_evidence_error", rule.src, rule.directive.Line, "changed target has no complete line evidence for its labelled region")
				f.TargetPath, f.TargetLabel = target.Path, target.Label
				emit(f)
				continue
			}
			region, ok := labels[target.Path][target.Label]
			touched = changed != nil && (changed.Deleted || (ok && blockChanged(changed, region.StartLine, region.EndLine)))
		}
		if rule.directive.Kind == core.Forbid {
			if touched {
				emit(finding("forbid_change", rule.src, rule.directive.Line, fmt.Sprintf("changes in '%s' are forbidden by this directive", raw)))
			}
			continue
		}
		if readErr != nil {
			touched = false
		}
		if touched {
			satisfied++
		} else {
			missing = append(missing, raw)
		}
	}
	if rule.directive.Kind == core.Forbid || total == 0 {
		return
	}
	if rule.directive.Kind == core.RequireAny && satisfied == 0 {
		emit(finding("require_any_missing", rule.src, rule.directive.Line, fmt.Sprintf("expected changes in at least one target: %s", strings.Join(missing, ", "))))
	}
	if rule.directive.Kind == core.RequireAll && satisfied < total {
		emit(finding("require_all_missing", rule.src, rule.directive.Line, fmt.Sprintf("expected changes in all targets; unchanged or unreadable: %s", strings.Join(missing, ", "))))
	}
}

func unknownFinding(path string, d core.LintDirective, policy string) (core.Finding, bool) {
	if strings.EqualFold(policy, "ignore") {
		return core.Finding{}, false
	}
	f := finding("unknown_directive", path, d.Line, fmt.Sprintf("%s: %s", d.Label, d.Name))
	if strings.EqualFold(policy, "warn") {
		f.Severity = "warning"
	}
	return f, true
}

func directiveSuppressed(dirs []core.LintDirective, f core.Finding) bool {
	disabled := make(map[string]bool)
	for _, d := range dirs {
		if d.Line > f.Line {
			break
		}
		rule := strings.ToLower(d.Label)
		switch d.Kind {
		case core.Disable:
			disabled[rule] = true
		case core.Enable:
			delete(disabled, rule)
		}
	}
	return disabled["all"] || disabled["*"] || disabled[strings.ToLower(f.RuleID)]
}

func needsReverseValidation(changes map[string]*core.FileChanges) bool {
	syn := core.CurrentDirectiveSyntax()
	for _, fc := range changes {
		if fc.Deleted || fc.Renamed {
			return true
		}
		for _, text := range fc.RemovedInNew {
			if strings.Contains(text, syn.TokenIfChange) || strings.Contains(text, syn.TokenLabel) {
				return true
			}
		}
	}
	return false
}

func sortFindings(findings []core.Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.RuleID != b.RuleID {
			return a.RuleID < b.RuleID
		}
		return a.Message < b.Message
	})
}

func workerLimit(n int) int {
	if n <= 0 {
		n = core.DefaultParallelism
	}
	if n > 64 {
		n = 64
	}
	return n
}

func hasRemovedDirective(fc *core.FileChanges) bool {
	syn := core.NewDirectiveSyntax(fc.DirectivePrefix)
	if fc.DirectivePrefix == "" {
		syn = core.CurrentDirectiveSyntax()
	}
	for _, text := range fc.RemovedInNew {
		if strings.Contains(text, syn.TokenIfChange) || strings.Contains(text, syn.TokenLabel) {
			return true
		}
	}
	return false
}

func emptyGoogleBlock(begin, end core.LintDirective, syntax ...core.DirectiveSyntax) bool {
	return syntaxOrDefault(syntax...).Prefix == "LINT" && begin.Label == "" && len(targetsOf(end)) == 0
}

func invalidGoogleDriveTarget(raw string, syntax ...core.DirectiveSyntax) bool {
	return syntaxOrDefault(syntax...).Prefix == "LINT" && !isRemotePath(raw) && len(raw) >= 3 &&
		((raw[0] >= 'a' && raw[0] <= 'z') || (raw[0] >= 'A' && raw[0] <= 'Z')) && raw[1] == ':' && (raw[2] == '/' || raw[2] == '\\')
}
