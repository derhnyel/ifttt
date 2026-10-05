package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	core "github.com/derhnyel/ifttt/internal"
	"github.com/derhnyel/ifttt/internal/diff"
	ilog "github.com/derhnyel/ifttt/internal/log"
	"github.com/derhnyel/ifttt/internal/parse"
	"github.com/derhnyel/ifttt/internal/scan"
	"github.com/derhnyel/ifttt/internal/vcs"
)

// Public entrypoints

type Options struct {
	// RevisionChanges and ReverseCandidates describe an immutable source snapshot.
	RevisionChanges   map[string]*core.FileChanges
	ReverseCandidates []string
	// DependencyChanges supplies foreign changed-line evidence without adding sources.
	DependencyChanges  func(string) (*core.FileChanges, error)
	Repository         *vcs.Backend
	StrictPaths        bool
	Parallelism        int
	Verbose            bool
	Ignores            []string // file or file#label
	CodeOnly           bool
	UnknownPolicy      string // error|warn|ignore
	SkipDirs           []string
	Fix                bool
	Files              FileProvider
	Factories          []FileProviderFactory
	CombinedDiffPolicy string
	SuppressCoChanges  bool
	SourceFiles        []string
	StructuralFiles    []string
}

const (
	CombinedDiffStrict = "strict"
	CombinedDiffWarn   = "warn"
	CombinedDiffIgnore = "ignore"
	CombinedDiffParent = "parent"
)

type Result struct {
	Findings   []core.Finding
	Suppressed []core.Finding
	Stats      map[string]any
}

type directiveResult struct {
	dirs []core.LintDirective
	err  error
}

type pairInfo struct {
	src              string
	ifLine, thenLine int
	ifLabel          string
	target           core.TargetRef
}

type lineMapBundle struct {
	fc          core.FileChanges
	added       map[int]bool
	removed     map[int]bool
	addedText   map[int]string
	removedText map[int]string
}

var lineMapPool = sync.Pool{
	New: func() any {
		return &lineMapBundle{
			added:       make(map[int]bool),
			removed:     make(map[int]bool),
			addedText:   make(map[int]string),
			removedText: make(map[int]string),
		}
	},
}

// Build changes map from a unified diff string using diff.Parser.
func ParseChangedLines(diffText string) (map[string]*core.FileChanges, error) {
	changes, _, err := parseChangedLinesReader(strings.NewReader(diffText), true, false)
	return changes, err
}

func ParseChangedLinesReader(r io.Reader) (map[string]*core.FileChanges, error) {
	changes, _, err := parseChangedLinesReader(r, true, false)
	return changes, err
}

func parseChangedLinesReader(r io.Reader, captureText bool, pooled bool) (map[string]*core.FileChanges, []func(), error) {
	m := make(map[string]*core.FileChanges)
	releases := make([]func(), 0)
	syn := core.CurrentDirectiveSyntax()
	p := diff.New(r)
	for {
		fp, err := p.NextFile()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			for _, rel := range releases {
				rel()
			}
			return nil, nil, err
		}
		if fp == nil {
			continue
		}
		path := filepath.Clean(fp.NewPath)
		deleted := fp.NewPath == "/dev/null"
		renamed := fp.OldPath != fp.NewPath && fp.OldPath != "/dev/null" && !deleted
		if deleted {
			path = filepath.Clean(fp.OldPath)
		}
		var (
			fc      *core.FileChanges
			release func()
			bundle  *lineMapBundle
		)
		if pooled {
			bundle, release = acquireLineMaps(captureText)
			fc = &bundle.fc
			fc.File = path
			fc.HasDirectiveHint = false
		} else {
			var addedByLine map[int]string
			var removedByLine map[int]string
			if captureText {
				addedByLine = make(map[int]string)
				removedByLine = make(map[int]string)
			}
			fc = &core.FileChanges{
				File:             path,
				AddedLines:       make(map[int]bool),
				RemovedLines:     make(map[int]bool),
				AddedByLine:      addedByLine,
				RemovedByLine:    removedByLine,
				HasDirectiveHint: false,
			}
		}
		fc.OldFile = filepath.Clean(fp.OldPath)
		fc.Deleted, fc.Renamed = deleted, renamed
		fc.RemovedInNew = make(map[int]string)
		hasChanges := false
		hasDirective := false
		for _, h := range fp.Chunks {
			oldLine := h.OldStart
			newLine := h.NewStart
			for _, ln := range h.Lines {
				if !hasDirective && strings.Contains(ln.Text, syn.PrefixDot) {
					hasDirective = true
				}
				switch ln.Kind {
				case '+':
					fc.AddedLines[newLine] = true
					if captureText {
						fc.AddedByLine[newLine] = ln.Text
					}
					newLine++
					hasChanges = true
				case '-':
					fc.RemovedLines[oldLine] = true
					fc.RemovedInNew[newLine] += ln.Text + "\n"
					if captureText {
						fc.RemovedByLine[oldLine] = ln.Text
					}
					oldLine++
					hasChanges = true
				default:
					oldLine++
					newLine++
				}
			}
		}
		if hasChanges || deleted || renamed {
			fc.HasDirectiveHint = hasDirective
			m[path] = fc
			if pooled && release != nil {
				releases = append(releases, release)
			}
		} else if pooled && release != nil {
			release()
		}
	}
	return m, releases, nil
}

