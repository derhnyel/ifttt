#!/usr/bin/env python3
"""Pinned real-repository CLI measurements with per-sample diagnostic checks."""
import argparse
from collections import Counter
from contextlib import contextmanager
import datetime
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import random
import shlex
import subprocess
import time

import benchmark as base

THREADS = (0, 1, 2, 4, 8, 16)
CONFIG = 'directives:\n  prefix: LINT\n'
SMOKE_IGNORES = ('depot/*', '<INTERNAL>/*', '<ROOT_DIR>/*')


def validate_options(samples, warmups, timeout):
    if samples < 15 or warmups < 0 or not math.isfinite(timeout) or timeout <= 0:
        raise ValueError('require at least 15 samples, nonnegative warmups and positive timeout')


def selected_workloads(modes, threads):
    selected = set(modes or ('filtered', 'tracked-all', 'native-head-clean'))
    return [('filtered', count) for count in threads or THREADS if 'filtered' in selected] + [(mode, 2) for mode in ('tracked-all', 'native-head-clean') if mode in selected]


def git(root, *args, allow_no_match=False):
    result = subprocess.run(['git', *args], cwd=root, capture_output=True, timeout=180)
    if result.returncode and not (allow_no_match and result.returncode == 1):
        raise RuntimeError(result.stderr.decode(errors='replace'))
    return result.stdout


def repository_inventory(root):
    if git(root, 'status', '--porcelain', '-z'):
        raise RuntimeError(f'{root} must be a clean checkout')
    tracked, sizes, modes = [], {}, Counter()
    for record in git(root, 'ls-tree', '-rlz', '--full-tree', 'HEAD').split(b'\0'):
        if not record:
            continue
        header, raw_path = record.split(b'\t', 1)
        mode, kind, _, size = header.split()
        name = os.fsdecode(raw_path)
        tracked.append(name)
        sizes[name] = int(size) if kind == b'blob' else 0
        modes[mode.decode()] += 1
    needle = git(root, 'grep', '-lz', '-e', r'LINT\.', '--', allow_no_match=True)
    directives = [os.fsdecode(name) for name in needle.split(b'\0') if name]
    if not set(directives).issubset(tracked):
        raise RuntimeError('directive discovery returned an untracked path')
    origin = subprocess.run(['git', 'remote', 'get-url', 'origin'], cwd=root, capture_output=True, timeout=10)
    return dict(path=str(root.resolve()), commit=git(root, 'rev-parse', 'HEAD').decode().strip(),
                url=origin.stdout.decode().strip(), tracked_files=tracked,
                tracked_file_count=len(tracked), tracked_bytes=sum(sizes.values()), tracked_modes=dict(modes),
                tracked_files_sha256=hashlib.sha256(b'\0'.join(os.fsencode(name) for name in tracked)).hexdigest(),
                directive_files=directives, directive_file_count=len(directives),
                directive_bytes=sum(sizes[name] for name in directives),
                directive_files_sha256=hashlib.sha256(needle).hexdigest(),
                discovery_command=['git', 'grep', '-lz', '-e', r'LINT\.', '--'],
                shallow=git(root, 'rev-parse', '--is-shallow-repository').decode().strip() == 'true')


def commands_for(binaries, repository, mode, threads, files):
    options = ['--format=json', '--vcs=git', '--strict=false', '--threads', str(threads)]
    if repository == 'chromium':
        options += [arg for pattern in SMOKE_IGNORES for arg in ('--ignore', pattern)]
    tail = ['--', *files] if mode == 'filtered' else ['--', '**/*'] if mode == 'tracked-all' else ['--diff', 'HEAD']
    return {tool: [binary, *options, *tail] for tool, binary in binaries.items()}


