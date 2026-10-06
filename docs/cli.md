# CLI and configuration

[README](../README.md) · [Directives and examples](directives.md) · [Cross-repository checks](cross-repository.md)

## Commands

```sh
ifttt --vcs git                          # local edits
ifttt --vcs git --staged                 # staged co-changes
ifttt --vcs git --diff main...HEAD        # Git revision range
ifttt --vcs jj --diff 'main..@'           # jj revset
ifttt --vcs auto '**/*'                   # all tracked files, structural checks
ifttt --vcs git path/to/source.go         # selected source, structural checks
ifttt --scan .                           # discover and validate directive files
ifttt --doctor                           # report orphan directives
ifttt patch.diff                         # unified diff file
git diff --cached | ifttt -              # unified diff on stdin
ifttt --change-set .ifttt-changes.yaml --format=json
```

Git and jj commands find the repository root from a subdirectory. Patch files and stdin use the current directory. Run those commands at the repository root.

Use `--vcs auto|git|jj` to select the backend. Automatic detection chooses jj when both exist. jj has no staging area.

<details>
<summary>More commands, options and exit codes</summary>

Source paths and quoted globs check directive syntax, target files and labels. With `--diff`, they limit which changed sources the tool checks. Stale-reference checks still search the whole repository. Repeat `--files` to select several sources.

<!-- LINT.IfChange(scan_local_artifacts) -->
Automatic discovery respects `.gitignore`, including nested rules and `!` exceptions. Git repositories also use Git's exclude files. Tracked files and explicitly linked targets are checked even when Git ignores them. Hidden files are included; symlinks are skipped.

