import hashlib
import json
from pathlib import Path
import tempfile
import subprocess
import unittest
from unittest.mock import patch
import zipfile

import release


class ReleaseTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        (self.root / 'vscode-extension').mkdir(parents=True)
        (self.root / 'go.mod').write_text('module github.com/acme/ifttt\n\ngo 1.26.0\n')
        (self.root / 'LICENSE').write_text('Fixture license text\n')
        self.package = dict(name='ifttt', publisher='acme', version='0.1.0', license='MIT', repository={'url': 'https://github.com/acme/ifttt.git'})
        self.write_package()
        self.assets = self.root / 'assets'
        self.assets.mkdir()
        for name in release.BINARIES:
            (self.assets / name).write_bytes(('fixture ' + name).encode())
        with zipfile.ZipFile(self.assets / 'ifttt-0.1.0.vsix', 'w') as archive:
            archive.writestr('extension/package.json', json.dumps(self.package))
            archive.writestr('extension/LICENSE.txt', 'Fixture license text\n')
        self.commit = 'a' * 40
        tag_patch = patch.object(release, 'tag_commit', return_value=self.commit, create=True)
        tag_patch.start(); self.addCleanup(tag_patch.stop)

    def write_package(self):
        (self.root / 'vscode-extension/package.json').write_text(json.dumps(self.package))

    def test_public_metadata_accepts_real_identity_and_matching_version(self):
        release.check_metadata(self.root, 'acme/ifttt', 'v0.1.0')
        release.check_metadata(self.root, 'acme/ifttt', 'v0.1.0-rc.1')

    def test_public_metadata_rejects_placeholders_missing_license_module_layout_and_version(self):
        mutations = [lambda: (self.root / 'LICENSE').unlink(),
                     lambda: self.package.update(publisher='yourname'),
                     lambda: self.package.update(version='0.2.0'),
                     lambda: self.package.update(license=''),
                     lambda: self.package.update(repository={'url':'https://github.com/other/repo'}),
                     lambda: (self.root / 'go.mod').write_text('module github.com/acme/ifttt/new\n')]
        for mutate in mutations:
            with self.subTest(mutate=mutate):
                (self.root / 'LICENSE').write_text('Fixture license text\n')
                (self.root / 'go.mod').write_text('module github.com/acme/ifttt\n')
                self.package.update(publisher='acme',version='0.1.0',license='MIT',repository={'url':'https://github.com/acme/ifttt.git'})
                mutate(); self.write_package()
                with self.assertRaises(ValueError): release.check_metadata(self.root, 'acme/ifttt', 'v0.1.0')
        for tag in ['dev', 'v1', 'v0.1.0;command', '../v0.1.0', 'v0.1.0\n', 'v0.1.0-']:
            with self.subTest(tag=tag), self.assertRaises(ValueError): release.check_metadata(self.root, 'acme/ifttt', tag)

    def test_seal_and_verify_exact_binary_and_vsix_set(self):
        release.seal_assets(self.assets)
        verified = release.verify_assets(self.assets, require_vsix=True, package=self.package)
        self.assertEqual(len(verified), 8)
        manifest = (self.assets / 'SHA256SUMS').read_text()
        self.assertIn(hashlib.sha256((self.assets / release.BINARIES[0]).read_bytes()).hexdigest(), manifest)
        (self.assets / release.BINARIES[0]).write_bytes(b'corruption')
        with self.assertRaises(ValueError): release.verify_assets(self.assets, require_vsix=True)

    def test_asset_gate_rejects_missing_extra_symlink_and_unsafe_manifest_entries(self):
        release.seal_assets(self.assets)
        original = (self.assets / 'SHA256SUMS').read_text()
        for body in [original + original.splitlines()[0] + '\n', original.replace(release.BINARIES[0], '../outside'), original.replace(release.BINARIES[0], 'unknown-binary')]:
            with self.subTest(body=body), self.assertRaises(ValueError):
                (self.assets / 'SHA256SUMS').write_text(body)
                release.verify_assets(self.assets, require_vsix=True)
        (self.assets / 'SHA256SUMS').write_text(original)
        (self.assets / 'unexpected.txt').write_text('extra')
        with self.assertRaises(ValueError): release.verify_assets(self.assets)
        (self.assets / 'unexpected.txt').unlink()
        name = release.BINARIES[0]
        (self.assets / name).unlink()
        (self.assets / name).symlink_to(self.root / 'LICENSE')
        with self.assertRaises(ValueError): release.seal_assets(self.assets)

    def test_vsix_gate_requires_embedded_license_and_correct_identity(self):
        name = self.assets / 'ifttt-0.1.0.vsix'
        for package, include_license in [({**self.package,'publisher':'other'}, True), (self.package, False)]:
            with self.subTest(package=package, license=include_license):
                with zipfile.ZipFile(name, 'w') as archive:
                    archive.writestr('extension/package.json',json.dumps(package))
                    if include_license: archive.writestr('extension/LICENSE.txt','Fixture license text')
                release.seal_assets(self.assets)
                with self.assertRaises(ValueError): release.verify_assets(self.assets, require_vsix=True, package=self.package)

    def test_draft_creation_and_retry_never_modify_published_or_wrong_commit_release(self):
        release.seal_assets(self.assets)
        for existing in [dict(isDraft=False,targetCommitish=self.commit,tagName='v0.1.0'), dict(isDraft=True,targetCommitish='b'*40,tagName='v0.1.0')]:
            with self.subTest(existing=existing), patch.object(release,'get_release',return_value=existing), patch.object(release,'gh') as gh:
                with self.assertRaises(ValueError): release.create_draft(self.assets,'acme/ifttt','v0.1.0',self.commit)
                gh.assert_not_called()
        with patch.object(release,'get_release',return_value=None), patch.object(release,'gh') as gh:
            release.create_draft(self.assets,'acme/ifttt','v0.1.0',self.commit)
            args=gh.call_args.args[0]
            self.assertIn('--draft',args); self.assertIn('--verify-tag',args)
            self.assertIn(str(self.assets/'SHA256SUMS'),args)

    def test_draft_uses_actual_changelog_notes_instead_of_only_generated_commit_notes(self):
        release.seal_assets(self.assets)
        captured = []
        def invoke(args):
            captured.append(args)
            self.assertEqual(Path(args[args.index('--notes-file')+1]).read_text(), '- Actual user-facing change.\n')
        with patch.object(release, 'get_release', return_value=None), patch.object(release, 'gh', side_effect=invoke):
            release.create_draft(self.assets, 'acme/ifttt', 'v0.1.0', self.commit, '- Actual user-facing change.\n')
        self.assertNotIn('--generate-notes', captured[0])

    def test_publication_cli_rejects_draft_asset_changes_even_with_valid_resealed_checksums(self):
        import shutil
        (self.root / 'CHANGELOG.md').write_text('## 0.1.0\n\n- Released change.\n')
        release.seal_assets(self.assets)
        prepared = self.root / 'prepared'
        shutil.copytree(self.assets, prepared)
        (self.assets / release.BINARIES[0]).write_bytes(b'replaced binary')
        release.seal_assets(self.assets)
        args = ['release.py', 'publish', '--repository','acme/ifttt','--tag','v0.1.0',
                '--commit',self.commit,'--assets',str(self.assets),'--prepared',str(prepared)]
        with patch.object(release, 'ROOT', self.root), patch('sys.argv', args), patch.object(release, 'gh') as gh:
            with self.assertRaises(SystemExit) as caught: release.main()
            self.assertEqual(caught.exception.code, 1)
            gh.assert_not_called()

    def test_publication_uses_verified_assets_pinned_identity_and_is_idempotent(self):
        release.seal_assets(self.assets)
        existing = dict(isDraft=True,targetCommitish=self.commit,tagName='v0.1.0')
        with patch.object(release,'get_release',return_value=existing), patch.object(release,'gh') as gh:
            release.publish(self.assets,'acme/ifttt','v0.1.0',self.commit,self.package)
            self.assertIn('--draft=false',gh.call_args.args[0])
        existing['isDraft']=False
        with patch.object(release,'get_release',return_value=existing), patch.object(release,'gh') as gh:
            release.publish(self.assets,'acme/ifttt','v0.1.0',self.commit,self.package)
            gh.assert_not_called()
        (self.assets / release.BINARIES[0]).write_bytes(b'corrupted')
        with patch.object(release,'get_release',return_value=existing), patch.object(release,'gh') as gh:
            with self.assertRaises(ValueError): release.publish(self.assets,'acme/ifttt','v0.1.0',self.commit,self.package)
            gh.assert_not_called()

    def test_moved_remote_tag_blocks_draft_creation(self):
        release.seal_assets(self.assets)
        with patch.object(release,'tag_commit',return_value='b'*40,create=True), patch.object(release,'get_release',return_value=None), patch.object(release,'gh') as gh:
            with self.assertRaises(ValueError): release.create_draft(self.assets,'acme/ifttt','v0.1.0',self.commit)
            gh.assert_not_called()

    def test_verify_command_validates_vsix_metadata_when_repository_is_given(self):
        name = self.assets / 'ifttt-0.1.0.vsix'
        with zipfile.ZipFile(name, 'w') as archive:
            archive.writestr('extension/package.json', json.dumps({**self.package, 'publisher':'other'}))
            archive.writestr('extension/LICENSE.txt', 'Fixture license text')
        release.seal_assets(self.assets)
        args = ['release.py', 'verify', '--assets', str(self.assets), '--require-vsix',
                '--repository', 'acme/ifttt', '--tag', 'v0.1.0']
        with patch.object(release, 'ROOT', self.root), patch('sys.argv', args):
            with self.assertRaises(SystemExit) as caught: release.main()
            self.assertEqual(caught.exception.code, 1)

    def test_native_smoke_runs_structural_success_and_failure_after_version(self):
        version = f'ifttt v0.1.0 (commit {self.commit})\n'
        responses = [subprocess.CompletedProcess([],0,version,''),
                     subprocess.CompletedProcess([],0,'{"errors":[]}',''),
                     subprocess.CompletedProcess([],1,'{"errors":[{"ruleId":"label_missing"}]}','')]
        with patch.object(release.subprocess,'run',side_effect=responses) as run:
            release.smoke_native(self.assets,'v0.1.0',self.commit)
            self.assertEqual(run.call_count,3)
        responses[-1] = subprocess.CompletedProcess([],0,'{"errors":[]}','')
        with patch.object(release.subprocess,'run',side_effect=responses):
            with self.assertRaises(ValueError): release.smoke_native(self.assets,'v0.1.0',self.commit)


