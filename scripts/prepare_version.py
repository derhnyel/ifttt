#!/usr/bin/env python3
"""Prepare a clean checkout's version/changelog changes for a release pull request."""
import argparse
from datetime import date
import json
from pathlib import Path
import re
import subprocess


# LINT.IfChange(version_preparation)
def prepare(root, bump, day):
    if bump not in ('major', 'minor', 'patch'):
        raise ValueError('bump must be major, minor or patch')
    status = subprocess.run(['git', 'status', '--porcelain'], cwd=root, capture_output=True,
                            text=True, check=True, timeout=30).stdout
    if status.strip():
        raise ValueError('version preparation requires a clean committed checkout')
    manifest = root / 'vscode-extension/package.json'
    lock_path = root / 'vscode-extension/package-lock.json'
    changelog_path = root / 'CHANGELOG.md'
    package = json.loads(manifest.read_text())
    lock = json.loads(lock_path.read_text())
    version = package['version']
    if not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)', version):
        raise ValueError('package version must be a stable semantic version')
    if lock['version'] != version or lock['packages']['']['version'] != version:
        raise ValueError('package and lockfile versions must agree')
    numbers = list(map(int, version.split('.')))
    index = ('major', 'minor', 'patch').index(bump)
    numbers[index] += 1
    numbers[index+1:] = [0] * (2-index)
    version = '.'.join(map(str, numbers))
    changelog = changelog_path.read_text()
    sections = list(re.finditer(r'^## (.+)$', changelog, re.MULTILINE))
    pending = [i for i, match in enumerate(sections) if match[1] == 'Unreleased']
    if len(pending) != 1 or pending[0] != 0:
        raise ValueError('changelog must start with exactly one Unreleased section')
    start = sections[0].end()
    end = sections[1].start() if len(sections) > 1 else len(changelog)
    body = changelog[start:end].strip()
    if not body or not re.search(r'^[-*] \S', body, re.MULTILINE):
        raise ValueError('Unreleased must contain release notes')
    if any(match[1].split(' ')[0] == version for match in sections):
        raise ValueError('next version already exists in the changelog')
    package['version'] = lock['version'] = lock['packages']['']['version'] = version
    updated = changelog[:start] + f'\n\n## {version} — {day}\n\n{body}\n\n' + changelog[end:]
    # Validate every input before writing any tracked file.
    manifest.write_text(json.dumps(package, indent=2) + '\n')
    lock_path.write_text(json.dumps(lock, indent=2) + '\n')
    changelog_path.write_text(updated)
    return 'v' + version
# LINT.ThenChange(//scripts/test_prepare_version.py:version_preparation)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('bump', choices=('patch', 'minor', 'major'))
    args = parser.parse_args()
    try:
        print(prepare(Path(__file__).resolve().parents[1], args.bump, date.today().isoformat()))
    except (ValueError, KeyError, OSError, subprocess.SubprocessError) as error:
        parser.exit(1, f'prepare version: {error}\n')
