#!/usr/bin/env python3
"""Correctness-gated locality, reverse-reference and worker-count measurements."""
import argparse
import hashlib
import json
from pathlib import Path
import random
import tempfile
import time
import benchmark as base


def fixture(root, kind, count, unrelated):
    base.fixture(root, 1, unrelated=unrelated)
    # Reset pair edits so reverse checks have an unchanged referring source.
    base.git(root, 'reset', '--hard', '-q', 'HEAD')
    target = root / 'target_0.go'
    source = root / 'source_0.go'
    if kind == 'label':
        source.write_text('// LINT.IfChange(pair_0)\nvalue = 0\n// LINT.ThenChange(//target_0.go:contract)\n')
        target.write_text('// LINT.IfChange(contract)\nvalue = 0\n// LINT.ThenChange()\n')
    (root / '.gitignore').write_text('ignored/\n')
    ignored = root / 'ignored'
    ignored.mkdir()
    (ignored / 'bad.go').write_text('// LINT.IfChange(ignored)\nvalue = 0\n// LINT.ThenChange(//missing.go)\n')
    for index in range(count):
        (root / f'changed_{index}.txt').write_text('before\n')
    base.git(root, 'add', '.')
    base.git(root, '-c', 'user.name=Benchmark', '-c', 'user.email=benchmark@example.invalid', 'commit', '-qm', 'scaling baseline')
    for index in range(count):
        (root / f'changed_{index}.txt').write_text('after\n')
    if kind == 'delete':
        target.unlink()
    elif kind == 'label':
        target.write_text(target.read_text().replace('IfChange(contract)', 'IfChange(renamed)'))
    elif kind == 'local':
        source.write_text(source.read_text().replace('value = 0', 'value = 1'))
        target.write_text('value = 1\n')
    return 1 if kind in ('delete', 'label') else 0