Default skips cover VCS metadata and dependency/cache folders: `.git`, `.jj`, `.hg`, `.svn`, `node_modules`, `vendor`, `.cache`, `.gocache`, `.venv` and `__pycache__`. Editor directories and build outputs follow your ignore rules. `--skip-dir` or configured `skip_directories` replace the defaults. `--ignore` or configured `ignores` add exclusions. `--verbose` shows discovery start and completion.
<!-- LINT.ThenChange(//internal/scan/scan.go:scan_local_artifacts, //test/integration/cli_test.go:scan_local_artifacts) -->

| Command | Purpose |
| --- | --- |
| `ifttt review --vcs git main...HEAD` | Check a Git range. Use `--vcs jj` for jj revision expressions. |
| `ifttt watch --vcs git --staged` | Check staged changes repeatedly. Use `--revision RANGE` for revisions. Omit `--staged` for working changes. |
| `ifttt jump target.go API` | Open a label using `$EDITOR`, `code --goto`, or print its location. |
| `ifttt jump --print-location -- target.go API` | Print JSON with an absolute file path and 1-based line. |
| `ifttt scaffold --source source.go --target target.go#API --label API` | Create linked directive blocks. Use `--source-only` or `--target-only` to limit writes. |
| `ifttt ignore add --file source.go --rule then_missing --line 10` | Insert a suppression comment. |
| `ifttt blame source.go` | List unlabelled IfChange directives with Git blame metadata. |
| `ifttt explain then_missing` | Show a rule's severity and how to fix it. |

<!-- LINT.IfChange(inspect_command) -->
`ifttt inspect --stdin source.go` reads editor text from stdin and reports parsed directives as JSON. It does not check targets or change files. Omit `--stdin` to read the file. It supports `--comment-style`.
<!-- LINT.ThenChange(//cmd/ifttt/inspect.go:directive_inspection) -->

`review --base BASE HEAD` selects the revisions to compare. For more options, use `ifttt --help` or a command's `--help`.

- `watch --interval 2s` sets the time between checks.
- `watch --diff 'COMMAND'` runs a shell command that produces a diff.
- `watch --revision RANGE` selects a Git or jj revision range.
- `watch --strict=false` and `review --strict=false` allow other local target path forms.
- `watch --status 'COMMAND'` runs the command before each check.
- `scaffold --preset 'BODY'` inserts the given text as a placeholder.

Common options:

- `--version`: print the executable version and source commit.
- `--format text|json|sarif|diagnostic-ls`: select output. JSON includes `workspaceRoot` for resolving finding paths.
- `--ignore PATTERN`: ignore a file or `file#label`. Repeat this option for several patterns. `*` and `?` match within one path component. `**` matches across directories.
- `--skip-dir DIR`: exclude a directory. Repeat this option for several directories.
- `--code-only`: ignore changes that affect only whitespace or comments when deciding whether a source block changed.
- `--strict=true` (default): require `//` root paths or same-file label selectors in standard LINT targets. Use `--strict=false` to allow other local path forms.
- `--warn`: report lint findings with exit status 0.
- `--list-suppressed`: report suppressed findings and exit 0. Invalid or incomplete change-set input still exits 2.
- `--fix`: add TODO placeholders or missing target label blocks. See [fix behavior](#fix-behavior).
- `--comment-style .tmpl=##`: set a custom comment prefix. Repeat this option for several extensions.
- `--combined strict|warn|ignore|parent`: handle combined merge diffs. `parent` tries to use the first parent as change evidence.
- `--verbose`, `--log-level debug|info|warn|error`, `--stats`: inspect logging and run statistics.
- `--parallel N` (aliases `--threads`, `-p`, `-t`): set the number of workers. Use `--threads 0` for automatic selection, which uses two workers.

Lint, scan and change-set commands use these exit codes:

| Code | Meaning |
| --- | --- |
| `0` | No lint violations. |
| `1` | Lint violations found. |
| `2` | Invalid input or configuration, or a failed run. |

`--warn` changes only `1` to `0`. Other command failures generally exit `1`.

</details>

<details>
<summary>What automatic fixes do</summary>

## Fix behavior

`ifttt --vcs git --fix` checks changes and adds placeholders for these local findings:

| Finding | Edit |
| --- | --- |
| Required target file has no matching edit (`then_missing`) | Append a `TODO(ifttt)` comment, creating the file if needed. |
| Required labelled region has no matching edit (`then_label_missing`) | Insert a TODO inside the region, or create a label block if absent. |
| Referenced label does not exist (`label_missing`) | Append a `LINT.Label` / `LINT.EndLabel` block containing a TODO. |

Fixes use the target language's comments and avoid duplicate placeholders. They write working files without staging or committing them. IFTTT Lint rejects fixes to remote targets, paths outside the workspace and symlinks. Cross-repository snapshot mode does not allow writes.

The report shows findings from before the edits. Run lint again afterward. **Replace each TODO with the required update.** A placeholder can pass the check while leaving the code or docs incorrect.

`ifttt --doctor --fix --scan .` removes duplicate targets and creates missing local label blocks. Doctor also reports unmatched opening and closing directives. It does not guess how to pair them. Plain `--scan` checks directive structure without applying fixes.

</details>

## What a check reads

A diff selects the source files to check. Their directives identify the target files and sections to read.
Some rules also need sources outside the diff: unchanged Match rules, and references to targets that were removed or renamed.
Git selects these candidates with fixed-string searches for directive text. Only the candidates and their targets need parsing.

Source discovery follows the [ignore policy](#commands). Explicit links still check Git-ignored targets.
Scans cover the selected workspace rather than a diff. Larger diffs, more links and slower storage can increase runtime.
Choose a worker count for your workload. More workers do not always make a check faster.
See [measured workloads](../README.md#benchmarks-and-correctness).

## Supported files

<!-- LINT.IfChange(language_comments) -->
Links can connect different languages and directories. Comment syntax comes from the file extension or a known filename, such as `Dockerfile`.
The [language registry](../internal/comments/languages.go) lists all built-in extensions, filename rules and string formats to skip.

| Files | Directive comments |
| --- | --- |
| C/C++, C#, Go, Java, JavaScript/TypeScript, Rust, Kotlin, Swift, Scala, Dart, Groovy, Protocol Buffers, SCSS | `//` and `/* ... */` |
| Objective-C / MATLAB (`.m`, `.mm`) | `//`, `%`, `/* ... */` |
| PHP (`.php`, `.phtml`) | `//`, `#`, `/* ... */` |
| Python, Shell, Ruby, Perl, R, YAML, TOML, GraphQL, Elixir, Make, Docker, GN, Bazel / Starlark | `#` |
| Nix | `#`, `/* ... */` |
| CMake | `#`, bracket comments such as `#[[ ... ]]` |
| PowerShell | `#`, `<# ... #>` |
| Terraform / HCL | `#`, `//`, `/* ... */` |
| Clojure, Lisp, Scheme, Racket | `;` |
| SQL | `--`, `/* ... */` |
| Lua | `--`, long comments such as `--[[ ... ]]` |
| Haskell | `--`, `{- ... -}` |
| TeX / LaTeX | `%` |
| CSS | `/* ... */` |
| HTML, XML, SVG, Markdown / MDX | `<!-- ... -->` |
| Vue, Svelte | `//`, `/* ... */`, `<!-- ... -->` |
| Go templates / Helm | `{{/* ... */}}` |
| Unknown extensions | `//`, `#`, `/* ... */` |

Both `%` and `//` work in `.m` files because MATLAB and Objective-C share this extension.
Known filenames include `CMakeLists.txt`, `Dockerfile`, `Dockerfile.*`, `Makefile`, `GNUmakefile`, `Rakefile`, `Gemfile`, `BUILD`, `BUILD.bazel` and `WORKSPACE`.

IFTTT Lint supports inline comments, single-line block comments and multiline block comments.
Put each directive on its own comment-content line. You can use a leading `*` inside a block comment:

```go
/*
 * LINT.IfChange(API)
 */
const apiVersion = 2
/*
 * LINT.ThenChange(//guide.md)
 */
```

The parser skips recognized strings, raw strings, heredocs and Markdown fenced examples.
It can read directives inside multiline comments. Do not leave active directive lines in commented-out examples.

IFTTT Lint reads Python docstring directives by default. Set `languages.python_docstrings: false` to disable them.
Assigned triple-quoted Python strings remain strings and do not create directives.
Use `--comment-style .ext=PREFIX` to add or override a line-comment format for an extension.
<!-- LINT.ThenChange(//internal/parse/languages_test.go:language_comments) -->

## Configuration

<details>
<summary>Config example, all options, overrides and custom prefixes</summary>

<!-- LINT.IfChange(readme_configuration) -->
Create `.ifttt-lint.yaml` for lint settings. The tool loads this filename automatically.
Create a separate [change-set manifest](cross-repository.md) to select repository revisions. Its filename is your choice.

<!-- example: configuration -->
```yaml
parallelism: auto
verbose: false
ignores:
  - third_party/**
  - 'examples/demo.go#EXPERIMENTAL'
directives:
  prefix: LINT
languages:
  python_docstrings: true
rules:
  unknown_directive: error       # warn|error|ignore
  combined_diff: skip_with_note  # skip_with_note|error|ignore|parent
  code_only: false
output:
  format: text                  # text|json|sarif|diagnostic-ls
  path: ""
```

Omit keys that use the defaults you need.

| Key | Values / default | Purpose |
| --- | --- | --- |
| `parallelism` | `auto` (2 workers), or a positive integer | Set the number of workers. |
| `verbose` | `false` | Show debug logs. |
| `ignores` | File globs or `file#label`, empty by default | Exclude files or specific labels. Quote patterns that start with `*`. |
| `skip_directories` | [Default skips](#commands), or your directory list | Replace the built-in directory skips. Lists from config layers merge. |
| `directives.prefix` | `LINT` | Set the directive prefix for this repository. |
| `languages.python_docstrings` | `true` | Read directives inside Python docstrings. |
| `rules.unknown_directive` | `error` by default, `warn`, `ignore` | Handle malformed or unknown directives. |
| `rules.combined_diff` | `skip_with_note`, `error`, `ignore`, `parent` | Handle combined merge diffs. |
| `rules.code_only` | `false` | Ignore comment/whitespace-only edits when deciding whether a source block changed. |
| `output.format` | `text`, `json`, `sarif`, `diagnostic-ls` | Select the report format. |
| `output.path` | Empty (standard output), or a filename | Write the report to a file. |
| `remotes[].type` | `github` | Select the remote provider. |
| `remotes[].repo` | Required `owner/name` | Identify the remote repository. |
| `remotes[].default_ref` | `main`, or a branch/tag/commit | Select a revision when the URI has no `?ref=`. |
| `remotes[].token_env` | Optional environment variable name | Read a token without storing it in YAML. |
| `remotes[].base_url` | GitHub API by default | Use a GitHub Enterprise API URL. |
| `remotes[].name` | Optional name | Name the config entry. References still select it by `repo`. |

The tool reads configuration from parent directories to the current directory. Child settings override parent settings. Ignore and skip lists merge.

Relative entries use the directory containing their configuration file. The tool normalizes them against the highest configuration directory. Explicit `verbose: false` and `rules.code_only: false` disable inherited values. CLI flags override configuration, including `--code-only=false` and `--verbose=false`.

For a child config, keep only the settings that differ. For example, **examples/.ifttt-lint.yaml** can contain:

<!-- example: child-configuration -->
```yaml
verbose: false
ignores: ['generated/**']
rules:
  code_only: false
```

Run from `examples/` to apply that layer. Its ignore pattern selects `examples/generated/**`.
To add directory skips, include the default skips you want to keep, such as `skip_directories: [.git, .jj, node_modules, vendor, generated]`.

For a custom prefix, set `directives: {prefix: SPEC}`. Its IfChange/ThenChange syntax uses quoted arguments:

<!-- example: custom-prefix -->
```go
// SPEC.IfChange("API")
const version = 2
// SPEC.ThenChange(["./guide.md#API", "./tests.go#API"])
```

Use `SPEC.Label("API")` and `SPEC.EndLabel` for named target sections.
Custom-prefix local paths are relative to the source directory. Use `#API` for the same file or `file#API` for another file.
Other extended directives use the same quoted argument forms.
Custom comment formats use a CLI option, such as `ifttt --scan . --comment-style '.tmpl=##'`.
Config does not have a `comment_style` key.
<!-- LINT.ThenChange(//cmd/ifttt/main.go:readme_configuration, //test/integration/readme_examples_test.go:readme_configuration) -->

</details>

<details>
<summary>Check a GitHub target without a change set</summary>

## Remote references

A remote reference checks whether a GitHub file and label exist:

```go
// LINT.ThenChange(github://your-org/your-repo/docs/api.md?ref=main#API)
```

Configure its provider and optional credentials:

```yaml
remotes:
  - type: github
    repo: your-org/your-repo
    default_ref: main
    token_env: GITHUB_TOKEN
```

Or repeat `--remote repo=your-org/your-repo,default_ref=main,token_env=GITHUB_TOKEN` on the CLI. `IFTTT_CACHE_DIR` selects a cache directory.

An existing remote target does not prove that someone updated it. To check matching edits, use `--change-set .ifttt-changes.yaml` with local Git/jj checkouts and explicit base/head revisions. This mode needs no GitHub credentials or network fetches. See [cross-repository change sets](cross-repository.md).

</details>

## Hooks

For [pre-commit](https://pre-commit.com/), put `ifttt` on PATH. Add these local hooks to your project's `.pre-commit-config.yaml`:

<!-- LINT.IfChange(pre_commit_hooks) -->

```yaml
repos:
  - repo: local
    hooks:
      - id: ifttt
        name: ifttt structural validation
        description: Validate directive syntax, pairing, targets and labels in staged files.
        entry: ifttt --vcs git
        language: system
        pass_filenames: true
        stages: [pre-commit]
      - id: ifttt-diff
        name: ifttt co-change validation
        description: Validate all unpushed co-changes, honoring NO_IFTTT commit-message suppression.
        entry: sh -c 'if [ -n "${PRE_COMMIT_FROM_REF:-}" ] && [ -n "${PRE_COMMIT_TO_REF:-}" ]; then exec ifttt --vcs git "$@" --diff "${PRE_COMMIT_FROM_REF}..${PRE_COMMIT_TO_REF}"; fi' --
        language: system
        pass_filenames: false
        always_run: true
        stages: [pre-push]
```

<!-- LINT.ThenChange(//scripts/test_benchmark.py:pre_commit_hooks) -->

Install both hooks:

```sh
pre-commit install
pre-commit install --hook-type pre-push
```

The `ifttt` hook checks directive structure in staged files. The `ifttt-diff` hook checks required edits across all unpushed commits before a push. Both use Git.

Manual pre-push runs without range variables do nothing. Use `pre-commit run ifttt --all-files` to check directive structure across tracked files. For a separate staged required-edit hook, use [scripts/pre-commit-ifttt.sh](../scripts/pre-commit-ifttt.sh).
