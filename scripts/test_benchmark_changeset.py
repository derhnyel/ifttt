import copy
from contextlib import redirect_stderr
import io
import json
from pathlib import Path
import subprocess
import tempfile
from unittest.mock import patch
import unittest

import benchmark_changeset as bench


class ChangeSetBenchmarkTests(unittest.TestCase):
    def spec(self, pairs=1, paired=False):
        return dict(workload=f'cross-repository-{pairs}-{"paired" if paired else "source-only"}',
                    pairs=pairs, paired=paired, expected_findings=0 if paired else pairs,
                    cwd='/fixture', manifest_path='/fixture/changes.yml',
                    repositories=[dict(repo='acme/source', path='/fixture/source', vcs='git', base='a'*40, head='b'*40),
                                  dict(repo='acme/target', path='/fixture/target', vcs='git', base='c'*40, head='d'*40 if paired else 'c'*40)])

    def report(self, pairs=1):
        return dict(workspaceRoot='/fixture', errors=[dict(file=f'/fixture/source/source_{i}.go', line=3,
                    severity='error', ruleId='then_label_missing', message='expected changes',
                    repository='acme/source', baseRevision='a'*40, headRevision='b'*40,
                    targetPath=f'github://acme/target/target_{i}.go', targetLabel='API') for i in range(pairs)], suppressed=[])

    def test_preflight_rejects_invalid_json_shape_operational_status_and_incorrect_count(self):
        validate=bench.validate_report
        for status, report in [(1,'bad'), (1,'{}'), (2,json.dumps(self.report())), (0,json.dumps(self.report())), (1,json.dumps(self.report(2)))]:
            with self.subTest(status=status, report=report), self.assertRaises(ValueError):
                validate(status, report, self.spec())
        self.assertEqual(validate(1,json.dumps(self.report()),self.spec()),self.report())
        self.assertEqual(validate(0,'{"workspaceRoot":"/fixture","errors":[]}',self.spec(paired=True))['errors'],[])

    def test_gate_checks_independent_source_repository_revisions_files_targets_and_labels(self):
        validate=bench.validate_report
        for key,value in [('repository','acme/target'), ('baseRevision','wrong'), ('headRevision','wrong'),
                          ('file','source_0.go'), ('targetPath','/fixture/local.go'), ('targetLabel','other'),
                          ('ruleId','then_missing'), ('line',1), ('severity','warning')]:
            with self.subTest(key=key), self.assertRaises(ValueError):
                report=self.report();report['errors'][0][key]=value
                validate(1,json.dumps(report),self.spec())
        report=self.report(2);report['errors'][1]=copy.deepcopy(report['errors'][0])
        with self.assertRaises(ValueError): validate(1,json.dumps(report),self.spec(2))
        report=self.report();report['workspaceRoot']='relative'
        with self.assertRaises(ValueError): validate(1,json.dumps(report),self.spec())
        report=self.report();report['suppressed']=[copy.deepcopy(report['errors'][0])]
        with self.assertRaises(ValueError): validate(1,json.dumps(report),self.spec())

    def test_complete_report_signature_preserves_unknown_fields_and_suppressed_payloads(self):
        signature=bench.report_signature;report=self.report();report['future']={'detail':'baseline'}
        self.assertEqual(signature(report),signature(json.loads(json.dumps(report))))
        for mutation in [lambda r:r['future'].update(detail='changed'), lambda r:r.update(suppressed=[{'message':'hidden'}]), lambda r:r['errors'][0].update(resolution='changed')]:
            changed=copy.deepcopy(report);mutation(changed);self.assertNotEqual(signature(report),signature(changed))

    def test_warmup_and_measured_samples_validate_complete_baselines_before_recording(self):
        collect=bench.collect_row;spec=self.spec()
        for drift_at in [1,3]:
            calls=[]
            def run():
                report=self.report();report['errors'][0]['message']='changed' if len(calls)==drift_at else 'baseline'
                calls.append(1);return subprocess.CompletedProcess(['/iflint'],1,json.dumps(report).encode(),b''),1.0
            with self.subTest(drift_at=drift_at), self.assertRaisesRegex(RuntimeError,'baseline'):
                collect(spec,run,15,2,False)
        calls=[]
        def clean_run():
            calls.append(1);return subprocess.CompletedProcess(['/iflint'],1,json.dumps(self.report()).encode(),b''),1.0
        result=collect(spec,clean_run,15,2,False)
        self.assertEqual(len(calls),18);self.assertEqual(result['samples_ms'],[1.0]*15)
        self.assertEqual(result['baseline']['report'],self.report());self.assertEqual(result['summary']['median_ms'],1.0)
        calls.clear();untimed=collect(spec,clean_run,21,3,True)
        self.assertEqual(len(calls),1);self.assertEqual(untimed['samples_ms'],[]);self.assertEqual(untimed['summary'],{})

    def test_rejected_preflight_exposes_raw_operational_error_without_collecting_samples(self):
        collect=bench.collect_row;calls=[]
        def run():
            calls.append(1);return subprocess.CompletedProcess(['/iflint'],2,b'invalid output',b'unknown change-set flag'),1.0
        with self.assertRaisesRegex(RuntimeError,'unknown change-set flag'):
            collect(self.spec(),run,21,3,False)
        self.assertEqual(len(calls),1)

    def test_binary_provenance_records_sha_compiler_settings_and_rejects_cgo(self):
        metadata=bench.binary_metadata
        with tempfile.TemporaryDirectory() as temp:
            binary=Path(temp)/'iflint';binary.write_bytes(b'binary contents')
            build_info=b'/iflint: go1.26.8\n\tbuild\t-compiler=gc\n\tbuild\t-trimpath=true\n\tbuild\tCGO_ENABLED=0\n'
            with patch.object(bench.subprocess,'run',return_value=subprocess.CompletedProcess([],0,build_info,b'')):
                result=metadata(str(binary))
            self.assertEqual(result['go_cgo_enabled'],'0');self.assertEqual(result['go_binary_version'],'go1.26.8')
            self.assertEqual(result['compiler_flags']['-trimpath'],'true');self.assertEqual(len(result['binary']['sha256']),64)
            with patch.object(bench.subprocess,'run',return_value=subprocess.CompletedProcess([],0,build_info.replace(b'CGO_ENABLED=0',b'CGO_ENABLED=1'),b'')):
                with self.assertRaisesRegex(ValueError,'CGO_ENABLED=0'):metadata(str(binary))

    def test_options_reject_invalid_samples_and_warmups(self):
        validate=bench.validate_options;validate(21,3)
        for values in [(14,3),(21,-1)]:
            with self.assertRaises(ValueError): validate(*values)
        parser=bench.parse_options;options=parser(['--go','build/iflint','--check-only'])
        self.assertTrue(options.check_only);self.assertEqual(options.samples,21);self.assertEqual(options.warmups,3)
        self.assertEqual(options.output,Path('build/benchmarks/cross-repository.json'))
        for args in [['--samples','14'],['--warmups','-1']]:
            with redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                parser(['--go','build/iflint',*args])

    def test_fixture_uses_two_independent_committed_snapshots_and_strict_manifest(self):
        fixture=bench.fixture
        with tempfile.TemporaryDirectory() as temp:
            for paired in (False,True):
                spec=fixture(Path(temp)/str(paired),2,paired)
                self.assertEqual(spec['expected_findings'],0 if paired else 2)
                self.assertEqual(spec['manifest']['version'],1)
                self.assertEqual(spec['manifest']['repositories'],spec['repositories'])
                self.assertEqual(Path(spec['manifest_path']).parent,Path(spec['cwd']))
                self.assertIn('version: 1',Path(spec['manifest_path']).read_text())
                for repository in spec['repositories']:
                    root=Path(repository['path']);self.assertTrue((root/'.git').is_dir())
                    status=subprocess.check_output(['git','status','--porcelain'],cwd=root)
                    self.assertEqual(status,b'')
                    for key in ('base','head'):
                        actual=subprocess.check_output(['git','rev-parse',repository[key]],cwd=root,text=True).strip()
                        self.assertEqual(actual,repository[key])
                source,target=spec['repositories']
                self.assertNotEqual(source['base'],source['head'])
                self.assertEqual(target['base']==target['head'],not paired)
                self.assertIn('SENTRY.ThenChange("github://acme/target/target_1.go#API")', (Path(source['path'])/'source_1.go').read_text())
                self.assertIn('SENTRY.EndLabel',(Path(target['path'])/'target_1.go').read_text())

    def test_markdown_json_roundtrip_preserves_preflight_and_shows_no_upstream_comparison(self):
        render=bench.render_report;write=bench.write_report
        data=dict(metadata=dict(samples=21,warmups=3,check_only=True,binary={'path':'/iflint','sha256':'binarysha'},go_build_info='go build metadata',go_cgo_enabled='0',build_command=['go','build','-trimpath'],compiler_flags=['-trimpath']),results=[{**self.spec(), 'command':['/iflint','--change-set','/fixture/changes.yml','--format=json','--threads','2'], 'baseline':{'exit':1,'report':self.report(),'stdout':json.dumps(self.report()),'stderr':''},'samples_ms':[],'summary':{}}])
        self.assertEqual(render(data),render(json.loads(json.dumps(data))))
        text=render(data);self.assertIn('Untimed preflight',text);self.assertIn('binarysha',text);self.assertIn('then_label_missing',text);self.assertIn('native Git',text);self.assertIn('--threads',text);self.assertNotIn('speedup',text.lower())
        with tempfile.TemporaryDirectory() as temp:
            output=Path(temp)/'cross-repository.json';write(data,output)
            self.assertEqual(json.loads(output.read_text()),data)
            self.assertIn('[cross-repository.json](cross-repository.json)',output.with_suffix('.md').read_text())

    def test_report_preserves_timings_and_complete_raw_provenance(self):
        data=dict(metadata=dict(samples=21,warmups=3,binary={'sha256':'fixture-binary-sha256'}),results=[])
        for pairs in (1,10,50):
            for paired in (False,True):
                report=self.report(0 if paired else pairs)
                data['results'].append({**self.spec(pairs,paired),
                    'baseline':{'exit':0 if paired else 1,'report':report,'stdout':json.dumps(report)},
                    'samples_ms':[1.0,2.0,3.0],
                    'summary':{'min_ms':1.0,'median_ms':2.0,'p95_ms':3.0,'max_ms':3.0,'stdev_ms':1.0}})
        data['metadata']['future_build_metadata']={'detail':'unique-build-provenance'}
        data['results'][0]['baseline']['report']['future_diagnostics']={'detail':'unique-preflight-payload'}
        data['results'][0]['future_workload_metadata']={'detail':'unique-workload-provenance'}
        original=copy.deepcopy(data)
        text=bench.render_report(data)
        self.assertIn(data['metadata']['binary']['sha256'],text)
        for row in data['results']:
            values=' | '.join(f'{row["summary"][key]:.3f}' for key in ('min_ms','median_ms','p95_ms','max_ms','stdev_ms'))
            self.assertIn(values,text)
        for detail in ('unique-build-provenance','unique-preflight-payload','unique-workload-provenance',data['results'][0]['baseline']['stdout']):
            self.assertNotIn(detail,text)
        with tempfile.TemporaryDirectory() as temp:
            output=Path(temp)/'cross-repository.json';bench.write_report(data,output)
            self.assertEqual(json.loads(output.read_text()),original)
            self.assertEqual(output.with_suffix('.md').read_text(),text)
        self.assertEqual(data,original)


if __name__=='__main__': unittest.main()
