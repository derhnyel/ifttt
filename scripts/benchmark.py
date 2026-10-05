#!/usr/bin/env python3
"""Correctness-gated, deterministic CLI comparison; standard library only."""
import argparse
import datetime
import hashlib
import json
import math
from pathlib import Path
import platform
import random
import statistics
import subprocess
import tempfile
import time

UPSTREAM_SHA = '41ad27c50e223731b5f33d7cd45a215259211692'

def summary(samples):
    ordered = sorted(samples)
    return dict(median_ms=statistics.median(samples), min_ms=ordered[0],
                p95_ms=ordered[math.ceil(.95 * len(samples)) - 1], max_ms=ordered[-1],
                stdev_ms=statistics.stdev(samples) if len(samples) > 1 else 0)

def count_findings(tool, output):
    if tool == 'upstream' and not output.strip():
        return 0
    data = json.loads(output)
    return len(data['errors' if tool == 'go' else 'diagnostics'])

def command(args, cwd, data=None):
    return subprocess.run(args, cwd=cwd, input=data, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, check=False)

def git(root, *args):
    result = command(['git', *args], root)
    if result.returncode:
        raise RuntimeError(result.stderr.decode())
    return result.stdout

def fixture(root, pairs, failing=False, unrelated=0):
    root.mkdir()
    git(root, 'init', '-q')
    (root / '.ifttt-lint.yaml').write_text('directives:\n  prefix: LINT\n')
    def content(i, changed):
        return f'// LINT.IfChange(pair_{i})\nvalue = {int(changed)}\n// LINT.ThenChange(//target_{i}.go)\n'
    for i in range(pairs):
        (root / f'source_{i}.go').write_text(content(i, False))
        (root / f'target_{i}.go').write_text('value = 0\n')
    for i in range(unrelated):
        (root / f'unrelated_{i}.txt').write_text('unchanged\n' * 16)
    git(root, 'add', '.')
    git(root, '-c', 'user.name=Benchmark', '-c', 'user.email=benchmark@example.invalid',
        'commit', '-qm', 'benchmark baseline')
    for i in range(pairs):
        (root / f'source_{i}.go').write_text(content(i, True))
        if not failing:
            (root / f'target_{i}.go').write_text('value = 1\n')
    return git(root, 'diff', '--no-renames', '--ignore-submodules=all', 'HEAD')

def cpu_description():
    if platform.system() == 'Darwin':
        return command(['sysctl', '-n', 'machdep.cpu.brand_string'], '.').stdout.decode().strip()
    return platform.processor() or platform.machine()

def archive_provenance(executable):
    sidecar = Path(executable).with_suffix('.provenance.json')
    if sidecar.exists():
        provenance = json.loads(sidecar.read_text())
        actual = hashlib.sha256(Path(executable).read_bytes()).hexdigest()
        if actual != provenance['executable_sha256']:
            raise RuntimeError('upstream executable no longer matches checksum-verified archive')
        return provenance['archive_sha256']
    return None

