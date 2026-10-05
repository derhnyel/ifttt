import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).with_name('build-release.sh')
PLATFORMS = (
    ('linux', 'amd64'), ('linux', 'arm64'),
    ('darwin', 'amd64'), ('darwin', 'arm64'),
    ('windows', 'amd64'), ('windows', 'arm64'),
)
ASSETS = [f'ifttt-{os_name}-{arch}' + ('.exe' if os_name == 'windows' else '')
          for os_name, arch in PLATFORMS]


class ReleaseBuildTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='ifttt-release-test-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / 'scripts').mkdir()
        shutil.copy2(SCRIPT, self.root / 'scripts/build-release.sh')
        (self.root / 'build').mkdir()
        self.legacy = {
            'ifttt': b'user native executable',
            'ifttt-linux-amd64': b'historical release',
            'ifttt-obsolete-platform': b'historical obsolete release',
            'SHA256SUMS': b'historical checksums',
        }
        for name, data in self.legacy.items():
            (self.root / 'build' / name).write_bytes(data)
        self.fake_bin = self.root / 'bin'
        self.fake_bin.mkdir()
        fake_go = self.fake_bin / 'go'
        fake_go.write_text(f'#!{sys.executable}\n' + '''
import os
from pathlib import Path
import sys

log = Path(os.environ['BUILD_LOG'])
with log.open('a') as file:
    file.write(repr((os.environ.get('CGO_ENABLED'), os.environ.get('GOOS'),
                     os.environ.get('GOARCH'), sys.argv[1:])) + '\\n')
if len(log.read_text().splitlines()) == int(os.environ.get('FAIL_BUILD', '0')):
    sys.exit(1)
destination = Path(sys.argv[sys.argv.index('-o') + 1])
destination.write_bytes((os.environ['GOOS'] + '/' + os.environ['GOARCH']).encode())
''')
        fake_go.chmod(0o755)
        self.env = os.environ.copy()
        self.env.pop('RELEASE_VERSION', None)
        self.env.pop('RELEASE_COMMIT', None)
        self.env.update(PATH=str(self.fake_bin) + os.pathsep + self.env['PATH'],
                        BUILD_LOG=str(self.root / 'build.log'))
        subprocess.run(['git', 'init', '-q', str(self.root)], check=True,
                       capture_output=True)

    def run_builder(self, **env):
        return subprocess.run(['bash', str(self.root / 'scripts/build-release.sh')],
                              env=dict(self.env, **env), cwd=self.root,
                              capture_output=True, text=True, timeout=30)

    def assert_legacy_unchanged(self):
        for name, data in self.legacy.items():
            self.assertEqual((self.root / 'build' / name).read_bytes(), data)

    def test_isolated_stamped_assets_and_exact_checksums(self):
        output = self.root / 'build/release'
        output.mkdir()
        (output / 'stale-asset').write_text('previous release')
        commit = 'a' * 40
        result = self.run_builder(RELEASE_VERSION='v1.2.3-rc.1', RELEASE_COMMIT=commit)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(sorted(path.name for path in output.iterdir()),
                         sorted(ASSETS + ['SHA256SUMS']))
        checksums = []
        for name, (os_name, arch) in zip(ASSETS, PLATFORMS):
            data = f'{os_name}/{arch}'.encode()
            self.assertEqual((output / name).read_bytes(), data)
            checksums.append(f'{hashlib.sha256(data).hexdigest()}  {name}\n')
        self.assertEqual((output / 'SHA256SUMS').read_text(), ''.join(checksums))
        lines = (self.root / 'build.log').read_text().splitlines()
        self.assertEqual(len(lines), 6)
        for line in lines:
            self.assertIn("('0',", line)
            self.assertIn("'-trimpath'", line)
            self.assertIn('-X main.version=v1.2.3-rc.1', line)
            self.assertIn('-X main.commit=' + commit, line)
        self.assert_legacy_unchanged()
        self.assertEqual(sorted(p.name for p in (self.root / 'build').iterdir()),
                         sorted(list(self.legacy) + ['release']))

    def test_failed_build_preserves_previous_release(self):
        output = self.root / 'build/release'
        output.mkdir()
        (output / 'previous-release').write_bytes(b'last successful release')
        result = self.run_builder(FAIL_BUILD='3', RELEASE_COMMIT='unknown')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual([p.name for p in output.iterdir()], ['previous-release'])
        self.assertEqual((output / 'previous-release').read_bytes(),
                         b'last successful release')
        self.assertEqual(sorted(p.name for p in (self.root / 'build').iterdir()),
                         sorted(list(self.legacy) + ['release']))
        self.assert_legacy_unchanged()

    def test_failed_initial_build_leaves_no_release(self):
        result = self.run_builder(FAIL_BUILD='2', RELEASE_COMMIT='unknown')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.root / 'build/release').exists())
        self.assertEqual(sorted(p.name for p in (self.root / 'build').iterdir()),
                         sorted(self.legacy))
        self.assert_legacy_unchanged()

    def test_rejects_invalid_versions_before_building(self):
        for version in ('', '1.2.3', 'v1.2', 'v1.2.3+', 'v1.2.3-',
                        'v01.2.3', 'v1.2.3-rc..1', 'v1.2.3 -X main.commit=bad',
                        'v1.2.3\n-X main.commit=bad', 'v1.2.3\tbad'):
            with self.subTest(version=version):
                result = self.run_builder(RELEASE_VERSION=version, RELEASE_COMMIT='unknown')
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('RELEASE_VERSION', result.stderr)
                self.assertFalse((self.root / 'build.log').exists())
                self.assertFalse((self.root / 'build/release').exists())
                self.assert_legacy_unchanged()

    def test_rejects_invalid_commits_before_building(self):
        for commit in ('', 'abc1234', 'g' * 40, 'a' * 41,
                       'unknown -X main.version=bad', 'unknown\n', 'unknown\t'):
            with self.subTest(commit=commit):
                result = self.run_builder(RELEASE_COMMIT=commit)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('RELEASE_COMMIT', result.stderr)
                self.assertFalse((self.root / 'build.log').exists())
                self.assertFalse((self.root / 'build/release').exists())
                self.assert_legacy_unchanged()

    def test_unborn_repository_defaults_to_dev_and_unknown(self):
        result = self.run_builder()
        self.assertEqual(result.returncode, 0, result.stderr)
        for line in (self.root / 'build.log').read_text().splitlines():
            self.assertIn('-X main.version=dev', line)
            self.assertIn('-X main.commit=unknown', line)

    def test_commit_defaults_to_repository_head(self):
        subprocess.run(['git', '-C', str(self.root), '-c', 'user.name=Release Test',
                        '-c', 'user.email=release@example.invalid', 'commit',
                        '--allow-empty', '-qm', 'fixture'], check=True, capture_output=True)
        head = subprocess.check_output(['git', '-C', str(self.root), 'rev-parse', 'HEAD'],
                                       text=True).strip()
        result = self.run_builder()
        self.assertEqual(result.returncode, 0, result.stderr)
        for line in (self.root / 'build.log').read_text().splitlines():
            self.assertIn('-X main.commit=' + head, line)


if __name__ == '__main__':
    unittest.main()
