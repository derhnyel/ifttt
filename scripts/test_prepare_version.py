"""Version preparation uses committed source and keeps npm/changelog metadata aligned."""
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest


class PrepareVersionTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        extension = self.root / 'vscode-extension'
        extension.mkdir()
        (extension / 'package.json').write_text(json.dumps({'name':'ifttt','version':'0.1.0'}))
        (extension / 'package-lock.json').write_text(json.dumps({'version':'0.1.0','packages':{'':{'version':'0.1.0'},'node_modules/fixture':{'version':'2.0.0'}}}))
        (self.root / 'CHANGELOG.md').write_text('# Changelog\n\n## Unreleased\n\n- Automate release checks.\n\n## 0.1.0 — 2026-10-05\n\n- First release.\n')
        self.git('init', '-q')
        self.git('config', 'user.name', 'Release test')
        self.git('config', 'user.email', 'release@example.invalid')
        self.git('add', '.')
        self.git('commit', '-qm', 'baseline')

    def git(self, *args):
        return subprocess.run(['git', *args], cwd=self.root, capture_output=True, text=True, check=True).stdout

    def prepare(self, bump='patch'):
        path = Path(__file__).with_name('prepare_version.py')
        spec = importlib.util.spec_from_file_location('prepare_version', path)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        return module.prepare(self.root, bump, '2026-10-06')

    # LINT.IfChange(version_preparation)
    def test_bumps_version_lockfile_and_promotes_unreleased_notes(self):
        for bump, version in [('patch','0.1.1'), ('minor','0.2.0'), ('major','1.0.0')]:
            with self.subTest(bump=bump):
                self.git('reset', '--hard', 'HEAD')
                self.assertEqual(self.prepare(bump), 'v' + version)
                package = json.loads((self.root / 'vscode-extension/package.json').read_text())
                lock = json.loads((self.root / 'vscode-extension/package-lock.json').read_text())
                self.assertEqual(package['version'], version)
                self.assertEqual(lock['version'], version)
                self.assertEqual(lock['packages']['']['version'], version)
                self.assertEqual(lock['packages']['node_modules/fixture']['version'], '2.0.0')
                changelog = (self.root / 'CHANGELOG.md').read_text()
                self.assertIn(f'## Unreleased\n\n## {version} — 2026-10-06\n\n- Automate release checks.', changelog)
                self.assertIn('## 0.1.0 — 2026-10-05', changelog)

    def test_refuses_dirty_empty_duplicate_and_invalid_release_without_partial_writes(self):
        cases = [('dirty', None), ('empty', '# Changelog\n\n## Unreleased\n\n## 0.1.0\n- First\n'),
                 ('duplicate', '# Changelog\n\n## Unreleased\n- New\n\n## 0.1.1\n- Already\n'),
                 ('missing', '# Changelog\n\n## 0.1.0\n- First\n')]
        for name, text in cases:
            with self.subTest(name=name):
                self.git('reset', '--hard', 'HEAD')
                if text is not None:
                    (self.root / 'CHANGELOG.md').write_text(text)
                    self.git('add', '.')
                    self.git('commit', '-qm', name)
                else:
                    (self.root / 'CHANGELOG.md').write_text('uncommitted')
                before = (self.root / 'vscode-extension/package.json').read_bytes()
                with self.assertRaises(ValueError): self.prepare()
                self.assertEqual((self.root / 'vscode-extension/package.json').read_bytes(), before)
                self.git('reset', '--hard', 'HEAD' if name == 'dirty' else 'HEAD~1')
        with self.assertRaises(ValueError): self.prepare('unknown')
    # LINT.ThenChange(//scripts/prepare_version.py:version_preparation)
