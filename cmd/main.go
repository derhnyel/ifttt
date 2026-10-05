package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	core "github.com/derhnyel/ifttt/internal"
	"github.com/derhnyel/ifttt/internal/comments"
	"github.com/derhnyel/ifttt/internal/config"
	eng "github.com/derhnyel/ifttt/internal/engine"
	ilog "github.com/derhnyel/ifttt/internal/log"
	"github.com/derhnyel/ifttt/internal/out"
	"github.com/derhnyel/ifttt/internal/parse"
	"github.com/derhnyel/ifttt/internal/scan"
	"github.com/derhnyel/ifttt/internal/vcs"
)

type ruleInfo struct {
	Summary    string
	Severity   string
	Resolution string
}

var ruleCatalog = map[string]ruleInfo{
	"change_evidence_error": {
		Summary:    "The selected change set cannot provide consistent dependency evidence.",
		Resolution: "Declare the target repository and select an available base/head range matching any explicit target ref.",
	},
	"then_missing": {
		Summary:    "The source file changed inside an IfChange block but the referenced target file had no corresponding edits.",
		Severity:   "error",
		Resolution: "Edit the target file (or the labelled region) to reflect the source change, or update the ThenChange path/label.",
	},
	"then_label_missing": {
		Summary:    "The source file changed but the required labelled region in the target file did not change.",
		Severity:   "error",
		Resolution: "Modify lines inside the labelled range or adjust the label to point at the correct block.",
	},
	"label_missing": {
		Summary:    "A ThenChange directive referenced a label that does not exist in the target file.",
		Severity:   "error",
		Resolution: "Add a matching Label/EndLabel pair in the target file or fix the label name in ThenChange.",
	},
	"label_ambiguous": {
		Summary:    "The target label is defined more than once, so its contract region cannot be selected safely.",
		Severity:   "error",
		Resolution: "Rename the duplicate labels and update their references so each target label identifies one region.",
	},
	"orphan_then": {
		Summary:    "Found a ThenChange directive without a preceding IfChange in the same file.",
		Severity:   "error",
		Resolution: "Add an IfChange just before the ThenChange or remove the orphan directive.",
	},
	"empty_then": {
		Summary:    "An unlabeled Google-style IfChange ends with an empty ThenChange and has no dependency to enforce.",
		Severity:   "error",
		Resolution: "Add a target to ThenChange, name the IfChange block so other directives can reference it, or remove the empty block.",
	},
	"invalid_target_path": {
		Summary:    "The target violates the selected path or remote-provider policy.",
		Severity:   "error",
		Resolution: "Use a workspace target accepted by the selected path policy, or configure a matching remote provider. Filesystem drive paths are unsupported.",
	},
	"orphan_if": {
		Summary:    "An IfChange directive was not followed by a ThenChange before the next directive or EOF.",
		Severity:   "error",
		Resolution: "Add the missing ThenChange directive or remove the dangling IfChange.",
	},
	"duplicate_label": {
		Summary:    "Two directives share the same label within a single file.",
		Severity:   "error",
		Resolution: "Rename one of the labels so each IfChange/Label is unique per file.",
	},
	"diff_parse_error": {
		Summary:    "The diff input could not be parsed.",
		Severity:   "error",
		Resolution: "Regenerate the diff (e.g. `git diff`) and retry.",
	},
	"error": {
		Summary:    "The linter hit an unexpected error while parsing directives.",
		Severity:   "error",
		Resolution: "Inspect the file noted in the message and fix the directive syntax.",
	},
}