def category(rule, message):
    text = message.lower()
    if rule == 'label_ambiguous' or ('label' in text and 'ambiguous' in text):
        return 'label_ambiguous'
    if 'target file not found' in text or 'target cannot be read' in text:
        return 'target_missing'
    if rule == 'label_missing' or ('label' in text and 'not found' in text):
        return 'label_missing'
    if rule == 'then_label_missing' or ('expected changes' in text and 'none found' in text):
        return 'cochange_missing'
    if rule in ('unclosed_ifchange', 'orphan_if') or 'ifchange without matching thenchange' in text:
        return 'unmatched_ifchange'
    if rule in ('orphan_thenchange', 'orphan_then') or 'without preceding ifchange' in text:
        return 'unmatched_thenchange'
    if 'empty' in text and 'thenchange' in text:
        return 'empty_thenchange'
    if 'self-referencing' in text:
        return 'self_reference'
    if rule == 'duplicate_label' or 'duplicate' in text and 'label' in text:
        return 'duplicate_label'
    if rule in ('invalid_directive', 'parse_error', 'unknown_directive') or 'malformed label' in text or 'malformed directive' in text or 'invalid directive' in text:
        return 'parse_error'
    if 'path traversal' in text or 'must be relative' in text:
        return 'path_policy'
    if 'url targets are not supported' in text:
        return 'url_policy'
    if rule == 'error':
        return 'read_failure'
    return 'unknown'


def normalize_report(tool, status, stdout):
    if status not in (0, 1):
        raise ValueError(f'operational failure: exit {status}')
    if not stdout.strip() and tool == 'upstream' and status == 0:
        entries = []
    else:
        parsed = json.loads(stdout)
        key = 'errors' if tool == 'go' else 'diagnostics'
        if not isinstance(parsed, dict) or not isinstance(parsed.get(key), list):
            raise ValueError(f'invalid {tool} report shape')
        entries = parsed[key]
    findings = []
    for raw in entries:
        if not isinstance(raw, dict) or not isinstance(raw.get('file'), str) or not isinstance(raw.get('line'), int) or raw['line'] < 1:
            raise ValueError(f'invalid {tool} finding: {raw!r}')
        if raw.get('suppressed'):
            continue
        message = raw.get('message', '')
        rule = raw.get('ruleId')
        comparison_line = raw.get('target', {}).get('then_change_line', raw['line']) if isinstance(raw.get('target'), dict) else raw['line']
        findings.append(dict(file=os.path.normpath(raw['file']), line=raw['line'], comparison_line=comparison_line,
                             severity=raw.get('severity', 'error'), rule=rule,
                             category=category(rule, message), message=message,
                             target=raw.get('target') if tool == 'upstream' else
                             dict(path=raw.get('targetPath'), label=raw.get('targetLabel'))))
    findings.sort(key=lambda item: json.dumps(item, sort_keys=True))
    return dict(exit=status, findings=findings)


def capture_report(tool, process):
    if process.returncode not in (0, 1):
        return dict(exit=process.returncode, findings=[], operational_error=dict(
            stdout=process.stdout.decode(errors='replace'), stderr=process.stderr.decode(errors='replace')))
    return normalize_report(tool, process.returncode, process.stdout.decode())


def load_checkpoint(output, metadata):
    previous = json.loads(output.read_text())
    recorded = previous['metadata']
    keys = ('samples', 'warmups', 'check_only', 'random_seed', 'binaries',
            'go_configuration_sha256', 'go_cgo_enabled', 'cpu', 'platform', 'go_build_info',
            'upstream_version', 'upstream_commit', 'verified_upstream_archive_sha256', 'git_version', 'python')
    defaults = dict(timeout_seconds=180, requested_repositories=['chromium', 'tensorflow'],
                    requested_workloads=[list(item) for item in selected_workloads(None, None)])
    if recorded.get('complete') or any(recorded.get(key) != metadata.get(key) for key in keys):
        raise RuntimeError('checkpoint metadata does not match requested measurement')
    if any(recorded.get(key, default) != metadata.get(key, default) for key, default in defaults.items()):
        raise RuntimeError('checkpoint options do not match requested measurement')
    seen = set()
    requested_repos = metadata.get('requested_repositories', defaults['requested_repositories'])
    requested_workloads = metadata.get('requested_workloads', defaults['requested_workloads'])
    for row in previous['results']:
        identity = row['repository'], row['mode'], row['threads']
        if identity in seen or row['repository'] not in requested_repos or [row['mode'], row['threads']] not in requested_workloads:
            raise RuntimeError('checkpoint has duplicate or unrequested workloads')
        seen.add(identity)
        failed = any('operational_error' in report for report in row.get('baseline', {}).values())
        count = 0 if recorded['check_only'] or failed else recorded['samples']
        if len(row['order']) != count or any(sorted(order) != ['go', 'upstream'] for order in row['order']):
            raise RuntimeError('checkpoint contains invalid sample order/count')
        for tool in ('go', 'upstream'):
            values = row['samples_ms'][tool]
            if len(values) != count or any(type(value) not in (int, float) or not math.isfinite(value) or value <= 0 for value in values):
                raise RuntimeError('checkpoint contains invalid sample count/value')
            if values and row['summary'].get(tool) != base.summary(values):
                raise RuntimeError('checkpoint summary does not match recorded samples')
    return previous


