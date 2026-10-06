# Reproduce the benchmarks

See the [measured results and correctness checks](../README.md#benchmarks-and-correctness).

Run these commands from the repository root. The scripts check findings before timing. Each JSON report records binary hashes, commands and samples. Different findings do not establish equivalent checking performance.

```sh
make build
python3 scripts/benchmark_upstream.py
python3 scripts/benchmark.py --go build/ifttt --upstream build/upstream/ifttt-lint
python3 scripts/benchmark_report.py build/benchmarks/comparison.json
python3 scripts/benchmark_scaling.py --go build/ifttt --upstream build/upstream/ifttt-lint
make benchmark-changeset
```

For the repository survey, clone both inputs into ignored local storage:

```sh
git clone --depth 1 https://github.com/chromium/chromium build/benchmark-repos/chromium
git clone --depth 1 https://github.com/tensorflow/tensorflow build/benchmark-repos/tensorflow
git -C build/benchmark-repos/chromium fetch --depth 1 origin f7a8030b4c5ad01f8bad7e1e392709a3fbf121dd
git -C build/benchmark-repos/chromium checkout --detach FETCH_HEAD
git -C build/benchmark-repos/tensorflow fetch --depth 1 origin 031dd1d53ac37fbb438eda763678c03c985db8cd
git -C build/benchmark-repos/tensorflow checkout --detach FETCH_HEAD
make benchmark-repositories
```

The report records the actual revisions. Pin the same commits when comparing runs. The scripts restore the original configuration after each run.

Reports default to `build/benchmarks`. Binaries, downloaded tools and repository checkouts also stay under ignored `build/`. Do not commit generated reports, profiles or test output. Commit test sources, fixtures and benchmark scripts.
