import json
from pathlib import Path
import subprocess
import tempfile
import unittest

import benchmark_repositories as bench


class RepositoryBenchmarkTests(unittest.TestCase):
    def test_ambiguous_labels_keep_their_category_and_target_identity(self):
        normalize = bench.normalize_report
        def report(target):
            return normalize('go', 1, json.dumps({'errors': [{
                'file': 'source.go', 'line': 3, 'ruleId': 'label_ambiguous',
                'message': 'label is ambiguous', 'targetPath': target, 'targetLabel': 'API'}]}))
        left, right = report('first.go'), report('second.go')
        self.assertEqual(left['findings'][0]['category'], 'label_ambiguous')
        comparison = bench.compare_reports(left, right)
        self.assertFalse(comparison['equivalent'])
        self.assertEqual(comparison['go_only'][0][-1], ('first.go', 'API'))

    def test_git_discovery_preserves_nul_separated_literal_paths_and_blob_sizes(self):
        inventory = bench.repository_inventory
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            def git(*args):
                return subprocess.run(['git', *args], cwd=root, check=True, capture_output=True).stdout
            git('init', '-q')
            files = ['space file.go', 'newline\nfile.go', '-dash.go', 'quote\"ü.go']
            for index, name in enumerate(files):
                (root / name).write_bytes(b'// LINT.IfChange\n' if index < 3 else b'ordinary\n')
            git('add', '--', *files)
            git('-c', 'user.name=Test', '-c', 'user.email=test@example.invalid', 'commit', '-qm', 'fixture')
            result = inventory(root)
            self.assertEqual(set(result['tracked_files']), set(files))
            self.assertEqual(set(result['directive_files']), set(files[:3]))
            self.assertEqual(result['tracked_bytes'], 3 * len(b'// LINT.IfChange\n') + len(b'ordinary\n'))
            self.assertEqual(result['directive_bytes'], 3 * len(b'// LINT.IfChange\n'))
            (root / 'untracked.go').write_text('LINT.IfChange\n')
            with self.assertRaisesRegex(RuntimeError, 'clean'):
                inventory(root)

    def test_commands_keep_literal_paths_separate_and_use_matching_smoke_options(self):
        commands = bench.commands_for
        files = ['-dash.go', 'newline\nfile.go', '$(touch injected).go']
        result = commands({'go': '/go', 'upstream': '/up'}, 'chromium', 'filtered', 8, files)
        for args in result.values():
            self.assertIn('--strict=false', args)
            self.assertEqual(args[-len(files)-1:], ['--', *files])
            self.assertEqual(args.count('--ignore'), 3)
        all_files = commands({'go': '/go', 'upstream': '/up'}, 'tensorflow', 'tracked-all', 2, [])
        self.assertTrue(all(args[-2:] == ['--', '**/*'] for args in all_files.values()))

    def test_output_normalization_retains_details_and_maps_comparable_rules(self):
        normalize = bench.normalize_report
        go = normalize('go', 1, json.dumps({'errors': [{'file': './a.go', 'line': 3, 'severity': 'error', 'ruleId': 'then_missing', 'message': 'target cannot be read'}]}))
        upstream = normalize('upstream', 1, json.dumps({'diagnostics': [{'file': 'a.go', 'line': 3, 'severity': 'error', 'message': 'target file not found: b.go'}]}))
        self.assertEqual(go['findings'][0]['rule'], 'then_missing')
        self.assertEqual(go['findings'][0]['category'], 'target_missing')
        self.assertTrue(bench.compare_reports(go, upstream)['equivalent'])
        self.assertEqual(normalize('upstream', 0, '')['findings'], [])
        with self.assertRaises(ValueError):
            normalize('upstream', 1, '')
        with self.assertRaises(ValueError):
            normalize('go', 0, '{}')

    def test_semantic_mismatch_excludes_speed_claims_and_tracks_unmatched_findings(self):
        normalize = bench.normalize_report
        compare = bench.compare_reports
        go = normalize('go', 1, '{"errors":[{"file":"a.go","line":3,"ruleId":"label_missing","message":"missing label"}]}')
        upstream = normalize('upstream', 0, '')
        result = compare(go, upstream)
        self.assertFalse(result['equivalent'])
        self.assertFalse(result['eligible_for_speed_claim'])
        self.assertEqual(len(result['go_only']), 1)
        row = dict(repository='fixture', mode='filtered', threads=2, comparison=result,
                   baseline={'go': go, 'upstream': upstream}, summary={'go': {'median_ms': 1}, 'upstream': {'median_ms': 2}})
        text = bench.render_report({'metadata': {'samples': 21, 'warmups': 3}, 'repositories': [], 'results': [row]})
        self.assertIn('excluded', text.lower())
        self.assertNotIn('2.00×', text)
        self.assertIn('a.go:3', text)
        self.assertIn('label_missing', text)

    def test_each_sample_must_preserve_full_baseline_and_clean_native_results(self):
        validate = bench.validate_result
        baseline = {'exit': 1, 'findings': [{'file': 'a.go', 'line': 1, 'rule': 'label_missing', 'category': 'label_missing', 'severity': 'error', 'message': 'old'}]}
        validate(baseline, baseline, False)
        changed = json.loads(json.dumps(baseline))
        changed['findings'][0]['message'] = 'changed'
        with self.assertRaisesRegex(RuntimeError, 'baseline'):
            validate(changed, baseline, False)
        with self.assertRaisesRegex(RuntimeError, 'clean'):
            validate(baseline, baseline, True)

    def test_samples_warmups_and_timeout_are_validated(self):
        validate = bench.validate_options
        validate(21, 3, 180)
        for args in [(14, 3, 180), (21, -1, 180), (21, 3, 0), (21, 3, float('inf')), (21, 3, float('nan'))]:
            with self.assertRaises(ValueError):
                validate(*args)

    def test_target_diagnostics_compare_thenchange_location_and_keep_reported_location(self):
        normalize = bench.normalize_report
        go = normalize('go', 1, json.dumps({'errors': [{'file': 'a.go', 'line': 9, 'ruleId': 'then_missing', 'message': 'target cannot be read', 'targetPath': 'b.go', 'targetLabel': 'shared'}]}))
        upstream = normalize('upstream', 1, json.dumps({'diagnostics': [{'file': 'a.go', 'line': 3, 'message': 'target file not found: b.go (label shared)', 'target': {'raw': 'b.go:shared', 'label': 'shared', 'then_change_line': 9}}]}))
        self.assertEqual(upstream['findings'][0]['line'], 3)
        self.assertEqual(upstream['findings'][0].get('comparison_line'), 9)
        self.assertEqual(upstream['findings'][0]['category'], 'target_missing')
        self.assertTrue(bench.compare_reports(go, upstream)['equivalent'])

    def test_known_orphans_and_path_policy_differences_remain_distinct_categories(self):
        categorize = bench.category
        self.assertEqual(categorize('orphan_if', 'missing ThenChange after IfChange'), 'unmatched_ifchange')
        self.assertEqual(categorize('orphan_then', 'without preceding IfChange'), 'unmatched_thenchange')
        self.assertEqual(categorize('unknown_directive', 'malformed target'), 'parse_error')
        self.assertEqual(categorize(None, 'path traversal (..) is not allowed in target: ../x'), 'path_policy')
        self.assertEqual(categorize(None, 'malformed directive: LINT.ThenChange('), 'parse_error')

    def test_partial_preflight_reports_are_persisted_and_visibly_incomplete(self):
        write = bench.write_report
        data = {'metadata': {'samples': 21, 'warmups': 3, 'check_only': True}, 'repositories': [], 'results': []}
        with tempfile.TemporaryDirectory() as temp:
            output = Path(temp) / 'report.json'
            write(data, output, False)
            self.assertFalse(json.loads(output.read_text())['metadata']['complete'])
            self.assertIn('incomplete', output.with_suffix('.md').read_text().lower())
            self.assertIn('untimed preflight', output.with_suffix('.md').read_text().lower())

    def test_preflight_honors_requested_workload(self):
        workloads = bench.selected_workloads
        self.assertEqual(workloads(['filtered'], [2]), [('filtered', 2)])

    def test_equal_locations_with_different_targets_are_not_semantically_equivalent(self):
        normalize = bench.normalize_report
        compare = bench.compare_reports
        go = normalize('go', 1, json.dumps({'errors': [{'file': 'dir/a.go', 'line': 9, 'ruleId': 'label_missing', 'message': 'missing label', 'targetPath': 'target.go', 'targetLabel': 'shared'}]}))
        upstream = normalize('upstream', 1, json.dumps({'diagnostics': [{'file': 'dir/a.go', 'line': 3, 'message': 'label shared not found in target.go', 'target': {'raw': '//target.go:shared', 'label': 'source_label', 'then_change_line': 9}}]}))
        self.assertTrue(compare(go, upstream)['equivalent'])
        upstream['findings'][0]['target']['raw'] = '//different.go:shared'
        self.assertFalse(compare(go, upstream)['equivalent'])

    def test_reproduction_pins_repository_commits_and_builds_verified_binaries(self):
        render = bench.render_report
        commit = 'a' * 40
        repo = dict(name='fixture', commit=commit, url='https://github.com/example/fixture.git',
                    tracked_file_count=1, tracked_bytes=20, directive_file_count=1, directive_bytes=20)
        text = render({'metadata': {'samples': 21, 'warmups': 3}, 'repositories': [repo], 'results': []})
        self.assertIn('git clone --no-checkout --depth 1', text)
        self.assertIn('fetch --depth 1 origin ' + commit, text)
        self.assertIn('checkout --detach FETCH_HEAD', text)
        self.assertIn('make build', text)
        self.assertIn('python3 scripts/benchmark_upstream.py', text)

    def test_upstream_slash_targets_are_root_relative_while_bare_names_are_source_relative(self):
        normalize = bench.normalize_report
        compare = bench.compare_reports
        for raw, resolved in [('b.go', 'source/b.go'), ('dir/b.go', 'dir/b.go'), ('/b.go', 'b.go'), ('//dir/b.go', 'dir/b.go')]:
            with self.subTest(raw=raw):
                go = normalize('go', 1, json.dumps({'errors': [{'file': 'source/a.go', 'line': 9, 'ruleId': 'label_missing', 'message': 'missing label', 'targetPath': resolved, 'targetLabel': 'shared'}]}))
                upstream = normalize('upstream', 1, json.dumps({'diagnostics': [{'file': 'source/a.go', 'line': 3, 'message': 'label shared not found', 'target': {'raw': raw + ':shared', 'then_change_line': 9}}]}))
                self.assertTrue(compare(go, upstream)['equivalent'])

    def test_upstream_colon_whitespace_is_a_literal_filename_not_a_label_separator(self):
        normalize = bench.normalize_report
        compare = bench.compare_reports
        raw = '//dir/target.go: Shared'
        go = normalize('go', 1, json.dumps({'errors': [{'file': 'source/a.go', 'line': 9, 'ruleId': 'then_missing', 'message': 'target cannot be read', 'targetPath': 'dir/target.go: Shared'}]}))
        upstream = normalize('upstream', 1, json.dumps({'diagnostics': [{'file': 'source/a.go', 'line': 3, 'message': 'target file not found', 'target': {'raw': raw, 'then_change_line': 9}}]}))
        self.assertTrue(compare(go, upstream)['equivalent'])

    def test_operational_failure_is_preserved_and_excluded_without_removing_inputs(self):
        capture = bench.capture_report
        failed = capture('upstream', subprocess.CompletedProcess(['/up'], 2, b'', b'error: read latin1.html\n'))
        self.assertEqual(failed['exit'], 2)
        self.assertEqual(failed['operational_error']['stderr'], 'error: read latin1.html\n')
        go = {'exit': 1, 'findings': []}
        compared = bench.compare_reports(go, failed)
        self.assertFalse(compared['eligible_for_speed_claim'])
        row = dict(repository='fixture', mode='tracked-all', threads=2, baseline={'go': go, 'upstream': failed}, comparison=compared, summary={})
        text = bench.render_report({'metadata': {'samples': 21, 'warmups': 3}, 'repositories': [], 'results': [row]})
        self.assertIn('operational failure', text.lower())
        self.assertIn('latin1.html', text)
        self.assertNotIn('0 / 0', text)

    def test_resume_rejects_changed_binary_or_config_and_keeps_previous_samples(self):
        resume = bench.load_checkpoint
        metadata = dict(samples=21, warmups=3, check_only=False, random_seed=20261005,
                        binaries={'go': {'sha256': 'g'}, 'upstream': {'sha256': 'u'}},
                        go_configuration_sha256='config', go_cgo_enabled='0', complete=False, requested_repositories=['fixture'])
        row = dict(repository='fixture', mode='filtered', threads=2, samples_ms={'go': [1]*21, 'upstream': [2]*21}, order=[['go', 'upstream']]*21,
                   summary={'go': bench.base.summary([1]*21), 'upstream': bench.base.summary([2]*21)})
        with tempfile.TemporaryDirectory() as temp:
            output = Path(temp) / 'report.json'
            output.write_text(json.dumps({'metadata': metadata, 'repositories': [], 'results': [row]}))
            self.assertEqual(resume(output, metadata)['results'], [row])
            for key, value in [('binaries', {'go': {'sha256': 'changed'}}), ('go_configuration_sha256', 'changed'), ('samples', 15),
                               ('timeout_seconds', 181), ('requested_repositories', ['tensorflow']),
                               ('requested_workloads', [['filtered', 1]]), ('cpu', 'different CPU'), ('platform', 'different OS'),
                               ('go_build_info', 'changed flags'), ('upstream_version', 'changed version'),
                               ('upstream_commit', 'changed source'), ('verified_upstream_archive_sha256', 'changed archive'),
                               ('git_version', 'changed Git'), ('python', 'changed Python')]:
                with self.subTest(key=key), self.assertRaisesRegex(RuntimeError, 'checkpoint'):
                    resume(output, {**metadata, key: value})
            corrupted = json.loads(output.read_text())
            corrupted['results'][0]['samples_ms']['go'].pop()
            output.write_text(json.dumps(corrupted))
            with self.assertRaisesRegex(RuntimeError, 'sample count'):
                resume(output, metadata)

    def test_resume_rejects_repository_commit_and_file_inventory_changes(self):
        validate = bench.validate_inventory
        original = dict(commit='a'*40, directive_files_sha256='files', tracked_bytes=10)
        validate(original, original.copy())
        for key, changed in [('commit', 'b'*40), ('directive_files_sha256', 'different'), ('tracked_bytes', 11)]:
            with self.subTest(key=key), self.assertRaisesRegex(RuntimeError, 'checkpoint'):
                validate(original, {**original, key: changed})

    def test_custom_report_links_its_data_and_reproduces_selected_workloads(self):
        with tempfile.TemporaryDirectory() as temp:
            output = Path(temp) / 'tensorflow-gap-fixes.json'
            data = {'metadata': {'samples': 21, 'warmups': 3,
                    'requested_repositories': ['tensorflow'],
                    'requested_workloads': [['tracked-all', 2]]}, 'repositories': [], 'results': []}
            bench.write_report(data, output, True)
            text = output.with_suffix('.md').read_text()
            self.assertIn('[tensorflow-gap-fixes.json](tensorflow-gap-fixes.json)', text)
            self.assertIn('--repository tensorflow', text)
            self.assertIn('--mode tracked-all', text)
            self.assertIn('--threads 2', text)
            self.assertIn('--output ' + str(output), text)

    def test_rendering_is_stable_after_saved_json_round_trip(self):
        row = dict(repository='fixture', mode='filtered', threads=2,
                   baseline={tool: {'exit': 1, 'findings': []} for tool in ('go', 'upstream')}, summary={},
                   comparison=dict(equivalent=False, eligible_for_speed_claim=False, reasons=['different target'],
                                   go_only=[('a.go', 1, 'error', 'label_missing', ('b.go', 'shared'))], upstream_only=[]))
        data = {'metadata': {'samples': 21, 'warmups': 3}, 'repositories': [], 'results': [row]}
        render = bench.render_report
        self.assertEqual(render(data), render(json.loads(json.dumps(data))))


if __name__ == '__main__':
    unittest.main()