def validate_inventory(previous, current):
    if previous != current:
        raise RuntimeError('checkpoint repository commit or inventory changed')


def compare_reports(go, upstream):
    failures = [f'{tool} operational failure (exit {report["exit"]}): {report["operational_error"]["stderr"].strip()}'
                for tool, report in (('go', go), ('upstream', upstream)) if 'operational_error' in report]
    if failures:
        return dict(equivalent=False, eligible_for_speed_claim=False, reasons=failures, go_only=[], upstream_only=[])
    def target_key(item):
        if item['category'] not in ('target_missing', 'label_missing', 'label_ambiguous', 'cochange_missing'):
            return None
        target = item.get('target') or {}
        if 'raw' not in target:
            return target.get('path'), target.get('label') or None
        raw, label = target['raw'], None
        if '://' not in raw and raw.rfind(':') > raw.rfind('/'):
            file_part, _, label_part = raw.rpartition(':')
            if raw.startswith(':') or (label_part and label_part[0].isascii() and label_part[0].isalpha()):
                raw, label = file_part, label_part
        if raw.startswith('/'):
            resolved = raw.lstrip('/')
        elif not raw:
            resolved = item['file']
        elif '://' in raw:
            resolved = raw
        elif '/' in raw or '\\' in raw:
            resolved = os.path.normpath(raw.replace('\\', '/'))
        else:
            resolved = os.path.normpath(os.path.join(os.path.dirname(item['file']), raw))
        return resolved, label or None
    def key(item):
        return item['file'], item.get('comparison_line', item['line']), item['severity'], item['category'], target_key(item)
    left, right = Counter(map(key, go['findings'])), Counter(map(key, upstream['findings']))
    go_only, upstream_only = list((left - right).elements()), list((right - left).elements())
    unknown = any(item['category'] == 'unknown' for report in (go, upstream) for item in report['findings'])
    equivalent = go['exit'] == upstream['exit'] and not go_only and not upstream_only and not unknown
    reasons = []
    if go['exit'] != upstream['exit']:
        reasons.append('different exit status')
    if go_only or upstream_only:
        reasons.append('different diagnostic locations, severities, categories or targets')
    if unknown:
        reasons.append('unmapped diagnostic categories require manual review')
    return dict(equivalent=equivalent, eligible_for_speed_claim=equivalent,
                reasons=reasons, go_only=go_only, upstream_only=upstream_only)


def validate_result(result, baseline, clean_native):
    if result != baseline:
        raise RuntimeError('sample diagnostics or exit status changed from baseline')
    if clean_native and (result['exit'] != 0 or result['findings']):
        raise RuntimeError('clean native HEAD checkout must produce exit 0 and no findings')


@contextmanager
def lint_configuration(parent):
    config = parent / '.ifttt-lint.yaml'
    previous = config.read_bytes() if config.exists() else None
    config.write_text(CONFIG)
    try:
        yield
    finally:
        if previous is None:
            config.unlink()
        else:
            config.write_bytes(previous)


