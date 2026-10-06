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

The manifest tells the tool which local repositories and commits to compare. It does not download repositories or synchronize their files.
The manifest is optional:

- Without `--change-set`, normal checks work without this file. They do not verify coordinated edits across repositories.
- With `--change-set`, the tool must read the file you specify. A missing or invalid manifest stops the check with exit code `2`.

The tool does not create a missing manifest or fall back to normal checks.

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
| `version` | Manifest format version. Use `1`. This is not your application version. |
| `repositories[].repo` | An `owner/name` ID that matches your `github://` targets. These IDs do not need a GitHub account or remote. |
| `repositories[].path` | Checkout root, relative to the manifest file or absolute. |
| `repositories[].vcs` | `git`, `jj`, or `auto` (default). |
| `repositories[].base` | One local commit before the changes. |
| `repositories[].head` | One local commit after the changes. |

<details>
<summary>Complete example: two repositories, different prefixes, linked edits and matching values</summary>

<!-- LINT.IfChange(readme_crossrepo) -->
1. Put the `api` and `contracts` Git checkouts in the same parent directory.
2. Create the three files below in their indicated checkouts.

The IDs `acme/api` and `acme/contracts` identify these local checkouts. They do not need to exist on GitHub.
The checker makes no network request in change-set mode.

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

These comments apply three checks:

- Changing `apiVersion` requires an edit inside the contract's `API` section.
- Changing any part of `contract.go` requires an edit inside the source's `API` section.
- `Match` extracts the numbers from both sections and requires them to be equal, even when neither section changes.

The API repository uses the default `LINT` prefix.
The contracts repository uses `SPEC` and excludes `generated/**`, as defined in its committed `.ifttt-lint.yaml`.

3. Run these commands from the parent directory. They commit the example files and print each baseline commit ID.

```sh
git -C api add source.go
git -C api commit -m "Add API version checks"
git -C contracts add .ifttt-lint.yaml contract.go
git -C contracts commit -m "Add contract version checks"
git -C api rev-parse HEAD
git -C contracts rev-parse HEAD
```

4. Record the two commit IDs as the base IDs.
5. Create `.ifttt-changes.yaml` in the parent directory:

```text
workspace/
  .ifttt-changes.yaml
  api/
    source.go
  contracts/
    .ifttt-lint.yaml
    contract.go
```

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

6. Replace the placeholders using this table. Do not keep the `<` and `>` characters.

| Placeholder | Value |
| --- | --- |
| `<api-base-commit>` | The API baseline commit ID. |
| `<api-head-commit>` | The API commit ID to check. Use its baseline ID for the first run. |
| `<contracts-base-commit>` | The contracts baseline commit ID. |
| `<contracts-head-commit>` | The contracts commit ID to check. Use its baseline ID for the first run. |

`base` means before the changes. `head` means after the changes.
Each repository has its own commit IDs. The paths are relative to the manifest file.

7. Run the check from the parent directory:

```sh
ifttt --change-set .ifttt-changes.yaml --format=json
```

The first run passes because both version numbers are `2` and neither repository changed.
For subsequent checks, keep both base IDs fixed.
Commit your edits in each changed repository, then run its `rev-parse HEAD` command again.
Update only that repository's `head` in the manifest and run the check again.
Saving a file without committing it does not affect the result.

Try these cases:

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
