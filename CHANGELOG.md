# Changelog

## Unreleased

- Clarify when linked-edit checks run, unlabelled blocks, supported comment styles and cross-repository manifest setup. Test the documented trigger cases and checkout layout with Git and jj.
- Simplify the README, explain how to resolve findings and show project benchmark results without tool comparisons.

## 0.2.1 — 2026-10-06

- Parse remote comments using the target file path when a GitHub reference selects a branch, tag or commit.

- Split detailed usage into linked guides. Document branch, tag and commit references and test the published examples with Git and jj. Cover three-repository chains, mixed local/remote conditions and committed suppression.

- Add tested usage examples for directives, nested links, configuration and committed cross-repository checks. Apply configured scan and doctor exclusions correctly when running from a subdirectory.

- Respect Git ignore rules and configured exclusions during scan, doctor and fallback directive discovery. Limit default skips to VCS metadata and dependency/cache folders; editor directories and build outputs follow project ignore rules. Show discovery progress with `--verbose`.

## 0.2.0 — 2026-10-05

- Load each change-set repository’s committed root config, including its directive prefix and Python comment settings. Cache verified snapshot commit IDs to avoid repeated Git/jj revision lookups.

- Show VS Code hover help for directives without findings, using the CLI parser and current editor text. Add read-only `inspect` JSON output.
- Keep linked-edit checks active when guards are renamed or retargeted and new neighbouring blocks are added, including copies of the previous body.
- Validate conditional target labels after committed repository config changes, even when their source is unchanged. Respect ignored targets and skipped directories.
- Add `LINT.Match` for labelled text equality and optional regex value extraction, including unchanged files and committed cross-repository snapshots.
- Resolve blank extension binary settings to `ifttt` and run equality checks for empty custom diffs.

- Enable strict local LINT target paths by default. Use `--strict=false` to allow other local path forms.
- Move the pre-commit hook definitions into the README as local configuration and remove the repository hook manifest.

## 0.1.1 — 2026-10-05

- Retry failed VS Code test-host downloads with cache bypass, preserve checksum verification and report rejected response details.
- Prepare patch, minor and major version PRs automatically, with synchronized npm versions and dated release notes.
- Release labelled, merged PRs or manually selected versions through all CI, lint, security, VS Code host and six native binary checks.
- Publish changelog-backed GitHub releases and the versioned Action from the original verified artifacts. Keep VS Code Marketplace publication optional.

## 0.1.0 — 2026-10-05

- Name the CLI `ifttt` and the VS Code extension `IFTTT Lint`. Go installs use `github.com/derhnyel/ifttt/cmd/ifttt`.
- Use `IFTTT_*` environment variables, `ifttt` hook IDs and `ifttt-<os>-<arch>` release binaries.
- Keep normal Git diffs active when changed source text contains a literal combined-diff header.
- Move the Go module, CLI, tests and VS Code extension to the repository root; consolidate project documentation into README sections.
- CLI with native Git/jj workflows, structural and co-change checks, conditional rules, JSON/SARIF/DLS reports, and explicit cross-repository snapshot validation.
- VS Code diagnostics, navigation, scaffolding and fixes; snapshot reports remain read-only.
- GitHub Action, pre-commit hooks, versioned release binaries and verified release assets.
- Standard `LINT.IfChange` / `LINT.ThenChange` directive syntax.
- GitHub Action checks require the selected PR head checkout and preserve multiline arguments. CI includes a required-status gate, dependency updates and issue/PR templates.