def specs():
    for kind in ('local', 'delete', 'label'):
        for count in (1, 100, 1000):
            yield kind, count, 1000
        for unrelated in (0, 10000):
            yield kind, 1, unrelated
    for workers in (0, 1, 2, 4, 8):
        yield 'structural', workers, 10000
    for workers in (2, 8):
        yield 'paired', workers, 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--go', type=Path, required=True)
    parser.add_argument('--upstream', type=Path, required=True)
    parser.add_argument('--output', type=Path, default=Path('build/benchmarks/scaling.json'))
    parser.add_argument('--samples', type=int, default=21)
    parser.add_argument('--warmups', type=int, default=3)
    parser.add_argument('--check-only', action='store_true')
    args = parser.parse_args()
    if args.samples < 15 or args.warmups < 0:
        parser.error('at least 15 samples and nonnegative warmups required')
    binaries = {name: str(getattr(args, name).resolve()) for name in ('go', 'upstream')}
    rng = random.Random(20261004)
    results = []
    with tempfile.TemporaryDirectory(prefix='ifttt-scaling-') as temp:
        for index, (kind, count, unrelated) in enumerate(specs()):
            root = Path(temp) / str(index)
            threads = count if kind in ('structural', 'paired') else 2
            if kind in ('structural', 'paired'):
                base.fixture(root, 1000, unrelated=unrelated)
                expected = 0
                tail = [f'source_{number}.go' for number in range(1000)] if kind == 'structural' else ['--diff', 'HEAD']
            else:
                expected = fixture(root, kind, count, unrelated)
                tail = ['--diff', 'HEAD']
            commands = {name: [binary, '--format', 'json', '--vcs', 'git', '--threads', str(threads), *tail] for name, binary in binaries.items()}
            name = f"paired-contracts1000-unrelated{unrelated}-threads{threads}" if kind == 'paired' else f"{kind}-changed{count if kind != 'structural' else 0}-unrelated{unrelated}-threads{threads}"
            checks = {}
            def run(tool):
                start = time.perf_counter_ns()
                result = base.command(commands[tool], root)
                elapsed = (time.perf_counter_ns() - start) / 1e6
                finding_count = base.count_findings(tool, result.stdout.decode())
                if result.returncode != int(bool(expected)) or finding_count != expected:
                    raise RuntimeError(f'{name} {tool}: wanted {expected}, got exit {result.returncode}, findings {finding_count}\n{result.stdout.decode()}\n{result.stderr.decode()}')
                return elapsed
            for tool in commands:
                run(tool)
                checks[tool] = dict(exit=int(bool(expected)), findings=expected)
            print(f'{name}: correctness passed', flush=True)
            timings = {tool: [] for tool in commands}
            orders = []
            if not args.check_only:
                for round_index in range(args.warmups + args.samples):
                    order = list(commands)
                    rng.shuffle(order)
                    if round_index >= args.warmups:
                        orders.append(order)
                    for tool in order:
                        elapsed = run(tool)
                        if round_index >= args.warmups:
                            timings[tool].append(elapsed)
            results.append(dict(workload=name, kind=kind, changed_nondirective_files=count if kind not in ('structural', 'paired') else 0,
                                unrelated_files=unrelated, contract_pairs=1000 if kind in ('paired', 'structural') else 1, changed_files=2000 if kind == 'paired' else 0 if kind == 'structural' else count + (2 if kind == 'local' else 1), threads=threads, expected_findings=expected, checks=checks,
                                commands=commands, samples_ms=timings, order=orders,
                                summary={tool: base.summary(samples) for tool, samples in timings.items() if samples}))
    metadata = dict(samples=args.samples, warmups=args.warmups, random_seed=20261004,
                    platform=base.platform.platform(), cpu=base.cpu_description(),
                    timestamp_utc=base.datetime.datetime.now(base.datetime.timezone.utc).isoformat(),
                    upstream_version=base.command([binaries['upstream'], '--version'], '.').stdout.decode().strip(),
                    upstream_commit=base.UPSTREAM_SHA, verified_upstream_archive_sha256=base.archive_provenance(binaries['upstream']),
                    binaries={tool: dict(path=binary, sha256=hashlib.sha256(Path(binary).read_bytes()).hexdigest()) for tool,binary in binaries.items()},
                    git_version=base.command(['git', '--version'], '.').stdout.decode().strip(),
                    go_build_info=base.command(['go', 'version', '-m', binaries['go']], '.').stdout.decode().strip(),
                    limits='Warm filesystem caches, process restart each run; no peak-memory measurement or CPU affinity; generation and build excluded.')
    metadata['go_cgo_enabled'] = base.compiled_cgo(metadata['go_build_info'])
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(dict(metadata=metadata, results=results), indent=2) + '\n')
    if not args.check_only:
        lines = ['# Locality and thread scaling benchmark', '', f"Measured {metadata['timestamp_utc']} on {metadata['cpu']}. {args.samples} measured runs after {args.warmups} warmups, randomized tool order. Every measured invocation is checked for its exact expected status and finding count.", '',
                 'Both tools use native Git diff acquisition and two workers except the explicit structural worker sweep. Local fixtures change one linked pair plus ordinary files. Reverse fixtures delete the target or rename its referenced label while the referring file stays unchanged. An ignored directive file with a missing target must contribute no findings. Structural fixtures validate the same 1,000 explicit sources with 10,000 unrelated files. Paired fixtures change both sides of 1,000 contracts and compare two against eight workers. The worker value zero asks each tool for its automatic default.', '',
                 '| Workload | Go median ms | Go p95 ms | Upstream median ms | Upstream p95 ms |', '| --- | ---: | ---: | ---: | ---: |']
        for result in results:
            g, u = result['summary']['go'], result['summary']['upstream']
            lines.append(f"| {result['workload']} | {g['median_ms']:.2f} | {g['p95_ms']:.2f} | {u['median_ms']:.2f} | {u['p95_ms']:.2f} |")
        rows = {row['workload']: row for row in results}
        def median(kind, count, unrelated, threads=2, tool='go'):
            key = f'paired-contracts1000-unrelated{unrelated}-threads{threads}' if kind == 'paired' else f'{kind}-changed{count}-unrelated{unrelated}-threads{threads}'
            return rows[key]['summary'][tool]['median_ms']
        lines += ['', f"With 1,000 unrelated files held fixed, Go local validation grows from {median('local', 1, 1000):.2f} ms at one ordinary changed file to {median('local', 1000, 1000):.2f} ms at 1,000; deletion validation grows from {median('delete', 1, 1000):.2f} to {median('delete', 1000, 1000):.2f} ms and label renames from {median('label', 1, 1000):.2f} to {median('label', 1000, 1000):.2f} ms. Thus the end-to-end CLI is **not flat in changed-file count**. Diff generation and parsing remain proportional to work even when reverse lookup is filtered.", '',
                  f"For Go structural validation, automatic zero, explicit two and eight workers measured {median('structural', 0, 10000, 0):.2f}, {median('structural', 0, 10000, 2):.2f} and {median('structural', 0, 10000, 8):.2f} ms. Paired 1,000-contract diff validation measured {median('paired', 0, 0, 2):.2f} ms at two workers versus {median('paired', 0, 0, 8):.2f} ms at eight. **This run does not support a claim that higher counts never help or that two is universally optimal.** Automatic zero and explicit two use the same configured Go worker count; their different observed timings also show environmental variability between fixture groups. Upstream eight-worker structural timing was {median('structural', 0, 10000, 8, 'upstream'):.2f} ms."]
        lines += ['', 'These fixtures test locality and measured scaling, not constant-time guarantees. Git diff generation and parsing necessarily depend on changed bytes; reverse-reference discovery must search repository contents. Worker contention conclusions apply only to this machine and fixture. Peak memory, cold-cache behavior, network targets and other hardware are not measured.', '',
                  f"The measured Go binary uses `CGO_ENABLED={metadata['go_cgo_enabled']} go build -trimpath`; release artifacts use `CGO_ENABLED=0`. Raw samples, commands, hashes and machine metadata: [scaling.json](scaling.json).", '', '```sh', f"CGO_ENABLED={metadata['go_cgo_enabled']} go build -trimpath -o build/iflint ./cmd", 'python3 scripts/benchmark_scaling.py --go build/iflint --upstream /path/to/ifttt-lint', '```', '']
        args.output.with_suffix('.md').write_text('\n'.join(lines))

if __name__ == '__main__':
    main()
