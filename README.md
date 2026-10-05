# IFTTT Lint

When you change code, related tests or docs may need changes too. IFTTT Lint reports the linked updates you missed.

Use `LINT.IfChange` and `LINT.ThenChange` comments to link sections. Check your changes with the CLI, VS Code or CI. Use Git or jj.

The tool checks for required edits. Tests and review must check whether the edits work.

- [When to use it](#when-to-use-it)
- [Quick start](#quick-start)
- [VS Code extension](#vs-code-extension)
- [CLI](#cli)
- [Directives and examples](docs/directives.md)
- [Configuration](#configuration)
- [GitHub Action and hooks](#github-action-and-hooks)
- [Coding agents](#coding-agents)
- [Cross-repository checks](#cross-repository-change-sets)
- [Benchmarks and correctness](#benchmarks-and-correctness)
- [Development](#development-and-verification)
- [Releases](#releases)
- [Contributing and license](#contributing-and-license)

## When to use it

Link sections that must change together. Keep each section small.

| Scenario | What to check |
| --- | --- |
| Code and docs | Require a guide edit when a linked API changes. |
| Schemas and clients | Require a client edit when a linked schema field changes. |
| Rules and tests | Require a test edit when a linked rule changes. |
| Defaults and examples | Require an example edit when a linked default changes. |
| Separate implementations | Require edits to linked implementations of the same protocol rule. |
| AI agent edits | Show agents which linked tests or docs need edits. |
| Multiple repositories | Check linked edits in committed snapshots with `--change-set`. |
| Repeated content | Keep labelled text or selected values equal with [LINT.Match](docs/directives.md#match-section-contents). |

Prefer shared code or generated files when they can remove duplication.

## Quick start

```go
// LINT.IfChange(API)
const apiVersion = 2
// LINT.ThenChange(//docs/api.md:API)
```

The comments link `apiVersion` to this section in `docs/api.md`:

```markdown
<!-- LINT.IfChange(API) -->
API version: 2
<!-- LINT.ThenChange() -->
```

Commit both files as the baseline. Change only `apiVersion`. The next check reports the missing edit in `docs/api.md`. Paths starting with `//` refer to the repository root.

Build from the repository root with Go 1.26.8, as specified in `go.mod`:

```sh
make build
./build/ifttt --vcs git
```

Put `build/ifttt` on PATH to use it elsewhere. Git or jj must also be on PATH. See [CLI commands](docs/cli.md#commands) for staged edits, revisions and jj.

Automatic detection chooses jj when both Git and jj exist. Use `--vcs git` for Git revision ranges.

## VS Code extension

See findings in Problems. Open linked files, read directive help and check changes on save.

See the [extension README](vscode-extension/README.md) for installation, settings, editor actions and the playground.

## CLI

```sh
ifttt --vcs git                    # local edits
ifttt --vcs git --staged           # staged edits
ifttt --vcs git --diff main...HEAD  # Git revisions
ifttt --vcs jj --diff 'main..@'    # jj revisions
ifttt --scan .                    # directive structure
```

See [all commands, options, ignore rules and fixes](docs/cli.md).
See [directive syntax and tested examples](docs/directives.md) for nested blocks, conditional links, matching values and suppression.

### Configuration

Create `.ifttt-lint.yaml` for lint settings. The tool loads it automatically.
Use a separate manifest with `--change-set` to select cross-repository revisions. You choose its filename.
See [config options, overrides and custom prefixes](docs/cli.md#configuration).

## GitHub Action and hooks

The GitHub Action builds the tool from source and uses Git. Pull requests check linked edits across the full PR range. Pushes check directive structure in all tracked files. Replace `<ref>` with a release tag or commit:

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

See [local pre-commit and pre-push hooks](docs/cli.md#hooks) for checks before committing or pushing.

## Coding agents

Follow [AGENTS.md](AGENTS.md) to add narrow dependency links, check changes and make the required updates.
The required `Lint` job checks directive structure and matching edits across pull requests and merge queues.

## Cross-repository change sets

Link a section in another repository with `github://owner/repo/path#label`.
Use `--change-set .ifttt-changes.yaml` to check linked edits in local Git/jj checkouts.
The manifest selects each repository's `base` and `head`. Dirty files cannot satisfy a dependency.

<!-- LINT.IfChange(remote_revision_refs) -->

### Select a branch, tag or commit

Put `?ref=` before `#label`:

| Revision | Target |
| --- | --- |
| Branch | `github://acme/contracts/schema.md?ref=main#API` |
| Tag | `github://acme/contracts/schema.md?ref=v2.0.0#API` |
| Commit | `github://acme/contracts/schema.md?ref=<full-commit-sha>#API` |

Replace `<full-commit-sha>` with the complete commit ID.
You can use these targets in `LINT.ThenChange` and other linking directives.

| Mode | Without `?ref=` | With `?ref=` |
| --- | --- | --- |
| Ordinary GitHub check | Use `remotes[].default_ref`, which defaults to `main`. | Read that branch, tag or commit. |
| `--change-set` | Use the target repository's manifest `head`. | Must resolve to that same head. A conflict exits `2`. |

Ordinary GitHub checks check only whether a file and label exist. They do not prove a matching edit.
For repeatable change-set checks, omit refs from directives and use full commit IDs in the manifest.

<!-- LINT.ThenChange(//test/integration/change_set_test.go:remote_revision_refs, //test/integration/remote_revisions_test.go:remote_revision_refs, //internal/engine/engine.go:remote_revision_refs) -->

See the [cross-repository guide](docs/cross-repository.md) for complete examples, mixed prefixes, manifest fields, editor/CI setup and limitations.
See [remote provider configuration](docs/cli.md#remote-references) for ordinary GitHub checks.

## Benchmarks and correctness

Measured with an Apple M1 Max (arm64), macOS 26.1 and Go 1.26.8. The Rust comparison uses [ifttt-lint v0.11.2](https://github.com/simonepri/ifttt-lint/releases/tag/v0.11.2).

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

**Correctness:** Isolated fixtures show support for nested blocks and inline/block-comment directives that Rust v0.11.2 rejects. The tool reports ambiguous labels and empty directives that have no effect. [Cross-repository checks](#cross-repository-change-sets) use exact committed snapshots. Dirty files cannot satisfy dependencies. Regression tests cover the audited upstream closed-issue cases.

The pinned TensorFlow audit produced **91 Go findings and 117 Rust findings, with 82 matching**. Path and parsing policies explain the differences. These include 26 Rust traversal errors for paths that stay inside the repository. Finding counts do not measure accuracy. Results depend on hardware and workload.

See [benchmark reproduction](docs/benchmarks.md) for commands, pinned inputs and report handling.

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

Releases use version tags (`vMAJOR.MINOR.PATCH`) and include changes from [CHANGELOG](CHANGELOG.md). A merged version PR with the `release` label starts the pipeline. You can also start it manually. Tests, lint, security checks and native binary checks must pass before publication.

Release builds include CLI binaries for Linux, macOS and Windows (amd64/arm64), a VS Code extension package (`.vsix`) and `SHA256SUMS`.

Download published packages from [GitHub releases](https://github.com/derhnyel/ifttt/releases). Check downloaded files against `SHA256SUMS`. See [extension installation](vscode-extension/README.md#install) for VSIX setup.

## Contributing and license

Use the GitHub templates to report issues. Follow [CONTRIBUTING](CONTRIBUTING.md) for pull requests and checks. See [CHANGELOG](CHANGELOG.md) for user-visible changes.

Licensed under [MIT](LICENSE).
