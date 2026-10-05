#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd -- "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

rm -rf .git
git init -q
git add .
git commit -m "baseline" >/dev/null
echo "Playground repo reset."
