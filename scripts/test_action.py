"""Execute the composite Action's shell steps against real Git repositories."""
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
STEPS = re.findall(r'      run: \|\n((?:        .*\n|\n)+)', (ROOT / 'action.yml').read_text())
BUILD, RUN = [re.sub(r'^        ', '', step, flags=re.MULTILINE) for step in STEPS]


class ActionIntegrationTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.state = tempfile.TemporaryDirectory(prefix='ifttt action integration ')
        cls.addClassCleanup(cls.state.cleanup)
        cls.tools = Path(cls.state.name) / 'tool bin'
        path_file = Path(cls.state.name) / 'github-path'
        environment = dict(os.environ, ACTION_ROOT=str(ROOT), TOOL_BIN=str(cls.tools),
                           RUNNER_OS='Windows' if os.name == 'nt' else 'Linux',
                           GITHUB_PATH=str(path_file), CGO_ENABLED='0')
        subprocess.run(['bash', '-c', BUILD], env=environment, check=True,
                       capture_output=True, text=True, timeout=120)
        if path_file.read_text().strip() != str(cls.tools):
            raise AssertionError('Action did not expose its built executable through GITHUB_PATH')

    def setUp(self):
        self.fixture = tempfile.TemporaryDirectory(prefix='ifttt action consumer ')
        self.addCleanup(self.fixture.cleanup)
        self.root = Path(self.fixture.name)
        self.origin = self.root / 'origin'
        self.origin.mkdir()
        self.git(self.origin, 'init', '-q', '-b', 'main')
        self.git(self.origin, 'config', 'user.name', 'Action test')
        self.git(self.origin, 'config', 'user.email', 'action@example.invalid')
        (self.origin / 'source.go').write_text('// LINT.IfChange(API)\nvar api = 1\n// LINT.ThenChange(//target.md:API)\n')
        (self.origin / 'target.md').write_text('<!-- LINT.IfChange(API) -->\nAPI: 1\n<!-- LINT.ThenChange() -->\n')
        self.base = self.commit('contract baseline')
        self.git(self.origin, 'checkout', '-qb', 'feature')

    def git(self, cwd, *args):
        return subprocess.run(['git', '-c', 'core.hooksPath=', *args], cwd=cwd,
                              check=True, capture_output=True, text=True, timeout=30).stdout.strip()

    def commit(self, message):
        self.git(self.origin, 'add', '.')
        self.git(self.origin, 'commit', '-qm', message)
        return self.git(self.origin, 'rev-parse', 'HEAD')

    def change_source(self):
        path = self.origin / 'source.go'
        path.write_text(path.read_text().replace('api = 1', 'api = 2'))
        self.commit('update API')
        (self.origin / 'unrelated.txt').write_text('second commit\n')
        return self.commit('follow-up change')

    def clone(self):
        checkout = self.root / 'checkout with spaces'
        self.git(self.root, 'clone', '-q', '--depth=1', '--branch', 'feature', self.origin.as_uri(), str(checkout))
        self.assertEqual(self.git(checkout, 'rev-parse', '--is-shallow-repository'), 'true')
        return checkout

    def run_action(self, checkout, expected, **inputs):
        environment = dict(os.environ, PATH=str(self.tools) + os.pathsep + os.environ['PATH'],
                           WORKDIR=str(checkout), CHANGE_SET_PATH='', DIFF_PATH='',
                           EXTRA_ARGS='--format=json', EVENT_NAME='pull_request',
                           BASE_SHA=self.base, HEAD_SHA=self.git(self.origin, 'rev-parse', 'HEAD'))
        environment.update(inputs)
        result = subprocess.run(['bash', '-c', RUN], cwd=self.root, env=environment,
                                capture_output=True, text=True, timeout=60)
        self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
        return result

    def test_shallow_pr_detects_missing_update_and_accepts_paired_update(self):
        self.change_source()
        checkout = self.clone()
        result = self.run_action(checkout, 1)
        self.assertTrue(any(item['ruleId'] == 'then_label_missing' for item in json.loads(result.stdout)['errors']))
        self.assertEqual(self.git(checkout, 'rev-parse', '--is-shallow-repository'), 'false')
        (self.origin / 'target.md').write_text((self.origin / 'target.md').read_text().replace('API: 1', 'API: 2'))
        head = self.commit('update dependent contract')
        self.git(checkout, 'fetch', 'origin', head)
        self.git(checkout, 'checkout', '--detach', head)
        self.assertEqual(json.loads(self.run_action(checkout, 0).stdout)['errors'], [])

    def test_multiline_arguments_preserve_requested_warn_mode(self):
        self.change_source()
        result = self.run_action(self.clone(), 0, EXTRA_ARGS='--format=json\n--warn')
        self.assertTrue(json.loads(result.stdout)['errors'])

    def test_pr_rejects_mismatched_checkout_and_missing_revision_evidence(self):
        self.change_source()
        checkout = self.clone()
        self.git(checkout, 'fetch', 'origin', self.base)
        self.git(checkout, 'checkout', '--detach', self.base)
        self.assertIn('checkout', self.run_action(checkout, 2).stderr.lower())
        self.assertIn('revision', self.run_action(checkout, 2, HEAD_SHA='').stderr.lower())

    def test_push_and_explicit_patch_keep_literal_paths(self):
        self.change_source()
        checkout = self.clone()
        self.assertEqual(json.loads(self.run_action(checkout, 0, EVENT_NAME='push').stdout)['errors'], [])
        patch = checkout / 'patch with spaces.diff'
        patch.write_text(self.git(self.origin, 'diff', self.base, 'HEAD') + '\n')
        result = self.run_action(checkout, 1, DIFF_PATH=patch.name, EVENT_NAME='workflow_dispatch')
        self.assertTrue(json.loads(result.stdout)['errors'])
        self.assertIn('stdin', self.run_action(checkout, 2, DIFF_PATH='-').stderr.lower())

    # LINT.IfChange(repository_ci)
    def run_repository_lint(self, checkout, step_name, expected, **inputs):
        workflow = (ROOT / '.github/workflows/lint.yml').read_text()
        section = workflow.split(f'      - name: {step_name}\n', 1)
        self.assertEqual(len(section), 2, f'Missing repository lint step: {step_name}')
        match = re.search(r'        run: \|\n((?:          .*\n|\n)+)', section[1])
        self.assertIsNotNone(match, f'Missing shell body: {step_name}')
        script = re.sub(r'^          ', '', match.group(1), flags=re.MULTILINE)
        executable = 'ifttt.exe' if os.name == 'nt' else 'ifttt'
        build = checkout / 'build'
        build.mkdir(exist_ok=True)
        shutil.copy2(self.tools / executable, build / executable)
        environment = dict(os.environ, BASE_SHA=self.base,
                           HEAD_SHA=self.git(self.origin, 'rev-parse', 'HEAD'))
        environment.update(inputs)
        result = subprocess.run(['bash', '-c', script], cwd=checkout, env=environment,
                                capture_output=True, text=True, timeout=120)
        self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
        return result

    def test_repository_ci_checks_all_pr_commits_and_requires_the_target_label(self):
        self.change_source()
        (self.origin / 'target.md').write_text('Unrelated edit outside the label.\n' +
                                             (self.origin / 'target.md').read_text())
        self.commit('edit unrelated target text')
        checkout = self.clone()
        self.git(checkout, 'fetch', '--unshallow', 'origin')
        self.run_repository_lint(checkout, 'Check linked edits', 1)
        target = self.origin / 'target.md'
        target.write_text(target.read_text().replace('API: 1', 'API: 2'))
        head = self.commit('update linked label')
        self.git(checkout, 'fetch', 'origin', head)
        self.git(checkout, 'checkout', '--detach', head)
        self.run_repository_lint(checkout, 'Check linked edits', 0)

    def test_repository_ci_checks_merge_group_range_and_rejects_bad_evidence(self):
        head = self.change_source()
        self.git(self.origin, 'checkout', 'main')
        (self.origin / 'main-only.txt').write_text('advance the base branch\n')
        merge_base = self.commit('advance main')
        self.git(self.origin, 'merge', '--no-ff', 'feature', '-m', 'merge queue candidate')
        merge_head = self.git(self.origin, 'rev-parse', 'HEAD')
        checkout = self.clone()
        self.git(checkout, 'fetch', '--unshallow', 'origin')
        self.git(checkout, 'fetch', 'origin', merge_head)
        self.git(checkout, 'checkout', '--detach', merge_head)
        self.run_repository_lint(checkout, 'Check linked edits', 1,
                                 BASE_SHA=merge_base, HEAD_SHA=merge_head)
        self.run_repository_lint(checkout, 'Check linked edits', 2,
                                 BASE_SHA='', HEAD_SHA=merge_head)
        self.run_repository_lint(checkout, 'Check linked edits', 2,
                                 BASE_SHA=merge_base, HEAD_SHA=head)

    def test_repository_ci_structural_check_rejects_broken_links(self):
        checkout = self.clone()
        self.run_repository_lint(checkout, 'Check directive structure', 0)
        (checkout / 'target.md').unlink()
        self.run_repository_lint(checkout, 'Check directive structure', 1)
    # LINT.ThenChange()

    def test_change_set_checks_real_committed_repositories_without_pr_fetches(self):
        contracts = self.root / 'contracts checkout'
        contracts.mkdir()
        self.git(contracts, 'init', '-q', '-b', 'main')
        self.git(contracts, 'config', 'user.name', 'Action test')
        self.git(contracts, 'config', 'user.email', 'action@example.invalid')
        target = contracts / 'target.md'
        target.write_text((self.origin / 'target.md').read_text())
        self.git(contracts, 'add', '.')
        self.git(contracts, 'commit', '-qm', 'contract baseline')
        target_base = self.git(contracts, 'rev-parse', 'HEAD')
        source = self.origin / 'source.go'
        source.write_text(source.read_text().replace('//target.md:API', 'github://acme/contracts/target.md#API'))
        self.base = self.commit('link foreign contract')
        source_head = self.change_source()
        manifest = self.root / 'snapshot manifests' / 'changes.yml'
        manifest.parent.mkdir()
        for expected in (1, 0):
            if not expected:
                target.write_text(target.read_text().replace('API: 1', 'API: 2'))
                self.git(contracts, 'add', '.')
                self.git(contracts, 'commit', '-qm', 'paired contract update')
            manifest.write_text(json.dumps({'version': 1, 'repositories': [
                {'repo': 'acme/api', 'path': str(self.origin), 'vcs': 'git', 'base': self.base, 'head': source_head},
                {'repo': 'acme/contracts', 'path': str(contracts), 'vcs': 'git', 'base': target_base,
                 'head': self.git(contracts, 'rev-parse', 'HEAD')},
            ]}))
            result = self.run_action(self.origin, expected, CHANGE_SET_PATH=str(manifest), HEAD_SHA='')
            findings = json.loads(result.stdout)['errors']
            if expected:
                self.assertTrue(any(item['ruleId'] == 'then_label_missing' for item in findings))
            else:
                self.assertEqual(findings, [])


if __name__ == '__main__':
    unittest.main()