func acquireLineMaps(captureText bool) (*lineMapBundle, func()) {
	raw := lineMapPool.Get().(*lineMapBundle)
	clearBoolMap(raw.added)
	clearBoolMap(raw.removed)
	clearStringMap(raw.addedText)
	clearStringMap(raw.removedText)
	raw.fc = core.FileChanges{
		AddedLines:   raw.added,
		RemovedLines: raw.removed,
	}
	if captureText {
		raw.fc.AddedByLine = raw.addedText
		raw.fc.RemovedByLine = raw.removedText
	} else {
		raw.fc.AddedByLine = nil
		raw.fc.RemovedByLine = nil
	}
	release := func() {
		clearBoolMap(raw.added)
		clearBoolMap(raw.removed)
		clearStringMap(raw.addedText)
		clearStringMap(raw.removedText)
		raw.fc = core.FileChanges{}
		lineMapPool.Put(raw)
	}
	return raw, release
}

func clearBoolMap(m map[int]bool) {
	for k := range m {
		delete(m, k)
	}
}

func clearStringMap(m map[int]string) {
	for k := range m {
		delete(m, k)
	}
}

// Lint evaluates the diff against directives in files.
func Lint(diffText string, opts Options) (Result, int) {
	ilog.Debug("engine: lint entry", "diff_bytes", len(diffText), "code_only", opts.CodeOnly, "parallelism", opts.Parallelism)
	if opts.CombinedDiffPolicy == CombinedDiffParent {
		if converted, ok := synthesizeCombinedDiff(diffText); ok {
			ilog.Debug("engine: combined diff converted using parent policy")
			diffText = converted
		}
	}
	return LintReader(strings.NewReader(diffText), opts)
}

