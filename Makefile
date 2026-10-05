export PATH := $(CURDIR)/build/tools:$(PATH)
.PHONY: check test lint smoke extension build release-build security coverage benchmark-test benchmark-repositories benchmark-changeset test-tools extension-host workflow-lint release-verify
check: lint test smoke extension security benchmark-test workflow-lint

test:
	go test -race ./...
lint:
	@test -z "$$(gofmt -l cmd internal test)" || (gofmt -l cmd internal test; exit 1)
	go vet ./...
smoke:
	bash test/smoke.sh
extension:
	cd vscode-extension && npm ci && npm run lint && npm test && npm audit
build:
	mkdir -p build
	@test -f build/go.mod || printf 'module ifttt.local/build\n' > build/go.mod
	CGO_ENABLED=0 go build -trimpath -o build/ifttt ./cmd/ifttt
release-build:
	bash scripts/build-release.sh
release-verify:
	python3 scripts/release.py verify
workflow-lint:
	go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.11 -shellcheck= -pyflakes=

security:
	go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...

# Instrument the actual CLI used by integration subprocesses, then union its
# covered blocks with the unit suite. Keep both component profiles reviewable.
coverage:
	mkdir -p build/coverage
	rm -rf build/coverage/integration
	mkdir -p build/coverage/integration
	go test -count=1 -covermode=atomic -coverpkg=./... -coverprofile=build/coverage/unit.out ./...
	IFTTT_INTEGRATION_COVER_DIR="$(CURDIR)/build/coverage/integration" go test -count=1 ./test/integration
	go tool covdata textfmt -i=build/coverage/integration -o=build/coverage/integration.out
	awk 'BEGIN { print "mode: atomic" } FNR == 1 { next } { key = $$1 " " $$2; counts[key] += $$3 } END { for (key in counts) print key, counts[key] }' build/coverage/unit.out build/coverage/integration.out > build/coverage/combined.out
	go tool cover -func=build/coverage/combined.out

benchmark-test:
	python3 -m unittest discover -s scripts -p 'test_*.py'

# Clone the benchmark checkouts as documented in the README Benchmarks section.
# Download verification and compilation run before any executable timing.
benchmark-repositories: build
	python3 scripts/benchmark_upstream.py --output build/upstream/ifttt-lint
	python3 scripts/benchmark_repositories.py --go build/ifttt --upstream build/upstream/ifttt-lint --repos build/benchmark-repos

# Independent committed Git snapshots; validate every report before timing.
benchmark-changeset: build
	python3 scripts/benchmark_changeset.py --go build/ifttt --output build/benchmarks/cross-repository.json

test-tools:
	python3 test/install-jj.py

extension-host:
	python3 vscode-extension/test/run-host.py
	IFTTT_HOST_VCS=jj python3 vscode-extension/test/run-host.py