var (
	commentStyleMu         sync.RWMutex
	commentPrefixOverrides = map[string]string{}
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "jump":
			if err := runJump(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "iflint:", err)
				os.Exit(1)
			}
			return
		case "watch":
			if err := runWatch(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "iflint watch:", err)
				os.Exit(1)
			}
			return
		case "review":
			if err := runReview(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "iflint review:", err)
				os.Exit(1)
			}
			return
		case "blame":
			if err := runBlame(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "iflint blame:", err)
				os.Exit(1)
			}
			return
		case "scaffold":
			if err := runScaffold(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "iflint scaffold:", err)
				os.Exit(1)
			}
			return
		case "ignore":
			if len(os.Args) > 2 && os.Args[2] == "add" {
				if err := runIgnoreAdd(os.Args[3:]); err != nil {
					fmt.Fprintln(os.Stderr, "iflint ignore add:", err)
					os.Exit(1)
				}
				return
			}
			fmt.Fprintln(os.Stderr, "iflint ignore: expected 'add' subcommand")
			os.Exit(1)
		case "explain":
			if err := runExplainCommand(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "iflint explain:", err)
				os.Exit(1)
			}
			return
		}
	}
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
		warn        = flag.Bool("w", false, "warn mode (exit 0 on findings)")
		verbose     = flag.Bool("v", false, "verbose")
		format      = flag.String("format", "", "output: text|json|sarif")
		logLevel    = flag.String("log-level", "", "log level (debug|info|warn|error)")
		ignore      multiFlag
		skipDir     multiFlag
		remote      multiFlag
		cStyles     multiFlag
		listSupp    = flag.Bool("list-suppressed", false, "print only suppressed findings and exit 0")
		parallel    = flag.Int("p", -1, "parallelism; -1 auto")
		scan        = flag.String("scan", "", "scan directory for directive validation")
		explain     = flag.String("explain", "", "print metadata for a rule and exit")
		doctor      = flag.Bool("doctor", false, "scan mode: report orphan directives and exit")
		codeOnly    = flag.Bool("code-only", false, "ignore whitespace/comment-only changes in triggers")
		fix         = flag.Bool("fix", false, "attempt to insert placeholder directives for missing target changes")
		combined    = flag.String("combined", "", "combined diff handling: strict|warn|ignore|parent")
		vcsKind     = flag.String("vcs", "", "VCS backend: auto|git|jj (explicit selection enables native working diff)")
		revision    = flag.String("diff", "", "native VCS revision range or jj revset")
		strictPaths = flag.Bool("strict", false, "require // root-relative Google LINT target paths")
		staged      = flag.Bool("staged", false, "lint staged Git changes")
		varFiles    multiFlag
		statsOut    = flag.Bool("stats", false, "print execution statistics (JSON) to stderr")
		changeSet   = flag.String("change-set", "", "validate cross-repository changes from a revision manifest")
	)
	flag.StringVar(revision, "d", "", "native revision (alias of --diff)")
	flag.BoolVar(warn, "warn", false, "warn mode (alias of -w)")
	flag.BoolVar(verbose, "verbose", false, "verbose logging (alias of -v)")
	flag.StringVar(format, "f", "", "output format (alias of --format)")
	flag.Var(&ignore, "i", "ignore pattern (alias of --ignore)")
	flag.IntVar(parallel, "parallel", -1, "worker threads (alias of -p)")
	flag.IntVar(parallel, "threads", -1, "worker threads; 0 automatic (alias of -p)")
	flag.IntVar(parallel, "t", -1, "worker threads (alias of --threads)")
	flag.Var(&ignore, "ignore", "ignore file or file#label (repeatable)")
	flag.Var(&skipDir, "skip-dir", "skip directory during scan and lint (repeatable, defaults: .git,.hg,.svn)")
	flag.Var(&remote, "remote", "add remote repo (repeatable). Format: repo=owner/name[,default_ref=main][,token_env=ENV][,base_url=URL]")
	flag.Var(&cStyles, "comment-style", "override comment prefix for extensions (repeatable). Format: .ext=// or .conf=hash")
	flag.Var(&varFiles, "files", "structural source file or glob (repeatable)")
	if err := flag.CommandLine.Parse(reorderFlagArguments(flag.CommandLine, os.Args[1:])); err != nil {
		fatalInput(err)
	}
	if *showVersion {
		fmt.Printf("iflint %s (commit %s)\n", version, commit)
		return
	}
	explicitFlags := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { explicitFlags[f.Name] = true })

	cfg, cfgErr := config.Load(".")
	if cfgErr != nil && !errors.Is(cfgErr, fs.ErrNotExist) {
		fmt.Fprintln(os.Stderr, "iflint:", cfgErr)
		os.Exit(2)
	}

	if *format != "" {
		cfg.Output.Format = canonicalOutputFormat(*format)
	}
	if explicitFlags["v"] || explicitFlags["verbose"] {
		cfg.Verbose = *verbose
	}
	if explicitFlags["code-only"] {
		cfg.Rules.CodeOnly = *codeOnly
	}
	if len(ignore) > 0 {
		cfg.Ignores = append([]string{}, ignore...)
	}
	if len(skipDir) > 0 {
		cfg.SkipDirs = append([]string{}, skipDir...)
	}
	if len(remote) > 0 {
		for _, raw := range remote {
			item, err := parseRemoteFlagValue(raw)
			if err != nil {
				fmt.Fprintf(os.Stderr, "invalid --remote value %q: %v\n", raw, err)
				os.Exit(2)
			}
			cfg.Remotes = append(cfg.Remotes, item)
		}
	}
	if err := config.Validate(&cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(cStyles) > 0 {
		if err := applyCommentStyleOverrides(cStyles); err != nil {
			fmt.Fprintln(os.Stderr, "iflint:", err)
			os.Exit(2)
		}
	}
	level := slog.LevelInfo
	if *logLevel != "" {
		parsed, err := ilog.ParseLevel(*logLevel)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid log level: %v\n", err)
			os.Exit(2)
		}
		level = parsed
	} else if cfg.Verbose {
		level = slog.LevelDebug
	}
	ilog.SetLevel(level)
	ilog.Debug("log level configured", "level", level.String())
	core.SetDirectivePrefix(cfg.Directives.Prefix)
	comments.SetPythonDocstrings(cfg.PythonDocstringsEnabled())
	factories, err := buildFactories(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "iflint:", err)
		os.Exit(2)
	}

	if explicitFlags["change-set"] {
		if strings.TrimSpace(*changeSet) == "" {
			fatalInput(errors.New("--change-set requires a manifest path"))
		}
		for _, name := range []string{"vcs", "diff", "d", "staged", "scan", "doctor", "fix", "files", "explain"} {
			if explicitFlags[name] {
				fatalInput(fmt.Errorf("--change-set cannot be combined with --%s", name))
			}
		}
		if len(flag.Args()) > 0 || len(varFiles) > 0 {
			fatalInput(errors.New("--change-set cannot read a diff or select source files"))
		}
	}

	if *explain != "" {
		if err := explainRule(*explain); err != nil {
			fmt.Fprintln(os.Stderr, "iflint:", err)
			os.Exit(2)
		}
		os.Exit(0)
	}

	rw := configuredWriter(cfg, cfg.Output.Format)
	if *changeSet != "" {
		opts := optionsFromConfig(cfg, nil)
		if *parallel != -1 {
			opts.Parallelism = *parallel
		}
		opts.StrictPaths = *strictPaths
		code, err := reportChangeSet(*changeSet, opts, rw, *listSupp, *statsOut)
		if err != nil {
			fatalInput(err)
		}
		if *warn && code == 1 {
			code = 0
		}
		os.Exit(code)
	}

	if *doctor {
		root := *scan
		if root == "" {
			root = "."
		}
		findings, err := runDoctor(root, cfg.SkipDirs, *fix)
		if err != nil {
			fmt.Fprintln(os.Stderr, "iflint doctor:", err)
			os.Exit(2)
		}
		if err := rw.Write(findings, nil); err != nil {
			fmt.Fprintln(os.Stderr, "iflint doctor:", err)
			os.Exit(2)
		}
		if len(findings) == 0 || *warn {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "iflint doctor: found %d issue(s)\n", len(findings))
		os.Exit(1)
	}

	if *scan != "" {
		files, err := scanForLint(*scan, cfg.SkipDirs)
		if err != nil {
			fmt.Fprintln(os.Stderr, "iflint:", err)
			os.Exit(2)
		}
		opts := optionsFromConfig(cfg, factories)
		if *parallel != -1 {
			opts.Parallelism = *parallel
		}
		opts.StrictPaths = *strictPaths
		res, code := eng.ValidateFiles(files, opts)
		if *listSupp {
			res.Findings = nil
			code = 0
		}
		if err := rw.Write(res.Findings, res.Suppressed); err != nil {
			fmt.Fprintln(os.Stderr, "iflint:", err)
			os.Exit(2)
		}
		if *warn && code == 1 {
			code = 0
		}
		os.Exit(code)
	}

	selected, patch, err := classifyInputs(flag.Args(), varFiles, *revision != "")
	if err != nil {
		fatalInput(err)
	}
	native := *revision != "" || *staged || (*vcsKind != "" && patch == "" && len(selected) == 0)
	if native && patch != "" {
		fatalInput(errors.New("native VCS mode cannot also read a diff file; use --files for structural selection"))
	}
	if *staged && *revision != "" {
		fatalInput(errors.New("--staged and --diff are mutually exclusive"))
	}
	var backend *vcs.Backend
	if native || *vcsKind != "" || len(selected) > 0 {
		backend, err = vcs.Open(context.Background(), ".", chooseNonEmpty(*vcsKind, "auto"))
		if err != nil && (native || *vcsKind != "") {
			fatalInput(err)
		}
		if err == nil {
			selected, err = rootSelectedFiles(selected, backend.Root)
			if err != nil {
				fatalInput(err)
			}
			if err := os.Chdir(backend.Root); err != nil {
				fatalInput(err)
			}
		}
	}
	var tracked []string
	needsTrackedFiles := false
	for _, pattern := range selected {
		if strings.ContainsAny(pattern, "*?[") {
			needsTrackedFiles = true
			break
		}
	}
	if backend != nil && needsTrackedFiles {
		tracked, err = backend.Files(context.Background())
		if err != nil {
			fatalInput(err)
		}
	}
	selected, err = expandTrackedFiles(selected, tracked)
	if err != nil {
		fatalInput(err)
	}
	diffText := ""
	suppress := false
	if native {
		request := vcs.Request{Revision: *revision, Staged: *staged}
		diffText, err = backend.Diff(context.Background(), request)
		if err != nil {
			fatalInput(err)
		}
		if *revision != "" {
			messages, e := backend.Messages(context.Background(), request)
			if e != nil {
				fatalInput(e)
			}
			suppress = hasSuppression(messages)
		}
	} else if len(selected) == 0 {
		diffReader, e := openDiff(patch)
		if e != nil {
			fatalInput(e)
		}
		diffBytes, e := io.ReadAll(diffReader)
		diffReader.Close()
		if e != nil {
			fatalInput(e)
		}
		diffText = string(diffBytes)
	}
	ilog.Debug("diff read", "bytes", len(diffText))
	ilog.Info("lint run starting", "code_only", cfg.Rules.CodeOnly, "fix", *fix)
	policy := canonicalCombinedPolicy(chooseNonEmpty(*combined, cfg.Rules.CombinedDiff))
	opts := optionsFromConfig(cfg, factories)
	if *parallel != -1 {
		opts.Parallelism = *parallel
	}
	opts.Fix = *fix
	opts.CombinedDiffPolicy = policy
	opts.StrictPaths = *strictPaths
	opts.StructuralFiles = selected
	opts.SourceFiles = selected
	opts.Repository = backend
	opts.SuppressCoChanges = suppress
	res, code := lintAndReport(diffText, opts)
	for _, f := range res.Findings {
		if f.RuleID == "diff_parse_error" {
			code = 2
			break
		}
	}
	write := func(findings, suppressed []core.Finding) {
		if err := rw.Write(findings, suppressed); err != nil {
			fmt.Fprintln(os.Stderr, "iflint:", err)
			os.Exit(2)
		}
	}
	if *listSupp {
		if len(res.Suppressed) == 0 {
			fmt.Fprintln(os.Stderr, "iflint: no suppressed findings")
		} else {
			write(nil, res.Suppressed)
		}
		os.Exit(0)
	}
	write(res.Findings, res.Suppressed)
	if *fix {
		if applied, ok := res.Stats["fix_applied"].([]string); ok && len(applied) > 0 {
			fmt.Fprintf(os.Stderr, "iflint: applied fixes\n%s\n", strings.Join(applied, "\n"))
		}
		if failures, ok := res.Stats["fix_errors"].([]string); ok && len(failures) > 0 {
			fmt.Fprintf(os.Stderr, "iflint: fix errors\n%s\n", strings.Join(failures, "\n"))
		}
	}
	if *statsOut {
		enc := json.NewEncoder(os.Stderr)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res.Stats)
	}
	if *warn && code == 1 {
		os.Exit(0)
	}
	ilog.Info("lint run finished", "findings", len(res.Findings), "suppressed", len(res.Suppressed), "exit_code", code)
	os.Exit(code)
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func parseRemoteFlagValue(raw string) (config.Remote, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return config.Remote{}, errors.New("remote cannot be empty")
	}
	remote := config.Remote{}
	remote.Type = "github"
	if strings.Contains(raw, "=") {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			kv := strings.SplitN(part, "=", 2)
			if len(kv) != 2 {
				return config.Remote{}, fmt.Errorf("invalid remote fragment %q", part)
			}
			key := strings.ToLower(strings.TrimSpace(kv[0]))
			val := strings.TrimSpace(kv[1])
			switch key {
			case "type":
				remote.Type = val
			case "repo":
				assignRepoAndRef(&remote, val)
			case "default_ref", "default-ref", "ref":
				remote.DefaultRef = val
			case "token_env", "token-env":
				remote.TokenEnv = val
			case "base_url", "base-url":
				remote.BaseURL = val
			default:
				return config.Remote{}, fmt.Errorf("unknown remote field %q", key)
			}
		}
	} else {
		assignRepoAndRef(&remote, raw)
	}
	if strings.TrimSpace(remote.Repo) == "" {
		return config.Remote{}, errors.New("remote repo is required")
	}
	return remote, nil
}