func LintReader(r io.Reader, opts Options) (Result, int) {
	start := time.Now()
	files := opts.Files
	if files == nil {
		files = &workspaceFileProvider{root: "."}
	}
	combinedStatus := ""
	changes, releaseFuncs, err := parseChangedLinesReader(r, opts.CodeOnly, true)
	if err != nil {
		if errors.Is(err, diff.ErrCombinedDiff) {
			pol := strings.ToLower(strings.TrimSpace(opts.CombinedDiffPolicy))
			switch pol {
			case "skip_with_note", CombinedDiffWarn:
				ilog.Warn("engine: combined diff detected; skipping with note")
				combinedStatus = "skipped"
				changes = make(map[string]*core.FileChanges)
				releaseFuncs = nil
			case "ignore":
				ilog.Warn("engine: combined diff detected; ignoring diff")
				combinedStatus = "ignored"
				changes = make(map[string]*core.FileChanges)
				releaseFuncs = nil
			default:
				return Result{
					Findings: []core.Finding{
						finding("diff_parse_error", "", 0, fmt.Sprintf("failed to parse diff: %v", err)),
					},
					Suppressed: nil,
					Stats:      map[string]any{"pre_ms": 0},
				}, 1
			}
		} else {
			return Result{
				Findings: []core.Finding{
					finding("diff_parse_error", "", 0, fmt.Sprintf("failed to parse diff: %v", err)),
				},
				Suppressed: nil,
				Stats:      map[string]any{"pre_ms": 0},
			}, 1
		}
	}
	for _, path := range opts.StructuralFiles {
		path = filepath.Clean(path)
		if _, ok := changes[path]; !ok {
			changes[path] = &core.FileChanges{File: path, HasDirectiveHint: true}
		}
	}
	if opts.RevisionChanges != nil {
		changes = make(map[string]*core.FileChanges, len(opts.RevisionChanges))
		for path, change := range opts.RevisionChanges {
			changes[path] = change
		}
	}
	dependencyChange := func(path string) (*core.FileChanges, error) {
		if opts.DependencyChanges != nil && isRemotePath(path) {
			return opts.DependencyChanges(path)
		}
		return changes[path], nil
	}

	defer func() {
		for _, rel := range releaseFuncs {
			rel()
		}
	}()
	pre := time.Since(start)
	skipDirs := effectiveSkipDirs(opts.SkipDirs)
	skipSet := makeSkipSet(skipDirs)

	// prepare ignores (compiled)
	ign := compileIgnores(opts.Ignores)
	ignored := func(tr core.TargetRef) bool {
		name := tr.Path
		base := filepath.Base(name)
		for _, p := range ign {
			if p.Label != "" && tr.Label != p.Label {
				continue
			}
			if p.Rx.MatchString(name) || p.Rx.MatchString(base) {
				return true
			}
		}
		return false
	}

	// discover changed files we care about
	var selected map[string]bool
	if opts.SourceFiles != nil {
		selected = make(map[string]bool, len(opts.SourceFiles))
		for _, p := range opts.SourceFiles {
			selected[filepath.Clean(p)] = true
		}
	}
	var changed []string
	for f := range changes {
		if pathInSkippedDir(f, skipSet) {
			delete(changes, f)
			continue
		}
		if !matchAnyFile(f, ign) && (selected == nil || selected[f]) {
			changed = append(changed, f)
		}
	}
	reverseCandidates := opts.ReverseCandidates
	if opts.Files == nil && needsReverseValidation(changes) {
		var err error
		repository := opts.Repository
		var openErr error
		if repository == nil {
			repository, openErr = vcs.Open(context.Background(), ".", "auto")
		}
		if openErr == nil {
			reverseCandidates, err = repository.DirectiveFiles(context.Background(), core.CurrentDirectiveSyntax().PrefixDot)
		} else {
			reverseCandidates, err = scan.FindDirectiveFiles(".", core.CurrentDirectiveSyntax().PrefixDot, workerLimit(opts.Parallelism), skipDirs)
		}
		if err != nil {
			return Result{Findings: []core.Finding{errFinding(".", 1, err)}}, 1
		}
	}

	sort.Strings(changed)
	ilog.Debug("engine: changed files parsed", "count", len(changed))

	workerCount := workerLimit(opts.Parallelism)

	// parse directives with single-flight
	var mu sync.Mutex
	dcache := map[string][]core.LintDirective{}
	perr := map[string]error{}
	getDirs := func(path string) ([]core.LintDirective, error) {
		ilog.Debug("engine: loading directives", "file", path)
		mu.Lock()
		d, ok := dcache[path]
		e := perr[path]
		mu.Unlock()
		if ok || e != nil {
			if ok {
				ilog.Debug("engine: directives cache hit", "file", path)
			} else {
				ilog.Debug("engine: directives previously errored", "file", path, "error", e)
			}
			return d, e
		}
		provider, actual, err := fileProviderForPath(files, opts.Factories, path)
		if err != nil {
			return nil, &directiveReadError{err: err}
		}
		dirs, err := loadDirectives(provider, actual)
		mu.Lock()
		dcache[path] = dirs
		perr[path] = err
		mu.Unlock()
		if err != nil {
			ilog.Warn("engine: directive load failed", "file", path, "error", err)
		} else {
			ilog.Debug("engine: directives loaded", "file", path, "count", len(dirs))
		}
		return dirs, err
	}

	if len(reverseCandidates) > 0 {
		seen := make(map[string]bool, len(changed))
		for _, path := range changed {
			seen[path] = true
		}
		for _, candidate := range reverseCandidates {
			candidate = filepath.Clean(candidate)
			if seen[candidate] || pathInSkippedDir(candidate, skipSet) || matchAnyFile(candidate, ign) {
				continue
			}
			dirs, err := getDirs(candidate)
			if err != nil {
				if opts.RevisionChanges != nil {
					return Result{Findings: []core.Finding{errFinding(candidate, 1, err)}}, 1
				}
				continue
			} // Unrelated invalid files do not affect a local change.
			relevant := false
			for _, d := range dirs {
				if opts.RevisionChanges != nil && d.Kind == core.Unknown {
					relevant = true
				}
				for _, raw := range targetsOf(d) {
					target := resolveTarget(candidate, raw)
					fc, lookupErr := dependencyChange(target.Path)
					if lookupErr != nil && opts.RevisionChanges != nil {
						return Result{Findings: []core.Finding{errFinding(candidate, d.Line, lookupErr)}}, 1
					}
					if fc != nil && (fc.Deleted || fc.Renamed || fc.TypeChanged || fc.Opaque || hasRemovedDirective(fc)) {
						relevant = true
						break
					}
				}
				if relevant {
					break
				}
			}
			if relevant {
				changed = append(changed, candidate)
				seen[candidate] = true
			}
		}
		sort.Strings(changed)
	}

	prefetchList := make([]string, 0, len(changed))
	for _, f := range changed {
		if fc := changes[f]; fc != nil && fc.HasDirectiveHint {
			prefetchList = append(prefetchList, f)
		}
	}

	prefetched := preloadDirectivesWithGetter(prefetchList, workerCount, getDirs)
	if len(prefetchList) > 0 {
		ilog.Debug("engine: directives prefetched", "count", len(prefetchList))
	}

	pairs := make([]pairInfo, 0, len(changed)*2)
	var extraRules []conditionalRule
	suppressionDirs := make(map[string][]core.LintDirective)
	findings := make([]core.Finding, 0, len(changed))
	needsLabelInfo := make(map[string]bool)
	fileIgnores := make(map[string]map[string]struct{})
	suppressed := make([]core.Finding, 0)
	var emitMu sync.Mutex
	emit := func(f core.Finding) {
		emitMu.Lock()
		defer emitMu.Unlock()
		if shouldSuppress(fileIgnores, f) || directiveSuppressed(suppressionDirs[f.File], f) {
			f.Suppressed = true
			suppressed = append(suppressed, f)
		} else {
			findings = append(findings, f)
		}
	}

	presenceCache := map[string]bool{}

	for _, src := range changed {
		if fc := changes[src]; fc != nil && fc.Deleted {
			continue
		}
		if pathInSkippedDir(src, skipSet) {
			continue
		}
		dres, ok := prefetched[src]
		dirs, err := dres.dirs, dres.err
		if !ok {
			if fc := changes[src]; opts.RevisionChanges == nil && fc != nil && !fc.HasDirectiveHint {
				val, ok := presenceCache[src]
				if !ok {
					val = fileContainsDirective(files, opts.Factories, src)
					presenceCache[src] = val
				}
				if !val {
					continue
				}
			}
			dirs, err = getDirs(src)
		}
		if err == nil {
			collectIgnores(fileIgnores, src, dirs)
			suppressionDirs[src] = dirs
		}
		if err != nil {
			emit(errFinding(src, 1, err))
			continue
		}
		if change := changes[src]; change != nil && change.Opaque {
			for _, directive := range dirs {
				if directive.Kind == core.IfChange || directive.Kind == core.RequireAny || directive.Kind == core.RequireAll || directive.Kind == core.Forbid {
					emit(finding("change_evidence_error", src, directive.Line, "changed source has no complete line evidence for its dependency contracts"))
					break
				}
			}
			continue
		}
		// validate uniqueness & structure
		localFindings := make([]core.Finding, 0, len(dirs))
		if errs := validateUniqueness(dirs, src); len(errs) > 0 {
			localFindings = append(localFindings, errs...)
		}
		thenEstimate := 0
		for _, d := range dirs {
			if d.Kind == core.ThenChange {
				thenEstimate += len(targetsOf(d))
			}
		}
		localPairs := make([]pairInfo, 0, thenEstimate)

		// Nested contracts close in last-in-first-out order. Rules inside a contract
		// use that contract's content range; standalone rules use the whole source.
		type openBlock struct {
			directive core.LintDirective
			rules     []int
		}
		var stack []openBlock
		for _, d := range dirs {
			switch d.Kind {
			case core.Unknown:
				if f, ok := unknownFinding(src, d, opts.UnknownPolicy); ok {
					localFindings = append(localFindings, f)
				}
			case core.RequireAny, core.RequireAll, core.Forbid:
				r := conditionalRule{src: src, directive: d}
				if len(stack) > 0 {
					r.ifLine = stack[len(stack)-1].directive.Line
					stack[len(stack)-1].rules = append(stack[len(stack)-1].rules, len(extraRules))
				}
				extraRules = append(extraRules, r)
			case core.IfChange:
				stack = append(stack, openBlock{directive: d})
			case core.ThenChange:
				if len(stack) == 0 {
					localFindings = append(localFindings, finding("orphan_then", src, d.Line, fmt.Sprintf("ThenChange '%s' without preceding IfChange", oneOr(d))))
					continue
				}
				block := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if emptyGoogleBlock(block.directive, d) {
					localFindings = append(localFindings, finding("empty_then", src, d.Line, "empty LINT.ThenChange() on unlabeled IfChange has no effect"))
				}
				for _, index := range block.rules {
					extraRules[index].thenLine = d.Line
				}
				for _, raw := range targetsOf(d) {
					if invalidGoogleDriveTarget(raw) {
						localFindings = append(localFindings, finding("invalid_target_path", src, d.Line, "absolute filesystem drive paths are not supported for LINT targets"))
						continue
					}
					if opts.StrictPaths && core.CurrentDirectiveSyntax().Prefix == "LINT" && !(opts.DependencyChanges != nil && isRemotePath(raw)) && !strings.HasPrefix(raw, "//") && !strings.HasPrefix(raw, ":") && !strings.HasPrefix(raw, "#") {
						localFindings = append(localFindings, finding("invalid_target_path", src, d.Line, "strict LINT targets must start with // or use a same-file label selector"))
						continue
					}
					tr := resolveTarget(src, raw)
					if pathInSkippedDir(tr.Path, skipSet) {
						continue
					}
					localPairs = append(localPairs, pairInfo{src: src, ifLine: block.directive.Line, thenLine: d.Line, ifLabel: block.directive.Label, target: tr})
				}
			}
		}
		for _, block := range stack {
			localFindings = append(localFindings, finding("orphan_if", src, block.directive.Line, msgIf(block.directive.Label)))
		}

		pairs = append(pairs, localPairs...)
		for _, lf := range localFindings {
			emit(lf)
		}
	}

	// Build label ranges for unique targets
	labelRanges := map[string]map[string]core.LineRange{}
	ambiguousLabels := map[string]map[string]bool{}
	uniq := map[string]struct{}{}
	pairedTargets := map[string]bool{}
	for _, p := range pairs {
		if ignored(p.target) {
			continue
		}
		uniq[p.target.Path] = struct{}{}
		pairedTargets[p.target.Path] = true
		if p.target.Label != "" {
			needsLabelInfo[p.target.Path] = true
		}
	}
	for _, rule := range extraRules {
		for _, raw := range targetsOf(rule.directive) {
			tr := resolveTarget(rule.src, raw)
			if ignored(tr) {
				continue
			}
			uniq[tr.Path] = struct{}{}
			if tr.Label != "" {
				needsLabelInfo[tr.Path] = true
			}
		}
	}

	diagnosedTargetErrors := make(map[string]bool)
	for _, f := range findings {
		if f.RuleID == "error" {
			diagnosedTargetErrors[f.File] = true
		}
	}
	for _, f := range suppressed {
		if f.RuleID == "error" {
			diagnosedTargetErrors[f.File] = true
		}
	}
	for tf := range uniq {
		if !needsLabelInfo[tf] {
			continue
		}
		if pathInSkippedDir(tf, skipSet) {
			continue
		}
		dirs, err := getDirs(tf)
		if err != nil {
			var readErr *directiveReadError
			if (!errors.As(err, &readErr) || !pairedTargets[tf]) && !diagnosedTargetErrors[tf] {
				emit(errFinding(tf, 1, err))
				diagnosedTargetErrors[tf] = true
			}
			continue
		}
		collectIgnores(fileIgnores, tf, dirs)
		_, alreadyValidated := suppressionDirs[tf]
		suppressionDirs[tf] = dirs
		targetFindings, _ := validateTargetDirectives(tf, dirs, opts.UnknownPolicy)
		if !alreadyValidated {
			for _, f := range targetFindings {
				emit(f)
			}
		}
		labelRanges[tf] = computeLabelRanges(dirs)
		counts := make(map[string]int)
		for _, d := range dirs {
			label := ""
			if d.Kind == core.IfChange {
				label = d.Label
			} else if d.Kind == core.Label {
				label = d.Name
			}
			if label != "" {
				counts[label]++
				if counts[label] > 1 {
					if ambiguousLabels[tf] == nil {
						ambiguousLabels[tf] = make(map[string]bool)
					}
					ambiguousLabels[tf][label] = true
				}
			}
		}
	}

	// Evaluate rules (ThenChange, RequireAny/All, Forbid) grouped by target
	targetGroups := make(map[string][]pairInfo, len(pairs))
	for _, p := range pairs {
		if ignored(p.target) || pathInSkippedDir(p.target.Path, skipSet) {
			continue
		}
		targetGroups[p.target.Path] = append(targetGroups[p.target.Path], p)
	}

	if len(targetGroups) > 0 {
		workers := opts.Parallelism
		if workers <= 0 {
			workers = core.DefaultParallelism
			if workers < 1 {
				workers = 1
			}
		}
		if workers > len(targetGroups) {
			workers = len(targetGroups)
		}
		type job struct {
			path  string
			pairs []pairInfo
		}
		jobs := make(chan job, workers)
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				local := make([]core.Finding, 0, 2)
				for job := range jobs {
					targetChanges, changeErr := dependencyChange(job.path)
					labelMap := labelRanges[job.path]
					for _, p := range job.pairs {
						local = local[:0]
						if changeErr != nil {
							f := finding("change_evidence_error", p.src, p.thenLine, changeErr.Error())
							f.TargetPath, f.TargetLabel = p.target.Path, p.target.Label
							emit(f)
							continue
						}
						if ambiguousLabels[job.path][p.target.Label] {
							f := finding("label_ambiguous", p.src, p.thenLine, fmt.Sprintf("label '%s' is ambiguous in '%s' because it is defined more than once", p.target.Label, p.target.Path))
							f.TargetPath, f.TargetLabel = p.target.Path, p.target.Label
							emit(f)
							continue
						}
						if evalThenChange(p, changes[p.src], targetChanges, labelMap, files, opts.Factories, opts.CodeOnly, &local, opts.SuppressCoChanges || (selected != nil && !selected[p.src])) {
							continue
						}
						for _, f := range local {
							emit(f)
						}
					}
				}
			}()
		}
		for path, group := range targetGroups {
			jobs <- job{path: path, pairs: group}
		}
		close(jobs)
		wg.Wait()
	}

	for _, rule := range extraRules {
		if opts.SuppressCoChanges || (selected != nil && !selected[rule.src]) {
			continue
		}
		evaluateConditionalRule(rule, changes, labelRanges, files, opts.Factories, opts.CodeOnly, ignored, emit, dependencyChange)
	}
	sortFindings(findings)
	sortFindings(suppressed)

	// stats
	stats := map[string]any{
		"pre_ms": pre.Milliseconds(),
		"files":  len(changed),
	}
	if combinedStatus != "" {
		stats["combined_diff"] = combinedStatus
	}
	if len(suppressed) > 0 {
		stats["suppressed"] = len(suppressed)
	}
	if remote := githubCacheSnapshot(); remote != nil {
		stats["remote_cache"] = remote
	}
	if opts.Fix {
		actions, fixErrs := applyFixes(findings)
		if len(actions) > 0 {
			stats["fix_applied"] = actions
		}
		if len(fixErrs) > 0 {
			stats["fix_errors"] = fixErrs
		}
	}
	return Result{Findings: findings, Suppressed: suppressed, Stats: stats}, exitCode(findings)
}

