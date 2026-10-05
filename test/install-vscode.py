#!/usr/bin/env python3
"""Download the pinned official Linux test host; installation belongs to CI."""
import hashlib
from pathlib import Path
import urllib.request

URL = ('https://vscode.download.prss.microsoft.com/dbazure/download/stable/'
       '07f806f999227108933c2e30515b26eecc1fda74/code_1.140.0-1790759618_amd64.deb')
SHA256 = 'e5ddfa528d68ce907c92cba18ed4edd7420874fe828cbaaf8e4484aa33530c3b'
destination = Path(__file__).resolve().parents[1] / 'build/tools/vscode.deb'
destination.parent.mkdir(parents=True, exist_ok=True)
digest = hashlib.sha256()
temporary = destination.with_suffix('.tmp')
try:
    with urllib.request.urlopen(URL, timeout=60) as response, temporary.open('wb') as output:
        size = 0
        while chunk := response.read(1024 * 1024):
            size += len(chunk)
            if size > 256 * 1024 * 1024:
                raise SystemExit('VS Code archive exceeds test-host limit')
            digest.update(chunk)
            output.write(chunk)
    if digest.hexdigest() != SHA256:
        raise SystemExit('VS Code archive checksum does not match official release')
    temporary.replace(destination)
finally:
    temporary.unlink(missing_ok=True)
print(destination)
