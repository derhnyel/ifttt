# IFLint

Check `LINT.IfChange` / `LINT.ThenChange` comments to require matching edits in dependent files. The repository includes a CLI, a VS Code extension and a reusable GitHub Action.

- [Quick start](#quick-start)
- [CLI](#cli)
- [GitHub Action and hooks](#github-action-and-hooks)
- [Cross-repository change sets](#cross-repository-change-sets)
- [Install the VS Code extension](#install-the-vs-code-extension)
- [Development and verification](#development-and-verification)
- [Benchmarks and correctness](#benchmarks-and-correctness)
- [Releases](#releases)
- [Contributing and license](#contributing-and-license)

## Quick start

```go
// LINT.IfChange(API)
const apiVersion = 2
// LINT.ThenChange(//docs/api.md:API)
```

The target region in `docs/api.md` uses `LINT.IfChange(API)` and `LINT.ThenChange()` inside Markdown comments.

Build from a local checkout with the Go toolchain pinned in `go.mod` (Go 1.26.8):

```sh
make build
./build/iflint --vcs git --staged       # check staged co-changes
./build/iflint --vcs git --diff main...HEAD
./build/iflint --vcs jj --diff 'main..@'
./build/iflint --vcs auto '**/*'        # structurally validate tracked files
```

Install `build/iflint` on your PATH for use in other repositories. Native Git/jj workflows require the selected VCS executable on PATH. Auto-detection prefers jj in colocated repositories; choose `--vcs git` for Git revision syntax. Source and releases are hosted at [derhnyel/ifttt](https://github.com/derhnyel/ifttt).

## CLI

### Commands

```sh
iflint --vcs git --staged                 # staged co-changes
iflint --vcs git --diff main...HEAD        # Git revision range
iflint --vcs jj --diff 'main..@'           # jj revset
iflint --vcs auto '**/*'                   # all tracked files, structural checks
iflint --vcs git path/to/source.go         # selected source, structural checks
iflint --scan .                           # discover and validate directive files
iflint --doctor                           # report orphan directives
iflint patch.diff                         # unified diff file
git diff --cached | iflint -              # unified diff on stdin
iflint --change-set changes.yaml --format=json
```

Native workflows resolve paths from the detected repository root, including when invoked from a subdirectory. Diff-file and stdin workflows use the invocation directory; run them at the repository root. `--vcs auto|git|jj` selects the backend; auto prefers jj in colocated repositories. jj has no staging area.

Source paths and quoted globs select structural validation. With `--diff`, they scope changed-source checks; reverse-reference checks still search globally. Repeat `--files` to select sources explicitly. `--scan` includes hidden and Git-ignored files, skips symlinks, and excludes VCS metadata, dependency directories and build outputs by default.

| Command | Purpose |
| --- | --- |
| `iflint review --vcs git main...HEAD` | Check a native Git range; `--vcs jj` accepts jj revsets. |
| `iflint watch --vcs git --staged` | Poll staged changes; use `--revision RANGE` for revisions or omit `--staged` for working changes. |
| `iflint jump target.go API` | Open a label using `$EDITOR`, `code --goto`, or print its location. |
| `iflint jump --print-location -- target.go API` | Print JSON with an absolute file path and 1-based line. |
| `iflint scaffold --source source.go --target target.go#API --label API` | Write paired directive stubs; `--source-only` or `--target-only` limits writes. |
| `iflint ignore add --file source.go --rule then_missing --line 10` | Insert a suppression comment. |
| `iflint blame source.go` | List unlabelled IfChange directives with Git blame metadata. |
| `iflint explain then_missing` | Show a rule's severity and remediation. |

Common options:

- `--version`: print the executable version and source commit.
- `--format text|json|sarif|diagnostic-ls`: select output. JSON includes `workspaceRoot` for resolving finding paths.
- `--ignore PATTERN`: ignore a file or `file#label`; repeatable. `*` and `?` match within a path component, while `**` matches recursively.
- `--skip-dir DIR`: exclude a directory; repeatable.
- `--code-only`: ignore whitespace/comment-only changes in triggers.
- `--strict=true`: require `//` root paths or same-file label selectors in standard LINT targets.
- `--warn`: report lint findings with exit status 0.
- `--list-suppressed`: report suppressed findings and exit 0; change-set input/evidence failures still exit 2.
- `--fix`: write placeholder directives for missing targets. Review the edits; placeholders do not fulfill the synchronization contract.
- `--comment-style .tmpl=##`: set a custom comment prefix; repeatable.
- `--combined strict|warn|ignore|parent`: handle combined merge diffs; `parent` uses best-effort first-parent evidence.
- `--verbose`, `--log-level debug|info|warn|error`, `--stats`: inspect logging and run statistics.
- `--parallel N` (aliases `--threads`, `-p`, `-t`): set workers; automatic selection uses two workers.

For main lint, scan and change-set invocations, exit statuses are `0` for success, `1` for lint violations and `2` for invalid input/configuration or execution failures. `--warn` changes only status `1` to `0`. Auxiliary command failures generally exit `1`.

### Directive syntax

Put directives in the source language's comments:

```go
// LINT.IfChange(API)
const apiVersion = 2
// LINT.ThenChange(//docs/api.md:API, //client/version.go)
```

The target can define its region with `LINT.IfChange(API)` and an empty `LINT.ThenChange()`. An empty closer requires a labelled opener. `LINT.IfChange()` opens an unlabelled source block. Labels and targets are bare arguments; comma-separated target lists may span lines. Strings, prose mentions and fenced examples are inactive.

In default permissive mode, `//docs/api.md`, `/docs/api.md` and `docs/api.md` resolve from the repository root. A bare filename such as `api.md`, or explicit `./` and `../` paths, resolves from the source directory and must stay inside the workspace. Local `:API` selects a same-file label; `//docs/api.md:API` selects a label in another file. `--strict=true` requires `//` paths or same-file selectors.

Extended directives use quoted arguments:

- `LINT.Label("API")` and `LINT.EndLabel` define a separate target region.
- `LINT.RequireAny(["one.go", "two.go"])` requires at least one changed target; `RequireAll` requires every target; `ForbidChange` rejects target edits. Inside an IfChange block they activate when the body changes; standalone rules activate when the source file changes.
- `LINT.Disable("RULE")`, `LINT.Enable("RULE")` and `LINT.Ignore("RULE")` suppress rules.

Native revision ranges honor `NO_IFTTT=<reason>` in commit messages for co-change suppression; structural and reverse-reference checks remain active. The literal empty `NO_IFTTT=` marker also suppresses. Change-set mode does not use commit-message suppression.

### Configuration

Create `.ifttt-lint.yaml` in your project:

```yaml
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

Configuration cascades from filesystem ancestors down to the invocation directory. Child settings override parents; ignore and skip lists merge. Relative entries are resolved from their defining configuration directory and normalized against the topmost configuration directory. Explicit `verbose: false` and `rules.code_only: false` disable inherited values. CLI flags override configuration, including explicit `--code-only=false` and `--verbose=false`. See [the example configuration](.ifttt-lint.example.yaml).

### Remote references

Ordinary remote references validate that a GitHub target and label exist:

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

Or repeat `--remote repo=your-org/your-repo,default_ref=main,token_env=GITHUB_TOKEN` on the CLI. `IFLINT_CACHE_DIR` selects a cache directory.

Remote readability cannot demonstrate matching edits in another repository. Use `--change-set changes.yaml` to compare explicit local Git/jj base/head snapshots and verify foreign target edits, without GitHub credentials or network fetches. See [the cross-repository guide](#cross-repository-change-sets) for setup and limitations.

## GitHub Action and hooks

The reusable GitHub Action builds its source, checks full PR revision ranges and validates all tracked files structurally on push. It explicitly selects Git. Choose a release tag or commit for `<ref>`:

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

PR checks require the checkout to match the PR head, as shown above. Use current GitHub-hosted runners; self-hosted runners must support the pinned actions' Node.js 24 runtime.

Optional inputs are `diff` (patch file; stdin is unavailable), `change-set`, `working-directory` and `go-version`. `args` is whitespace-separated, including multiline YAML values, without shell quoting or expansion; use dedicated path inputs for spaces. `change-set` requires all declared checkouts/revisions and cannot combine with `diff`.

For [pre-commit](https://pre-commit.com/), install `iflint` on PATH and use the repository's system hooks:

```yaml
repos:
  - repo: https://github.com/derhnyel/ifttt
    rev: <ref>
    hooks:
      - id: iflint
      - id: iflint-diff
```

Run `pre-commit install` and `pre-commit install --hook-type pre-push`. `iflint` structurally validates staged files; `iflint-diff` checks the full unpushed range on pre-push. Both select Git. Manual pre-push runs without range variables do nothing; use `pre-commit run iflint --all-files` for structural validation. A standalone staged co-change hook is also available at [scripts/pre-commit-iflint.sh](scripts/pre-commit-iflint.sh).

## Cross-repository change sets

Use `--change-set` to require matching edits across explicit local Git/jj checkouts. The checker reads committed base/head snapshots, so dirty working files cannot satisfy a dependency.

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
iflint --change-set changes.yaml --format=json
```

Paths resolve relative to the manifest and must identify distinct checkout roots. Repository IDs are case-insensitive caller-defined aliases; they are not inferred from remotes. `vcs` accepts `git`, `jj` or `auto`. Both `base` and `head` must resolve to exactly one commit; full immutable commit IDs help reproduce results. All checkouts and endpoints must already be available. See [the example manifest](.ifttt-changes.example.yaml).

Changed sources in every participating repository are checked. Missing target edits, edits outside a required label, deleted targets and renamed labels fail. Incoming references are checked even when their sources are unchanged. `RequireAny`, `RequireAll` and `ForbidChange` also use cross-repository evidence.

LINT targets use bare arguments: remote URIs use `#label`, while local targets use `//path:label`. An optional remote `?ref=` must match the declared target head; undeclared repositories and conflicting refs fail. Ordinary remote validation checks readability/labels; change sets check actual matching edits without credentials or network fetches.

The invocation's configuration applies to every snapshot; per-checkout configuration is not loaded separately. Change-set mode cannot combine with `--vcs`, `--diff`, `--staged`, positional patch/source inputs, `--files`, scan/doctor, fix or explain. Commit-message suppression is not applied; directive suppression and configured ignores remain available.

Only regular file snapshots are supported. Symlinks, submodules, conflicts, unavailable snapshots, divergent jj operations and incomplete change evidence fail. Whole-file binary edits can satisfy whole-file dependencies; labelled dependencies require line evidence. The checker verifies required edits, not semantic compatibility, release order or atomic merges across repositories.

Exit statuses are `0` for satisfied contracts, `1` for lint violations and `2` for invalid input/configuration or unavailable/incomplete snapshot evidence. `--warn` changes only `1` to `0`; `--list-suppressed` preserves `2`. Findings include the source repository and resolved base/head revisions.

### Editor and CI

Set VS Code's `iftttLint.changeSet` to the manifest path, relative to `iftttLint.workingDirectory`. Snapshot diagnostics show revision identity and allow opening checkout files. Fixes, scaffolding and label jumps are disabled; displayed lines describe the selected head and may differ from dirty files. This setting cannot combine with `iftttLint.diffCommand`. See [extension settings](#settings).

After checking out every declared repository and both endpoints, invoke the reusable Action. Replace the repository/ref placeholders with your published Action:

```yaml
- uses: derhnyel/ifttt@<ref>
  with:
    change-set: changes.yaml
    args: --format=json
```

The Action performs no repository fetches in this mode and rejects a simultaneous `diff` input. Manifest and checkout paths support spaces. See [Action usage](#github-action-and-hooks) and [release setup](#releases).

## VS Code extension

Run `iflint` in VS Code to show diagnostics, rule explanations, a findings tree, target navigation and code lenses. Scaffolding and placeholder fixes can modify local files.

### Install the VS Code extension

Requires VS Code 1.88+, Node.js 22+, npm and the Go toolchain listed in [quick start](#quick-start).

1. Build the CLI from the repository root:

   ```sh
   make build
   ```

   In VS Code Settings, set **IFTTT Lint › Binary** (`iftttLint.binary`) to the absolute path of `build/iflint`, or put `iflint` on PATH.

2. Package the extension from the repository root:

   ```sh
   cd vscode-extension
   npm ci
   npm run package:local
   ```

3. Open the Command Palette, run **Extensions: Install from VSIX...** and select the generated `.vsix` in `vscode-extension/`. Open a trusted Git or jj workspace and run **IFTTT Lint: Run**. Findings appear in Problems and the **IFLint Findings** tree; linting also runs on save by default.

Marketplace publication requires registration of the `derhnyel` publisher and its publishing token; see [release setup](#releases).

Automatic CLI download is disabled by default. To opt in, set `iftttLint.downloadBaseUrl` to an HTTPS release directory containing `iflint-<os>-<arch>` binaries (`.exe` on Windows) and `SHA256SUMS`. The extension requires a matching checksum and checks cached binaries before reuse. A configured executable or CLI on PATH takes precedence.

### Settings

| Setting | Behavior |
| --- | --- |
| `iftttLint.binary` | Executable path or PATH command; defaults to `iflint`. |
| `iftttLint.vcs` | `auto` (default), `git` or `jj`; auto prefers jj in colocated repositories. |
| `iftttLint.diffMode` | `working-tree` (default) or `staged` (Git only). |
| `iftttLint.revision` | Git revision/range or jj revset; cannot combine with staged mode. |
| `iftttLint.changeSet` | Optional snapshot manifest, relative to `workingDirectory`. |
| `iftttLint.workingDirectory` | Defaults to the selected workspace folder; relative paths resolve from that folder. |
| `iftttLint.args` | Additional CLI argument array; JSON output is enforced for diagnostics. |
| `iftttLint.runOnSave` | Run after saves; enabled by default. |
| `iftttLint.skipDirectories` | Directory exclusions passed to the CLI. |
| `iftttLint.verboseLogging` | Show details in the IFLint output channel. |
| `iftttLint.diffCommand` | Optional shell command producing a Git-format diff; overrides native selection. |
| `iftttLint.downloadBaseUrl` | Optional HTTPS binary/checksum directory. |

Commands require a trusted filesystem workspace. Manual runs use the active editor's workspace folder and its settings; saves use the saved document's folder. Diagnostics and actions retain their originating settings in multi-root workspaces. CLI subprocesses have a 30-second deadline. Review scaffold and fix edits before committing.

`iftttLint.changeSet` validates committed Git/jj snapshots and shows repository/revision identity in findings. Every declared checkout must already exist locally. Snapshot findings allow opening checkout files; fixes, scaffolding and label jumps are disabled. Displayed lines refer to the selected head revision and may differ from dirty working files. The extension does not check out revisions. Change-set mode cannot combine with `diffCommand` or `--fix`; folders sharing a manifest share one report.

See [CLI configuration](#configuration), [cross-repository setup](#cross-repository-change-sets) and [development and host tests](#development-and-verification).

### Extension playground

`test/extension-playground` contains a tiny repo you can use for smoke-testing the VS Code extension with default standard LINT directives. `source.go` uses `LINT.IfChange(LBL)` and the same-directory dependency `LINT.ThenChange(target.go:LBL)`. `target.go` defines its region with `LINT.IfChange(LBL)` and `LINT.ThenChange()`.


```bash
cd test/extension-playground
git init
git add .
git commit -m "baseline"
code .
```

Inside VS Code run **IFTTT Lint: Run** (from the Command Palette) to populate diagnostics, or edit `source.go` to reproduce missing target-change diagnostics for quick-fix testing. From the playground directory, use `scripts/reset.sh` to recreate the repository with the current files as its baseline. Restore any edited fixture contents before running it if you want the original baseline.

## Development and verification

Use the Go toolchain pinned in `go.mod`, Node.js 22+, Python 3.11+, Git and Bash.

```sh
make test-tools         # install pinned, checksum-verified jj for native tests
make build             # build/iflint
make check             # format, vet, race tests, CLI smoke, extension tests, audits and workflow lint
make extension-host    # actual VS Code host workflows for Git and jj
make coverage          # union unit coverage with the actual CLI subprocess coverage
```

Set `IFLINT_VSCODE_BINARY` if VS Code is not discovered automatically. Linux host tests need a display or `xvfb-run`.

The Tests workflow runs Go race/integration checks on Linux, macOS and Windows, Python automation tests, extension tests and Git/jj editor host workflows. The Lint workflow checks formatting, module metadata, workflows and vulnerabilities. The `CI` gate fails if any test job fails, is skipped or is cancelled; pull requests and merge queues run both workflows.

Keep generated reports, coverage, binaries, downloaded checkouts/tools, VSIX packages and working notes in ignored local directories. Commit source, meaningful tests, fixtures and dependency lockfiles. See [CONTRIBUTING](CONTRIBUTING.md) for review and repository policies.

## Benchmarks and correctness

Recorded on **2026-10-05**, Apple M1 Max (arm64), macOS 26.1: Go 1.26.8 vs the Rust [ifttt-lint v0.11.2](https://github.com/simonepri/ifttt-lint/releases/tag/v0.11.2) binary. Medians of 21 runs after 3 warmups, with warm filesystem caches. Native Git timings include Git operations and process startup; builds are excluded. Both tools passed independent expected exit-status and finding-count checks for these fixtures.

| Workload | IFLint (Go) | ifttt-lint (Rust) | Result |
| --- | ---: | ---: | --- |
| 1 changed dependency pair, 10,000 unrelated files | 102.67 ms | 452.05 ms | **Go 4.40× faster** |
| 1,000 pairs, matching edits | 229.74 ms | 313.12 ms | **Go 1.36× faster** |
| 1,000 pairs, missing target edits | 118.17 ms | 181.38 ms | **Go 1.53× faster** |
| 1,000 pairs, structural validation | 109.78 ms | 50.68 ms | Rust 2.17× faster |

**Correctness strengths:** IFLint supports nested blocks and inline/block-comment directives rejected by v0.11.2 in our isolated fixtures. It reports ambiguous target labels and ineffective empty directives, and [cross-repository checks](#cross-repository-change-sets) use exact committed snapshots so dirty files cannot satisfy dependencies. Regression tests cover the audited upstream closed-issue cases.

The [pinned TensorFlow](https://github.com/tensorflow/tensorflow/commit/031dd1d53ac37fbb438eda763678c03c985db8cd) audit produced **91 Go findings vs 117 Rust findings, with 82 matching**. Different path and parsing policies explain differences, including 26 Rust traversal errors for paths that stay inside the repository. Counts are not an accuracy score; this audit is excluded from speed claims. Results depend on hardware and workload.

### Reproduce

Run from the repository root. Harnesses check diagnostics before timing and preserve binary hashes, commands and samples in each JSON report. Different diagnostics do not establish equivalent validation performance.

```sh
make build
python3 scripts/benchmark_upstream.py
python3 scripts/benchmark.py --go build/iflint --upstream build/upstream/ifttt-lint
python3 scripts/benchmark_report.py build/benchmarks/comparison.json
python3 scripts/benchmark_scaling.py --go build/iflint --upstream build/upstream/ifttt-lint
make benchmark-changeset
```

For the repository survey, clone both inputs into ignored local storage:

```sh
git clone --depth 1 https://github.com/chromium/chromium build/benchmark-repos/chromium
git clone --depth 1 https://github.com/tensorflow/tensorflow build/benchmark-repos/tensorflow
make benchmark-repositories
```

The report records the actual revisions; pin the same commits for comparisons between runs. Benchmark configuration is temporary and restored after the run.

Results default to `build/benchmarks`; binaries, downloaded tools and repository checkouts also stay under ignored `build/`. Do not commit generated reports, profiles or test output. Test sources, fixtures and benchmark harnesses belong in version control.

## Releases

The CLI, VSIX and GitHub Action are distributed from one repository. Release builds produce Linux, macOS and Windows binaries for amd64/arm64, a VSIX and `SHA256SUMS`. Publication uses the prepared assets without rebuilding.

### One-time setup

1. Keep the release identity consistent: the Go module/imports use `github.com/derhnyel/ifttt`, and the extension repository is `https://github.com/derhnyel/ifttt.git`.
2. Register the `derhnyel` publisher configured in `vscode-extension/package.json` before publishing to VS Code Marketplace. The project uses MIT; the release workflow copies the root `LICENSE` into the VSIX. Local installation does not require Marketplace registration.
3. Configure a GitHub environment named `release` with required reviewers. Restrict release tags to maintainers. Put `VSCE_PAT` in that environment only if Marketplace publishing is wanted; use a token authorized for the configured publisher. GitHub uploads use the job-scoped `GITHUB_TOKEN`.
4. Run the repository's Tests and Lint workflows on GitHub and exercise the Action from another repository. Local builds do not substitute for these checks.

Protect the default branch with the `CI` and `Lint` status checks once they have run. Both workflows also handle merge queues. Weekly Dependabot updates cover Go, npm and the commit-pinned GitHub Actions.

Use the [VS Code publishing guide](https://code.visualstudio.com/api/working-with-extensions/publishing-extension) for publisher setup. Release preflight rejects placeholder repository, module and publisher identities.

### Prepare a release

Set the extension version to the tag's numeric version: `0.1.0` for either `v0.1.0` or `v0.1.0-rc.1`. Record changes in `CHANGELOG.md`, including migrations. Commit all release source before tagging.

```sh
python3 scripts/release.py check --repository derhnyel/ifttt --tag v0.1.0
make check
git tag v0.1.0
git push origin v0.1.0
```

Pushing a `v*` tag starts **Prepare Release**. To retry an existing tag:

```sh
gh workflow run release.yml -f tag=v0.1.0
```

The workflow pins the tagged commit, runs the reusable test/lint workflows, builds versioned assets, packages a licensed VSIX and validates the exact checksum manifest. Six native runner jobs execute each downloaded binary's version and structural success/failure probes. Only after those jobs pass does it create or update a draft GitHub Release. It rejects attempts to overwrite a published release or reuse a release for a different commit.

Review the draft's generated notes, migration guidance, assets and checksums. Before the first public release, install the downloaded CLI and VSIX in a separate checkout and verify the published Action reference from a consumer repository.

### Publish

Run **Publish Release** after reviewing the draft:

```sh
gh workflow run publish.yml -f tag=v0.1.0 -f marketplace=false
```

Approve the `release` environment job. The workflow downloads the prepared assets, checks hashes, VSIX identity, native behavior and the current remote tag/commit, then publishes the GitHub draft. Prerelease tags stay prereleases and are not marked latest. An already published matching release is left unchanged, permitting a retry of a failed Marketplace step.

Set `marketplace=true` to pass the exact verified asset bytes between jobs through a workflow artifact and publish the VSIX with the environment's `VSCE_PAT`. Marketplace publication in this workflow requires a stable tag. A Marketplace failure does not roll back an already published GitHub release; rerun publication after fixing the failure.

The protected environment must actually be configured in GitHub; declaring its name in YAML does not establish required reviewers. Consider enabling [immutable releases](https://docs.github.com/en/repositories/releasing-projects-on-github/about-releases) before publishing.

The root module uses the same `v0.1.0` release tag for versioned Go installs. Users can run `go install github.com/derhnyel/ifttt/cmd@v0.1.0` (the executable is named `cmd`). The downloadable binary is named `iflint`.

### Local release verification

```sh
RELEASE_VERSION=v0.1.0 RELEASE_COMMIT="$(git rev-parse HEAD)" make release-build
make release-verify
python3 scripts/release.py verify --smoke --tag v0.1.0 --commit "$(git rev-parse HEAD)"
```

The output is isolated in `build/release`; a failed build preserves the preceding successful directory. `RELEASE_VERSION` defaults to `dev`, and the commit defaults to Git HEAD or `unknown` outside a committed checkout. Native binaries report both through `--version`.

Checksums verify downloaded bytes against the release manifest. They are not an independent publisher signature. To use opt-in extension downloads, point `iftttLint.downloadBaseUrl` at the versioned HTTPS release directory containing the exact binary names and `SHA256SUMS`.

## Contributing and license

Report issues using the GitHub templates and follow [CONTRIBUTING](CONTRIBUTING.md) for pull requests and verification. User-visible changes are recorded in [CHANGELOG](CHANGELOG.md).

Licensed under [MIT](LICENSE).
