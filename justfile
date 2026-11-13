set default-list

# Keep this version in sync with .github/workflows/lint.yml.
golangci_lint_version := "v2.15.0"
golangci_lint_dir := justfile_directory() / ".bin" / golangci_lint_version
golangci_lint := golangci_lint_dir / "golangci-lint"

# Tidy module files and verify downloaded dependencies.
[group('modules')]
tidy:
    go mod tidy
    go mod verify

# Lint and run all tests with the race detector.
[group('quality')]
check: lint unit-test

# Validate the configuration and lint production and test code.
[group('quality')]
lint: lint-install
    "{{ golangci_lint }}" config verify
    "{{ golangci_lint }}" run ./...

# Format Go source and organize imports.
[group('quality')]
fmt: lint-install
    "{{ golangci_lint }}" fmt ./...

# Run fresh unit tests with the race detector; accepts additional go test flags.
[group('tests')]
[positional-arguments]
unit-test *args:
    go test -mod=readonly -race -count=1 "$@" ./...

# Run benchmarks with allocation reporting; accepts additional go test flags.
[group('tests')]
[positional-arguments]
bench *args:
    go test -mod=readonly -run '^$' -bench . -benchmem "$@" ./...

# Install the pinned linter locally, reusing an existing executable.
[private]
lint-install:
    #!/usr/bin/env sh
    set -eu
    if [ -x "{{ golangci_lint }}" ]; then
        exit 0
    fi
    mkdir -p "{{ golangci_lint_dir }}"
    curl -sSfL "https://golangci-lint.run/install.sh" -o "{{ golangci_lint_dir }}/install.sh"
    sh "{{ golangci_lint_dir }}/install.sh" -b "{{ golangci_lint_dir }}" "{{ golangci_lint_version }}"