def render_report(data):
    metadata = data['metadata']
    lines = ['# Real-repository CLI benchmark', '',
             f"{metadata['samples']} samples after {metadata['warmups']} warmups, randomized tool order per round. Warm filesystem caches; subprocess startup, serialization and Git operations included. Clone/download/build/discovery excluded.", '',
             'Tool order is randomized within each row; thread settings are measured sequentially. In both tools, `--threads 0` uses the default of 2 workers. Differences between the 0 and 2 rows expose variation between measurement periods; these observations do not establish a reliable thread ranking or optimum.', '',
             'The filtered rows reproduce upstream smoke inputs: tracked files containing `LINT.`; both tools use `--strict=false`, and Chromium excludes `depot/*`, `<INTERNAL>/*`, `<ROOT_DIR>/*`. The tracked-all rows use the same tracked `**/*` glob. Native HEAD rows verify clean working-tree diff acquisition; they do not measure validation of the last commit.', '',
             'Every measured output must match its full preflight diagnostic baseline, including file, line, severity, message and rule. Cross-tool comparisons use diagnostic file/severity/category/target, exit status, and the ThenChange line when upstream target metadata supplies it (upstream otherwise reports the IfChange line). Raw reported locations are preserved. Unknown categories and mismatched rows are excluded from speed claims. This checks the observed repository cases rather than establishing general semantic parity.', '']
    if metadata.get('check_only'):
        lines[2:2] = ['Untimed preflight; these rows contain no performance measurements.', '']
    if metadata.get('complete') is False:
        lines[2:2] = ['Incomplete checkpoint; requested workloads have not all completed.', '']
    for repo in data['repositories']:
        lines.append(f"- {repo['name']}: [{repo['commit']}]({repo['url'].removesuffix('.git')}/commit/{repo['commit']}), {repo['tracked_file_count']:,} tracked files / {repo['tracked_bytes']:,} blob bytes; {repo['directive_file_count']:,} directive files / {repo['directive_bytes']:,} bytes.")
    lines += ['', '| Repository / mode / threads | Go median ms | Upstream median ms | Findings Go / upstream | Upstream / Go |', '| --- | ---: | ---: | ---: | ---: |']
    for row in data['results']:
        summary = row.get('summary', {})
        go, upstream = summary.get('go', {}).get('median_ms'), summary.get('upstream', {}).get('median_ms')
        ratio = f'{upstream / go:.2f}×' if go and upstream and row['comparison']['eligible_for_speed_claim'] else 'excluded' if not row['comparison']['equivalent'] else 'not timed'
        lines.append(f"| {row['repository']} / {row['mode']} / {row['threads']} | {go:.2f}" if go is not None else f"| {row['repository']} / {row['mode']} / {row['threads']} | —")
        lines[-1] += f" | {upstream:.2f}" if upstream is not None else ' | —'
        def finding_count(tool):
            report = row['baseline'][tool]
            return f'operational failure (exit {report["exit"]})' if 'operational_error' in report else str(len(report['findings']))
        lines[-1] += f" | {finding_count('go')} / {finding_count('upstream')} | {ratio} |"
    for row in data['results']:
        for tool, report in row['baseline'].items():
            if 'operational_error' in report:
                elapsed = row.get('baseline_elapsed_ms', {}).get(tool)
                duration = f' after {elapsed:.2f} ms' if elapsed is not None else ''
                lines += ['', f"{row['repository']} / {row['mode']} / {row['threads']}: {tool} operational failure (exit {report['exit']}){duration}: `{report['operational_error']['stderr'].strip()}`. This row has no measured samples or ratio. The original input selection is retained; no unreadable files are silently removed."]
    representatives = {}
    for row in data['results']:
        if row['mode'] != 'native-head-clean' and (row['repository'] not in representatives or (row['mode'] == 'filtered' and row['threads'] == 2)):
            representatives[row['repository']] = row
    for name, row in representatives.items():
        comparison = row['comparison']
        lines += ['', f"{name} diagnostic comparison ({row['mode']}, {row['threads']} workers):"]
        for tool in ('go', 'upstream'):
            counts = Counter(finding['category'] for finding in row['baseline'][tool]['findings'])
            lines.append(f"- {tool}: " + (', '.join(f'{category}={count}' for category, count in sorted(counts.items())) or 'no findings') + '.')
        if comparison['equivalent']:
            lines.append('- Diagnostic signatures and exit status match for this observed case.')
            continue
        lines.append('- Excluded from speed claims: ' + '; '.join(comparison['reasons']) + '.')
        categories = {finding['category'] for tool in ('go', 'upstream') for finding in row['baseline'][tool]['findings']}
        if 'path_policy' in categories:
            lines.append('- Upstream rejects traversal targets such as `../file` even with `--strict=false`; this project permits paths that remain inside the workspace. These policies produce different findings on this checkout.')
        if 'url_policy' in categories:
            lines.append('- Upstream rejects URL targets. This project supports configured remote providers, so unresolved URL references can produce different error categories.')
        if any(': ' in (finding.get('target') or {}).get('raw', '') for finding in row['baseline']['upstream']['findings']):
            lines.append('- Some multiline targets contain whitespace after `:`. Upstream treats those strings as literal filenames and reports missing targets; this project rejects malformed target syntax and can also report the resulting unmatched IfChange.')
        if categories & {'unmatched_ifchange', 'unmatched_thenchange'}:
            lines.append('- Block-pairing differences require care: this project supports nested blocks, while upstream retains one pending IfChange. The unmatched locations below are preserved rather than treated as equivalent.')
        for tool, difference in (('Go only', comparison['go_only']), ('Upstream only', comparison['upstream_only'])):
            lines.append(f'- {tool}: {len(difference)} unmatched diagnostic signatures; examples:')
            for item in difference[:5]:
                target = f' target={json.dumps(item[4], ensure_ascii=False)}' if len(item) > 4 and item[4] is not None else ''
                lines.append(f'  - `{item[0]}:{item[1]}` — {item[3]} ({item[2]}){target}.')
    report_path = metadata.get('report_path', 'build/benchmarks/repositories.json')
    report_file = Path(report_path).name
    lines += ['', f'Full commands, pinned commits, exact blob sizes, file-selection hashes, binary hashes/build settings, archive provenance, raw baseline diagnostics, comparison differences and every measured sample are in [{report_file}]({report_file}).', '',
              'Measurements apply to these pinned checkouts and this machine. They do not establish constant runtime, universal thread-count optimality or peak memory usage. Structural repository diagnostics are retained even when the checkout itself is clean.', '', 'Reproduce from the project root with new clone directories. Fetching the recorded commits pins the inputs even after upstream HEAD advances:', '', '```sh', 'mkdir -p build/benchmark-repos']
    for repo in data['repositories']:
        directory = shlex.quote('build/benchmark-repos/' + repo['name'])
        lines += [f"git clone --no-checkout --depth 1 {shlex.quote(repo['url'])} {directory}",
                  f"git -C {directory} fetch --depth 1 origin {shlex.quote(repo['commit'])}",
                  f"git -C {directory} checkout --detach FETCH_HEAD"]
    reproduce = ['python3', 'scripts/benchmark_repositories.py', '--go', 'build/iflint',
                 '--upstream', 'build/upstream/ifttt-lint', '--repos', 'build/benchmark-repos']
    for repo in metadata.get('requested_repositories', []):
        reproduce += ['--repository', repo]
    workloads = metadata.get('requested_workloads', [])
    for mode in dict.fromkeys(row[0] for row in workloads):
        reproduce += ['--mode', mode]
    for threads in dict.fromkeys(row[1] for row in workloads):
        reproduce += ['--threads', str(threads)]
    reproduce += ['--samples', str(metadata['samples']), '--warmups', str(metadata['warmups'])]
    if 'report_path' in metadata:
        reproduce += ['--output', report_path]
    lines += ['make build', 'python3 scripts/benchmark_upstream.py --output build/upstream/ifttt-lint',
              shlex.join(reproduce), '```', '']
    return '\n'.join(lines)


