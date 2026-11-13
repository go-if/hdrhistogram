# Repository Guidelines

## Project Structure & Module Organization

This repository contains the `github.com/go-if/hdrhistogram` Go library. Production
code lives at the root: `geometry.go` implements bucket indexing, `histogram.go`
manages mutable state, and `snapshot.go` provides immutable views. Package
documentation is in `doc.go`; usage examples are in `README.md` and
`example_test.go`. Unit, concurrency, fuzz, and benchmark tests use `*_test.go`.
Lint configuration lives in `.golangci.yml`, with CI in `.github/workflows/lint.yml`.

## Build, Test, and Development Commands

Use Go 1.27.4 or newer and Just. Run `just` to list public recipes.

- `go build ./...`: compile the library.
- `just unit-test`: run fresh tests with `-race` and read-only module settings.
- `just unit-test -cover`: report coverage; additional Go test flags are supported.
- `just check`: run lint and unit tests.
- `just fmt`: apply `gofmt` and `goimports`.
- `just bench -benchtime=200ms`: run benchmarks with allocation reporting.
- `just tidy`: tidy module files and verify downloaded dependencies.

Lint and formatting automatically install the pinned linter into ignored `.bin/`;
the initial download requires `curl` and internet access.

## Coding Style & Naming Conventions

Use standard Go formatting with tabs, idiomatic mixedCaps names, and documentation
comments for exported APIs. Keep production dependencies limited to the standard
library. Follow `.golangci.yml`; any `nolint` suppression must name the linter and
explain its necessity. Keep linter versions synchronized between `justfile` and CI.

## Testing Guidelines

Use Go's `testing` package with Testify `assert` and `require`. Prefer named,
table-driven subtests and `t.Parallel()` for independent cases. Keep allocation
tests serial. Name functions `TestXxx`, `FuzzXxx`, `BenchmarkXxx`, or `ExampleXxx`.
No numeric coverage threshold is configured; add regression cases for behavior
changes, especially boundaries, overflow, and concurrency. Successful ordinary
and weighted recording must remain allocation-free. Run targeted fuzzing with
`go test -run '^$' -fuzz '^FuzzBucketMapping$' -fuzztime 10s`.

## Architecture Constraints

Construct histograms through `New`; never copy them by value. Preserve synchronized
operations, immutable snapshots, and unchanged destination state on errors.
Iterator callbacks must execute without holding histogram locks.

## Commit & Pull Request Guidelines

History currently contains only `Init project`, so no commit convention is
established. Use concise imperative subjects. Keep pull requests focused; explain
behavior changes, link relevant issues, and report validation commands and results.
Include benchmark comparisons for performance changes and update affected examples.

## Agent Instructions

Before acting on a user instruction, return a polished version of the prompt,
then proceed with the requested work.