// ——— helpers ———

func compileIgnores(list []string) []core.IgnorePattern {
	var out []core.IgnorePattern
	for _, raw := range list {
		name, label := raw, ""
		if i := strings.IndexByte(raw, '#'); i >= 0 {
			name, label = raw[:i], raw[i+1:]
		}
		out = append(out, core.IgnorePattern{TargetName: name, Label: label, Rx: core.CompileGlob(name)})
	}
	return out
}

// Directives are memoized within each lint invocation. Reading current content
// avoids stale stat-only cache hits and unbounded background cache state in watch.
func loadDirectives(provider FileProvider, actualPath string) ([]core.LintDirective, error) {
	pf := parse.Provider{ReadFile: func(string) ([]byte, error) {
		data, err := provider.ReadFile(actualPath)
		if err != nil {
			return nil, &directiveReadError{err: err}
		}
		return data, nil
	}}
	return pf.Parse(actualPath)
}

func fileContainsDirective(files FileProvider, factories []FileProviderFactory, path string) bool {
	syn := core.CurrentDirectiveSyntax()
	provider, actual, err := fileProviderForPath(files, factories, path)
	if err != nil {
		return false
	}
	data, err := provider.ReadFile(actual)
	if err != nil {
		return false
	}
	return bytes.Contains(data, []byte(syn.PrefixDot))
}

