# IFTTT Lint

IFTTT Lint catches forgotten updates to related code, tests and documentation. For example, you change an API but forget its client or docs.

Link the related sections with `LINT.IfChange` and `LINT.ThenChange` comments. IFTTT Lint checks your changes and reports any linked file or section you leave untouched. It works with Git and jj, in VS Code and in CI.

It checks that required edits exist. It cannot tell whether those edits do the right thing.

- [Quick start](#quick-start)
- [VS Code extension](#vs-code-extension)
- [CLI](#cli)
- [GitHub Action and hooks](#github-action-and-hooks)
- [Coding agents](#coding-agents)
- [Cross-repository checks](#cross-repository-change-sets)
- [Benchmarks and correctness](#benchmarks-and-correctness)
- [Development](#development-and-verification)
- [Releases](#releases)
- [Contributing and license](#contributing-and-license)

## Quick start

```go
// LINT.IfChange(API)
const apiVersion = 2
// LINT.ThenChange(//docs/api.md:API)
```

This comment links `apiVersion` to the `API` section in `docs/api.md`:

```markdown
<!-- LINT.IfChange(API) -->
API version: 2
<!-- LINT.ThenChange() -->
```

Once you commit both files, changing `apiVersion` without changing that section produces a finding. Paths that start with `//` refer to the repository root.

Build from a local checkout. Use Go 1.26.8, as specified in `go.mod`:

```sh
make build
./build/ifttt --vcs git                # check local edits
./build/ifttt --vcs git --staged       # check staged co-changes
./build/ifttt --vcs git --diff main...HEAD
./build/ifttt --vcs jj --diff 'main..@'
./build/ifttt --vcs auto '**/*'        # structurally validate tracked files
```

Put `build/ifttt` on PATH to use it in other repositories. Git or jj must also be on PATH.

If a repository contains both Git and jj metadata, automatic detection chooses jj. Use `--vcs git` for Git revision ranges. Find the source and releases at [derhnyel/ifttt](https://github.com/derhnyel/ifttt).

## VS Code extension

See required updates in Problems, open linked files and check changes on save.

See the [extension README](vscode-extension/README.md) for installation, settings, editor actions and the playground.

## CLI

### Commands

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
ifttt --change-set changes.yaml --format=json
```

Git and jj commands use the repository root, even when you run IFTTT Lint from a subdirectory. For patch files or stdin, run IFTTT Lint at the repository root. These modes use the current directory.

Use `--vcs auto|git|jj` to select the backend. Automatic detection chooses jj when both exist. jj has no staging area.

<details>
<summary>More commands, options and exit codes</summary>

Source paths and quoted globs check directive syntax, target files and labels. With `--diff`, they also limit which changed sources IFTTT Lint checks. Checks for stale incoming references still search the whole repository. Repeat `--files` to select several sources.

`--scan` includes hidden and Git-ignored files. It skips symlinks. By default, it also skips VCS metadata, dependency directories and build outputs.

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

`review --base BASE HEAD` selects the revisions to compare. For more options, use `ifttt --help` or a command's `--help`.

- `watch --interval 2s` sets the time between checks.
- `watch --diff 'COMMAND'` runs a shell command that produces a diff.
- `watch --revision RANGE` selects a Git or jj revision range.
- `watch --status 'COMMAND'` runs the command before each check.
- `scaffold --preset 'BODY'` inserts the given text as a placeholder.

Common options:

- `--version`: print the executable version and source commit.
- `--format text|json|sarif|diagnostic-ls`: select output. JSON includes `workspaceRoot` for resolving finding paths.
- `--ignore PATTERN`: ignore a file or `file#label`. Repeat this option for several patterns. `*` and `?` match within one path component. `**` matches across directories.
- `--skip-dir DIR`: exclude a directory. Repeat this option for several directories.
- `--code-only`: ignore changes that affect only whitespace or comments when deciding whether a source block changed.
- `--strict=true`: require `//` root paths or same-file label selectors in standard LINT targets.
- `--warn`: report lint findings with exit status 0.
- `--list-suppressed`: report suppressed findings and exit 0. Invalid or incomplete change-set input still exits 2.
- `--fix`: add TODO placeholders or missing target label blocks. See [fix behavior](#fix-behavior).
- `--comment-style .tmpl=##`: set a custom comment prefix. Repeat this option for several extensions.
- `--combined strict|warn|ignore|parent`: handle combined merge diffs. `parent` tries to use the first parent as change evidence.
- `--verbose`, `--log-level debug|info|warn|error`, `--stats`: inspect logging and run statistics.
- `--parallel N` (aliases `--threads`, `-p`, `-t`): set the number of workers. Automatic selection uses two workers.

Lint, scan and change-set commands use these exit codes:

| Code | Meaning |
| --- | --- |
| `0` | No lint violations. |
| `1` | Lint violations found. |
| `2` | Invalid input or configuration, or a failed run. |

`--warn` changes only `1` to `0`. Other command failures generally exit `1`.

</details>

### Fix behavior

`ifttt --vcs git --fix` checks changes and adds placeholders for these local findings:

| Finding | Edit |
| --- | --- |
| Required target file has no matching edit (`then_missing`) | Append a `TODO(ifttt)` comment, creating the file if needed. |
| Required labelled region has no matching edit (`then_label_missing`) | Insert a TODO inside the region, or create a label block if absent. |
| Referenced label does not exist (`label_missing`) | Append a `LINT.Label` / `LINT.EndLabel` block containing a TODO. |

Fixes use the target language's comments and avoid duplicate placeholders. They write working files without staging or committing them. IFTTT Lint rejects fixes to remote targets, paths outside the workspace and symlinks. Cross-repository snapshot mode does not allow writes.

The CLI reports findings from before the edits. Run lint again afterward. **Replace each TODO with the actual update.** A placeholder can pass the change check while leaving the code or docs incorrect.

`ifttt --doctor --fix --scan .` removes duplicate targets and creates missing local label blocks. Doctor also reports unmatched opening and closing directives. It does not guess how to pair them. Plain `--scan` checks directive structure without applying fixes.

### Directive syntax

Put directives in the source language's comments:

```go
// LINT.IfChange(API)
const apiVersion = 2
// LINT.ThenChange(//docs/api.md:API, //client/version.go)
```

A target section can use `LINT.IfChange(API)` and an empty `LINT.ThenChange()`. The empty closing directive requires a label on the opening directive. `LINT.IfChange()` starts a source block without a label.

Write labels and targets without quotes. Separate multiple targets with commas. Target lists can span lines. IFTTT Lint ignores directive text in strings, prose and fenced examples.

By default, `//docs/api.md`, `/docs/api.md` and `docs/api.md` refer to the repository root. Bare filenames such as `api.md` refer to the source directory. Explicit `./` and `../` paths also refer to the source directory. All local paths must stay inside the workspace.

Use `:API` for a label in the same file. Use `//docs/api.md:API` for a label in another file. `--strict=true` requires `//` paths or same-file label references.

Extended directives use quoted arguments:

- `LINT.Label("API")` and `LINT.EndLabel` define a separate target region.
- `LINT.RequireAny(["one.go", "two.go"])` requires at least one target to change. `RequireAll` requires every target to change. `ForbidChange` rejects target edits. Inside an IfChange block, these rules apply when the block body changes. Outside a block, they apply when the source file changes.
- `LINT.Disable("RULE")`, `LINT.Enable("RULE")` and `LINT.Ignore("RULE")` suppress rules.

For native revision ranges, `NO_IFTTT=<reason>` in a commit message suppresses required-edit checks. The empty marker `NO_IFTTT=` also suppresses them. Directive structure and stale-reference checks still run. Change-set mode does not use commit-message suppression.

<details>
<summary>Supported files, configuration and remote references</summary>

### Supported files

Links can connect different languages and directories in one repository. IFTTT Lint reads comments in these languages and their common file extensions:

| Files | Directive comments |
| --- | --- |
| C/C++, C#, Go, Java, JavaScript/TypeScript, Rust, Kotlin, Swift, Scala, Dart, Groovy, Objective-C, PHP, Protocol Buffers, SCSS | `//` and `/* ... */` |
| Python, Shell, Ruby, Perl, R, YAML, TOML, GraphQL, Elixir, Nix, PowerShell, CMake, Make, Docker, GN, Bazel | `#` and supported block comments |
| Terraform / HCL | `#`, `//`, `/* ... */` |
| Clojure, Lisp, Scheme, Racket | `;` |
| SQL, Lua, Haskell | `--` and supported block comments |
| TeX / LaTeX | `%` |
| CSS | `/* ... */` |
| HTML, XML, SVG, Markdown / MDX | `<!-- ... -->` |
| Vue, Svelte | HTML and JavaScript comments |
| Go templates | `{{/* ... */}}` |

IFTTT Lint reads Python docstring directives by default. Set `languages.python_docstrings: false` to disable them. Use `--comment-style .ext=PREFIX` to add a line-comment format.

IFTTT Lint supports nested IfChange blocks and directives in inline and block comments. It ignores strings and Markdown fenced examples.

### Configuration

Create `.ifttt-lint.yaml` in your project:

```yaml
parallelism: auto
verbose: false
ignores:
  - third_party/**
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

IFTTT Lint reads configuration from ancestor directories down to the current directory. Child settings override parent settings. Ignore and skip lists merge.

Relative entries use the directory that contains their configuration file. IFTTT Lint normalizes these entries against the highest configuration directory. Explicit `verbose: false` and `rules.code_only: false` disable inherited values. CLI flags override configuration, including `--code-only=false` and `--verbose=false`.

Additional YAML keys:

- `skip_directories`: directories to exclude.
- `directives.prefix`: custom directive prefix. The default is `LINT`.
- `output.path`: file to receive the report.
- Remote provider `base_url`: GitHub Enterprise API endpoint.

### Remote references

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

An existing remote target does not prove that someone updated it. To check matching edits, use `--change-set changes.yaml` with local Git/jj checkouts and explicit base/head revisions. This mode needs no GitHub credentials or network fetches. See [cross-repository change sets](#cross-repository-change-sets).

</details>

## GitHub Action and hooks

The GitHub Action builds IFTTT Lint from source and uses Git. On pull requests, it checks changes across the full PR range. On push, it checks directive structure in all tracked files. Replace `<ref>` with a release tag or commit:

```yaml
on: [push, pull_request]
permissions:
  contents: read
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          ref: ${{ github.event.pull_request.head.sha || github.sha }}
          fetch-depth: 0
      - uses: derhnyel/ifttt@<ref>
        with:
          args: --format=json
```

The checkout must match the PR head, as shown above. Use current GitHub-hosted runners. Self-hosted runners must support the pinned actions' Node.js 24 runtime.

Optional inputs are `diff`, `change-set`, `working-directory` and `go-version`. `diff` accepts a patch file, not stdin.

Separate `args` with whitespace, including in multiline YAML values. The Action does not apply shell quoting or expansion. Use the dedicated path inputs for paths with spaces. `change-set` needs all declared checkouts and revisions. It cannot combine with `diff`.

For [pre-commit](https://pre-commit.com/), put `ifttt` on PATH. Add the repository's system hooks:

```yaml
repos:
  - repo: https://github.com/derhnyel/ifttt
    rev: <ref>
    hooks:
      - id: ifttt
      - id: ifttt-diff
```

Install both hooks:

```sh
pre-commit install
pre-commit install --hook-type pre-push
```

The `ifttt` hook checks directive structure in staged files. The `ifttt-diff` hook checks required edits across all unpushed commits before a push. Both use Git.

Manual pre-push runs without range variables do nothing. Use `pre-commit run ifttt --all-files` to check directive structure across tracked files. For a separate staged required-edit hook, use [scripts/pre-commit-ifttt.sh](scripts/pre-commit-ifttt.sh).

## Coding agents

Follow [AGENTS.md](AGENTS.md) to add narrow dependency links, check changes and make the required updates.
The required `Lint` job checks directive structure and matching edits across pull requests and merge queues.

## Cross-repository change sets

Use `--change-set` when linked files live in different repositories. Declare local Git/jj checkouts and the base/head commits to compare. IFTTT Lint reads committed snapshots. Uncommitted working files cannot satisfy a dependency.

An API source can require a labelled contract update:

```go
// LINT.IfChange(API)
func Version() string { return "v2" }
// LINT.ThenChange(github://acme/contracts/schema.md#API)
```

The target Markdown file defines that region:

```markdown
<!-- LINT.IfChange(API) -->
API version: v2
<!-- LINT.ThenChange() -->
```

Declare both checkout roots in `changes.yaml`. Git and jj can participate in the same manifest:

```yaml
version: 1
repositories:
  - repo: acme/api
    path: ./api
    vcs: git
    base: main
    head: feature/api-change
  - repo: acme/contracts
    path: ./contracts
    vcs: jj
    base: main
    head: '@'
```

```sh
ifttt --change-set changes.yaml --format=json
```

<details>
<summary>Change-set rules and limitations</summary>

Manifest paths are relative to the manifest file. Each must identify a different checkout root. All checkouts and revisions must already exist locally.

You choose the repository IDs. They ignore letter case and do not come from remote settings. `vcs` accepts `git`, `jj` or `auto`. Both `base` and `head` must resolve to exactly one commit. Use full commit IDs to reproduce the same result.

IFTTT Lint checks changed sources in every declared repository. It reports missing target edits, edits outside required labels, removed targets and renamed labels. It also checks incoming references from sources that did not change. `RequireAny`, `RequireAll` and `ForbidChange` use the same cross-repository evidence.

Write LINT targets without quotes. Remote URIs use `#label`. Local targets use `//path:label`. An optional remote `?ref=` must match the declared target head. Undeclared repositories and conflicting refs fail.

Ordinary remote checks test whether a file and label exist. Change sets check matching edits without credentials or network fetches.

The current run's configuration applies to every snapshot. IFTTT Lint does not load each checkout's configuration separately.

Change-set mode cannot combine with these inputs or commands:

- `--vcs`, `--diff` or `--staged`.
- Positional patch/source inputs or `--files`.
- Scan, doctor, fix or explain.

Commit messages do not suppress checks in this mode. Directive suppression and configured ignores still apply.

IFTTT Lint supports only regular file snapshots. Symlinks, submodules, conflicts, unavailable snapshots, divergent jj operations and incomplete change evidence fail.

Binary edits can satisfy whole-file dependencies. Labelled dependencies require evidence of changed lines. The checker cannot prove compatibility, manage release order or merge repositories together as one operation.

Exit codes are `0` for satisfied dependencies and `1` for lint violations. Invalid input/configuration or unavailable/incomplete snapshot evidence exits `2`. `--warn` changes only `1` to `0`. `--list-suppressed` keeps `2`. Findings include the source repository and resolved base/head revisions.

</details>

### Editor and CI

For editor setup, see [cross-repository checks in the extension README](vscode-extension/README.md#cross-repository-checks).

Check out every declared repository and both revisions first. Then run the Action. Replace `<ref>` with your published Action tag or commit:

```yaml
- uses: derhnyel/ifttt@<ref>
  with:
    change-set: changes.yaml
    args: --format=json
```

The Action does not fetch repositories in this mode. It rejects a simultaneous `diff` input. Manifest and checkout paths can contain spaces. See [Action usage](#github-action-and-hooks).

## Benchmarks and correctness

Measured on **2026-10-05** with an Apple M1 Max (arm64), macOS 26.1 and Go 1.26.8. The Rust comparison uses [ifttt-lint v0.11.2](https://github.com/simonepri/ifttt-lint/releases/tag/v0.11.2).

Times are medians of 21 runs after 3 warmups, with warm filesystem caches. Native Git timings include Git operations and process startup. They exclude builds. Both tools passed independent checks for expected exit codes and finding counts in these fixtures.

| Workload | IFTTT Lint (Go) | ifttt-lint (Rust) | Result |
| --- | ---: | ---: | --- |
| 1 changed dependency pair, 10,000 unrelated files | 102.67 ms | 452.05 ms | **Go 4.40× faster** |
| 1,000 pairs, matching edits | 229.74 ms | 313.12 ms | **Go 1.36× faster** |
| 1,000 pairs, missing target edits | 118.17 ms | 181.38 ms | **Go 1.53× faster** |
| 1,000 pairs, structural validation | 109.78 ms | 50.68 ms | Rust 2.17× faster |

Real-repository scans use the same machine, **2 workers**, `--strict=false`, 21 runs and 3 warmups. These scans check directive structure. Directive-file scans select tracked files that contain `LINT.` and exclude discovery time. Chromium uses upstream's smoke exclusions: `depot/*`, `<INTERNAL>/*` and `<ROOT_DIR>/*`.

| Repository / workload | IFTTT Lint (Go) median | ifttt-lint (Rust) median | Findings Go / Rust |
| --- | ---: | ---: | ---: |
| [Chromium](https://github.com/chromium/chromium/commit/f7a8030b4c5ad01f8bad7e1e392709a3fbf121dd): 2,416 directive files | 1,020.34 ms | 1,857.58 ms | 480 / 518 |
| [TensorFlow](https://github.com/tensorflow/tensorflow/commit/031dd1d53ac37fbb438eda763678c03c985db8cd): 251 directive files | 107.25 ms | 352.80 ms | 91 / 117 |
| TensorFlow: all 37,098 tracked files | 1,835.44 ms | 1,339.66 ms | 91 / 117 |

The real-repository scans report different findings, so their times do not prove a speedup for equivalent checks. Rust is faster on TensorFlow's full scan.

**Correctness:** In isolated fixtures, IFTTT Lint supports nested blocks and inline/block-comment directives that v0.11.2 rejects. It reports ambiguous target labels and empty directives that have no effect. [Cross-repository checks](#cross-repository-change-sets) use exact committed snapshots, so dirty files cannot satisfy dependencies. Regression tests cover the upstream closed-issue cases we audited.

The pinned TensorFlow audit produced **91 Go findings and 117 Rust findings, with 82 matching**. Path and parsing policies explain the differences. These include 26 Rust traversal errors for paths that stay inside the repository. Finding counts do not measure accuracy. Results depend on hardware and workload.

<details>
<summary>Reproduce the benchmarks</summary>

### Reproduce

Run these commands from the repository root. The scripts check findings before timing. Each JSON report records binary hashes, commands and samples. Different findings do not establish equivalent checking performance.

```sh
make build
python3 scripts/benchmark_upstream.py
python3 scripts/benchmark.py --go build/ifttt --upstream build/upstream/ifttt-lint
python3 scripts/benchmark_report.py build/benchmarks/comparison.json
python3 scripts/benchmark_scaling.py --go build/ifttt --upstream build/upstream/ifttt-lint
make benchmark-changeset
```

For the repository survey, clone both inputs into ignored local storage:

```sh
git clone --depth 1 https://github.com/chromium/chromium build/benchmark-repos/chromium
git clone --depth 1 https://github.com/tensorflow/tensorflow build/benchmark-repos/tensorflow
git -C build/benchmark-repos/chromium fetch --depth 1 origin f7a8030b4c5ad01f8bad7e1e392709a3fbf121dd
git -C build/benchmark-repos/chromium checkout --detach FETCH_HEAD
git -C build/benchmark-repos/tensorflow fetch --depth 1 origin 031dd1d53ac37fbb438eda763678c03c985db8cd
git -C build/benchmark-repos/tensorflow checkout --detach FETCH_HEAD
make benchmark-repositories
```

The report records the actual revisions. Pin the same commits when comparing runs. The scripts restore the original configuration after each run.

Reports default to `build/benchmarks`. Binaries, downloaded tools and repository checkouts also stay under ignored `build/`. Do not commit generated reports, profiles or test output. Commit test sources, fixtures and benchmark scripts.

</details>

## Development and verification

Use the Go toolchain pinned in `go.mod`, Node.js 22+, Python 3.11+, Git and Bash.

```sh
make test-tools         # install pinned, checksum-verified jj for native tests
make build             # build/ifttt
make check             # format, vet, race tests, CLI smoke, extension tests, audits and workflow lint
make extension-host    # actual VS Code host workflows for Git and jj
make coverage          # union unit coverage with the actual CLI subprocess coverage
```

Set `IFTTT_VSCODE_BINARY` if VS Code is not discovered automatically. Linux host tests need a graphical session or `xvfb-run`.

The Tests workflow runs Go race and integration checks on Linux, macOS and Windows. It also runs Python automation tests, extension tests and Git/jj editor host tests. The Lint workflow also checks IFTTT Lint directives and required linked edits. It checks formatting, module metadata, workflows and vulnerabilities.

The `CI` gate fails if any test job fails, skips or cancels. Pull requests and merge queues run both workflows.

Keep generated reports, coverage, binaries, downloaded checkouts/tools, VSIX packages and working notes in ignored local directories. Commit source, meaningful tests, fixtures and dependency lockfiles. See [CONTRIBUTING](CONTRIBUTING.md) for review and repository policies.

## Releases

Releases use version tags (`vMAJOR.MINOR.PATCH`) and include the changes from [CHANGELOG](CHANGELOG.md). A merged version PR with the `release` label starts the release pipeline; it can also run manually. Tests, lint, security checks and native binary checks must pass before publication.

Release builds include CLI binaries for Linux, macOS and Windows (amd64/arm64), a VS Code extension package (`.vsix`) and `SHA256SUMS`.

Download published packages from [GitHub releases](https://github.com/derhnyel/ifttt/releases). Check downloaded files against `SHA256SUMS`. See [extension installation](vscode-extension/README.md#install) for VSIX setup.

## Contributing and license

Use the GitHub templates to report issues. Follow [CONTRIBUTING](CONTRIBUTING.md) for pull requests and checks. See [CHANGELOG](CHANGELOG.md) for user-visible changes.

Licensed under [MIT](LICENSE).
