# Directives and examples

[README](../README.md) · [CLI and configuration](cli.md) · [Cross-repository checks](cross-repository.md)

## Directive syntax

Put directives in the source language's comments:

```go
// LINT.IfChange(API)
const apiVersion = 2
// LINT.ThenChange(//docs/api.md:API, //client/version.go)
```

A target section can use `LINT.IfChange(API)` and an empty `LINT.ThenChange()`. The empty closing directive requires a label on the opening directive. Bare `LINT.IfChange` or `LINT.IfChange()` starts a source block without a label.

Write labels and targets without quotes. Separate multiple targets with commas. Target lists can span lines. IFTTT Lint ignores directive text in strings, prose and fenced examples.

Standard labels must start with a letter. The remaining characters can be letters, digits, underscores, dots or dashes. Label names are case-sensitive.

<!-- LINT.IfChange(strict_paths_default) -->

By default, local targets must use one of these forms:

- `//docs/api.md`: a file at the repository root.
- `:API`: a label in the same file.
- `//docs/api.md:API`: a label in another file.

With `--strict=false`, `/docs/api.md` and `docs/api.md` also refer to the repository root. Bare filenames such as `api.md` refer to the source directory. Explicit `./` and `../` paths refer to the source directory. All local paths must stay inside the workspace.