func assignRepoAndRef(remote *config.Remote, raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	if strings.Contains(raw, "@") {
		parts := strings.SplitN(raw, "@", 2)
		remote.Repo = strings.TrimSpace(parts[0])
		if remote.DefaultRef == "" {
			remote.DefaultRef = strings.TrimSpace(parts[1])
		}
		return
	}
	remote.Repo = raw
}

// fileResultWriter owns routing for every CLI mode; individual serializers share
// the same stdout contract. The CLI invokes Write sequentially.
type fileResultWriter struct {
	writer core.ResultWriter
	path   string
}

func (w fileResultWriter) Write(findings, suppressed []core.Finding) error {
	f, err := os.Create(w.path)
	if err != nil {
		return fmt.Errorf("open output %s: %w", w.path, err)
	}
	original := os.Stdout
	os.Stdout = f
	defer func() { os.Stdout = original }()
	writeErr := w.writer.Write(findings, suppressed)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func configuredWriter(cfg config.Config, format string) core.ResultWriter {
	writer := writerForFormat(format)
	if strings.TrimSpace(cfg.Output.Path) == "" {
		return writer
	}
	p := cfg.Output.Path
	if !filepath.IsAbs(p) && cfg.BaseDir != "" {
		p = filepath.Join(cfg.BaseDir, p)
	}
	return fileResultWriter{writer: writer, path: p}
}

func canonicalOutputFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "pretty", "plain":
		return "text"
	default:
		return format
	}
}

func writerForFormat(format string) core.ResultWriter {
	switch strings.ToLower(strings.TrimSpace(canonicalOutputFormat(format))) {
	case "json":
		return out.JSON{IncludeWorkspaceRoot: true}
	case "sarif":
		return out.SARIF{}
	case "diagnostic", "diagnostic-ls", "dls":
		return out.DiagnosticLS{}
	default:
		return out.Text{}
	}
}

func scanForLint(root string, skip []string) ([]string, error) {
	syn := core.CurrentDirectiveSyntax()
	files, err := scan.FindDirectiveFiles(root, syn.PrefixDot, -1, skip)
	if err != nil {
		return nil, err
	}
	return files, nil
}

func openDiff(p string) (io.ReadCloser, error) {
	if p == "" || p == "-" {
		return io.NopCloser(os.Stdin), nil
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func loadDirectiveConfiguration() (config.Config, error) {
	cfg, err := config.Load(".")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return cfg, err
	}
	core.SetDirectivePrefix(cfg.Directives.Prefix)
	comments.SetPythonDocstrings(cfg.PythonDocstringsEnabled())
	return cfg, nil
}

func runJump(args []string) error {
	flags := flag.NewFlagSet("jump", flag.ContinueOnError)
	printLocation := flags.Bool("print-location", false, "print the resolved file and 1-based line as JSON without opening an editor")
	if err := flags.Parse(reorderFlagArguments(flags, args)); err != nil {
		return err
	}
	args = flags.Args()
	if len(args) == 0 {
		return errors.New("jump requires a target file (optionally file#label)")
	}
	if _, err := loadDirectiveConfiguration(); err != nil {
		return err
	}
	target := args[0]
	label := ""
	if strings.Contains(target, "#") {
		parts := strings.SplitN(target, "#", 2)
		target = parts[0]
		label = parts[1]
	} else if len(args) > 1 {
		label = args[1]
	}
	path := filepath.Clean(target)
	if !filepath.IsAbs(path) {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		path = filepath.Join(cwd, path)
	}
	line := 1
	if label != "" {
		ranges, err := eng.LabelRanges(path)
		if err != nil {
			return err
		}
		rng, ok := ranges[label]
		if !ok {
			return fmt.Errorf("label %q not found in %s. Use `iflint scaffold --source <source-file> --target %s#%s` to create it.", label, path, target, label)
		}
		line = rng.StartLine
	}
	if *printLocation {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("target %s is not a regular file", path)
		}
		return json.NewEncoder(os.Stdout).Encode(struct {
			File string `json:"file"`
			Line int    `json:"line"`
		}{path, line})
	}
	return openAt(path, line)
}

