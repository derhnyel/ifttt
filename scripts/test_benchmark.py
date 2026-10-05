import unittest
import benchmark

class BenchmarkTests(unittest.TestCase):
    def test_summary_nearest_rank_and_spread(self):
        result = benchmark.summary(list(range(1, 21)))
        self.assertEqual(result['median_ms'], 10.5)
        self.assertEqual(result['p95_ms'], 19)
        self.assertEqual(result['min_ms'], 1)
        self.assertEqual(result['max_ms'], 20)
    def test_count_does_not_hide_invalid_output(self):
        self.assertEqual(benchmark.count_findings('go', '{"errors": []}'), 0)
        self.assertEqual(benchmark.count_findings('upstream', ''), 0)
        self.assertEqual(benchmark.count_findings('upstream', '{"diagnostics": [{}, {}]}'), 2)
        with self.assertRaises(ValueError):
            benchmark.count_findings('go', '')
        with self.assertRaises(KeyError):
            benchmark.count_findings('go', '{}')


class ProvenanceTests(unittest.TestCase):
    def test_modified_upstream_binary_rejected_and_unknown_provenance_not_invented(self):
        import tempfile
        import hashlib
        import json
        from pathlib import Path
        with tempfile.TemporaryDirectory() as directory:
            binary = Path(directory) / 'ifttt-lint'
            binary.write_bytes(b'verified binary')
            self.assertIsNone(benchmark.archive_provenance(str(binary)))
            binary.with_suffix('.provenance.json').write_text(json.dumps(dict(executable_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(), archive_sha256='archive')))
            self.assertEqual(benchmark.archive_provenance(str(binary)), 'archive')
            binary.write_bytes(b'modified binary')
            with self.assertRaises(RuntimeError):
                benchmark.archive_provenance(str(binary))

class FixtureTests(unittest.TestCase):
    def test_fixture_diff_changes_sources_and_targets_only_when_passing(self):
        import tempfile
        from pathlib import Path
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            passing = benchmark.fixture(root / 'pass', 2).decode()
            failing = benchmark.fixture(root / 'fail', 2, failing=True).decode()
            self.assertEqual(passing.count('diff --git'), 4)
            self.assertEqual(failing.count('diff --git'), 2)
            self.assertIn('LINT.IfChange(pair_0)', failing)
            self.assertIn('LINT.ThenChange(//target_0.go)', failing)
            self.assertNotIn('diff --git a/target_', failing)

class HookTests(unittest.TestCase):
    def test_real_staged_git_hook_forwards_options_without_evaluation(self):
        import os
        import subprocess
        import tempfile
        from pathlib import Path
        hook = Path(__file__).resolve().parents[1] / 'scripts/pre-commit-ifttt.sh'
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            benchmark.git(root, 'init', '-q')
            (root / 'source.go').write_text('changed\n')
            benchmark.git(root, 'add', 'source.go')
            bindir = root / 'bin'
            bindir.mkdir()
            executable = bindir / 'ifttt'
            executable.write_text("#!/usr/bin/env python3\nimport json,sys\nfrom pathlib import Path\nPath('captured.json').write_text(json.dumps(dict(args=sys.argv[1:], stdin=sys.stdin.read())))\n")
            executable.chmod(0o755)
            env = dict(os.environ, PATH=str(bindir) + os.pathsep + os.environ['PATH'])
            custom = ['-w', '-ignore', 'literal $(touch injected)']
            result = subprocess.run(['bash', str(hook), *custom], cwd=root, env=env, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr.decode())
            import json
            captured = json.loads((root / 'captured.json').read_text())
            self.assertEqual(captured['args'], ['--vcs', 'git', '--staged', '--format=text', *custom])
            self.assertEqual(captured['stdin'], '')
            self.assertFalse((root / 'injected').exists())

    def test_hook_missing_binary_fails_closed(self):
        import os
        import subprocess
        from pathlib import Path
        hook = Path(__file__).resolve().parents[1] / 'scripts/pre-commit-ifttt.sh'
        result = subprocess.run(['/bin/bash', str(hook)], env=dict(os.environ, PATH='/usr/bin:/bin'), capture_output=True)
        self.assertEqual(result.returncode, 2)
        self.assertIn(b'install it before committing', result.stderr)

