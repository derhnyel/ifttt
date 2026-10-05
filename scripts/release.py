#!/usr/bin/env python3
"""Validate release metadata/assets and publish explicitly selected GitHub drafts."""
import argparse
import hashlib
import json
from pathlib import Path
import platform
import re
import subprocess
import tempfile
import zipfile

BINARIES = tuple(f'ifttt-{system}-{arch}' + ('.exe' if system == 'windows' else '')
                 for system in ('linux', 'darwin', 'windows') for arch in ('amd64', 'arm64'))
TAG = re.compile(r'v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?')
ROOT = Path(__file__).resolve().parents[1]


def validate_identity(repository, tag, commit=None):
    if not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', repository) or repository.split('/')[0].lower() == 'yourname':
        raise ValueError('configure the real GitHub owner/repository')
    if not TAG.fullmatch(tag):
        raise ValueError('release tag must be vMAJOR.MINOR.PATCH with an optional prerelease suffix')
    if commit is not None and not re.fullmatch(r'[a-fA-F0-9]{40}', commit):
        raise ValueError('release commit must be a full 40-character commit ID')


def check_metadata(root, repository, tag):
    validate_identity(repository, tag)
    package = json.loads((root / 'vscode-extension/package.json').read_text())
    module = re.search(r'^module\s+(\S+)', (root / 'go.mod').read_text(), re.MULTILINE)
    problems = []
    if not module or module.group(1) != f'github.com/{repository}':
        problems.append(f'Go module and imports must use github.com/{repository} for the root module layout')
    url = package.get('repository', {})
    if isinstance(url, dict): url = url.get('url', '')
    if not isinstance(url, str) or url.removesuffix('.git').lower() != f'https://github.com/{repository}'.lower():
        problems.append('extension repository URL must match the release repository')
    if not package.get('publisher') or package['publisher'].lower() == 'yourname':
        problems.append('configure the real Marketplace publisher')
    if package.get('version') != tag[1:].split('-')[0]:
        problems.append('extension version must match the release tag, excluding its prerelease suffix')
    if not package.get('license') or package['license'] == 'UNLICENSED':
        problems.append('choose and declare the extension license')
    license_path = root / 'LICENSE'
    if license_path.is_symlink() or not license_path.is_file() or not license_path.read_text().strip():
        problems.append('add the chosen LICENSE text at the repository root')
    if problems: raise ValueError('; '.join(problems))
    return package


def asset_names(directory, require_vsix=False):
    if directory.is_symlink() or not directory.is_dir():
        raise ValueError('release directory must be a real directory')
    entries = {p.name: p for p in directory.iterdir()}
    vsix = [name for name in entries if name.endswith('.vsix')]
    if len(vsix) > 1 or (require_vsix and len(vsix) != 1):
        raise ValueError('release must contain exactly one VSIX')
    names = set(BINARIES) | set(vsix)
    if set(entries) - names - {'SHA256SUMS'} or not names <= entries.keys():
        raise ValueError('release contains missing or unexpected asset names')
    for name in names | ({'SHA256SUMS'} if 'SHA256SUMS' in entries else set()):
        if entries[name].is_symlink() or not entries[name].is_file():
            raise ValueError(f'release asset is not a regular file: {name}')
    return sorted(names)


def seal_assets(directory):
    names = asset_names(directory)
    lines = [f'{hashlib.sha256((directory / name).read_bytes()).hexdigest()}  {name}\n' for name in names]
    (directory / 'SHA256SUMS').write_text(''.join(lines))


def verify_assets(directory, require_vsix=False, package=None):
    names = asset_names(directory, require_vsix)
    checksums = {}
    try:
        for line in (directory / 'SHA256SUMS').read_text().splitlines():
            match = re.fullmatch(r'([a-f0-9]{64})  ([A-Za-z0-9_.-]+)', line)
            if not match or match[2] in checksums:
                raise ValueError('malformed, unsafe or duplicate checksum entry')
            checksums[match[2]] = match[1]
    except FileNotFoundError as error:
        raise ValueError('release checksum manifest is missing') from error
    if set(checksums) != set(names):
        raise ValueError('checksum entries must match the exact release assets')
    for name in names:
        if hashlib.sha256((directory / name).read_bytes()).hexdigest() != checksums[name]:
            raise ValueError(f'release checksum mismatch: {name}')
        if name.endswith('.vsix') and package is not None:
            try:
                with zipfile.ZipFile(directory / name) as archive:
                    embedded = json.loads(archive.read('extension/package.json'))
                    if not archive.read('extension/LICENSE.txt').strip():
                        raise ValueError('VSIX license is empty')
                if any(embedded.get(key) != package.get(key) for key in ('name', 'publisher', 'version', 'license')):
                    raise ValueError('VSIX identity does not match configured release metadata')
            except (KeyError, zipfile.BadZipFile, json.JSONDecodeError) as error:
                raise ValueError('VSIX must contain its package metadata and license') from error
    return [directory / name for name in names] + [directory / 'SHA256SUMS']