func openAt(path string, line int) error {
	editor := os.Getenv("EDITOR")
	if editor != "" {
		parts := strings.Fields(editor)
		if len(parts) == 0 {
			parts = []string{editor}
		}
		args := append(parts[1:], fmt.Sprintf("+%d", line), path)
		cmd := exec.Command(parts[0], args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	if commandExists("code") {
		cmd := exec.Command("code", "--goto", fmt.Sprintf("%s:%d", path, line))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	if runtime.GOOS == "darwin" && commandExists("open") {
		cmd := exec.Command("open", path)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err == nil {
			fmt.Printf("Opened %s (line %d)\n", path, line)
			return nil
		}
	}
	if (runtime.GOOS == "linux" || runtime.GOOS == "freebsd") && commandExists("xdg-open") {
		cmd := exec.Command("xdg-open", path)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err == nil {
			fmt.Printf("Opened %s (line %d)\n", path, line)
			return nil
		}
	}
	fmt.Printf("%s:%d\n", path, line)
	fmt.Println("(set $EDITOR or install 'code' to jump automatically)")
	return nil
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func runExplainCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: iflint explain <rule-id>")
	}
	return explainRule(strings.Join(args, " "))
}

func explainRule(rule string) error {
	key := strings.ToLower(strings.TrimSpace(rule))
	if key == "" {
		return errors.New("empty rule id")
	}
	info, ok := ruleCatalog[key]
	if !ok {
		return fmt.Errorf("unknown rule %q", rule)
	}
	fmt.Printf("%s (severity: %s)\n", key, info.Severity)
	fmt.Println(info.Summary)
	if info.Resolution != "" {
		fmt.Printf("Suggested fix: %s\n", info.Resolution)
	}
	return nil
}

func runScaffold(args []string) error {
	flags := flag.NewFlagSet("scaffold", flag.ContinueOnError)
	var source string
	var target string
	var label string
	preset := flags.String("preset", "", "language preset for placeholder body (e.g. go, js, rb)")
	sourceOnly := flags.Bool("source-only", false, "only modify the source file")
	targetOnly := flags.Bool("target-only", false, "only modify the target file")
	flags.StringVar(&source, "source", "", "path to the file containing the IfChange directive")
	flags.StringVar(&target, "target", "", "path to the target file (optionally file#label)")
	flags.StringVar(&label, "label", "", "label to seed in the directives (optional)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if source == "" || target == "" {
		return errors.New("scaffold requires --source and --target")
	}
	if *sourceOnly && *targetOnly {
		return errors.New("cannot use --source-only and --target-only together")
	}
	writeSource := !*targetOnly
	writeTarget := !*sourceOnly
	ilog.Info(
		"scaffold invoked",
		"source", source,
		"target", target,
		"label", label,
		"source_only", *sourceOnly,
		"target_only", *targetOnly,
	)
	if _, err := loadDirectiveConfiguration(); err != nil {
		return err
	}
	targetPath := target
	if idx := strings.Index(target, "#"); idx >= 0 {
		if label == "" && idx < len(target)-1 {
			label = target[idx+1:]
		}
		targetPath = target[:idx]
	}
	if label == "" {
		label = deriveLabel(targetPath)
	}
	if label == "" {
		label = "LABEL"
	}
	thenSpec := target
	if !strings.Contains(target, "#") {
		thenSpec = fmt.Sprintf("%s#%s", targetPath, label)
	}
	syn := core.CurrentDirectiveSyntax()
	sourceLines := []string{
		commentLine(source, fmt.Sprintf("%s(\"%s\")", syn.TokenIfChange, label)),
		commentLine(source, fmt.Sprintf("%s(\"%s\")", syn.TokenThenChange, thenSpec)),
	}
	targetLines := []string{
		commentLine(targetPath, fmt.Sprintf("%s(\"%s\")", syn.TokenLabel, label)),
	}
	targetLines = append(targetLines, scaffoldBody(targetPath, *preset)...)
	targetLines = append(targetLines, commentLine(targetPath, fmt.Sprintf("%s.EndLabel", syn.Prefix)))
	if syn.Prefix == "LINT" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		absoluteTarget, err := filepath.Abs(targetPath)
		if err != nil {
			return err
		}
		root := cwd
		if backend, err := vcs.Open(context.Background(), cwd, "auto"); err == nil {
			root = backend.Root
		}
		relativeTarget, err := filepath.Rel(root, absoluteTarget)
		if err != nil {
			return err
		}
		thenSpec = "//" + filepath.ToSlash(relativeTarget) + ":" + label
		sourceLines = []string{
			commentLine(source, syn.TokenIfChange+"("+label+")"),
			commentLine(source, syn.TokenThenChange+"("+thenSpec+")"),
		}
		targetLines = []string{commentLine(targetPath, syn.TokenIfChange+"("+label+")")}
		targetLines = append(targetLines, scaffoldBody(targetPath, *preset)...)
		targetLines = append(targetLines, commentLine(targetPath, syn.TokenThenChange+"()"))
	}
	// Validate both generated snippets before either file can be mutated.
	for _, snippet := range []struct {
		path  string
		lines []string
	}{{source, sourceLines}, {targetPath, targetLines}} {
		generated, err := (parse.Provider{ReadFile: func(string) ([]byte, error) {
			return []byte(strings.Join(snippet.lines, "\n")), nil
		}}).Parse(snippet.path)
		if err != nil {
			return err
		}
		if len(generated) != 2 {
			return fmt.Errorf("cannot scaffold directives in %s: comment syntax did not produce a complete pair", snippet.path)
		}
		for _, directive := range generated {
			if directive.Kind == core.Unknown {
				return fmt.Errorf("cannot scaffold directive: %s", directive.Label)
			}
		}
	}
	if writeSource && writeTarget {
		absoluteSource, err := filepath.Abs(source)
		if err != nil {
			return err
		}
		absoluteTarget, err := filepath.Abs(targetPath)
		if err != nil {
			return err
		}
		sameFile := absoluteSource == absoluteTarget
		if sourceInfo, err := os.Stat(source); err == nil {
			if targetInfo, err := os.Stat(targetPath); err == nil && os.SameFile(sourceInfo, targetInfo) {
				sameFile = true
			}
		}
		if sameFile {
			return errors.New("source and target are the same file; use --source-only or --target-only to avoid duplicate labels")
		}
	}
	if writeSource {
		if err := appendSnippet(source, sourceLines); err != nil {
			return fmt.Errorf("update source: %w", err)
		}
	}
	if writeTarget {
		if err := appendSnippet(targetPath, targetLines); err != nil {
			return fmt.Errorf("update target: %w", err)
		}
	}
	switch {
	case writeSource && writeTarget:
		msg := fmt.Sprintf("iflint scaffold: inserted directives for label %s (source + target)\n", label)
		fmt.Print(msg)
		ilog.Info("scaffold completed", "label", label, "mode", "both")
	case writeSource:
		msg := fmt.Sprintf("iflint scaffold: inserted source directives for label %s\n", label)
		fmt.Print(msg)
		ilog.Info("scaffold completed", "label", label, "mode", "source-only")
	case writeTarget:
		msg := fmt.Sprintf("iflint scaffold: inserted target directives for label %s\n", label)
		fmt.Print(msg)
		ilog.Info("scaffold completed", "label", label, "mode", "target-only")
	default:
		msg := fmt.Sprintf("iflint scaffold: nothing to do for label %s\n", label)
		fmt.Print(msg)
		ilog.Warn("scaffold requested no changes", "label", label)
	}
	return nil
}

func runIgnoreAdd(args []string) error {
	flags := flag.NewFlagSet("ignore add", flag.ContinueOnError)
	var file string
	var line int
	var rule string
	flags.StringVar(&file, "file", "", "file to update (required)")
	flags.IntVar(&line, "line", 0, "1-based line number to insert before (0 appends)")
	flags.StringVar(&rule, "rule", "then_missing", "rule id to ignore (use 'all' to suppress everything)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if file == "" {
		if flags.NArg() > 0 {
			file = flags.Arg(0)
		} else {
			return errors.New("ignore add requires --file")
		}
	}
	file = filepath.Clean(file)
	if rule == "" {
		rule = "then_missing"
	}
	ilog.Info("ignore add invoked", "file", file, "line", line, "rule", rule)
	if _, err := loadDirectiveConfiguration(); err != nil {
		return err
	}
	syn := core.CurrentDirectiveSyntax()
	lineText := commentLine(file, fmt.Sprintf("%s(\"%s\")", syn.PrefixDot+"Ignore", rule))
	inserted, err := insertDirectiveLine(file, line, lineText)
	if err != nil {
		return err
	}
	fmt.Printf("iflint ignore add: inserted %s at %s:%d\n", syn.PrefixDot+"Ignore", file, inserted)
	ilog.Info("ignore directive inserted", "file", file, "line", inserted, "rule", rule)
	return nil
}

func appendSnippet(path string, lines []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var (
		existing []byte
		perm     os.FileMode = 0o644
	)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode()
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		existing = data
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(existing) > 0 {
		duplicate := true
		for _, line := range lines {
			if !bytes.Contains(existing, []byte(line)) {
				duplicate = false
				break
			}
		}
		if duplicate {
			return nil
		}
	}
	buf := bytes.NewBuffer(nil)
	if len(existing) > 0 {
		buf.Write(existing)
		if existing[len(existing)-1] != '\n' {
			buf.WriteByte('\n')
		}
	}
	for _, line := range lines {
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	return os.WriteFile(path, buf.Bytes(), perm.Perm())
}

func insertDirectiveLine(path string, line int, text string) (int, error) {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return 0, err
		}
	}
	var (
		data []byte
		perm os.FileMode = 0o644
	)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode()
		bytes, readErr := os.ReadFile(path)
		if readErr != nil {
			return 0, readErr
		}
		data = bytes
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	lines := []string{}
	if len(data) > 0 {
		content := strings.TrimRight(string(data), "\n")
		if content != "" {
			lines = strings.Split(content, "\n")
		}
	}
	insertIdx := len(lines)
	if line > 0 {
		idx := line - 1
		if idx < 0 {
			idx = 0
		}
		if idx < len(lines) {
			insertIdx = idx
		} else if idx == len(lines) {
			insertIdx = len(lines)
		}
	}
	lines = append(lines, "")
	copy(lines[insertIdx+1:], lines[insertIdx:])
	lines[insertIdx] = text
	content := strings.Join(lines, "\n")
	if content != "" {
		content += "\n"
	} else {
		content = text + "\n"
	}
	if err := os.WriteFile(path, []byte(content), perm.Perm()); err != nil {
		return 0, err
	}
	return insertIdx + 1, nil
}

