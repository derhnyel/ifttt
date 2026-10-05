#!/usr/bin/env bash
set -euo pipefail

if ! command -v iflint >/dev/null 2>&1; then
	echo "iflint not found on PATH; install it before committing." >&2
	exit 2
fi

printf 'iflint: validating staged diff...\n' >&2
set +e
iflint --vcs git --staged --format=text "$@" </dev/null
status=$?
set -e
if [ "$status" -ne 0 ]; then
	cat <<'EOF' >&2
iflint detected issues in the staged changes.
Fix the findings (or run: iflint --vcs git --staged --fix) before committing.
EOF
	exit "$status"
fi