func fileProviderForPath(base FileProvider, factories []FileProviderFactory, path string) (FileProvider, string, error) {
	provider := base
	if provider == nil {
		provider = localFileProvider{}
	}
	if len(factories) == 0 && !isRemotePath(path) {
		return provider, path, nil
	}
	prov, actual, err := selectFileProvider(provider, factories, path)
	if err != nil {
		return nil, "", err
	}
	if prov == nil {
		prov = provider
	}
	if actual == "" {
		actual = path
	}
	return prov, actual, nil
}

func selectFileProvider(base FileProvider, factories []FileProviderFactory, target string) (FileProvider, string, error) {
	for _, factory := range factories {
		if factory == nil {
			continue
		}
		if factory.Match(target) {
			prov, actual, err := factory.Provider(target)
			if err != nil {
				return nil, "", err
			}
			if prov != nil {
				if actual == "" {
					actual = target
				}
				return prov, actual, nil
			}
		}
	}
	if isRemotePath(target) {
		return nil, "", fmt.Errorf("%w: %s", errUnsupportedRemoteTarget, target)
	}
	return base, target, nil
}

func isRemotePath(path string) bool {
	return strings.Contains(path, "://")
}

func matchAnyFile(path string, pats []core.IgnorePattern) bool {
	base := filepath.Base(path)
	for _, p := range pats {
		if p.Label != "" {
			continue
		}
		if p.Rx.MatchString(path) || p.Rx.MatchString(base) {
			return true
		}
	}
	return false
}

func collectIgnores(store map[string]map[string]struct{}, file string, dirs []core.LintDirective) {
	if len(dirs) == 0 {
		return
	}
	for _, d := range dirs {
		if d.Kind != core.Ignore {
			continue
		}
		rule := strings.TrimSpace(strings.ToLower(d.Label))
		if rule == "" {
			continue
		}
		if store[file] == nil {
			store[file] = make(map[string]struct{})
		}
		store[file][rule] = struct{}{}
	}
}

func shouldSuppress(store map[string]map[string]struct{}, f core.Finding) bool {
	if len(store) == 0 {
		return false
	}
	rules := store[f.File]
	if len(rules) == 0 {
		return false
	}
	rule := strings.ToLower(strings.TrimSpace(f.RuleID))
	if _, ok := rules["all"]; ok {
		return true
	}
	if _, ok := rules[rule]; ok {
		return true
	}
	if _, ok := rules["*"]; ok {
		return true
	}
	return false
}

