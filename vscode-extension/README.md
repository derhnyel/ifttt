# IFTTT Lint for VS Code

IFTTT Lint catches forgotten updates to related code, tests and documentation in your editor. It checks `LINT.IfChange` and `LINT.ThenChange` comments with the `ifttt` CLI. Git and jj are supported.

- See findings in **Problems** and the **IFTTT Lint Findings** tree.
- Hover over a directive for help, or a finding for an explanation and suggested fix.
- Open linked files and labels with code lenses and quick actions.
- Create directive blocks and placeholders for local edits.
- Check committed changes across repositories with a change-set manifest.

## Install

You need **VS Code 1.88+**, a trusted filesystem workspace, the `ifttt` CLI and Git or jj on PATH. The extension does not bundle the CLI.

To build from this repository, use the Go toolchain specified in `go.mod`, Node.js 22+ and npm.

1. Build the CLI from the repository root:

   ```sh
   make build
   ```

2. Set **IFTTT Lint › Binary** (`iftttLint.binary`) to the absolute path of `build/ifttt` in VS Code Settings. You can also put `ifttt` on PATH. Windows executables may use the `.exe` suffix.

3. Package the extension from the repository root:

   ```sh
   cd vscode-extension
   npm ci
   cp ../LICENSE LICENSE
   npm run package:local
   ```

4. Run **Extensions: Install from VSIX...** from the Command Palette. Select the generated `.vsix` in `vscode-extension/`.
5. Open a trusted Git or jj workspace. Run **IFTTT Lint: Run**. Linting also runs on save by default.

## Use

In `source.go`:

```go
// LINT.IfChange(API)
const apiVersion = 1
// LINT.ThenChange(//docs/api.md:API)
```

In `docs/api.md`:

```markdown
<!-- LINT.IfChange(API) -->
API version: 1
<!-- LINT.ThenChange() -->
```

Commit both files as the baseline. Change only `apiVersion` and save. IFTTT Lint reports the missing edit as an error in Problems. Update the target section and run lint again. Editing a directive comment alone does not require a linked content edit. Directive names are case-sensitive: use `LINT.IfChange`.

`LINT.Match` findings show unequal section text or extracted values, even on an empty diff. Use target navigation to review the other section. Apply Fix does not change Match content or create its missing sections.

