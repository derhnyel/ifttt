#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd -- "$(dirname "${BASH_SOURCE[0]}")"/.. && pwd)"
BIN="$ROOT/test/.ifttt-smoke"
TMP_REPO="$(mktemp -d)"
trap 'rm -rf "$TMP_REPO" "$BIN"' EXIT

echo "[1/8] building ifttt binary..."
(cd "$ROOT" && GO111MODULE=on go build -o "$BIN" ./cmd/ifttt)

echo "[2/8] setting up temporary repository at $TMP_REPO"
cd "$TMP_REPO"
git init -q
git config user.name "IFTTT Lint Smoke Test"
git config user.email "smoke@example.invalid"
cat <<'EOF' > .ifttt-lint.yaml
parallelism: "auto"
verbose: false
ignores: []
skip_directories: []
rules:
  unknown_directive: warn
  combined_diff: error
  code_only: false
output:
  format: text
EOF

cat <<'EOF' > source.go
package main

// LINT.IfChange(LBL)
func doThing() {
	println("old")
}
// LINT.ThenChange(//target.go:LBL)
EOF

cat <<'EOF' > target.go
package main

// LINT.IfChange(LBL)
func target() {
	println("original")
}
// LINT.ThenChange()
EOF

git add . >/dev/null
git commit -m "initial" >/dev/null

# introduce a staged change that violates ThenChange.
perl -0pi -e 's/println\("old"\)/println("new")/' source.go
git add source.go >/dev/null

echo "[3/8] running lint against staged diff (expecting failure)..."
if git diff --cached | "$BIN" -; then
	echo "ERROR: expected lint failure for missing ThenChange" >&2
	exit 1
fi

echo "[4/8] verifying --fix inserts placeholder..."
git checkout -- target.go >/dev/null
if git diff --cached | "$BIN" -fix -; then
	echo "ERROR: expected non-zero exit when running --fix" >&2
	exit 1
fi
if ! grep -q "TODO(ifttt)" target.go; then
	echo "ERROR: --fix did not insert placeholder in target.go" >&2
	exit 1
fi
git checkout -- target.go >/dev/null

echo "[5/8] validating auxiliary commands..."
"$BIN" --explain then_missing >/dev/null
"$BIN" --doctor --scan . >/dev/null
"$BIN" --scan . >/dev/null
"$BIN" review HEAD >/dev/null
EDITOR=true "$BIN" jump target.go LBL >/dev/null

echo "[6/8] confirming JSON output is well-formed..."
json_output=$({ git diff --cached | "$BIN" --format=json - 2>/dev/null; } || true)
if ! grep -q '"ruleId": "then_label_missing"' <<<"$json_output"; then
	echo "ERROR: expected JSON output to contain then_label_missing rule" >&2
	echo "$json_output"
	exit 1
fi

echo "[7/8] ensuring passing diff succeeds..."
perl -0pi -e 's/println\("original"\)/println("updated")/' target.go
git add target.go >/dev/null
git diff --cached | "$BIN" - >/dev/null

echo "[8/8] smoke test completed successfully."
