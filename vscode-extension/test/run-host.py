#!/usr/bin/env python3
"""Exercise the extension in an installed VS Code host, with isolated state."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time

extension = Path(__file__).resolve().parents[1]
project = extension.parent
native_mac_host = Path('/Applications/Visual Studio Code.app/Contents/MacOS/Code')
code = os.environ.get('IFTTT_VSCODE_BINARY') or (str(native_mac_host) if native_mac_host.exists() else shutil.which('code'))
if not code:
    raise SystemExit('Install VS Code or set IFTTT_VSCODE_BINARY to its CLI executable')
backend = os.environ.get('IFTTT_HOST_VCS', 'git')
if backend not in ('git', 'jj'):
    raise SystemExit('IFTTT_HOST_VCS must be git or jj')
environment = os.environ.copy()
# The Electron CLI shim may exit successfully without launching an extension host.
environment.pop('ELECTRON_RUN_AS_NODE', None)
environment['PATH'] = str(project / 'build/tools') + os.pathsep + environment.get('PATH', '')
with tempfile.TemporaryDirectory(prefix='ifttt-host-') as temporary:
    state = Path(temporary)
    root = state / 'workspace'
    root.mkdir()
    binary = state / ('ifttt.exe' if os.name == 'nt' else 'ifttt')
    subprocess.run(['go', 'build', '-o', str(binary), './cmd/ifttt'], cwd=project, check=True, env=environment)
    subprocess.run(['npm', 'run', 'compile'], cwd=extension, check=True, env=environment)
    def vcs(*args):
        subprocess.run([backend, *args], cwd=root, check=True, env=environment, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if backend == 'git':
        vcs('init', '-q')
        vcs('config', 'user.name', 'Test')
        vcs('config', 'user.email', 'test@example.invalid')
    else:
        vcs('git', 'init')
        vcs('config', 'set', '--repo', 'user.name', 'Test')
        vcs('config', 'set', '--repo', 'user.email', 'test@example.invalid')
    source = '// SENTRY.IfChange("API")\nvar api = 1\n// SENTRY.ThenChange("target.go")\n'
    (root / '.ifttt-lint.yaml').write_text('directives:\n  prefix: SENTRY\n')
    (root / 'source.go').write_text(source)
    (root / 'target.go').write_text('var target = 1\n')
    (root / 'navigation.go').write_text('// SENTRY.Label("primary_label")\nvar navigation = 1\n// SENTRY.EndLabel\n')
    if backend == 'git':
        vcs('add', '.')
        vcs('commit', '-qm', 'initial')
    else:
        vcs('describe', '-m', 'initial')
        vcs('new')
    (root / 'source.go').write_text(source.replace('api = 1', 'api = 2'))
    (root / 'nested cwd').mkdir()
    (root / '.vscode').mkdir()
    (root / '.vscode/settings.json').write_text(json.dumps({
        'iftttLint.binary': str(binary), 'iftttLint.vcs': backend,
        'iftttLint.diffMode': 'working-tree', 'iftttLint.diffCommand': '',
        'iftttLint.workingDirectory': 'nested cwd',
        'iftttLint.runOnSave': False, 'iftttLint.downloadBaseUrl': '',
        'extensions.ignoreRecommendations': True
    }))
    secondary = state / 'secondary'
    secondary.mkdir()
    primary = root
    root = secondary
    if backend == 'git':
        vcs('init', '-q')
        vcs('config', 'user.name', 'Test')
        vcs('config', 'user.email', 'test@example.invalid')
    else:
        vcs('git', 'init')
        vcs('config', 'set', '--repo', 'user.name', 'Test')
        vcs('config', 'set', '--repo', 'user.email', 'test@example.invalid')
    (secondary / 'source.go').write_text('// LINT.IfChange(API)\nvar api = 1\n// LINT.ThenChange(//target.go:shared_label)\n')
    (secondary / 'target.go').write_text('const example = "LINT.IfChange(shared_label)"\n// See LINT.IfChange(shared_label)\n// ```go\n// LINT.IfChange(shared_label)\n// LINT.ThenChange()\n// ```\n// LINT.IfChange(shared_label)\nvar target = 1\n// LINT.ThenChange()\n')
    if backend == 'git':
        vcs('add', '.')
        vcs('commit', '-qm', 'secondary baseline')
    else:
        vcs('describe', '-m', 'secondary baseline')
        vcs('new')
    (secondary / '.vscode').mkdir()
    settings = json.loads((primary / '.vscode/settings.json').read_text())
    settings['iftttLint.runOnSave'] = True
    settings['iftttLint.workingDirectory'] = '.'
    (secondary / '.vscode/settings.json').write_text(json.dumps(settings))
    nested_repo = state / 'nested-repository'
    nested_repo.mkdir()
    root = nested_repo
    if backend == 'git':
        vcs('init', '-q')
        vcs('config', 'user.name', 'Test')
        vcs('config', 'user.email', 'test@example.invalid')
    else:
        vcs('git', 'init')
        vcs('config', 'set', '--repo', 'user.name', 'Test')
        vcs('config', 'set', '--repo', 'user.email', 'test@example.invalid')
    opened = nested_repo / 'opened'
    opened.mkdir()
    (opened / 'driver.go').write_text('var driver = 1\n')
    (nested_repo / 'source.go').write_text('// LINT.IfChange(API)\nvar api = 1\n// LINT.ThenChange(//target.go:shared_label)\n')
    (nested_repo / 'target.go').write_text('// LINT.IfChange(shared_label)\nvar target = 1\n// LINT.ThenChange()\n')
    for config_root in (nested_repo, opened):
        (config_root / '.ifttt-lint.yaml').write_text('directives:\n  prefix: LINT\n')
    if backend == 'git':
        vcs('add', '.')
        vcs('commit', '-qm', 'nested baseline')
    else:
        vcs('describe', '-m', 'nested baseline')
        vcs('new')
    (nested_repo / 'source.go').write_text((nested_repo / 'source.go').read_text().replace('api = 1', 'api = 2'))
    (opened / '.vscode').mkdir()
    nested_settings = dict(settings)
    nested_settings['iftttLint.runOnSave'] = False
    (opened / '.vscode/settings.json').write_text(json.dumps(nested_settings))
    workspace = state / 'fixture.code-workspace'
    workspace.write_text(json.dumps({'folders': [{'path': str(primary)}, {'path': str(secondary)}, {'path': str(opened)}]}))
    marker = state / 'host-result.json'
    environment['IFTTT_HOST_RESULT'] = str(marker)
    # LINT.IfChange(default_binary_host)
    environment['PATH'] = str(state) + os.pathsep + environment['PATH']
    # LINT.ThenChange(//vscode-extension/test/host/index.js:default_binary_host)
    command = [code, '--user-data-dir', str(state / 'user'),
                    '--extensions-dir', str(state / 'extensions'),
                    '--disable-workspace-trust', '--skip-welcome', '--skip-release-notes',
                    '--extensionDevelopmentPath=' + str(extension),
                    '--extensionTestsPath=' + str(extension / 'test/host'),
                    str(workspace)]
    if sys.platform.startswith('linux'):
        # Match vscode-test's sandbox flags and use software rendering under Xvfb.
        command[1:1] = ['--no-sandbox', '--disable-gpu-sandbox', '--disable-gpu']
    # Native macOS VS Code can keep its application process alive after the test
    # extension host exits. Completion is the explicit assertion result, not an
    # unrelated application lifecycle event.
    process = subprocess.Popen(command, env=environment)
    try:
        deadline = time.monotonic() + 120
        while not marker.exists() and process.poll() is None:
            if time.monotonic() >= deadline:
                raise SystemExit('VS Code extension host assertions timed out')
            time.sleep(0.1)
    finally:
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=10)


    if not marker.exists():
        raise SystemExit('VS Code exited without executing extension host assertions')
    result = json.loads(marker.read_text())
    if result.get('passed') is not True:
        raise SystemExit('Extension host failed: ' + str(result.get('error', 'no assertion result')))
    print(f'Actual VS Code {backend} extension host assertions passed')