<!-- LINT.ThenChange(//cmd/ifttt/main.go:strict_paths_default, //test/integration/cli_test.go:strict_paths_default) -->

These additional directives use quoted arguments. They are specific to this implementation:

| Directive | Purpose |
| --- | --- |
| `LINT.Label("API")` | Start a named target section without creating an outgoing dependency. |
| `LINT.EndLabel` | Close the most recent `Label` section. |
| `LINT.Match(":A", "//other.txt:B")` | Compare the contents of two labelled sections. See [Match section contents](#match-section-contents). |
| `LINT.RequireAny(["//one.go", "//two.go"])` | Require an edit to at least one target. |
| `LINT.RequireAll(["//one.go", "//two.go"])` | Require edits to every target. |
| `LINT.ForbidChange("//generated.go")` | Report an edit to the target when the source changes. |
| `LINT.Disable("then_missing")` | Suppress this rule from this line until a matching `Enable`, or the end of the file. |
| `LINT.Enable("then_missing")` | End the matching `Disable` scope. |
| `LINT.Ignore("then_missing")` | Suppress this rule throughout the file, including earlier lines. |

Inside an IfChange block, `RequireAny`, `RequireAll` and `ForbidChange` apply when the block body changes. Outside a block, they apply when the source file changes. These rules need a diff to check edits.

<!-- LINT.IfChange(conditional_target_structure) -->
Their target files and labels must exist, even when the source did not change or required-edit checks are suppressed. Configured ignores and skipped directories exclude targets. Editor and build directories are eligible by default. Git ignore rules do not suppress explicitly linked targets. A config change does not count as an edit to a target section.
Scan checks keep exclusion patterns relative to their configuration directory, even when you run from a subdirectory.
<!-- LINT.ThenChange(//internal/engine/rules.go:conditional_target_structure, //test/integration/change_set_test.go:conditional_target_structure) -->

Targets can select a file or a labelled section, such as `//one.go:API`. Extended `Label` names can use `#` selectors, such as `//one.go#API`.

Suppression uses finding rule IDs, not directive names. Use `"all"` or `"*"` to suppress all rules. A remaining `Disable("all")` scope still suppresses a rule after `Enable("then_missing")`.

Run `ifttt explain then_missing` for help with a finding. `--list-suppressed` shows suppressed findings. Prefer fixing a dependency over suppressing it.

For native revision ranges, `NO_IFTTT=<reason>` in any commit message suppresses required-edit checks for the whole range. The empty marker `NO_IFTTT=` also suppresses them. Directive structure and stale-reference checks still run. Change-set mode does not use commit-message suppression.

<!-- LINT.IfChange(match_contract) -->

### Match section contents

Use `LINT.Match` to keep two labelled sections equal, even when neither section changed:

```text
// LINT.Match(":COPY", "//other.txt:COPY")
```

Both labels must define complete, unique `IfChange` / `ThenChange` or `Label` / `EndLabel` sections. Put Match outside the compared sections.

The check compares content between the directive lines. Spaces and blank lines matter. CRLF and LF line endings match. Empty sections can match.

Match findings support target navigation in VS Code. `--fix` does not change Match content or create missing sections. Equal text or values do not prove equivalent behaviour.

<details>
<summary>Compare selected values with a regex</summary>

To compare values in different formats, add a regex:

```text
// LINT.Match(":VERSION", "//config.yaml:VERSION", '([0-9]+\.[0-9]+\.[0-9]+)')
```

This pattern compares all version values in order:

- One capture group compares the captured value.
- No capture group compares each full match.
- More than one capture group produces an error.
- Invalid patterns, no matches and empty values produce errors.

Use [Go regex syntax](https://pkg.go.dev/regexp/syntax). Lookaround and backreferences are not supported. Single-quoted patterns keep backslashes as written. Escape backslashes in double-quoted arguments.

</details>

Match also runs on empty diffs and `NO_IFTTT` ranges. Git/jj runs find tracked Match directives. Select untracked files explicitly. File selections and scans limit the source files checked. Scan discovery respects Git ignores and configured source exclusions; explicitly linked Git-ignored sections are still compared. Cross-repository manifests compare committed snapshots. Each section uses its repository’s committed prefix and Python comment settings. Use `#label` selectors with a custom directive prefix.

Git uses one fixed-string query to find Match sources. It reuses the query for reverse-reference checks. The engine parses only the hits and their targets. Prefer shared content or generation when possible.

<!-- LINT.ThenChange(//internal/engine/match.go:match_contract, //internal/parse/match.go:match_contract, //test/integration/match_test.go:match_contract, //cmd/ifttt/main.go:match_rules) -->

## Usage examples

Start with the [quick start](../README.md#quick-start). Use these examples when you need more than one link.
Each example is independent. Create the named files, commit a baseline, then make the described edits.

<details>
<summary>Nested blocks, multiple targets and same-file links</summary>

<!-- LINT.IfChange(readme_nested) -->
**api.go**

<!-- example: nested-api -->
```go
/* LINT.IfChange(API) */
const apiName = "example"
// LINT.IfChange(VERSION)
const apiVersion = 2
// LINT.ThenChange(//docs/api.md:VERSION)
/*
 * LINT.ThenChange(
 *   //docs/api.md:API,
 *   //client.go
 * )
 */
```

**docs/api.md**

<!-- example: nested-guide -->
```markdown
<!-- LINT.Label("API") -->
API name: example
<!-- LINT.Label("VERSION") -->
API version: 2
<!-- LINT.EndLabel -->
<!-- LINT.EndLabel -->
```

Create `client.go` with `const clientVersion = 2`.
Changing `apiVersion` requires an edit inside `VERSION` in the guide and an edit to `client.go`.
The inner block is also part of the outer block.
Changing only `apiName` requires an edit inside `API` in the guide and an edit to `client.go`.
Editing the guide alone does not require an API edit because `Label` creates no outgoing link.

For a link in both directions, give each section a target. This example uses two labels in **local.go**:

<!-- example: same-file -->
```go
// LINT.IfChange(SERVER)
const serverPort = 8080
// LINT.ThenChange(:CLIENT)

// LINT.IfChange(CLIENT)
const clientPort = 8080
// LINT.ThenChange(:SERVER)
```

Changing either port requires an edit to the other section. `:CLIENT` and `:SERVER` select labels in the same file.
An unlabelled source can use `LINT.IfChange` and a nonempty `LINT.ThenChange(...)` target list.
<!-- LINT.ThenChange(//test/integration/readme_examples_test.go:readme_nested) -->

</details>

<details>
<summary>Require all targets, choose one target, or forbid an edit</summary>

<!-- LINT.IfChange(readme_conditional) -->
Combine rules inside a small source block in **api.go**:

<!-- example: conditional-api -->
```go
// LINT.IfChange(API)
// LINT.RequireAll(["//client.go:API", "//tests.go:API"])
// LINT.RequireAny(["//migration-a.sql", "//migration-b.sql"])
// LINT.ForbidChange("//generated.go")
const apiVersion = 2
// LINT.ThenChange(//guide.md:API)
```

Create an `API` target section in `client.go`, `tests.go` and `guide.md`, as shown in the quick start.
Create both migration files and `generated.go` before the baseline commit.
When `apiVersion` changes, this combination requires:

- An edit inside `API` in both `client.go` and `tests.go`.
- An edit to at least one migration file.
- An edit inside `API` in `guide.md`.
- No edit to `generated.go`.

Use file targets for any edit in a file. Use labelled targets for an edit inside a specific section.
An unrelated edit outside the required section does not satisfy the rule.
Put conditional directives outside all IfChange blocks to apply them when any part of the source file changes.
For example, `LINT.RequireAll(["//tests.go:API"])` before the opening comment requires a test edit for any source edit.
The tool checks target files and labels even when the source does not change.
<!-- LINT.ThenChange(//test/integration/readme_examples_test.go:readme_conditional) -->

</details>

<details>
<summary>Match exact text or extract values from different formats</summary>

<!-- LINT.IfChange(readme_matching) -->
For exact text, create these files.

**message.txt**

<!-- example: match-message -->
```text
// LINT.Label("MESSAGE")
Try again.
// LINT.EndLabel
// LINT.Match(":MESSAGE", "//copy.txt:MESSAGE")
```

**copy.txt**

<!-- example: match-copy -->
```text
// LINT.IfChange(MESSAGE)
Try again.
// LINT.ThenChange()
```

Changing only one message produces `match_mismatch`. Both section types work as Match targets.
Exact matching includes spaces and blank lines. Put the Match directive outside the compared sections.

To compare a version across Go and YAML, use one regex capture group.

**version.go**

<!-- example: match-version -->
```go
// LINT.Label("VERSION")
const version = "1.2.3"
// LINT.EndLabel
// LINT.Match(":VERSION", "//package.yaml:VERSION", '([0-9]+\.[0-9]+\.[0-9]+)')
```

**package.yaml**

<!-- example: match-package -->
```yaml
# LINT.Label("VERSION")
version: 1.2.3
# LINT.EndLabel
```

The check compares `1.2.3` and ignores the surrounding Go/YAML syntax.
A regex without a capture group compares the complete matches. Multiple values must occur in the same order.
See [regex rules](#match-section-contents) for invalid patterns and missing values.
Combine Match with IfChange/ThenChange when you need both required edits and equal contents.
Match checks equality even with an empty diff. It cannot prove that two implementations behave the same way.
<!-- LINT.ThenChange(//test/integration/readme_examples_test.go:readme_matching) -->

</details>

<details>
<summary>Suppress one rule for a section, a file or a revision range</summary>

<!-- LINT.IfChange(readme_suppression) -->
Use rule IDs from findings. This example suppresses `then_missing` only for the experimental block in **api.go**:

<!-- example: suppression-api -->
```go
// LINT.Disable("then_missing")
// LINT.IfChange(EXPERIMENTAL)
const experimentalVersion = 2
// LINT.ThenChange(//guide.md)
// LINT.Enable("then_missing")

// LINT.IfChange(STABLE)
const stableVersion = 2
// LINT.ThenChange(//guide.md)
```

Create `guide.md` before the baseline commit.
Changing both constants without a guide edit reports the stable block only.
Other rule IDs still apply. To suppress this rule throughout the file, add `// LINT.Ignore("then_missing")`.
Different rule IDs can have overlapping `Disable` scopes. A remaining `Disable("all")` still suppresses rules after an individual `Enable`.
Use `ifttt --vcs git --list-suppressed` to inspect suppressed findings.

For a deliberate exception in a native revision range, include `NO_IFTTT=reason` in a commit message:

```sh
git commit -m 'Refactor comments. NO_IFTTT=no contract change'
ifttt --vcs git --diff main...HEAD
```

The marker suppresses required-edit checks for the whole range. Structure, stale-reference and Match checks still run.
Change-set mode does not use commit-message suppression.
<!-- LINT.ThenChange(//test/integration/readme_examples_test.go:readme_suppression) -->

</details>
