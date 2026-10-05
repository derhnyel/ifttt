# Coding agent instructions

Follow [CONTRIBUTING.md](CONTRIBUTING.md) for setup, tests and repository policies.

When related code, tests or docs must change together, add `LINT.IfChange` and `LINT.ThenChange` comments.
Prefer shared code or generated files when they can remove the duplication.
Keep each guarded section small. Use labels to link the specific sections that need matching edits.

Before finishing an edit:

1. Build the CLI with `make build`.
2. Run `./build/ifttt --vcs git --format=json` to check working changes. For jj, use `--vcs jj`.
3. Read each finding and make the required update in the linked file or section.
4. Run lint again and run the tests that cover the change.

Git working diffs omit untracked files. Check new files explicitly with `./build/ifttt --vcs git path/to/file`.

For a supplied cross-repository manifest, run `./build/ifttt --change-set changes.yaml --format=json`.
Use the manifest's actual path. All declared checkouts and committed revisions must exist locally.
Dirty working files cannot satisfy snapshot dependencies.

Make meaningful updates. Do not add TODO placeholders, remove links or suppress findings just to pass lint.
`--fix` adds placeholders. It cannot make the actual code or documentation update for you.
Lint checks that declared dependencies changed. Tests and review must check whether those edits work correctly.

CI checks directive structure and linked edits in the required `Lint` job.
Run `make check` before submitting a pull request. Keep plans and generated output in ignored local directories.
