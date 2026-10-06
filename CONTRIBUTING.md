# Contributing

Report bugs with a minimal directive/diff example, the CLI version, expected behavior and actual output. For editor issues, include the VS Code version and relevant IFTTT Lint settings. Remove credentials and private paths from reports.

## Local setup

Use the Go toolchain pinned in `go.mod`, Node.js 22+, Python 3.11+, Git and Bash. The Go module lives at the repository root; the extension lives in `vscode-extension`.

```sh
make test-tools         # install pinned, checksum-verified jj for native tests
make build             # build/ifttt
make check             # format, vet, race tests, CLI smoke, extension tests, audits and workflow lint
```

For editor changes, run `make extension-host` to exercise an actual VS Code host with Git and jj. Set `IFTTT_VSCODE_BINARY` if VS Code is not discovered automatically; Linux needs a display or `xvfb-run`.

Optional checks are `make coverage` and the workloads in [benchmarks](README.md#benchmarks-and-correctness). For local VSIX packaging, run `npm ci` and `npm run package:local` in `vscode-extension`.

## Add a language

Start with the [language registry](internal/comments/languages.go). Reuse an existing comment grammar when possible.
Add a string-skip rule if the language has literals that can contain comment markers.
Test real comments and lookalike directives inside strings in [parser language tests](internal/parse/languages_test.go).
Update the [supported-file guide](docs/cli.md#supported-files).
For a simple custom line prefix, users can use `--comment-style` without a code change.

## Pull requests

- Keep each change focused and explain the problem, resulting behavior and verification performed.
- Add tests that catch meaningful behavior, security or performance regressions. Prefer observable outcomes over private representation, incidental wording or tests that repeat the implementation. Consolidate overlapping cases; CLI/editor integration tests should exercise the real executable in temporary repositories.
- Update user documentation when commands, configuration or behavior change; record user-visible changes in `CHANGELOG.md`.
- Run `make check` before submitting. For performance claims, record commands, binary hashes and correctness comparisons.

## What belongs in version control

Commit source, tests, fixtures, dependency lockfiles and durable documentation for users, contributors and releases. Keep documentation concise and avoid repeating another guide.

Keep agent plans, Superpowers specs, scratch notes and review transcripts in ignored `.local/` or `docs/superpowers/`. Generated benchmarks, coverage, profiles, downloaded repositories/tools, binaries and VSIX packages stay in ignored local directories. Check `git diff --cached --name-only` before committing.

Contributions are made under the project's [MIT license](LICENSE). Preserve applicable third-party licenses and attribution when incorporating code.