func effectiveSkipDirs(user []string) []string {
	if user == nil {
		return append([]string{}, scan.DefaultSkippedDirs...)
	}
	out := make([]string, 0, len(user))
	for _, dir := range user {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		out = append(out, dir)
	}
	return out
}

func makeSkipSet(skip []string) map[string]struct{} {
	if len(skip) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(skip))
	sep := string(filepath.Separator)
	for _, raw := range skip {
		if raw == "" {
			continue
		}
		clean := filepath.Clean(raw)
		parts := strings.Split(clean, sep)
		if len(parts) == 0 {
			continue
		}
		name := parts[len(parts)-1]
		if name == "." || name == string(filepath.Separator) {
			continue
		}
		set[name] = struct{}{}
	}
	return set
}

func pathInSkippedDir(path string, skip map[string]struct{}) bool {
	if len(skip) == 0 {
		return false
	}
	if isRemotePath(path) {
		return false
	}
	cur := filepath.Clean(path)
	for cur != "." && cur != string(filepath.Separator) && cur != "" {
		if _, ok := skip[filepath.Base(cur)]; ok {
			return true
		}
		next := filepath.Dir(cur)
		if next == cur {
			break
		}
		cur = next
	}
	return false
}

// ResolveTarget resolves a selector using the configured directive grammar.
// Workspace-root targets remain relative so callers can apply their own root.
func ResolveTarget(src, raw string) core.TargetRef {
	return resolveTarget(src, raw)
}

