# Cross-repository checks

[README](../README.md) · [Directives and examples](directives.md) · [CLI and configuration](cli.md)

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

Declare both checkout roots in `.ifttt-changes.yaml`. This manifest selects revisions. It does not replace `.ifttt-lint.yaml`.
The filename is your choice. The tool reads it only when you pass `--change-set`.
Git and jj can participate in the same manifest:

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
ifttt --change-set .ifttt-changes.yaml --format=json
```

| Manifest field | Meaning |
| --- | --- |
| `version` | Use `1`. |
| `repositories[].repo` | An `owner/name` ID that matches your `github://` targets. These IDs do not need a GitHub account or remote. |
| `repositories[].path` | Checkout root, relative to the manifest file or absolute. |
| `repositories[].vcs` | `git`, `jj`, or `auto` (default). |
| `repositories[].base` | One local commit before the changes. |
| `repositories[].head` | One local commit after the changes. |

<details>
<summary>Complete example: two repositories, different prefixes, linked edits and matching values</summary>

<!-- LINT.IfChange(readme_crossrepo) -->
Create sibling repositories named `api` and `contracts`. Use Git for both in this example.
The IDs `acme/api` and `acme/contracts` identify these local checkouts. The checker makes no network request.

**api/source.go**

<!-- example: crossrepo-api -->
```go
// LINT.IfChange(API)
const apiVersion = 2
// LINT.ThenChange(github://acme/contracts/contract.go#API)
// LINT.Match(":API", "github://acme/contracts/contract.go#API", '([0-9]+)')
```

**contracts/.ifttt-lint.yaml**

<!-- example: crossrepo-config -->
```yaml
directives:
  prefix: SPEC
ignores: ['generated/**']
```

**contracts/contract.go**

<!-- example: crossrepo-contract -->
```go
// SPEC.Label("API")
const contractVersion = 2
// SPEC.EndLabel
// SPEC.RequireAll(["github://acme/api/source.go#API"])
```

The API block requires a contract section edit. The contract file requires an API section edit when it changes.
Match also requires the two version numbers to be equal. Each repository uses its own committed prefix and exclusions.

Commit these files in both repositories. Create **.ifttt-changes.yaml** beside the checkout directories:

<!-- example: crossrepo-manifest -->
```yaml
version: 1
repositories:
  - repo: acme/api
    path: ./api
    vcs: git
    base: '<api-base-commit>'
    head: '<api-head-commit>'
  - repo: acme/contracts
    path: ./contracts
    vcs: git
    base: '<contracts-base-commit>'
    head: '<contracts-head-commit>'
```

Replace each placeholder with a commit ID. Read the current ID with `git -C api rev-parse HEAD` or `git -C contracts rev-parse HEAD`.
For the first check, use each repository's baseline ID for both its base and head.

```sh
ifttt --change-set .ifttt-changes.yaml --format=json
```

Try these cases. Keep the base IDs fixed and update the head IDs after each commit.

| Changes | Result |
| --- | --- |
| Neither repository changes, both values are `2`. | Passes. Match still runs. |
| Commit `apiVersion = 3` only. | Reports a missing contract edit and a Match mismatch. |
| Save `contractVersion = 3` without committing it. | Still fails. Dirty files do not count. |
| Commit `contractVersion = 3` and select that head. | Passes. Both sections changed and their values match. |
| Edit only an unrelated line in the contract file. | Does not satisfy the API's labelled dependency. |
| Change both values but set them to different numbers. | Linked edits pass, but Match fails. |

To use jj for either checkout, set its `vcs` to `jj`. Select one revision for each base and head.
Read the current jj commit ID with `jj log -r @ --no-graph -T 'commit_id'`.
Git refs and jj revsets must resolve locally. Pin full commit IDs for a repeatable result.

Conditional targets can also mix local and remote paths:

```go
// LINT.RequireAny(["github://acme/contracts/contract.go#API", "//fallback.go:API"])
// LINT.RequireAll(["github://acme/contracts/contract.go#API", "//tests.go:API"])
// LINT.ForbidChange("github://acme/contracts/generated.go")
```

Use the rules your contract needs. Create every target file and label before checking.
All referenced repositories must appear in the manifest, including optional RequireAny candidates.
An optional URI `?ref=` can select a branch, tag or commit. It must resolve to the target repository's selected head.
See [revision examples](../README.md#select-a-branch-tag-or-commit).
Use [ordinary remote references](cli.md#remote-references) when you only need to check that a GitHub target exists.
<!-- LINT.ThenChange(//test/integration/readme_examples_test.go:readme_crossrepo) -->

</details>

<details>
<summary>Change-set rules and limitations</summary>

Manifest paths are relative to the manifest file. Each must identify a different checkout root. All checkouts and revisions must already exist locally.

You choose the repository IDs. They ignore letter case and do not come from remote settings. `vcs` accepts `git`, `jj` or `auto`. Both `base` and `head` must resolve to exactly one commit. Use full commit IDs to reproduce the same result.

IFTTT Lint checks changed sources in every declared repository. It reports missing target edits, edits outside required labels, removed targets and renamed labels. It also checks incoming references from sources that did not change. `RequireAny`, `RequireAll` and `ForbidChange` use the same cross-repository evidence.

Write LINT targets without quotes. Remote URIs use `#label`. Local targets use `//path:label`. An optional remote `?ref=` must match the declared target head. Undeclared repositories and conflicting refs fail.

Ordinary remote checks test whether a file and label exist. Change sets check matching edits without credentials or network fetches.

<!-- LINT.IfChange(snapshot_config) -->
Each repository uses its root `.ifttt-lint.yaml` from the selected `head` commit. Linked sections can use different configured prefixes. A repository without this file uses `LINT` and the default rules.

IFTTT Lint reads each config once. Config changes also trigger incoming-reference checks. Dirty checkout configs, configs above the checkout and nested configs do not apply in change-set mode. Commit each repository's prefix, Python comment settings, exclusions and lint rules in its root config. Explicit `--code-only`, `--ignore`, `--skip-dir` and thread flags override its source settings. Output flags apply to the whole report.
<!-- LINT.ThenChange(//internal/changeset/run.go:snapshot_config) -->

<!-- LINT.IfChange(snapshot_ignore_policy) -->
Change-set checks read only committed files. Each source repository uses its committed `ignores` and `skip_directories`, with the same default skips. Local ignored files and dirty ignore settings do not affect the result. A `.gitignore` rule does not exclude an already committed file or an explicitly linked target. Use the linter configuration to exclude committed sources.
<!-- LINT.ThenChange(//internal/changeset/run.go:snapshot_ignore_policy, //test/integration/change_set_test.go:snapshot_ignore_policy) -->

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

For editor setup, see [cross-repository checks in the extension README](../vscode-extension/README.md#cross-repository-checks).

Prepare each declared checkout with both revisions. Then run the Action. Replace `<ref>` with your published Action tag or commit:

```yaml
- uses: derhnyel/ifttt@<ref>
  with:
    change-set: .ifttt-changes.yaml
    args: --format=json
```

The Action does not fetch repositories in this mode. It rejects a simultaneous `diff` input. Manifest and checkout paths can contain spaces. See [Action usage](../README.md#github-action-and-hooks).
