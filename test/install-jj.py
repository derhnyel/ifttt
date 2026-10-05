#!/usr/bin/env python3
"""Install a pinned, checksum-verified jj executable into ignored build/tools."""
import hashlib
import io
import os
from pathlib import Path
import platform
import tarfile
import urllib.request
import zipfile

VERSION = '0.45.1'
HASHES = {
 'aarch64-apple-darwin': '51ba42e3d0682616f6eb015045bfe45289b396f03511f9897f645ce8e9272743',
 'x86_64-apple-darwin': '6171582d0b5a98a1005cd9643faebff7936812ec264d7968a39d9cef3654a99b',
 'aarch64-pc-windows-msvc': '0f869d108316149f211d2ba50a626b4fe8ef99232db52f13300bde106821be9e',
 'x86_64-pc-windows-msvc': '5dbf2619272c897394d34190a94b21a6ab8d1e8e59fcf7dfc173d8fa8aa90bf0',
 'aarch64-unknown-linux-musl': '7349a43dd5a20dbc998b10114daa0ee63d2ab863fb822c7eb6b0ebca5903cc69',
 'x86_64-unknown-linux-musl': 'f35438350b5d61963aac5dd74ede510b31d6b9690769d1a6268cf058cc825f72',
}
arch = {'arm64': 'aarch64', 'aarch64': 'aarch64', 'amd64': 'x86_64', 'x86_64': 'x86_64'}.get(platform.machine().lower())
system = {'Darwin': 'apple-darwin', 'Linux': 'unknown-linux-musl', 'Windows': 'pc-windows-msvc'}.get(platform.system())
target = f'{arch}-{system}'
if target not in HASHES:
    raise SystemExit(f'Unsupported test platform: {target}')
name = f'jj-v{VERSION}-{target}' + ('.zip' if os.name == 'nt' else '.tar.gz')
url = f'https://github.com/jj-vcs/jj/releases/download/v{VERSION}/{name}'
with urllib.request.urlopen(url, timeout=60) as response:
    archive = response.read(100 * 1024 * 1024)
if hashlib.sha256(archive).hexdigest() != HASHES[target]:
    raise SystemExit('jj archive checksum does not match pinned official release')
executable = 'jj.exe' if os.name == 'nt' else 'jj'
if name.endswith('.zip'):
    with zipfile.ZipFile(io.BytesIO(archive)) as package:
        matches = [n for n in package.namelist() if Path(n).name == executable]
        if len(matches) != 1:
            raise SystemExit('jj archive must contain one executable')
        data = package.read(matches[0])
else:
    with tarfile.open(fileobj=io.BytesIO(archive), mode='r:gz') as package:
        matches = [m for m in package.getmembers() if m.isfile() and Path(m.name).name == executable]
        if len(matches) != 1:
            raise SystemExit('jj archive must contain one executable')
        data = package.extractfile(matches[0]).read()
destination = Path(__file__).resolve().parents[1] / 'build/tools' / executable
destination.parent.mkdir(parents=True, exist_ok=True)
destination.write_bytes(data)
destination.chmod(0o755)
print(destination)
