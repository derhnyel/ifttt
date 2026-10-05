#!/usr/bin/env python3
"""Download checksum-verified official ifttt-lint v0.11.2 for the host."""
import argparse
import hashlib
import json
import platform
from pathlib import Path
import tarfile
import tempfile
import urllib.request

VERSION = 'v0.11.2'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, default=Path('build/upstream/ifttt-lint'))
    opts = parser.parse_args()
    arch = {'arm64': 'aarch64', 'aarch64': 'aarch64', 'x86_64': 'x86_64'}.get(platform.machine())
    target = {'Darwin': 'apple-darwin', 'Linux': 'unknown-linux-gnu'}.get(platform.system())
    if not arch or not target:
        parser.error('supported release platforms are ARM64/x86-64 macOS/Linux')
    name = f'ifttt-lint-{arch}-{target}'
    base = f'https://github.com/simonepri/ifttt-lint/releases/download/{VERSION}/{name}'
    checksum = urllib.request.urlopen(base + '.sha256', timeout=30).read().decode().split()[0]
    archive = urllib.request.urlopen(base + '.tar.gz', timeout=30).read()
    actual = hashlib.sha256(archive).hexdigest()
    if actual != checksum:
        raise RuntimeError(f'checksum mismatch: expected {checksum}, got {actual}')
    with tempfile.TemporaryFile() as stream:
        stream.write(archive)
        stream.seek(0)
        with tarfile.open(fileobj=stream, mode='r:gz') as tar:
            entries = [entry for entry in tar.getmembers() if entry.isfile() and Path(entry.name).name == 'ifttt-lint']
            if len(entries) != 1:
                raise RuntimeError('archive must contain exactly one ifttt-lint executable')
            content = tar.extractfile(entries[0]).read()
    opts.output.parent.mkdir(parents=True, exist_ok=True)
    opts.output.write_bytes(content)
    opts.output.chmod(0o755)
    opts.output.with_suffix('.provenance.json').write_text(json.dumps(dict(version=VERSION, archive_url=base + '.tar.gz', archive_sha256=actual, executable_sha256=hashlib.sha256(content).hexdigest()), indent=2) + '\n')
    print(f'{opts.output}: verified {VERSION}, archive SHA256 {actual}')

if __name__ == '__main__':
    main()