func directiveLine(prefix, directive string) string {
	if prefix == "" {
		return directive
	}
	return prefix + " " + directive
}

func commentLine(path, text string) string {
	if prefix, ok := getCommentPrefixOverride(strings.ToLower(filepath.Ext(path))); ok {
		return directiveLine(prefix, text)
	}
	return comments.FormatComment(path, text)
}

func applyCommentStyleOverrides(values []string) error {
	for _, raw := range values {
		ext, prefix, err := parseCommentStyle(raw)
		if err != nil {
			return err
		}
		comments.RegisterLineComment(ext, prefix)
		setCommentPrefixOverride(ext, prefix)
	}
	return nil
}

func parseCommentStyle(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", errors.New("comment style cannot be empty")
	}
	parts := strings.SplitN(raw, "=", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("comment style %q must be .ext=prefix", raw)
	}
	ext := strings.TrimSpace(parts[0])
	if ext == "" {
		return "", "", fmt.Errorf("comment style %q missing extension", raw)
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	ext = strings.ToLower(ext)
	prefix, err := normalizeCommentPrefix(parts[1])
	if err != nil {
		return "", "", err
	}
	return ext, prefix, nil
}

func normalizeCommentPrefix(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("comment prefix cannot be empty")
	}
	switch strings.ToLower(value) {
	case "hash", "pound", "#":
		return "#", nil
	case "dash", "--", "sql":
		return "--", nil
	case "slash", "//", "c", "line":
		return "//", nil
	default:
		return value, nil
	}
}

func setCommentPrefixOverride(ext, prefix string) {
	commentStyleMu.Lock()
	commentPrefixOverrides[ext] = prefix
	commentStyleMu.Unlock()
}

func getCommentPrefixOverride(ext string) (string, bool) {
	commentStyleMu.RLock()
	defer commentStyleMu.RUnlock()
	prefix, ok := commentPrefixOverrides[ext]
	return prefix, ok
}

func resetCommentPrefixOverrides() {
	commentStyleMu.Lock()
	commentPrefixOverrides = map[string]string{}
	commentStyleMu.Unlock()
}

func commentPrefix(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if prefix, ok := getCommentPrefixOverride(ext); ok {
		return prefix
	}
	switch ext {
	case ".py", ".bzl", ".sh", ".yaml", ".yml", ".toml":
		return "#"
	case ".sql", ".lua":
		return "--"
	default:
		return "//"
	}
}

func deriveLabel(path string) string {
	base := filepath.Base(path)
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "label"
	}
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.ToUpper(base)
	clean := make([]rune, 0, len(base))
	lastUnderscore := false
	for _, r := range base {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			clean = append(clean, r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			clean = append(clean, '_')
			lastUnderscore = true
		}
	}
	label := strings.Trim(string(clean), "_")
	if label == "" {
		label = "LABEL"
	}
	return label
}