class RemoteReleaseStateTests(unittest.TestCase):
    def test_remote_commit_resolves_lightweight_and_annotated_tags(self):
        commit = 'a' * 40
        for objects in [[dict(type='commit', sha=commit)],
                        [dict(type='tag', sha='b'*40), dict(type='commit', sha=commit)]]:
            with self.subTest(objects=objects), patch.object(release, 'gh', side_effect=[json.dumps({'object': o}) for o in objects]):
                self.assertEqual(release.tag_commit('acme/ifttt', 'v0.1.0'), commit)
        with patch.object(release, 'gh', return_value=json.dumps({'object': {'type':'tree', 'sha':commit}})):
            with self.assertRaises(ValueError): release.tag_commit('acme/ifttt', 'v0.1.0')

    def test_missing_release_is_distinct_from_authentication_failure(self):
        missing = subprocess.CalledProcessError(1, ['gh'], stderr='release not found')
        denied = subprocess.CalledProcessError(1, ['gh'], stderr='HTTP 401: Bad credentials')
        with patch.object(release, 'gh', side_effect=missing):
            self.assertIsNone(release.get_release('acme/ifttt', 'v0.1.0'))
        with patch.object(release, 'gh', side_effect=denied):
            with self.assertRaises(subprocess.CalledProcessError): release.get_release('acme/ifttt', 'v0.1.0')


