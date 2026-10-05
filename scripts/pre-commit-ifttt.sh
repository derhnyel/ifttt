#!/usr/bin/env bash
set -euo pipefail

if ! command -v ifttt >/dev/null 2>&1; then
	echo "ifttt not found on PATH; install it before committing." >&2
	exit 2
fi

printf 'ifttt: validating staged diff...\n' >&2
set +e
ifttt --vcs git --staged --format=text "$@" </dev/null
status=$?
set -e
if [ "$status" -ne 0 ]; then
	cat <<'EOF' >&2
ifttt detected issues in the staged changes.
Fix the findings (or run: ifttt --vcs git --staged --fix) before committing.
EOF
	exit "$status"
fi