Paths that start with `//` refer to the repository root. See the [project README](https://github.com/derhnyel/ifttt#directive-syntax) for other directives and supported languages.

## Editor actions

| Action | Behavior |
| --- | --- |
| **IFTTT Lint: Run** | Check the selected workspace using its configured diff or revision. |
| Click an **IFTTT Lint Findings** entry | Open the source at the reported line. |
| Hover over a directive | Show its purpose and targets, even without findings. Uses the current editor text. |
| Hover over a finding | Show the rule, explanation and suggested fix. |
| **Jump to label** / **Open target file** | Open a local target or label. Ambiguous labels open the file without choosing a section. |
| **Create label block** | Create directive stubs. This can change both source and target files. |
| **IFTTT Lint: Apply --fix** / **Insert placeholder via ifttt --fix** | Run the CLI fixer, then refresh findings. |

### What Apply Fix does

Apply Fix adds `TODO(ifttt)` comments to unchanged targets or labelled sections. It also creates missing label blocks. It uses the source workspace's arguments and selected diff. One quick action can fix several eligible findings in that scope. It writes working files without staging or committing them.

**Replace each placeholder with the actual update.** A placeholder can clear a missing-change finding while leaving the code or docs incorrect. Remote targets, paths outside the workspace and symlink targets cannot be fixed. Change-set snapshot mode does not allow writes. Review scaffold and fix edits before committing.

## Settings

Configure these in VS Code Settings or workspace `settings.json`:

| Setting | Behavior |
| --- | --- |
| `iftttLint.binary` | Executable path or PATH command. Default: `ifttt`. |
| `iftttLint.vcs` | `auto` (default), `git` or `jj`. Auto chooses jj when both exist. |
| `iftttLint.diffMode` | `working-tree` (default) or `staged` (Git only). |
| `iftttLint.revision` | Git revision/range or jj revision expression. Cannot combine with staged mode. |
| `iftttLint.changeSet` | Optional snapshot manifest, relative to `workingDirectory`. |
| `iftttLint.workingDirectory` | Defaults to the selected workspace folder. Relative paths use that folder. |
| `iftttLint.args` | Additional CLI argument array. The extension requires JSON output for findings. |
| `iftttLint.runOnSave` | Run after saves. Enabled by default. |
| `iftttLint.skipDirectories` | Directory exclusions passed as `--skip-dir`. Defaults to VCS metadata directories. |
| `iftttLint.verboseLogging` | Show details in the IFTTT Lint output channel. |
| `iftttLint.diffCommand` | Shell command that produces a Git-format diff. Overrides native diff selection. |
| `iftttLint.downloadBaseUrl` | Optional HTTPS binary/checksum directory. Downloads are disabled by default. |

Commands require a trusted filesystem workspace. Manual runs use the active editor's workspace folder and settings. Runs after a save use the saved document's folder. In multi-root workspaces, findings and actions keep their source workspace's settings.

Save your edits before linting. Lint uses saved files; directive hover also reads unsaved editor text. CLI and custom diff commands have a 30-second deadline.

For staged Git checks:

```json
{
  "iftttLint.vcs": "git",
  "iftttLint.diffMode": "staged"
}
```

For jj, select `"iftttLint.vcs": "jj"`. Use working-tree mode or a revision such as `"iftttLint.revision": "main..@"`. jj has no staging area.

### Cross-repository checks

Set `iftttLint.changeSet` to a manifest path, relative to `iftttLint.workingDirectory`. Follow the [cross-repository setup guide](https://github.com/derhnyel/ifttt#cross-repository-change-sets). Every declared checkout and base/head revision must already exist locally.

Each repository uses its root `.ifttt-lint.yaml` from the selected head commit, including its prefix and Python comment settings. Checkout config edits do not apply.

Findings show the source repository and revision. Dirty working files cannot satisfy snapshot dependencies. Snapshot mode lets you open checkout files but disables fixes, scaffolding and label jumps. Reported lines refer to the selected head and may differ from dirty files.

The extension does not check out revisions. Change-set mode cannot combine with `diffCommand` or `--fix`. Folders that use the same manifest share one report.

### Optional CLI downloads

The extension does not download the CLI by default. To enable downloads, set `iftttLint.downloadBaseUrl` to an HTTPS release directory. It must contain `ifttt-<os>-<arch>` binaries and `SHA256SUMS`. Windows names include `.exe`.

The extension requires a matching checksum for downloaded and cached binaries. It uses a configured executable or CLI on PATH before trying a download.

## Extension playground

Use `test/extension-playground` to try the extension with standard LINT directives. In `source.go`, `LINT.IfChange(LBL)` opens a block. Its closing directive links to `//test/extension-playground/target.go:LBL`. In `target.go`, `LINT.IfChange(LBL)` and `LINT.ThenChange()` define the target section.

Open the repository root in VS Code:

```bash
code .
```

Run **IFTTT Lint: Run** from the Command Palette. Edit `test/extension-playground/source.go` to try a missing-target finding and its quick action. Restore your fixture edits after the check.

See [CLI configuration](https://github.com/derhnyel/ifttt#configuration), [cross-repository setup](https://github.com/derhnyel/ifttt#cross-repository-change-sets) and [development and host tests](https://github.com/derhnyel/ifttt#development-and-verification) for more details.

## Troubleshooting

- **CLI not found:** set `iftttLint.binary` to an absolute executable path. Check that Git/jj is on PATH.
- **No findings:** save the file. Check the selected workspace, diff mode and committed baseline.
- **Failed run:** open **View → Output → IFTTT Lint**. Enable `iftttLint.verboseLogging` for details. Commands may exceed the 30-second deadline.
- **Commands unavailable:** trust the workspace. Virtual workspaces are unsupported.

[Report an issue](https://github.com/derhnyel/ifttt/issues). Licensed under [MIT](https://github.com/derhnyel/ifttt/blob/main/LICENSE).