# LINT.IfChange(release_notes)
class ReleaseNotesTests(unittest.TestCase):
    def test_notes_include_only_selected_changelog_and_action_reference(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / 'CHANGELOG.md').write_text('# Changelog\n\n## Unreleased\n\n- Future work.\n\n## 1.2.3 — 2026-10-05\n\n- Fix linked edits.\n- Ship six binaries.\n\n## 1.2.2 — 2026-09-01\n\n- Older change.\n')
            notes = release.release_notes(root, 'acme/ifttt', 'v1.2.3')
            self.assertIn('- Fix linked edits.', notes)
            self.assertIn('acme/ifttt@v1.2.3', notes)
            self.assertIn('https://github.com/acme/ifttt/blob/v1.2.3/README.md#github-action-and-hooks', notes)
            self.assertIn('SHA256SUMS', notes)
            self.assertNotIn('Future work', notes)
            self.assertNotIn('Older change', notes)
            for text in ['# Changelog\n', '## 1.2.3\n\n', '## 1.2.3\n- One\n## 1.2.3\n- Two\n']:
                (root / 'CHANGELOG.md').write_text(text)
                with self.subTest(text=text), self.assertRaises(ValueError):
                    release.release_notes(root, 'acme/ifttt', 'v1.2.3')

    def test_merged_release_selector_requires_exact_commit_and_default_branch(self):
        commit = 'a' * 40
        pull = dict(merged_at='2026-10-05', merge_commit_sha=commit, base={'ref':'main'}, labels=[{'name':'release'}])
        self.assertTrue(release.is_release_commit([pull], commit, 'main'))
        for values in [dict(merged_at=None), dict(merge_commit_sha='b'*40), dict(base={'ref':'other'}), dict(labels=[])]:
            with self.subTest(values=values):
                self.assertFalse(release.is_release_commit([{**pull, **values}], commit, 'main'))

    def test_tag_creation_never_moves_existing_tags_and_distinguishes_auth_errors(self):
        commit = 'a' * 40
        with patch.object(release, 'tag_commit', return_value=commit), patch.object(release, 'gh') as gh:
            release.ensure_tag('acme/ifttt', 'v1.2.3', commit)
            gh.assert_not_called()
        with patch.object(release, 'tag_commit', return_value='b'*40), patch.object(release, 'gh') as gh:
            with self.assertRaises(ValueError): release.ensure_tag('acme/ifttt', 'v1.2.3', commit)
            gh.assert_not_called()
        missing = subprocess.CalledProcessError(1, ['gh'], stderr='HTTP 404: Not Found')
        with patch.object(release, 'tag_commit', side_effect=[missing, commit]), patch.object(release, 'gh') as gh:
            release.ensure_tag('acme/ifttt', 'v1.2.3', commit)
            args = gh.call_args.args[0]
            self.assertIn('POST', args)
            self.assertIn('ref=refs/tags/v1.2.3', args)
            self.assertIn(f'sha={commit}', args)
        denied = subprocess.CalledProcessError(1, ['gh'], stderr='HTTP 403: Forbidden')
        with patch.object(release, 'tag_commit', side_effect=denied), patch.object(release, 'gh') as gh:
            with self.assertRaises(subprocess.CalledProcessError): release.ensure_tag('acme/ifttt', 'v1.2.3', commit)
            gh.assert_not_called()
    def test_original_artifact_selection_uses_draft_run_identity_and_requires_successful_gate(self):
        commit = 'a' * 40
        existing = dict(isDraft=True, targetCommitish=commit, tagName='v1.2.3', body='Changes\n<!-- ifttt-release-run: 12 -->\n')
        run = {'id':12,'head_sha':'b'*40,'path':'.github/workflows/release.yml'}
        with patch.object(release, 'get_release', return_value=existing), patch.object(release, 'gh', side_effect=[json.dumps(run), json.dumps({'jobs':[{'name':'draft','conclusion':'success'}]})]):
            self.assertEqual(release.prepared_run('acme/ifttt', 'v1.2.3', commit), 12)
        for jobs in [[], [{'name':'draft','conclusion':'failure'}]]:
            with patch.object(release, 'get_release', return_value=existing), patch.object(release, 'gh', side_effect=[json.dumps(run), json.dumps({'jobs':jobs})]):
                with self.assertRaises(ValueError): release.prepared_run('acme/ifttt', 'v1.2.3', commit)
        with patch.object(release, 'get_release', return_value={**existing,'tagName':'v1.2.3-rc.1'}):
            with self.assertRaises(ValueError): release.prepared_run('acme/ifttt', 'v1.2.3', commit)
        with patch.object(release, 'get_release', return_value=existing), patch.object(release, 'gh', return_value=json.dumps({**run,'path':'.github/workflows/test.yml'})):
            with self.assertRaises(ValueError): release.prepared_run('acme/ifttt', 'v1.2.3', commit)

# LINT.ThenChange(//scripts/release.py:release_notes)
