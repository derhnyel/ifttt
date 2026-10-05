package engine

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	core "github.com/derhnyel/ifttt/internal"
	"github.com/derhnyel/ifttt/internal/scan"
	"github.com/derhnyel/ifttt/internal/vcs"
)

// LINT.IfChange(match_contract)
type matchRule struct {
	src       string
	directive core.LintDirective
}

// A narrow needle search discovers equality contracts even on an empty diff.
// Explicit selections/scans already supply their sources; snapshot providers
// supply candidates from committed heads rather than searching dirty checkouts.
func matchCandidates(opts Options, skip []string) ([]string, error) {
	if opts.MatchCandidates != nil {
		return opts.MatchCandidates, nil
	}
	if opts.Files != nil || opts.SourceFiles != nil || opts.StructuralFiles != nil {
		return nil, nil
	}
	needle := opts.directiveSyntax().PrefixDot + "Match"
	repository := opts.Repository
	if repository == nil {
		var err error
		// Supplied Git-format diffs must not switch to jj merely because
		// colocated metadata exists. Native invocations supply their backend.
		repository, err = vcs.Open(context.Background(), ".", "git")
		if err != nil {
			repository, err = vcs.Open(context.Background(), ".", "auto")
		}
		if err != nil {
			return scan.FindDirectiveFiles(".", needle, workerLimit(opts.Parallelism), skip)
		}
	}
	return repository.DirectiveFilesWithBinary(context.Background(), needle)
}

func evaluateMatch(rule matchRule, opts Options, files FileProvider, getDirs func(string) ([]core.LintDirective, error), ignored func(core.TargetRef) bool, emit func(core.Finding)) {
	d := rule.directive
	report := func(id, message string, target core.TargetRef) {
		f := finding(id, rule.src, d.Line, message)
		f.TargetPath, f.TargetLabel = target.Path, target.Label
		emit(f)
	}
	if d.Error != "" || len(d.List) != 2 {
		report("match_invalid", d.Error, core.TargetRef{})
		return
	}
	refs := make([]core.TargetRef, 2)
	for i, raw := range d.List {
		refs[i] = resolveTarget(rule.src, raw, opts.directiveSyntax())
		if invalidGoogleDriveTarget(raw, opts.directiveSyntax()) || (opts.StrictPaths && opts.directiveSyntax().Prefix == "LINT" && !isRemotePath(raw) && !strings.HasPrefix(raw, "//") && !strings.HasPrefix(raw, ":") && !strings.HasPrefix(raw, "#")) {
			report("invalid_target_path", "strict LINT targets must start with // or use a same-file label selector", refs[i])
			return
		}
		if refs[i].Label == "" {
			report("match_invalid", "Match references must select labelled sections", refs[i])
			return
		}
		if ignored(refs[i]) || pathInSkippedDir(refs[i].Path, makeSkipSet(effectiveSkipDirs(opts.SkipDirs))) {
			return
		}
	}
	var pattern *regexp.Regexp
	if d.Pattern != "" {
		var err error
		pattern, err = regexp.Compile(d.Pattern)
		if err != nil {
			report("match_pattern", fmt.Sprintf("invalid Match regex: %v", err), refs[1])
			return
		}
		if pattern.NumSubexp() > 1 {
			report("match_pattern", "Match regex must have at most one capture group", refs[1])
			return
		}
	}
	bodies := make([]string, 2)
	for i, target := range refs {
		dirs, err := getDirs(target.Path)
		if err != nil {
			report("match_target_error", fmt.Sprintf("cannot read Match section '%s': %v", d.List[i], err), target)
			return
		}
		for _, f := range validateUniqueness(dirs, target.Path) {
			if f.RuleID == "duplicate_label" {
				// Only the selected duplicate makes this endpoint ambiguous; other
				// malformed structure is reported below without guessing its body.
				count := 0
				for _, dir := range dirs {
					if (dir.Kind == core.Label && dir.Name == target.Label) || (dir.Kind == core.IfChange && dir.Label == target.Label) {
						count++
					}
				}
				if count > 1 {
					report("match_label_ambiguous", fmt.Sprintf("Match label '%s' is defined more than once in '%s'", target.Label, target.Path), target)
					return
				}
			}
		}
		problems, valid := validateTargetDirectives(target.Path, dirs, targetPolicy(files, opts.Factories, target.Path, opts.UnknownPolicy), targetSyntax(files, opts.Factories, target.Path, opts.directiveSyntax()))
		for _, problem := range problems {
			if !valid || problem.Severity == "error" {
				report("match_target_error", fmt.Sprintf("invalid Match target '%s': %s", target.Path, problem.Message), target)
				return
			}
			emit(problem)
		}
		region, ok := computeLabelRanges(dirs)[target.Label]
		if !ok {
			report("match_label_missing", fmt.Sprintf("Match label '%s' not found in '%s'", target.Label, target.Path), target)
			return
		}
		provider, actual, err := fileProviderForPath(files, opts.Factories, target.Path)
		var data []byte
		if err == nil {
			data, err = provider.ReadFile(actual)
		}
		if err != nil {
			report("match_target_error", fmt.Sprintf("cannot read Match section '%s': %v", d.List[i], err), target)
			return
		}
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		if region.StartLine < 1 || region.EndLine > len(lines) || region.StartLine > region.EndLine+1 {
			report("match_target_error", "Match section has an invalid line range", target)
			return
		}
		bodies[i] = strings.Join(lines[region.StartLine-1:region.EndLine], "\n")
		if region.EndLine >= region.StartLine {
			bodies[i] += "\n"
		}
	}
	equal := bodies[0] == bodies[1]
	if pattern != nil {
		values := make([][]string, 2)
		for i, body := range bodies {
			matches := pattern.FindAllStringSubmatch(body, -1)
			if len(matches) == 0 {
				report("match_no_match", fmt.Sprintf("Match regex found no values in '%s'", d.List[i]), refs[i])
				return
			}
			group := pattern.NumSubexp()
			for _, match := range matches {
				if match[group] == "" {
					report("match_no_match", fmt.Sprintf("Match regex extracted an empty value in '%s'", d.List[i]), refs[i])
					return
				}
				values[i] = append(values[i], match[group])
			}
		}
		equal = slices.Equal(values[0], values[1])
	}
	if !equal {
		report("match_mismatch", fmt.Sprintf("Match sections '%s' and '%s' differ", d.List[0], d.List[1]), refs[1])
	}
}

// LINT.ThenChange(//internal/parse/match.go:match_contract, //test/integration/match_test.go:match_contract, //README.md:match_contract)
