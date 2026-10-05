# Changelog

## Unreleased

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