def compiled_cgo(build_info):
    for line in build_info.splitlines():
        fields = line.split()
        if len(fields) == 2 and fields[0] == 'build' and fields[1].startswith('CGO_ENABLED='):
            setting = fields[1].split('=', 1)[1]
            if setting in ('0', '1'):
                return setting
    raise ValueError('Go executable build info must identify CGO_ENABLED')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--go', required=True, type=Path)
    parser.add_argument('--upstream', required=True, type=Path)
    parser.add_argument('--output', type=Path, default=Path('build/benchmarks/comparison.json'))
    parser.add_argument('--samples', type=int, default=21)
    parser.add_argument('--warmups', type=int, default=3)
    parser.add_argument('--check-only', action='store_true')
    opts = parser.parse_args()
    if opts.samples < 15:
        parser.error('--samples must be >=15')
    binaries = {k: str(getattr(opts, k).resolve()) for k in ('go', 'upstream')}
    rng = random.Random(20261004)
    results = []
    with tempfile.TemporaryDirectory(prefix='ifttt-benchmark-') as tmp:
        specs = [(f'diff-{n}-{state}', n, state == 'fail', 0, False)
                 for n in (1, 100, 1000) for state in ('pass', 'fail')]
        specs += [('sparse-10000-pass', 1, False, 10000, False),
                  ('structural-1000-pass', 1000, False, 0, True),
                  ('structural-1000-fail', 1000, True, 0, True)]
        for name, pairs, failing, unrelated, structural in specs:
            root = Path(tmp) / name
            diff = fixture(root, pairs, failing, unrelated)
            if structural and failing:
                for i in range(pairs):
                    (root / f'target_{i}.go').unlink()
            expected = pairs if failing else 0
            commands = {'go_stdin': [binaries['go'], '-format', 'json'],
                        'go_git': [binaries['go'], '-format', 'json', '--vcs', 'git', '--diff', 'HEAD'],
                        'upstream': [binaries['upstream'], '--format', 'json', '--vcs', 'git', '--diff', 'HEAD']}
            if structural:
                commands = {'go_scan': [binaries['go'], '-format', 'json', '--vcs', 'git', *[f'source_{i}.go' for i in range(pairs)]],
                            'upstream': [binaries['upstream'], '--format', 'json', '--vcs', 'git',
                                         *[f'source_{i}.go' for i in range(pairs)]]}
            def run(mode):
                start = time.perf_counter_ns()
                data = diff if mode == 'go_stdin' else None
                result = command(commands[mode], root, data)
                elapsed = (time.perf_counter_ns() - start) / 1e6
                return result, elapsed
            checks = {}
            for mode in commands:
                result, _ = run(mode)
                count = count_findings('upstream' if mode == 'upstream' else 'go', result.stdout.decode())
                checks[mode] = dict(exit=result.returncode, findings=count)
                if result.returncode != int(bool(expected)) or count != expected:
                    raise RuntimeError(f'{name} {mode}: expected exit={int(bool(expected))}, findings={expected}; got {checks[mode]}\n{result.stdout.decode()}\n{result.stderr.decode()}')
            print(f'{name}: correctness passed {checks}', flush=True)
            timings = {mode: [] for mode in commands}
            order_log = []
            if not opts.check_only:
                for round_index in range(opts.warmups + opts.samples):
                    modes = list(commands)
                    rng.shuffle(modes)
                    if round_index >= opts.warmups:
                        order_log.append(modes)
                    for mode in modes:
                        result, elapsed = run(mode)
                        if result.returncode != int(bool(expected)):
                            raise RuntimeError(f'exit changed during timing: {name} {mode}')
                        if round_index >= opts.warmups:
                            timings[mode].append(elapsed)
            results.append(dict(workload=name, pairs=pairs, unrelated_files=unrelated,
                                diff_bytes=len(diff), diff_sha256=hashlib.sha256(diff).hexdigest(),
                                expected_findings=expected, checks=checks,
                                commands=commands, samples_ms=timings, order=order_log,
                                summary={k: summary(v) for k, v in timings.items() if v}))
    metadata = dict(timestamp_utc=datetime.datetime.now(datetime.timezone.utc).isoformat(),
                    platform=platform.platform(), machine=platform.machine(), python=platform.python_version(),
                    cpu=cpu_description(),
                    go_version=command(['go', 'version'], '.').stdout.decode().strip(),
                    git_version=command(['git', '--version'], '.').stdout.decode().strip(),
                    upstream_version=command([binaries['upstream'], '--version'], '.').stdout.decode().strip(),
                    upstream_commit=UPSTREAM_SHA, verified_upstream_archive_sha256=archive_provenance(binaries['upstream']),
                    binaries={k: dict(path=v, sha256=hashlib.sha256(Path(v).read_bytes()).hexdigest()) for k,v in binaries.items()},
                    samples=opts.samples, warmups=opts.warmups, random_seed=20261004,
                    cache_state='Warm OS filesystem caches; no cache flush; process restarted each sample',
                    fixture='Deterministic real Git repo, committed baseline, working-tree changes; generation/build excluded')
    metadata['go_build_info'] = command(['go', 'version', '-m', binaries['go']], '.').stdout.decode().strip()
    metadata['go_cgo_enabled'] = compiled_cgo(metadata['go_build_info'])
    metadata['go_binary_version'] = metadata['go_build_info'].splitlines()[0].split()[-1]
    opts.output.parent.mkdir(parents=True, exist_ok=True)
    opts.output.write_text(json.dumps(dict(metadata=metadata, results=results), indent=2) + '\n')

if __name__ == '__main__':
    main()