def write_report(data, output, complete):
    data = {**data, 'metadata': {**data['metadata'], 'complete': complete, 'report_path': str(output)}}
    output.parent.mkdir(parents=True, exist_ok=True)
    for target, contents in ((output, json.dumps(data, indent=2) + '\n'), (output.with_suffix('.md'), render_report(data))):
        temporary = target.with_suffix(target.suffix + '.tmp')
        temporary.write_text(contents)
        temporary.replace(target)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--go', required=True, type=Path)
    parser.add_argument('--upstream', required=True, type=Path)
    parser.add_argument('--repos', type=Path, default=Path('build/benchmark-repos'))
    parser.add_argument('--repository', choices=('chromium', 'tensorflow'), action='append')
    parser.add_argument('--mode', choices=('filtered', 'tracked-all', 'native-head-clean'), action='append')
    parser.add_argument('--threads', type=int, choices=THREADS, action='append')
    parser.add_argument('--samples', type=int, default=21)
    parser.add_argument('--warmups', type=int, default=3)
    parser.add_argument('--timeout', type=float, default=180)
    parser.add_argument('--check-only', action='store_true')
    parser.add_argument('--resume', action='store_true', help='resume an incomplete report only when binaries, options and repository inventories match')
    parser.add_argument('--output', type=Path, default=Path('build/benchmarks/repositories.json'))
    args = parser.parse_args()
    try:
        validate_options(args.samples, args.warmups, args.timeout)
    except ValueError as err:
        parser.error(str(err))
    binaries = {tool: str(getattr(args, tool).resolve()) for tool in ('go', 'upstream')}
    hashes = {tool: hashlib.sha256(Path(binary).read_bytes()).hexdigest() for tool, binary in binaries.items()}
    provenance = base.archive_provenance(binaries['upstream'])
    if provenance is None:
        raise RuntimeError('upstream executable needs checksum-verified archive provenance sidecar')
    build_info = base.command(['go', 'version', '-m', binaries['go']], '.').stdout.decode().strip()
    if base.compiled_cgo(build_info) != '0':
        raise RuntimeError('measure the canonical CGO_ENABLED=0 Go executable')
    metadata = dict(timestamp_utc=datetime.datetime.now(datetime.timezone.utc).isoformat(),
                    platform=platform.platform(), cpu=base.cpu_description(), python=platform.python_version(),
                    git_version=git('.', '--version').decode().strip(), samples=args.samples, warmups=args.warmups,
                    random_seed=20261005, check_only=args.check_only, go_build_info=build_info, go_cgo_enabled='0',
                    upstream_version=base.command([binaries['upstream'], '--version'], '.').stdout.decode().strip(),
                    upstream_commit=base.UPSTREAM_SHA, verified_upstream_archive_sha256=provenance,
                    binaries={tool: dict(path=binary, sha256=hashes[tool]) for tool, binary in binaries.items()},
                    go_configuration=CONFIG, go_configuration_sha256=hashlib.sha256(CONFIG.encode()).hexdigest(),
                    timeout_seconds=args.timeout, requested_repositories=args.repository or ['chromium', 'tensorflow'],
                    requested_workloads=[list(item) for item in selected_workloads(args.mode, args.threads)],
                    upstream_smoke_source='https://github.com/simonepri/ifttt-lint/blob/' + base.UPSTREAM_SHA + '/tests/smoke.rs')
    checkpoint = load_checkpoint(args.output, metadata) if args.resume else None
    if checkpoint:
        metadata['timestamp_utc'] = checkpoint['metadata']['timestamp_utc']
        metadata['resumed_at_utc'] = [*checkpoint['metadata'].get('resumed_at_utc', []), datetime.datetime.now(datetime.timezone.utc).isoformat()]
    rng, repositories, results = random.Random(20261005), [], checkpoint['results'] if checkpoint else []
    completed = {(row['repository'], row['mode'], row['threads']): row for row in results}
    with lint_configuration(args.repos):
        for name in args.repository or ('chromium', 'tensorflow'):
            root = args.repos / name
            if (root / '.ifttt-lint.yaml').exists():
                raise RuntimeError(f'{root}: repo-level Go config would override benchmark LINT configuration')
            inventory = repository_inventory(root)
            if not inventory['directive_files']:
                raise RuntimeError(f'{name} has no discovered directive files')
            recorded_inventory = dict(name=name, **{key: value for key, value in inventory.items() if key != 'tracked_files'})
            if checkpoint:
                for previous in checkpoint['repositories']:
                    if previous['name'] == name:
                        validate_inventory(previous, recorded_inventory)
            repositories.append(recorded_inventory)
            modes = selected_workloads(args.mode, args.threads)
            for mode, threads in modes:
                prior = completed.get((name, mode, threads))
                if prior:
                    if not args.check_only and not any('operational_error' in report for report in prior['baseline'].values()):
                        for _ in range(args.warmups + args.samples):
                            order = list(binaries)
                            rng.shuffle(order)
                    print(f'{name}/{mode}/{threads}: retained checkpoint samples', flush=True)
                    continue
                for tool, binary in binaries.items():
                    if hashlib.sha256(Path(binary).read_bytes()).hexdigest() != hashes[tool]:
                        raise RuntimeError(f'{tool} binary changed; rebuild/preflight before measuring')
                commands = commands_for(binaries, name, mode, threads, inventory['directive_files'])
                baseline, baseline_elapsed, timings, order_log = {}, {}, {tool: [] for tool in binaries}, []
                def run(tool):
                    start = time.perf_counter_ns()
                    process = subprocess.run(commands[tool], cwd=root, capture_output=True, timeout=args.timeout)
                    elapsed = (time.perf_counter_ns() - start) / 1e6
                    try:
                        report = capture_report(tool, process)
                    except (ValueError, UnicodeError) as err:
                        raise RuntimeError(f'{name}/{mode}/{threads} {tool}: {err}; {process.stderr.decode(errors="replace")}') from err
                    return report, elapsed
                for tool in binaries:
                    baseline[tool], baseline_elapsed[tool] = run(tool)
                    if 'operational_error' not in baseline[tool]:
                        validate_result(baseline[tool], baseline[tool], mode == 'native-head-clean')
                comparison = compare_reports(baseline['go'], baseline['upstream'])
                failed = any('operational_error' in report for report in baseline.values())
                print(f'{name}/{mode}/{threads}: ' + ('; '.join(comparison['reasons']) if failed else f'Go={len(baseline["go"]["findings"])} upstream={len(baseline["upstream"]["findings"])} equivalent={comparison["equivalent"]}'), flush=True)
                if not args.check_only and not failed:
                    for index in range(args.warmups + args.samples):
                        order = list(binaries)
                        rng.shuffle(order)
                        if index >= args.warmups:
                            order_log.append(order)
                        for tool in order:
                            report, elapsed = run(tool)
                            validate_result(report, baseline[tool], mode == 'native-head-clean')
                            if index >= args.warmups:
                                timings[tool].append(elapsed)
                results.append(dict(repository=name, mode=mode, threads=threads, commands=commands,
                                    baseline=baseline, baseline_elapsed_ms=baseline_elapsed, comparison=comparison, samples_ms=timings, order=order_log,
                                    summary={tool: base.summary(values) for tool, values in timings.items() if values}))
                write_report(dict(metadata=metadata, repositories=repositories, results=results), args.output, False)
    for tool, binary in binaries.items():
        if hashlib.sha256(Path(binary).read_bytes()).hexdigest() != hashes[tool]:
            raise RuntimeError(f'{tool} binary changed during benchmark; discard timings')
    for repo in repositories:
        if git(repo['path'], 'rev-parse', 'HEAD').decode().strip() != repo['commit'] or git(repo['path'], 'status', '--porcelain', '-z'):
            raise RuntimeError('repository changed during benchmark; discard timings')
    data = dict(metadata=metadata, repositories=repositories, results=results)
    data['metadata']['operational_failure_workloads'] = sum(any('operational_error' in report for report in row['baseline'].values()) for row in results)
    write_report(data, args.output, True)


if __name__ == '__main__':
    main()
