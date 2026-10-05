#!/usr/bin/env python3
"""Download the pinned official Linux test host; installation belongs to CI."""
import hashlib
from http.client import HTTPException
from pathlib import Path
import sys
import time
import urllib.request
import uuid

URL = ('https://vscode.download.prss.microsoft.com/dbazure/download/stable/'
       '07f806f999227108933c2e30515b26eecc1fda74/code_1.140.0-1790759618_amd64.deb')
SHA256 = 'e5ddfa528d68ce907c92cba18ed4edd7420874fe828cbaaf8e4484aa33530c3b'


# LINT.IfChange(verified_host_download)
def download(url, checksum, destination):
    destination.parent.mkdir(parents=True, exist_ok=True)
    temporary = destination.with_suffix('.tmp')
    for attempt in range(3):
        selected = url
        if attempt:
            selected += ('&' if '?' in url else '?') + 'ifttt_download=' + uuid.uuid4().hex
        request = urllib.request.Request(selected, headers={'Cache-Control':'no-cache'})
        try:
            digest = hashlib.sha256()
            size = 0
            with urllib.request.urlopen(request, timeout=60) as response, temporary.open('wb') as output:
                if response.status != 200 or response.headers.get('Content-Range'):
                    raise ValueError(f'VS Code archive requires a complete HTTP 200 response; got {response.status}')
                while chunk := response.read(1024 * 1024):
                    size += len(chunk)
                    if size > 256 * 1024 * 1024:
                        raise ValueError('VS Code archive exceeds test-host limit')
                    digest.update(chunk)
                    output.write(chunk)
            actual = digest.hexdigest()
            if actual != checksum:
                raise ValueError(f'VS Code archive checksum mismatch: received {size} bytes, '
                                 f'SHA256 {actual}; expected {checksum}')
            temporary.replace(destination)
            return
        except (OSError, HTTPException, ValueError) as error:
            print(f'VS Code download attempt {attempt+1}/3: {error}', file=sys.stderr)
            if attempt == 2:
                raise ValueError('VS Code archive could not be verified: ' + str(error)) from error
            time.sleep(attempt+1)
        finally:
            temporary.unlink(missing_ok=True)
# LINT.ThenChange(//scripts/test_vscode_download.py:verified_host_download)


if __name__ == '__main__':
    destination = Path(__file__).resolve().parents[1] / 'build/tools/vscode.deb'
    try:
        download(URL, SHA256, destination)
    except (OSError, HTTPException, ValueError) as error:
        raise SystemExit(str(error)) from error
    print(destination)