func runDoctor(root string, skip []string, fix bool) ([]core.Finding, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root = absRoot
	syn := core.CurrentDirectiveSyntax()
	files, err := scan.FindDirectiveFiles(root, syn.PrefixDot, -1, skip)
	if err != nil {
		return nil, err
	}
	provider := parse.Provider{}
	findings := make([]core.Finding, 0)
	for _, path := range files {
		dirs, err := provider.Parse(path)
		if err != nil {
			findings = append(findings, core.Finding{
				RuleID:   "error",
				Severity: "error",
				File:     path,
				Line:     1,
				Message:  err.Error(),
			})
			continue
		}
		var fixer *doctorFixer
		if fix {
			fixer, err = newDoctorFixer(path)
			if err != nil {
				findings = append(findings, finding("error", path, 1, fmt.Sprintf("doctor fix setup failed: %v", err)))
				continue
			}
		}
		fileFindings := doctorFile(path, dirs, fixer, root, syn)
		findings = append(findings, fileFindings...)
		if fix && fixer != nil && fixer.changed {
			if err := fixer.flush(); err != nil {
				findings = append(findings, finding("error", path, 1, fmt.Sprintf("doctor fix write failed: %v", err)))
			}
		}
	}
	return findings, nil
}

func doctorFile(path string, dirs []core.LintDirective, fixer *doctorFixer, root string, syn core.DirectiveSyntax) []core.Finding {
	findings := make([]core.Finding, 0)
	var stack []core.LintDirective
	for _, d := range dirs {
		switch d.Kind {
		case core.IfChange:
			stack = append(stack, d)
		case core.ThenChange:
			if len(stack) == 0 {
				target := firstTarget(d)
				if target == "" {
					target = "<target>"
				}
				findings = append(findings, core.Finding{
					RuleID:   "orphan_then",
					Severity: "error",
					File:     path,
					Line:     d.Line,
					Message:  fmt.Sprintf("ThenChange '%s' without preceding IfChange", target),
				})
			} else {
				stack = stack[:len(stack)-1]
				if fixer != nil {
					if d.Target != "" && fixer.convertSingleThen(d.Line) {
						findings = append(findings, core.Finding{
							RuleID:   "doctor_fix_then",
							Severity: "info",
							File:     path,
							Line:     d.Line,
							Message:  "converted ThenChange string to array",
						})
					}
					if len(d.List) > 0 {
						uniq := dedupeStrings(d.List)
						if len(uniq) < len(d.List) && fixer.replaceThenList(d.Line, uniq) {
							findings = append(findings, core.Finding{
								RuleID:   "doctor_fix_dedupe",
								Severity: "info",
								File:     path,
								Line:     d.Line,
								Message:  "deduplicated ThenChange targets",
							})
						}
					}
					for _, raw := range targetsOf(d) {
						ref := resolveDoctorTarget(path, raw)
						if ref.Label == "" {
							continue
						}
						ok, err := ensureLabelScaffoldWithFixer(root, ref, syn, fixer)
						if err != nil {
							findings = append(findings, finding("doctor_scaffold_error", path, d.Line, fmt.Sprintf("scaffold failed for %s: %v", ref.Path, err)))
							continue
						}
						if ok {
							findings = append(findings, core.Finding{
								RuleID:   "doctor_scaffold_label",
								Severity: "info",
								File:     ref.Path,
								Line:     1,
								Message:  fmt.Sprintf("inserted label scaffold for %s", ref.Label),
							})
						}
					}
				}
			}
		}
	}
	for _, curIf := range stack {
		msg := "missing ThenChange after IfChange"
		if curIf.Label != "" {
			msg = fmt.Sprintf("missing ThenChange after IfChange(\"%s\")", curIf.Label)
		}
		findings = append(findings, core.Finding{
			RuleID:   "orphan_if",
			Severity: "error",
			File:     path,
			Line:     curIf.Line,
			Message:  msg,
		})
	}
	return findings
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		result = append(result, v)
	}
	return result
}

func finding(rule, file string, line int, msg string) core.Finding {
	return core.Finding{RuleID: rule, Severity: "error", File: file, Line: line, Message: msg}
}

type doctorFixer struct {
	path    string
	lines   []string
	changed bool
}

func scaffoldBody(path string, preset string) []string {
	template := preset
	if template == "" {
		template = commentLine(path, "TODO: update matching block for scaffolded directive")
	}
	return []string{template}
}

func newDoctorFixer(path string) (*doctorFixer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	contents := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	return &doctorFixer{path: path, lines: contents}, nil
}

func (f *doctorFixer) convertSingleThen(line int) bool {
	idx := line - 1
	if idx < 0 || idx >= len(f.lines) {
		return false
	}
	row := f.lines[idx]
	open := strings.Index(row, "(")
	close := strings.LastIndex(row, ")")
	if open == -1 || close == -1 || close <= open+1 {
		return false
	}
	body := strings.TrimSpace(row[open+1 : close])
	if strings.HasPrefix(body, "[") {
		return false
	}
	if !strings.HasPrefix(body, "\"") || !strings.HasSuffix(body, "\"") {
		return false
	}
	newBody := fmt.Sprintf("[%s]", body)
	f.lines[idx] = row[:open+1] + newBody + row[close:]
	f.changed = true
	return true
}

func (f *doctorFixer) replaceThenList(line int, values []string) bool {
	idx := line - 1
	if idx < 0 || idx >= len(f.lines) {
		return false
	}
	row := f.lines[idx]
	open := strings.Index(row, "(")
	close := strings.LastIndex(row, ")")
	if open == -1 || close == -1 || close <= open+1 {
		return false
	}
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		quoted = append(quoted, fmt.Sprintf("\"%s\"", v))
	}
	body := fmt.Sprintf("[%s]", strings.Join(quoted, ", "))
	if core.CurrentDirectiveSyntax().Prefix == "LINT" {
		body = strings.Join(values, ", ")
	}
	f.lines[idx] = row[:open+1] + body + row[close:]
	f.changed = true
	return true
}

func (f *doctorFixer) flush() error {
	if !f.changed {
		return nil
	}
	content := strings.Join(f.lines, "\n")
	return os.WriteFile(f.path, []byte(content), 0o644)
}