def smoke_native(directory, tag, commit):
    arch = {'arm64':'arm64','aarch64':'arm64','x86_64':'amd64','amd64':'amd64'}.get(platform.machine().lower())
    system = {'Darwin':'darwin','Linux':'linux','Windows':'windows'}.get(platform.system())
    if not arch or not system: raise ValueError('unsupported release smoke-test host')
    binary = directory / f'ifttt-{system}-{arch}'
    if system == 'windows': binary = binary.with_suffix('.exe')
    if system != 'windows': binary.chmod(binary.stat().st_mode | 0o111)
    result = subprocess.run([str(binary.resolve()), '--version'], capture_output=True, text=True, check=True, timeout=15)
    if result.stdout != f'ifttt {tag} (commit {commit})\n':
        raise ValueError('native release executable has incorrect version/commit metadata')
    with tempfile.TemporaryDirectory(prefix='ifttt-release-smoke-') as temporary:
        root = Path(temporary)
        (root / '.ifttt-lint.yaml').write_text('directives:\n  prefix: LINT\nrules:\n  unknown_directive: error\n')
        (root / 'source.go').write_text('// LINT.IfChange(API)\nvar api = 1\n// LINT.ThenChange(//target.md:API)\n')
        target = root / 'target.md'
        target.write_text('<!-- LINT.IfChange(API) -->\nAPI: 1\n<!-- LINT.ThenChange() -->\n')
        for expected in (0, 1):
            if expected: target.write_text('API: 1\n')
            result = subprocess.run([str(binary.resolve()), '--scan', '.', '--strict=true', '--format=json'], cwd=root,
                                    capture_output=True, text=True, timeout=15)
            findings = json.loads(result.stdout).get('errors')
            if result.returncode != expected or not isinstance(findings, list) or (not expected and findings):
                raise ValueError('native release structural smoke test failed')
            if expected and not any(f.get('ruleId') == 'label_missing' for f in findings):
                raise ValueError('native release did not detect a missing target label')


def gh(args):
    return subprocess.run(['gh', *args], check=True, capture_output=True, text=True, timeout=120).stdout


def tag_commit(repository, tag):
    value = json.loads(gh(['api', f'repos/{repository}/git/ref/tags/{tag}']))['object']
    for _ in range(5):
        if value.get('type') == 'commit': return value.get('sha')
        if value.get('type') != 'tag': break
        value = json.loads(gh(['api', f'repos/{repository}/git/tags/{value["sha"]}']))['object']
    raise ValueError('remote release tag does not identify a commit')


def get_release(repository, tag):
    try:
        return json.loads(gh(['release','view',tag,'--repo',repository,'--json','isDraft,targetCommitish,tagName']))
    except subprocess.CalledProcessError as error:
        if 'release not found' in error.stderr.lower() or 'http 404' in error.stderr.lower(): return None
        raise


def ensure_release_identity(existing, tag, commit):
    if not existing or existing.get('tagName') != tag or existing.get('targetCommitish') != commit:
        raise ValueError('GitHub release does not match the selected tag and commit')


def create_draft(directory, repository, tag, commit):
    validate_identity(repository, tag, commit)
    assets = verify_assets(directory, require_vsix=True)
    if tag_commit(repository, tag) != commit: raise ValueError('remote tag no longer matches the selected commit')
    existing = get_release(repository, tag)
    if existing:
        ensure_release_identity(existing, tag, commit)
        if not existing.get('isDraft'): raise ValueError('cannot overwrite a published release')
        gh(['release','upload',tag,'--repo',repository,'--clobber',*[str(p) for p in assets]])
    else:
        gh(['release','create',tag,'--repo',repository,'--draft','--verify-tag','--target',commit,'--title',tag,'--generate-notes',*[str(p) for p in assets]])


def publish(directory, repository, tag, commit, package):
    validate_identity(repository, tag, commit)
    verify_assets(directory, require_vsix=True, package=package)
    if tag_commit(repository, tag) != commit: raise ValueError('remote tag no longer matches the selected commit')
    existing = get_release(repository, tag)
    ensure_release_identity(existing, tag, commit)
    if existing.get('isDraft'):
        prerelease = '-' in tag
        gh(['release','edit',tag,'--repo',repository,'--draft=false',f'--prerelease={str(prerelease).lower()}',f'--latest={str(not prerelease).lower()}'])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('operation', choices=['check','seal','verify','draft','publish'])
    parser.add_argument('--repository')
    parser.add_argument('--tag')
    parser.add_argument('--commit')
    parser.add_argument('--assets', type=Path, default=ROOT/'build/release')
    parser.add_argument('--require-vsix', action='store_true')
    parser.add_argument('--smoke', action='store_true')
    args = parser.parse_args()
    try:
        package = None
        if args.operation in ('check','draft','publish') or (args.operation == 'verify' and args.repository):
            if not args.repository or not args.tag: raise ValueError('--repository and --tag are required')
            package = check_metadata(ROOT, args.repository, args.tag)
        if args.operation == 'seal': seal_assets(args.assets)
        elif args.operation == 'verify':
            verify_assets(args.assets,args.require_vsix,package)
            if args.smoke:
                if not args.tag or not args.commit: raise ValueError('--smoke requires --tag and --commit')
                smoke_native(args.assets,args.tag,args.commit)
        elif args.operation in ('draft','publish'):
            if not args.commit: raise ValueError('--commit is required')
            verify_assets(args.assets,True,package)
            smoke_native(args.assets,args.tag,args.commit)
            if args.operation == 'draft': create_draft(args.assets,args.repository,args.tag,args.commit)
            else: publish(args.assets,args.repository,args.tag,args.commit,package)
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        parser.exit(1, f'release: {error}\n')


if __name__ == '__main__': main()