func resolveTarget(src, raw string) core.TargetRef {
	name := raw
	lbl := ""
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		name, lbl = raw[:i], raw[i+1:]
	} else if core.CurrentDirectiveSyntax().Prefix == "LINT" && !isRemotePath(raw) {
		if i := strings.LastIndexByte(raw, ':'); i > strings.LastIndexAny(raw, `/\`) && !(i == 1 && len(raw) > 2 && (raw[2] == '/' || raw[2] == '\\')) {
			name, lbl = raw[:i], raw[i+1:]
		}
	}
	googleLocal := core.CurrentDirectiveSyntax().Prefix == "LINT" && !isRemotePath(name)
	driveAbsolute := invalidGoogleDriveTarget(name)
	normalized := strings.ReplaceAll(name, `\`, "/")
	explicitRelative := strings.HasPrefix(normalized, "./") || strings.HasPrefix(normalized, "../")
	rootRelative := googleLocal && !driveAbsolute && (strings.HasPrefix(name, "/") || (strings.ContainsAny(name, `/\`) && !explicitRelative && !filepath.IsAbs(name)))
	if rootRelative {
		if strings.HasPrefix(name, "//") {
			name = strings.TrimPrefix(name, "//")
		} else {
			name = strings.TrimPrefix(name, "/")
		}
	}
	if googleLocal && !driveAbsolute {
		name = filepath.FromSlash(strings.ReplaceAll(name, `\`, "/"))
	}
	sameFile := name == ""
	if sameFile {
		name = src
	}
	if !sameFile && !rootRelative && !driveAbsolute && !isRemotePath(name) && !filepath.IsAbs(name) {
		name = filepath.Join(filepath.Dir(src), name)
	}
	if !isRemotePath(name) {
		name = filepath.Clean(name)
	}
	return core.TargetRef{Raw: raw, Path: name, Label: lbl}
}

func oneOr(d core.LintDirective) string {
	if d.Target != "" {
		return d.Target
	}
	if len(d.List) > 0 {
		return d.List[0]
	}
	return ""
}

func targetsOf(d core.LintDirective) []string {
	if d.Target != "" {
		return []string{d.Target}
	}
	return append([]string{}, d.List...)
}

func errFinding(path string, line int, err error) core.Finding {
	return finding("error", path, line, err.Error())
}

func finding(rule, file string, line int, msg string) core.Finding {
	return core.Finding{RuleID: rule, Severity: "error", File: file, Line: line, Message: msg}
}

func msgIf(lbl string) string {
	if lbl == "" {
		return "missing ThenChange after IfChange"
	}
	return fmt.Sprintf("missing ThenChange after IfChange('%s')", lbl)
}

func exitCode(fs []core.Finding) int {
	for _, f := range fs {
		if f.Severity == "error" {
			return 1
		}
	}
	return 0
}

func synthesizeCombinedDiff(diff string) (string, bool) {
	if !strings.Contains(diff, "diff --cc ") {
		return "", false
	}
	lines := strings.Split(diff, "\n")
	var out []string
	i := 0
	for i < len(lines) {
		line := lines[i]
		if strings.HasPrefix(line, "diff --cc ") {
			block, consumed, ok := convertCombinedBlock(lines[i:])
			if !ok {
				return "", false
			}
			out = append(out, block...)
			i += consumed
			continue
		}
		out = append(out, line)
		i++
	}
	return strings.Join(out, "\n"), true
}

func convertCombinedBlock(lines []string) ([]string, int, bool) {
	if len(lines) == 0 {
		return nil, 0, false
	}
	header := lines[0]
	path := strings.TrimSpace(strings.TrimPrefix(header, "diff --cc"))
	block := []string{fmt.Sprintf("diff --git a/%[1]s b/%[1]s", path)}
	parents := 0
	i := 1
	for i < len(lines) {
		line := lines[i]
		if strings.HasPrefix(line, "diff --") && !strings.HasPrefix(line, "diff --cc ") {
			break
		}
		switch {
		case strings.HasPrefix(line, "index "):
			info := strings.TrimPrefix(line, "index ")
			sep := strings.LastIndex(info, "..")
			if sep == -1 {
				return nil, 0, false
			}
			left := info[:sep]
			right := info[sep+2:]
			parts := strings.Split(left, ",")
			if len(parts) == 0 {
				return nil, 0, false
			}
			parents = len(parts)
			block = append(block, fmt.Sprintf("index %s..%s", parts[0], right))
		case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "):
			block = append(block, line)
		case strings.HasPrefix(line, "@@@"):
			converted := convertCombinedHeader(line)
			if converted == "" {
				return nil, 0, false
			}
			block = append(block, converted)
		default:
			if parents > 0 && hasCombinedPrefix(line) {
				converted := convertCombinedContent(line, parents)
				block = append(block, converted...)
			} else {
				block = append(block, line)
			}
		}
		i++
	}
	return block, i, true
}

func hasCombinedPrefix(line string) bool {
	if line == "" {
		return false
	}
	ch := line[0]
	return ch == ' ' || ch == '+' || ch == '-'
}

func convertCombinedHeader(line string) string {
	start := strings.Index(line, "@@@")
	end := strings.LastIndex(line, "@@@")
	if start == -1 || end == -1 || end <= start {
		return ""
	}
	body := strings.TrimSpace(line[start+3 : end])
	parts := strings.Fields(body)
	if len(parts) < 2 {
		return ""
	}
	first := parts[0]
	last := parts[len(parts)-1]
	return fmt.Sprintf("@@ %s %s @@", first, last)
}

func convertCombinedContent(line string, parents int) []string {
	if len(line) < parents+1 {
		return []string{line}
	}
	if strings.HasPrefix(line, "\\") {
		return []string{line}
	}
	prefix := line[:parents+1]
	body := strings.TrimLeft(line[parents+1:], "\t")
	if body == "" {
		body = line[parents:]
	}
	p1 := rune(prefix[0])
	res := rune(prefix[parents])
	var out []string
	if p1 == '-' {
		out = append(out, "-"+body)
	}
	if res == '+' {
		out = append(out, "+"+body)
	}
	if len(out) == 0 {
		out = append(out, " "+body)
	}
	return out
}

// Referenced files need structural validation even when absent from the diff.
// Unknown directive policy controls diagnostics; ambiguous contract ranges are
// withheld so duplicate or incomplete labels cannot satisfy a dependency.
func validateTargetDirectives(path string, dirs []core.LintDirective, policy string) ([]core.Finding, bool) {
	findings := validateUniqueness(dirs, path)
	valid := len(findings) == 0
	var stack []core.LintDirective
	for _, d := range dirs {
		switch d.Kind {
		case core.Unknown:
			if f, ok := unknownFinding(path, d, policy); ok {
				findings = append(findings, f)
			}
		case core.IfChange:
			stack = append(stack, d)
		case core.ThenChange:
			for _, raw := range targetsOf(d) {
				if invalidGoogleDriveTarget(raw) {
					findings = append(findings, finding("invalid_target_path", path, d.Line, "absolute filesystem drive paths are not supported for LINT targets"))
					valid = false
				}
			}
			if len(stack) == 0 {
				findings = append(findings, finding("orphan_then", path, d.Line, fmt.Sprintf("ThenChange '%s' without preceding IfChange", oneOr(d))))
				valid = false
			} else {
				if emptyGoogleBlock(stack[len(stack)-1], d) {
					findings = append(findings, finding("empty_then", path, d.Line, "empty LINT.ThenChange() on unlabeled IfChange has no effect"))
					valid = false
				}
				stack = stack[:len(stack)-1]
			}
		}
	}
	for _, d := range stack {
		findings = append(findings, finding("orphan_if", path, d.Line, msgIf(d.Label)))
		valid = false
	}
	return findings, valid
}

func computeLabelRanges(dirs []core.LintDirective) map[string]core.LineRange {
	ranges := make(map[string]core.LineRange)
	counts := make(map[string]int)
	var labels []core.LintDirective
	var pairs []core.LintDirective
	for _, d := range dirs {
		switch d.Kind {
		case core.Label:
			counts[d.Name]++
			labels = append(labels, d)
		case core.EndLabel:
			if len(labels) > 0 {
				start := labels[len(labels)-1]
				labels = labels[:len(labels)-1]
				ranges[start.Name] = core.LineRange{StartLine: start.Line + 1, EndLine: d.Line - 1}
			}
		case core.IfChange:
			if d.Label != "" {
				counts[d.Label]++
			}
			pairs = append(pairs, d)
		case core.ThenChange:
			if len(pairs) > 0 {
				start := pairs[len(pairs)-1]
				pairs = pairs[:len(pairs)-1]
				if start.Label != "" {
					ranges[start.Label] = core.LineRange{StartLine: start.Line + 1, EndLine: d.Line - 1}
				}
			}
		}
	}
	for label, count := range counts {
		if count > 1 {
			delete(ranges, label)
		}
	}
	return ranges
}

// LabelRanges returns the label line ranges for the given file by parsing directives.
func LabelRanges(path string) (map[string]core.LineRange, error) {
	pf := parse.Provider{}
	dirs, err := pf.Parse(path)
	if err != nil {
		return nil, err
	}
	return computeLabelRanges(dirs), nil
}

func preloadDirectivesWithGetter(files []string, workers int, getter func(string) ([]core.LintDirective, error)) map[string]directiveResult {
	out := make(map[string]directiveResult, len(files))
	if len(files) == 0 {
		return out
	}
	if workers <= 0 {
		workers = 1
	}
	if workers > len(files) {
		workers = len(files)
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var next atomic.Uint64
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				index := next.Add(1) - 1
				if index >= uint64(len(files)) {
					return
				}
				file := files[index]
				dirs, err := getter(file)
				mu.Lock()
				out[file] = directiveResult{dirs: dirs, err: err}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return out
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Validate duplicates (IfChange labels & Label names unique per file)
func validateUniqueness(dirs []core.LintDirective, file string) []core.Finding {
	seen := map[string]struct{}{}
	var out []core.Finding
	for _, d := range dirs {
		if d.Kind == core.IfChange && d.Label != "" {
			if _, ok := seen[d.Label]; ok {
				out = append(out, finding("duplicate_label", file, d.Line, fmt.Sprintf("duplicate directive label '%s'", d.Label)))
			} else {
				seen[d.Label] = struct{}{}
			}
		}
		if d.Kind == core.Label {
			if _, ok := seen[d.Name]; ok {
				out = append(out, finding("duplicate_label", file, d.Line, fmt.Sprintf("duplicate directive label '%s'", d.Name)))
			} else {
				seen[d.Name] = struct{}{}
			}
		}
	}
	return out
}

// Evaluate ThenChange with optional label region
func evalThenChange(p pairInfo, src, tgt *core.FileChanges, labelMap map[string]core.LineRange, files FileProvider, factories []FileProviderFactory, codeOnly bool, out *[]core.Finding, suppress ...bool) bool {
	targetFinding := func(rule, message string) core.Finding {
		f := finding(rule, p.src, p.thenLine, message)
		f.TargetPath = p.target.Path
		f.TargetLabel = p.target.Label
		return f
	}
	provider, actual, err := fileProviderForPath(files, factories, p.target.Path)
	if err == nil {
		_, err = provider.ReadFile(actual)
	}
	if err != nil {
		if errors.Is(err, errUnsupportedRemoteTarget) {
			*out = append(*out, targetFinding("invalid_target_path", err.Error()))
			return false
		}
		*out = append(*out, targetFinding("then_missing", fmt.Sprintf("expected changes in '%s' but target cannot be read: %v", p.target.Path, err)))
		return false
	}
	if p.target.Label != "" {
		if _, ok := labelMap[p.target.Label]; !ok {
			*out = append(*out, targetFinding("label_missing", fmt.Sprintf("label '%s' not found in '%s'", p.target.Label, p.target.Path)))
			return false
		}
	}
	if (len(suppress) > 0 && suppress[0]) || !pairTriggered(p, src, codeOnly) {
		return true
	}
	if tgt != nil && tgt.Opaque && p.target.Label != "" {
		*out = append(*out, targetFinding("change_evidence_error", "changed target has no complete line evidence for its labelled region"))
		return false
	}
	if tgt != nil && !tgt.Deleted {
		if p.target.Label == "" && (tgt.ContentChanged || len(tgt.AddedLines)+len(tgt.RemovedLines) > 0) {
			return true
		}
		if p.target.Label != "" {
			r := labelMap[p.target.Label]
			if blockChanged(tgt, r.StartLine, r.EndLine) {
				return true
			}
		}
	}
	if p.target.Label != "" {
		r := labelMap[p.target.Label]
		*out = append(*out, targetFinding("then_label_missing", fmt.Sprintf("expected changes in '%s#%s' (%d-%d) but none found", p.target.Path, p.target.Label, r.StartLine, r.EndLine)))
	} else {
		*out = append(*out, targetFinding("then_missing", fmt.Sprintf("expected changes in '%s' but none found", p.target.Path)))
	}
	return false
}

func pairTriggered(p pairInfo, src *core.FileChanges, codeOnly bool) bool {
	if src == nil || src.Deleted || p.thenLine <= p.ifLine {
		return false
	}
	if src.AddedLines[p.ifLine] && src.AddedLines[p.thenLine] {
		return false
	}
	return blockChanged(src, p.ifLine+1, p.thenLine-1) && (!codeOnly || !onlyCommentChanges(src, p.ifLine+1, p.thenLine-1))
}

func onlyCommentChanges(fc *core.FileChanges, start, end int) bool {
	if fc == nil {
		return true
	}

	for ln := range fc.AddedLines {
		if ln >= start && ln <= end && (fc.AddedByLine == nil || !isCommentOrWhitespace(fc.AddedByLine[ln])) {
			return false
		}
	}
	if fc.RemovedInNew == nil {
		for ln := range fc.RemovedLines {
			if ln >= start && ln <= end && (fc.RemovedByLine == nil || !isCommentOrWhitespace(fc.RemovedByLine[ln])) {
				return false
			}
		}
	}

	for line, text := range fc.RemovedInNew {
		if line >= start && line <= end+1 {
			for _, removed := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
				if !isCommentOrWhitespace(removed) {
					return false
				}
			}
		}
	}
	return true
}

func isCommentOrWhitespace(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true
	}
	prefixes := []string{"//", "#", "/*", "*/", "--", "\"\"\"", "'''"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

func blockChanged(fc *core.FileChanges, start, end int) bool {
	if fc == nil {
		return false
	}
	for line := range fc.AddedLines {
		if line >= start && line <= end {
			return true
		}
	}
	if fc.RemovedInNew != nil {
		for line, text := range fc.RemovedInNew {
			if line >= start && line <= end+1 {
				for _, removed := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
					if !isDirectiveMetadataLine(removed) {
						return true
					}
				}
			}
		}
	} else {
		for line := range fc.RemovedLines {
			if line >= start && line <= end {
				return true
			}
		}
	}
	return false
}

// ValidateFiles checks every supplied file's directive structure and targets without
// requiring a diff or treating existing bodies as changed.
func ValidateFiles(paths []string, opts Options) (Result, int) {
	opts.StructuralFiles = paths
	return Lint("", opts)
}

func isDirectiveMetadataLine(text string) bool {
	text = strings.TrimSpace(text)
	for _, prefix := range []string{"//", "#", "--", "/*", "*", "<!--"} {
		if strings.HasPrefix(text, prefix) {
			return strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(text, prefix)), core.CurrentDirectiveSyntax().PrefixDot)
		}
	}
	return false
}