func firstTarget(d core.LintDirective) string {
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

func resolveDoctorTarget(src, raw string) core.TargetRef {
	return eng.ResolveTarget(src, raw)
}

func ensureLabelScaffold(root string, ref core.TargetRef, syn core.DirectiveSyntax) (bool, error) {
	return ensureLabelScaffoldWithFixer(root, ref, syn, nil)
}

func ensureLabelScaffoldWithFixer(root string, ref core.TargetRef, syn core.DirectiveSyntax, fixer *doctorFixer) (bool, error) {
	if ref.Label == "" || strings.Contains(ref.Path, "://") {
		return false, nil
	}
	targetPath := ref.Path
	if !filepath.IsAbs(targetPath) {
		targetPath = filepath.Join(root, targetPath)
	}
	targetPath = core.NormalizePath(targetPath)
	if err := eng.ValidateFixPath(root, targetPath); err != nil {
		return false, err
	}
	provider := parse.Provider{}
	// Same-file scaffolds must share the deduplication buffer so the final
	// flush preserves both edits, including references through hard links.
	sameFile := false
	if fixer != nil {
		sameFile = filepath.Clean(fixer.path) == targetPath
		if sourceInfo, err := os.Stat(fixer.path); err == nil {
			if targetInfo, err := os.Stat(targetPath); err == nil {
				sameFile = sameFile || os.SameFile(sourceInfo, targetInfo)
			}
		}
		if sameFile {
			provider.ReadFile = func(string) ([]byte, error) {
				return []byte(strings.Join(fixer.lines, "\n")), nil
			}
		}
	}
	dirs, err := provider.Parse(targetPath)
	if err == nil {
		for _, d := range dirs {
			if (d.Kind == core.Label && d.Name == ref.Label) || (d.Kind == core.IfChange && d.Label == ref.Label) {
				return false, nil
			}
		}
	}
	lines := []string{}
	lines = append(lines, commentLine(targetPath, fmt.Sprintf("%s(%q)", syn.TokenLabel, ref.Label)))
	lines = append(lines, scaffoldBody(targetPath, "")...)
	lines = append(lines, commentLine(targetPath, fmt.Sprintf("%s.EndLabel", syn.Prefix)))
	if syn.Prefix == "LINT" {
		native := []string{commentLine(targetPath, syn.TokenIfChange+"("+ref.Label+")")}
		native = append(native, scaffoldBody(targetPath, "")...)
		native = append(native, commentLine(targetPath, syn.TokenThenChange+"()"))
		generated, err := (parse.Provider{ReadFile: func(string) ([]byte, error) {
			return []byte(strings.Join(native, "\n")), nil
		}}).Parse(targetPath)
		if err == nil && len(generated) == 2 && generated[0].Kind == core.IfChange && generated[1].Kind == core.ThenChange {
			lines = native
		}
	}
	if sameFile {
		text := strings.Join(fixer.lines, "\n")
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		fixer.lines = strings.Split(text+strings.Join(lines, "\n")+"\n", "\n")
		fixer.changed = true
		return true, nil
	}
	if err := appendSnippet(targetPath, lines); err != nil {
		return false, err
	}
	return true, nil
}

func runWatch(args []string) error {
	flags := flag.NewFlagSet("watch", flag.ContinueOnError)
	interval := flags.Duration("interval", 2*time.Second, "polling interval for re-running lint")
	vcsKind := flags.String("vcs", "auto", "VCS backend: auto|git|jj")
	revision := flags.String("revision", "", "native VCS revision range or jj revset")
	staged := flags.Bool("staged", false, "watch staged (git diff --cached) instead of working tree")
	diffCmd := flags.String("diff", "", "override diff command (e.g. \"git diff HEAD~1..HEAD\")")
	statusCmd := flags.String("status", "", "command used to detect file changes before running the diff (set empty to disable)")
	format := flags.String("format", "", "override output format (text|json|sarif)")
	codeOnly := flags.Bool("code-only", false, "run in code-only mode while watching")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if *interval <= 0 {
		return errors.New("watch interval must be positive")
	}

	cfg, cfgErr := config.Load(".")
	if cfgErr != nil && !errors.Is(cfgErr, fs.ErrNotExist) {
		return cfgErr
	}
	if err := config.Validate(&cfg); err != nil {
		return err
	}
	core.SetDirectivePrefix(cfg.Directives.Prefix)
	comments.SetPythonDocstrings(cfg.PythonDocstringsEnabled())
	factories, err := buildFactories(cfg)
	if err != nil {
		return err
	}
	opts := optionsFromConfig(cfg, factories)
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "code-only" {
			opts.CodeOnly = *codeOnly
		}
	})
	opts.Fix = false

	writer := configuredWriter(cfg, chooseNonEmpty(*format, cfg.Output.Format))

	command := strings.TrimSpace(*diffCmd)
	var backend *vcs.Backend
	if command == "" {
		backend, err = vcs.Open(context.Background(), ".", *vcsKind)
		if err != nil {
			return err
		}
		if *staged && *revision != "" {
			return errors.New("--staged and --revision are mutually exclusive")
		}
		if backend.Kind == "jj" && *staged {
			return errors.New("Jujutsu has no staging area; use --revision or a working-copy diff")
		}
		if err := os.Chdir(backend.Root); err != nil {
			return err
		}
	}
	opts.Repository = backend
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var lastHash string

	ilog.Info("watch starting", "interval", interval.String(), "diff_command", command, "status_command", *statusCmd)
	fmt.Fprintf(os.Stderr, "iflint watch: polling \"%s\" every %s (Ctrl+C to stop)\n", command, interval)

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, "iflint watch: stopping")
			return nil
		case <-ticker.C:
			statusCommand := strings.TrimSpace(*statusCmd)
			if statusCommand != "" {
				statusOut, err := runShellCommand(statusCommand)
				if err != nil {
					fmt.Fprintf(os.Stderr, "iflint watch: status command failed: %v\n", err)
					ilog.Error("status command failed", "command", statusCommand, "error", err)
					continue
				}
				_ = statusOut // Porcelain status cannot detect repeated edits to already-modified files.
			}
			var diff string
			var err error
			if backend != nil {
				diff, err = backend.Diff(ctx, vcs.Request{Revision: *revision, Staged: *staged})
			} else {
				diff, err = runShellCommand(command)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "iflint watch: diff command failed: %v\n", err)
				ilog.Error("diff command failed", "command", command, "error", err)
				continue
			}
			trimmed := strings.TrimSpace(diff)
			hash := diffDigest(trimmed)
			if hash == lastHash {
				ilog.Debug("diff hash unchanged, skipping run", "hash", hash)
				continue
			}
			lastHash = hash
			if trimmed == "" {
				fmt.Fprintln(os.Stderr, "iflint watch: no diff")
				ilog.Debug("watch diff empty, skipping run")
				continue
			}
			fmt.Fprintf(os.Stderr, "\niflint watch @ %s\n", time.Now().Format(time.RFC3339))

			if backend != nil && *revision != "" {
				messages, e := backend.Messages(ctx, vcs.Request{Revision: *revision, Staged: *staged})
				if e != nil {
					fmt.Fprintln(os.Stderr, "iflint watch:", e)
					continue
				}
				opts.SuppressCoChanges = hasSuppression(messages)
			}
			res, code := lintAndReport(diff, opts)
			if err := writer.Write(res.Findings, res.Suppressed); err != nil {
				fmt.Fprintf(os.Stderr, "iflint watch: lint failed: %v\n", err)
				ilog.Error("watch lint failed", "error", err)
				continue
			}
			fmt.Fprintf(
				os.Stderr,
				"iflint watch: findings=%d suppressed=%d exit=%d\n",
				len(res.Findings),
				len(res.Suppressed),
				code,
			)
			ilog.Info("watch completed lint iteration", "findings", len(res.Findings), "suppressed", len(res.Suppressed), "exit_code", code)
		}
	}
}

