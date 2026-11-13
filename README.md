# hdrhistogram

A thread-safe [HDR Histogram](https://github.com/HdrHistogram/HdrHistogram) for
nonnegative integer measurements in Go. It provides fixed-memory recording,
percentiles, estimated statistics, immutable snapshots, coordinated omission
correction, and exact merging of identically configured histograms.

Requires Go 1.27.4 or newer. The library uses only the standard library; tests use
[Testify](https://github.com/stretchr/testify) for assertions.

## Record latency

```go
package main

import (
    "fmt"
    "time"

    "github.com/go-if/hdrhistogram"
)

func main() {
    // Track microseconds up to one hour with three significant figures.
    h, err := hdrhistogram.New(1, time.Hour.Microseconds(), 3)
    if err != nil {
        panic(err)
    }
    for _, latency := range []time.Duration{
        100 * time.Microsecond,
        200 * time.Microsecond,
        300 * time.Microsecond,
    } {
        if err := h.RecordValue(latency.Microseconds()); err != nil {
            panic(err)
        }
    }

    // All these queries describe the same captured samples.
    s := h.Snapshot()
    p99, err := s.ValueAtPercentile(99)
    if err != nil {
        panic(err)
    }
    fmt.Printf("count=%d mean=%.0fµs p99=%dµs\n", s.TotalCount(), s.Mean(), p99)
    // count=3 mean=200µs p99=300µs
}
```

Construct histograms with `New`; their zero value is not ready for recording.
Do not copy a `Histogram` by value. Use `Snapshot` for an independent read-only
view, and `Reset` to empty a histogram while retaining its allocated storage.

## Configuration and precision

`New(lowestDiscernible, highestTrackable, significantFigures)` requires:

- `lowestDiscernible >= 1` and `highestTrackable >= 2 * lowestDiscernible`.
- Between zero and five significant figures, inclusive.
- Representable bucket geometry; configurations whose initial resolution would
  exceed the signed integer geometry are rejected before allocation.

Values in `[0, highestTrackable]` are accepted, including values below
`lowestDiscernible`. The lowest discernible value controls the base resolution,
rounded down to a power of two. For example, a setting of `1000` uses resolution
`512` near zero. Choose consistent units for bounds, samples, and correction
intervals; the library does not perform unit conversions.

The HDR layout uses exponentially widening ranges with linear sub-buckets. At
three significant figures and base resolution one, values through `2047` have
unit resolution, `2048` and `2049` share a bucket, and `4096` through `4099` share
a bucket. The storage needed depends on configuration, not the sample count;
`New(1, 3_600_000_000, 3)` allocates about 184 KiB for its counts array.

`Min` returns the lowest occupied bucket's lower bound; `Max` returns the highest
occupied bucket's upper bound. `Mean` and population `StdDev` use integer bucket
midpoints rounded up. These are estimates of the original observations. Reported
upper bounds can exceed `highestTrackable`: recording `2050` in a three-digit
histogram can report a maximum of `2051`, even though recording a value above the
configured limit is rejected.

`ValueAtPercentile` accepts finite percentiles from zero to 100. It uses HDR's
nearest-rank calculation with a one-ULP adjustment to avoid floating-point jumps
at rank boundaries. Percentile zero returns `Min`; positive percentiles return
the selected bucket's upper bound; percentile 100 returns `Max`. Empty histograms
return zero for all valid queries. Invalid percentiles still return an error on
an empty histogram. `TotalCount` distinguishes empty histograms from recorded zeros.

## Concurrent recording and snapshots

Every `Histogram` method is safe for concurrent use:

```go
h, err := hdrhistogram.New(1, 1_000_000, 3)
if err != nil {
    panic(err)
}
var workers sync.WaitGroup
for value := range int64(4) {
    workers.Go(func() {
        for range 100 {
            if err := h.RecordValue(value); err != nil {
                panic(err)
            }
        }
    })
}
workers.Wait()
fmt.Println(h.TotalCount()) // 400
```

This snippet uses `fmt` and `sync` from the standard library. Complete runnable
examples are included in `example_test.go`.

The implementation uses `sync.RWMutex`. Each operation observes consistent state,
but successive queries may observe different samples. A snapshot copies the full
counts array under a read lock and exposes the same query methods. It is immutable
and safe for concurrent reads. Long queries and snapshot copies can delay writers;
for heavily contended recording, use a histogram per worker and merge later.

`RecordedValues` yields `Bucket{From, To, Count}` values for occupied buckets in
ascending order, with inclusive bounds. On a histogram, it captures a snapshot
when the method is called, before iteration starts. Caller code runs without a
histogram lock and may record or reset the source during iteration. Iterators are
reusable; snapshot iteration does not copy its counts array again.

## Weighted recording, correction, and merging

`RecordValues(value, count)` records multiple occurrences in constant time. Zero
counts are no-ops after argument validation; negative counts are invalid.

`RecordCorrectedValue(value, expectedInterval)` accounts for missed observations
when sampling pauses during slow operations. For example, recording `100` with an
expected interval of `20` records `100, 80, 60, 40, 20`. An interval of zero records
only the original value; negative intervals fail. Use this only when the expected
sampling interval is known, and apply correction once per measurement stream.
The implementation groups generated observations by bucket, so even very large
correction ratios do not require a loop per synthetic observation.

`destination.Merge(source)` requires all three constructor settings to match
exactly, even when different settings happen to produce the same geometry. It
adds a consistent snapshot of the source without changing it. Source capture and
destination update are separate synchronized steps. A self-merge atomically doubles
the current counts. Reciprocal merges cannot deadlock because they never hold two
histogram locks simultaneously.

Every failed mutation leaves the destination unchanged. Counts use `int64`, with
overflow checked before recording, correction, or merging. Use `errors.Is` with:

| Error | Meaning |
| --- | --- |
| `ErrInvalidArgument` | Invalid configuration, negative count/interval, invalid percentile, nil merge source, or uninitialized histogram |
| `ErrOutOfRange` | A sample is negative or exceeds the configured maximum |
| `ErrCountOverflow` | The resulting total would exceed `math.MaxInt64` |
| `ErrIncompatibleConfiguration` | Merge settings differ |

## Costs

Let **B** be the configured counts-array length and **K** the number of occupied
buckets touched by a corrected sequence. Costs below exclude mutex contention.

| Operation | Time | Additional storage |
| --- | --- | --- |
| `RecordValue`, `RecordValues` | O(1) | Zero allocations on success |
| `RecordCorrectedValue` | O(K) | O(1) |
| `TotalCount`, `Min`, `Max` | O(1) | O(1) |
| Percentile, mean, standard deviation | O(B) | O(1) |
| `Reset` | O(B) | Reuses allocated storage |
| `Snapshot`, histogram iteration | O(B) | O(B) |
| Snapshot iteration | O(B) | O(1) |
| `Merge` | O(B) | O(B); O(1) for self-merge |

## Validation

Run `just` or `just --list` to list recipes grouped by purpose:

```sh
just
just unit-test
just check
```

| Group | Recipe | Purpose |
| --- | --- | --- |
| modules | `just tidy` | Tidy module files and verify downloaded dependencies. |
| quality | `just check` | Run lint and tests with the race detector. |
| quality | `just fmt` | Format Go source and organize imports. |
| quality | `just lint` | Validate the linter configuration and lint production and test code. |
| tests | `just unit-test` | Run all unit tests with the race detector and without cached results. |
| tests | `just bench` | Run benchmarks with allocation reporting. |

Test and benchmark recipes use `-mod=readonly` and forward extra Go test flags
without shell interpolation. For example:

```sh
just unit-test -run '^TestRecordingAllocations$' -v
just unit-test -cover
just bench -benchtime=200ms
```

The first `just lint`, `just fmt`, or `just check` run downloads golangci-lint
v2.15.0 into the ignored `.bin/` directory
using the official release installer; it requires [Just](https://just.systems/),
`curl`, and internet access. Subsequent runs reuse the binary.

The configuration checks production and test code for correctness, security,
performance, modern Go idioms, and test quality. It includes `gosec`, `gocritic`,
`modernize`, `revive`, `testifylint`, `paralleltest`, and `exhaustruct_v5`; the full
list is in [`.golangci.yml`](.golangci.yml). Formatting uses `gofmt` and `goimports`.
Independent tests run in parallel; the allocation test stays serial because it
measures process-wide allocations and changes `GOMAXPROCS`. Narrow inline
suppressions document the validated bounds of conversions in the recording path.

The GitHub Actions lint workflow uses the same pinned version and the Go version
from `go.mod` on pushes and pull requests. Update the version in both `justfile`
and the workflow when upgrading.

```sh
go vet ./...
go test -run '^$' -fuzz '^FuzzBucketMapping$' -fuzztime 10s
go test -run '^$' -fuzz '^FuzzPercentilesAndCounts$' -fuzztime 10s
go test -run '^$' -fuzz '^FuzzCorrectedRecording$' -fuzztime 10s
```

Tests cover independent bucket/rank oracles, reference-layout fixtures, statistics,
overflow and atomic failure, large correction ratios, snapshots, concurrent
operations, and zero-allocation recording. Unit cases use named, table-driven
subtests with Testify assertions. Assertions run outside measured benchmark loops.
Benchmarks report construction at
different precisions, recording contention, correction, percentiles, statistics,
reset, merge, and snapshot allocation costs. Iteration benchmarks compare capturing
a histogram snapshot with iterating an existing snapshot.

This implementation follows the bucket model developed by Gil Tene in
[HdrHistogram](https://github.com/HdrHistogram/HdrHistogram). It provides a new Go
API. Serialization, histogram logs, automatic resizing, floating-point samples,
and rolling windows are outside this version's scope.
