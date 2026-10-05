#!/usr/bin/env python3
"""Render a concise report from benchmark.py's raw measurements."""
import argparse
import json
from pathlib import Path
import benchmark as base


def render(data):
    meta = data['metadata']
    cgo = base.compiled_cgo(meta['go_build_info'])
    lines = ['# Comparative CLI benchmark', '',
             f"Measured {meta['timestamp_utc']} on {meta['cpu']} ({meta['machine']}), {meta['platform']}.", '',
             'Both tools checked identical Google-style `LINT.IfChange/ThenChange` directives in deterministic real Git repositories. Go was configured with `directives.prefix: LINT`. Before any timing, every tool had to produce the independently expected exit status and number of findings. Source-only edits produce one finding per pair; edits to both source and target pass. Structural failures remove every referenced target.', '',
             f"Upstream: `{meta['upstream_version']}`, commit `{meta['upstream_commit']}`, official native release archive verified against its published SHA-256. Go executable compiler: `{meta['go_binary_version']}` (host command: `{meta['go_version']}`). Executable hashes, build info, exact commands, sample orders and all raw samples are in [comparison.json](comparison.json).", '',
             f"{meta['samples']} samples per command after {meta['warmups']} warmups, randomized command order with seed {meta['random_seed']}. Each sample restarts the executable. Warm OS filesystem caches; no cache flush. Builds and fixture generation are excluded. Timings include subprocess startup, output serialization/capture, and required Git work.", '',
             f'The measured Go executable was built with `CGO_ENABLED={cgo} go build -trimpath`. Release builds use `CGO_ENABLED=0`. Executable hashes and full compiler build settings are recorded in the raw results.', '',
             'The primary comparison runs each native CLI with `--vcs git --diff HEAD`, including its own Git discovery, diff and commit-message operations. Go stdin-only timing separately isolates the diff-consumer interface and excludes diff generation. Structural checks pass the same explicit source filenames to both tools; fixture generation, builds and shell glob expansion are excluded.', '',
             '| Workload | Go native median ms | Upstream median ms | Upstream / Go native | Go stdin median ms |',
             '| --- | ---: | ---: | ---: | ---: |']
    for result in data['results']:
        stats = result['summary']
        primary = stats.get('go_git', stats.get('go_scan'))
        if not primary:
            continue
        upstream = stats['upstream']['median_ms']
        go = primary['median_ms']
        stdin = f"{stats['go_stdin']['median_ms']:.2f}" if 'go_stdin' in stats else '—'
        lines.append(f"| {result['workload']} | {go:.2f} | {upstream:.2f} | {upstream/go:.2f}× | {stdin} |")
    lines += ['', 'Dispersion (milliseconds; sample standard deviation):', '',
              '| Workload / command | min | median | p95 | max | stdev |',
              '| --- | ---: | ---: | ---: | ---: | ---: |']
    for result in data['results']:
        for mode, stats in result['summary'].items():
            values = ' | '.join(f"{stats[key]:.2f}" for key in ('min_ms', 'median_ms', 'p95_ms', 'max_ms', 'stdev_ms'))
            lines.append(f"| {result['workload']} / {mode} | {values} |")
    lines += ['', 'These measurements characterize these fixtures and this machine only. They do not establish universal performance or feature parity; different repository shapes, hardware, cache state, and rules can change the results. No conclusion is based on correctness-mismatched workloads.', '',
              'Reproduce from the repository root (supply a checksum-verified native upstream executable):', '',
              '```sh', f'CGO_ENABLED={cgo} go build -trimpath -o build/ifttt ./cmd/ifttt',
              'python3 scripts/benchmark_upstream.py',
              'python3 -m unittest discover -s scripts -p "test_benchmark.py"',
              'python3 scripts/benchmark.py --go build/ifttt --upstream /path/to/ifttt-lint',
              'python3 scripts/benchmark_report.py build/benchmarks/comparison.json', '```', '']
    return '\n'.join(lines)

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('results', type=Path)
    args = parser.parse_args()
    args.results.with_suffix('.md').write_text(render(json.loads(args.results.read_text())))