func runReview(args []string) error {
	flags := flag.NewFlagSet("review", flag.ContinueOnError)
	vcsKind := flags.String("vcs", "auto", "VCS backend: auto|git|jj")
	base := flags.String("base", "", "explicit base revision (defaults to parent of revision)")
	format := flags.String("format", "", "override output format (text|json|sarif)")
	codeOnly := flags.Bool("code-only", false, "run in code-only mode")
	listSupp := flags.Bool("list-suppressed", false, "print only suppressed findings and exit 0")
	if err := flags.Parse(args); err != nil {
		return err
	}
	rev := ""
	if flags.NArg() > 0 {
		rev = flags.Arg(0)
	}
	ilog.Info("review run starting", "revision", rev, "base", *base)

	cfg, cfgErr := config.Load(".")
	if cfgErr != nil && !errors.Is(cfgErr, fs.ErrNotExist) {
		return cfgErr
	}
	if err := config.Validate(&cfg); err != nil {
		return err
	}
	core.SetDirectivePrefix(cfg.Directives.Prefix)
	comments.SetPythonDocstrings(cfg.PythonDocstringsEnabled())
	factories, err := buildFactories(cfg)
	if err != nil {
		return err
	}
	opts := optionsFromConfig(cfg, factories)
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "code-only" {
			opts.CodeOnly = *codeOnly
		}
	})
	writer := configuredWriter(cfg, chooseNonEmpty(*format, cfg.Output.Format))

	backend, err := vcs.Open(context.Background(), ".", *vcsKind)
	if err != nil {
		return err
	}
	if err := os.Chdir(backend.Root); err != nil {
		return err
	}
	request := vcs.Request{Revision: rev, Base: *base, Review: true}
	diff, err := backend.Diff(context.Background(), request)
	if err != nil {
		return err
	}
	if strings.TrimSpace(diff) == "" {
		fmt.Fprintf(os.Stderr, "iflint review: diff for %s is empty\n", rev)
		os.Exit(0)
	}

	messages, err := backend.Messages(context.Background(), request)
	if err != nil {
		return err
	}
	opts.Repository = backend
	opts.SuppressCoChanges = hasSuppression(messages)
	res, code := lintAndReport(diff, opts)
	if *listSupp {
		if len(res.Suppressed) == 0 {
			fmt.Fprintln(os.Stderr, "iflint review: no suppressed findings")
		} else if err := writer.Write(nil, res.Suppressed); err != nil {
			return err
		}
		os.Exit(0)
	}
	if err := writer.Write(res.Findings, res.Suppressed); err != nil {
		return err
	}
	fmt.Fprintf(
		os.Stderr,
		"iflint review: findings=%d suppressed=%d exit=%d\n",
		len(res.Findings),
		len(res.Suppressed),
		code,
	)
	ilog.Info("review run completed", "revision", rev, "findings", len(res.Findings), "suppressed", len(res.Suppressed), "exit_code", code)
	os.Exit(code)
	return nil
}

func runBlame(args []string) error {
	flags := flag.NewFlagSet("blame", flag.ContinueOnError)
	file := flags.String("file", "", "file to inspect for unlabeled IfChange directives")
	if err := flags.Parse(args); err != nil {
		return err
	}
	target := strings.TrimSpace(*file)
	if target == "" {
		if flags.NArg() == 0 {
			return errors.New("blame requires a file path")
		}
		target = flags.Arg(0)
	}
	target = filepath.Clean(target)

	if _, err := loadDirectiveConfiguration(); err != nil {
		return err
	}
	provider := parse.Provider{}
	dirs, err := provider.Parse(target)
	if err != nil {
		return err
	}

	var unlabeled []core.LintDirective
	for _, d := range dirs {
		if d.Kind == core.IfChange && strings.TrimSpace(d.Label) == "" {
			unlabeled = append(unlabeled, d)
		}
	}
	if len(unlabeled) == 0 {
		fmt.Printf("%s: all IfChange directives carry labels\n", target)
		return nil
	}

	fmt.Printf("%s: unlabeled IfChange directives\n", target)
	for _, d := range unlabeled {
		info, blameErr := gitBlameLine(target, d.Line)
		if blameErr != nil {
			fmt.Printf("  line %d: git blame failed: %v\n", d.Line, blameErr)
			continue
		}
		fmt.Printf("  line %d -> %s\n", d.Line, info)
	}
	return nil
}

func lintAndReport(diff string, opts eng.Options) (eng.Result, int) {
	res, code := eng.Lint(diff, opts)
	for i := range res.Findings {
		enrichFinding(&res.Findings[i])
	}
	for i := range res.Suppressed {
		enrichFinding(&res.Suppressed[i])
	}
	return res, code
}

func enrichFinding(f *core.Finding) {
	info, ok := ruleCatalog[strings.ToLower(strings.TrimSpace(f.RuleID))]
	if !ok {
		return
	}
	if f.Summary == "" {
		f.Summary = info.Summary
	}
	if f.Resolution == "" {
		f.Resolution = info.Resolution
	}
	if strings.TrimSpace(f.Severity) == "" {
		f.Severity = info.Severity
	}
}

func optionsFromConfig(cfg config.Config, factories []eng.FileProviderFactory) eng.Options {
	parallelism := -1
	if n, err := strconv.Atoi(strings.TrimSpace(cfg.Parallelism)); err == nil && n > 0 {
		parallelism = n
	}
	return eng.Options{
		Parallelism:        parallelism,
		Verbose:            cfg.Verbose,
		Ignores:            append([]string{}, cfg.Ignores...),
		CodeOnly:           cfg.Rules.CodeOnly,
		UnknownPolicy:      cfg.Rules.UnknownDirective,
		SkipDirs:           append([]string{}, cfg.SkipDirs...),
		Fix:                false,
		Factories:          append([]eng.FileProviderFactory{}, factories...),
		CombinedDiffPolicy: canonicalCombinedPolicy(cfg.Rules.CombinedDiff),
	}
}

func runShellCommand(command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", errors.New("empty command")
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", command)
	} else {
		cmd = exec.Command("bash", "-lc", command)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s failed: %w (output: %s)", command, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func gitBlameLine(file string, line int) (string, error) {
	arg := fmt.Sprintf("-L %d,%d", line, line)
	out, err := exec.Command("git", "blame", arg, "--", file).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git blame failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func chooseNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func canonicalCombinedPolicy(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "strict", "error":
		return eng.CombinedDiffStrict
	case "warn", "note", "skip_with_note":
		return eng.CombinedDiffWarn
	case "ignore":
		return eng.CombinedDiffIgnore
	case "parent", "parent1", "convert":
		return eng.CombinedDiffParent
	default:
		return eng.CombinedDiffStrict
	}
}

func diffDigest(diff string) string {
	if diff == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(diff))
	return hex.EncodeToString(sum[:])
}

func buildFactories(cfg config.Config) ([]eng.FileProviderFactory, error) {
	var factories []eng.FileProviderFactory
	var githubRemotes []eng.GitHubRemote
	for _, remote := range cfg.Remotes {
		typeName := strings.ToLower(strings.TrimSpace(remote.Type))
		if typeName == "" {
			typeName = "github"
		}
		switch typeName {
		case "github":
			token := ""
			if remote.TokenEnv != "" {
				token = os.Getenv(remote.TokenEnv)
			}
			githubRemotes = append(githubRemotes, eng.GitHubRemote{
				Repo:       remote.Repo,
				DefaultRef: remote.DefaultRef,
				Token:      token,
				BaseURL:    remote.BaseURL,
			})
		}
	}
	if len(githubRemotes) > 0 {
		factory, err := eng.NewGitHubFactory(githubRemotes)
		if err != nil {
			return nil, err
		}
		if factory != nil {
			factories = append(factories, factory)
		}
	}
	return factories, nil
}
