#!/usr/bin/env python3
"""Correctness-gated immutable cross-repository CLI benchmark; no upstream comparison."""
import argparse
import datetime
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import shlex
import subprocess
import tempfile
import time

import benchmark as base


def selected_workloads():
    return [(pairs, paired) for pairs in (1, 10, 50) for paired in (False, True)]


def validate_options(samples, warmups):
    if samples < 15 or warmups < 0:
        raise ValueError('require at least 15 samples and nonnegative warmups')


def parse_options(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--go', required=True, type=Path)
    parser.add_argument('--output', type=Path, default=Path('build/benchmarks/cross-repository.json'))
    parser.add_argument('--samples', type=int, default=21)
    parser.add_argument('--warmups', type=int, default=3)
    parser.add_argument('--check-only', action='store_true')
    options = parser.parse_args(argv)
    try:
        validate_options(options.samples, options.warmups)
    except ValueError as error:
        parser.error(str(error))
    return options


def git_environment():
    environment = {key: value for key, value in os.environ.items() if not key.startswith('GIT_')}
    environment.update(GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=os.devnull,
                       GIT_AUTHOR_DATE='2026-10-05T00:00:00Z', GIT_COMMITTER_DATE='2026-10-05T00:00:00Z')
    return environment


def git(root, *args):
    result = subprocess.run(['git', *args], cwd=root, capture_output=True,
                            env=git_environment(), timeout=60)
    if result.returncode:
        raise RuntimeError(result.stderr.decode(errors='replace'))
    return result.stdout.decode().strip()


def commit(root, message):
    git(root, 'add', '.')
    git(root, '-c', 'user.name=Benchmark', '-c', 'user.email=benchmark@example.invalid',
        '-c', 'core.hooksPath=', 'commit', '-qm', message)
    return git(root, 'rev-parse', 'HEAD')


def fixture(root, pairs, paired):
    root.mkdir()
    root = root.resolve()
    source, target = root / 'source', root / 'target'
    for checkout in (source, target):
        checkout.mkdir()
        git(checkout, '-c', 'init.defaultBranch=main', 'init', '-q', '--object-format=sha1')
    def source_content(index, changed):
        return (f'// SENTRY.IfChange("API")\nvar source = {int(changed)}\n'
                f'// SENTRY.ThenChange("github://acme/target/target_{index}.go#API")\n')
    def target_content(changed):
        return f'// SENTRY.Label("API")\nvar target = {int(changed)}\n// SENTRY.EndLabel\n'
    for index in range(pairs):
        (source / f'source_{index}.go').write_text(source_content(index, False))
        (target / f'target_{index}.go').write_text(target_content(False))
    source_base, target_base = commit(source, 'source baseline'), commit(target, 'target baseline')
    for index in range(pairs):
        (source / f'source_{index}.go').write_text(source_content(index, True))
        if paired:
            (target / f'target_{index}.go').write_text(target_content(True))
    source_head = commit(source, 'source changes')
    target_head = commit(target, 'paired target changes') if paired else target_base
    repositories = [dict(repo='acme/source', path=str(source), vcs='git', base=source_base, head=source_head),
                    dict(repo='acme/target', path=str(target), vcs='git', base=target_base, head=target_head)]
    manifest = dict(version=1, repositories=repositories)
    manifest_yaml = 'version: 1\nrepositories:\n' + ''.join(
        '  - repo: ' + repository['repo'] + '\n' + ''.join(
            f'    {key}: {json.dumps(repository[key])}\n' for key in ('path', 'vcs', 'base', 'head'))
        for repository in repositories)
    manifest_path = root / 'changes.yml'
    manifest_path.write_text(manifest_yaml)
    (root / '.ifttt-lint.yaml').write_text('directives:\n  prefix: SENTRY\n')
    return dict(workload=f'cross-repository-{pairs}-{"paired" if paired else "source-only"}',
                pairs=pairs, paired=paired, expected_findings=0 if paired else pairs,
                cwd=str(root), manifest_path=str(manifest_path), repositories=repositories,
                manifest=manifest, manifest_yaml=manifest_yaml,
                manifest_sha256=hashlib.sha256(manifest_yaml.encode()).hexdigest())


def command_for(binary, spec):
    return [str(binary), '--change-set', spec['manifest_path'], '--format=json', '--threads', '2']


def validate_report(status, stdout, spec):
    expected = spec['expected_findings']
    if status != int(bool(expected)):
        raise ValueError(f'expected exit {int(bool(expected))}, got {status}')
    def invalid_constant(value):
        raise ValueError(f'invalid JSON constant {value}')
    report = json.loads(stdout, parse_constant=invalid_constant)
    if not isinstance(report, dict) or not isinstance(report.get('errors'), list):
        raise ValueError('invalid JSON report shape')
    if len(report['errors']) != expected or report.get('suppressed', []) != []:
        raise ValueError(f'expected {expected} active findings and no suppressed findings')
    if report.get('workspaceRoot') != spec['cwd'] or not Path(spec['cwd']).is_absolute():
        raise ValueError('report workspaceRoot must match the canonical absolute fixture root')
    source = spec['repositories'][0]
    expected_files = {str(Path(source['path']) / f'source_{index}.go'): index for index in range(spec['pairs'])}
    seen = set()
    for finding in report['errors']:
        if not isinstance(finding, dict) or finding.get('file') not in expected_files or finding['file'] in seen:
            raise ValueError('unexpected or duplicate source finding')
        seen.add(finding['file'])
        index = expected_files[finding['file']]
        required = dict(repository=source['repo'], baseRevision=source['base'], headRevision=source['head'],
                        targetPath=f'github://acme/target/target_{index}.go', targetLabel='API',
                        ruleId='then_label_missing', severity='error', line=3)
        if any(finding.get(key) != value for key, value in required.items()):
            raise ValueError('incorrect diagnostic repository, revisions, target or location metadata')
        if not isinstance(finding.get('message'), str) or not finding['message'].strip() or finding.get('suppressed'):
            raise ValueError('invalid active diagnostic message or suppression')
    return report


def report_signature(report):
    canonical = json.dumps(report, sort_keys=True, separators=(',', ':'), ensure_ascii=False, allow_nan=False)
    return hashlib.sha256(canonical.encode()).hexdigest()


def capture(process, spec):
    stdout, stderr = process.stdout.decode(), process.stderr.decode()
    report = validate_report(process.returncode, stdout, spec)
    return dict(exit=process.returncode, report=report, report_sha256=report_signature(report),
                stdout=stdout, stderr=stderr)


def collect_row(spec, run, samples, warmups, check_only):
    process, _ = run()
    try:
        baseline = capture(process, spec)
    except ValueError as error:
        raise RuntimeError(f'{spec["workload"]}: preflight rejected: {error}\n'
                           f'stdout: {process.stdout.decode(errors="replace")}\n'
                           f'stderr: {process.stderr.decode(errors="replace")}') from error
    values = []
    if not check_only:
        for index in range(warmups + samples):
            process, elapsed = run()
            try:
                current = capture(process, spec)
            except ValueError as error:
                raise RuntimeError(f'{spec["workload"]}: sample changed from baseline: {error}') from error
            if (current['exit'], current['report'], current['report_sha256'], current['stderr']) != (
                    baseline['exit'], baseline['report'], baseline['report_sha256'], baseline['stderr']):
                raise RuntimeError(f'{spec["workload"]}: complete report changed from baseline')
            if not math.isfinite(elapsed) or elapsed <= 0:
                raise RuntimeError('invalid elapsed sample duration')
            if index >= warmups:
                values.append(elapsed)
    return {**spec, 'baseline': baseline, 'samples_ms': values,
            'summary': base.summary(values) if values else {}}


def render_report(data, output_name='cross-repository.json'):
    metadata = data['metadata']
    lines = ['# Cross-repository CLI benchmark', '',
             f"Measured binary SHA-256: `{metadata['binary']['sha256']}`.", '']
    if metadata.get('timestamp_utc'):
        lines += [f"Recorded {metadata['timestamp_utc']} on {metadata.get('cpu', metadata.get('machine', 'unspecified host'))} ({metadata.get('platform', 'unspecified platform')}).", '']
    if metadata.get('check_only'):
        lines += ['Untimed preflight; no performance measurements. Commands use native Git snapshots and `--threads 2`.', '']
    else:
        lines += [f"{metadata['samples']} samples after {metadata['warmups']} warmups; each CLI invocation uses `--threads 2`. Warm OS filesystem caches. Process startup, JSON output and native Git snapshot operations included; build and fixture generation excluded.", '']
    lines += ['Each pair has distinct source and target files in two independent Git repositories. Source-only committed changes must fail with one `then_label_missing` finding per pair (exit 1); paired committed changes must be clean (exit 0). Cross-repository change-set mode has no supported upstream counterpart.', '',
              'Preflight checks the expected status, count, source file, repository, base/head revisions, target URI/label, rule and location. Every warmup and sample must preserve the complete report and its canonical SHA-256 signature, including suppressed and unknown fields.', '',
              f'Raw data: [{output_name}]({output_name}) contains all manifests, repository revisions, exact commands, full preflight reports, build metadata, unknown fields and raw samples.', '',
              '| Pairs / selection | Findings / exit | Min ms | Median ms | p95 ms | Max ms | Stdev ms |',
              '| --- | ---: | ---: | ---: | ---: | ---: | ---: |']
    for row in data['results']:
        summary = row['summary']
        values = [f'{summary[key]:.3f}' if summary else '—' for key in ('min_ms', 'median_ms', 'p95_ms', 'max_ms', 'stdev_ms')]
        lines.append(f'| {row["pairs"]} / {"paired" if row["paired"] else "source-only"} | {row["expected_findings"]} / {row["baseline"]["exit"]} | ' + ' | '.join(values) + ' |')
    lines += ['', 'Reproduce with a fresh standard build and generated fixtures:', '', '```sh',
              'make build', 'python3 scripts/benchmark_changeset.py --go build/iflint --output build/benchmarks/cross-repository.json',
              '# Add --check-only for an untimed correctness preflight.', '```', '']
    return '\n'.join(lines)


def write_report(data, output):
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(data, indent=2, allow_nan=False) + '\n')
    output.with_suffix('.md').write_text(render_report(data, output.name))