class DeclarativeHookTests(unittest.TestCase):
    # LINT.IfChange(pre_commit_hooks)
    def test_prepush_range_pin_and_argument_forwarding(self):
        import os
        import subprocess
        import tempfile
        import shlex
        import json
        import re
        from pathlib import Path
        readme = (Path(__file__).resolve().parents[1] / 'README.md').read_text()
        source = next(block for block in re.findall(r'```yaml\n(.*?)\n```', readme, re.DOTALL)
                      if 'repo: local' in block and 'id: ifttt-diff' in block)
        entry = source.split('entry: ', 2)[2].splitlines()[0]
        command = shlex.split(entry)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            executable = root / 'ifttt'
            executable.write_text("#!/usr/bin/env python3\nimport json,sys\nfrom pathlib import Path\nPath('captured.json').write_text(json.dumps(sys.argv[1:]))\n")
            executable.chmod(0o755)
            env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ['PATH'], PRE_COMMIT_FROM_REF='', PRE_COMMIT_TO_REF='')
            result = subprocess.run(command, cwd=root, env=env, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr.decode())
            self.assertFalse((root / 'captured.json').exists())
            env.update(PRE_COMMIT_FROM_REF='before', PRE_COMMIT_TO_REF='after')
            result = subprocess.run([*command, '--ignore', 'literal $(touch injected)'], cwd=root, env=env, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr.decode())
            self.assertEqual(json.loads((root / 'captured.json').read_text()), ['--vcs', 'git', '--ignore', 'literal $(touch injected)', '--diff', 'before..after'])
            self.assertFalse((root / 'injected').exists())
    # LINT.ThenChange(//README.md:pre_commit_hooks)

class ActionTests(unittest.TestCase):
    def test_event_modes_use_git_and_preserve_literal_arguments(self):
        import os
        import subprocess
        import tempfile
        import json
        from pathlib import Path
        source = (Path(__file__).resolve().parents[1] / 'action.yml').read_text()
        script = source.rsplit('      run: |\n', 1)[1]
        script = '\n'.join(line[8:] for line in script.splitlines())
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for name, body in [('ifttt', "import json,sys\nfrom pathlib import Path\nPath('captured.json').write_text(json.dumps(sys.argv[1:]))"), ('git', "import sys\nif sys.argv[1:] == ['rev-parse', 'HEAD']: print('b' * 40)")]:
                executable = root / name
                executable.write_text('#!/usr/bin/env python3\n' + body + '\n')
                executable.chmod(0o755)
            env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ['PATH'], WORKDIR='', DIFF_PATH='', CHANGE_SET_PATH='', EXTRA_ARGS='--ignore=$(touch-injected)', BASE_SHA='a' * 40, HEAD_SHA='b' * 40)
            for event, tail in [('pull_request', ['--diff', 'a' * 40 + '...' + 'b' * 40]), ('push', ['**/*']), ('workflow_dispatch', [])]:
                result = subprocess.run(['bash', '-c', script], cwd=root, env=dict(env, EVENT_NAME=event), capture_output=True)
                self.assertEqual(result.returncode, 0, result.stderr.decode())
                self.assertEqual(json.loads((root / 'captured.json').read_text()), ['--vcs', 'git', '--ignore=$(touch-injected)', *tail])
            env.update(EVENT_NAME='push', DIFF_PATH='patch with spaces.diff')
            result = subprocess.run(['bash', '-c', script], cwd=root, env=env, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr.decode())
            self.assertEqual(json.loads((root / 'captured.json').read_text())[-1], 'patch with spaces.diff')

if __name__ == '__main__': unittest.main()