def binary_metadata(binary):
    result = subprocess.run(['go', 'version', '-m', binary], capture_output=True, check=True)
    build_info = result.stdout.decode().strip()
    cgo = base.compiled_cgo(build_info)
    if cgo != '0':
        raise ValueError('benchmark requires a binary compiled with CGO_ENABLED=0 (make build)')
    settings = {}
    for line in build_info.splitlines():
        fields = line.split()
        if len(fields) == 2 and fields[0] == 'build' and '=' in fields[1]:
            key, value = fields[1].split('=', 1)
            settings[key] = value
    return dict(binary=dict(path=binary, sha256=hashlib.sha256(Path(binary).read_bytes()).hexdigest()),
                go_build_info=build_info, go_cgo_enabled=cgo,
                go_binary_version=build_info.splitlines()[0].split()[-1],
                compiler_flags=settings, build_command=['env', 'CGO_ENABLED=0', 'go', 'build', '-trimpath', '-o', '../build/iflint', './cmd'])


def main(argv=None):
    options = parse_options(argv)
    binary = str(options.go.resolve())
    metadata = {**binary_metadata(binary), 'timestamp_utc': datetime.datetime.now(datetime.timezone.utc).isoformat(),
                'platform': platform.platform(), 'machine': platform.machine(), 'python': platform.python_version(),
                'cpu': base.cpu_description(), 'go_version': subprocess.check_output(['go','version'], text=True).strip(),
                'git_version': subprocess.check_output(['git','--version'], text=True).strip(),
                'samples': options.samples, 'warmups': options.warmups, 'check_only': options.check_only,
                'cache_state': 'Warm OS filesystem caches; no cache flush; CLI process restarted each sample',
                'timing_scope': 'Subprocess startup, native Git snapshot work and JSON output included; build/fixture generation excluded'}
    results = []
    with tempfile.TemporaryDirectory(prefix='iflint-changeset-benchmark-') as temporary:
        for pairs, paired in selected_workloads():
            spec = fixture(Path(temporary) / f'{pairs}-{"paired" if paired else "source-only"}', pairs, paired)
            command = command_for(binary, spec)
            def run():
                start = time.perf_counter_ns()
                process = subprocess.run(command, cwd=spec['cwd'], input=b'', capture_output=True, timeout=60,
                                         env=git_environment())
                return process, (time.perf_counter_ns() - start) / 1e6
            row = collect_row(spec, run, options.samples, options.warmups, options.check_only)
            row['command'] = command
            results.append(row)
            print(f'{spec["workload"]}: expected findings={spec["expected_findings"]}; correctness passed', flush=True)
        for row in results:
            for repository in row['repositories']:
                if git(repository['path'], 'rev-parse', 'HEAD') != repository['head'] or git(repository['path'], 'status', '--porcelain'):
                    raise RuntimeError('benchmark fixture changed; discard results')
    if hashlib.sha256(Path(binary).read_bytes()).hexdigest() != metadata['binary']['sha256']:
        raise RuntimeError('benchmark binary changed; discard results')
    write_report(dict(metadata=metadata, results=results), options.output)


if __name__ == '__main__':
    main()
