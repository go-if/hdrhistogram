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
        if err := h.Record(latency.Microseconds()); err != nil {
            panic(err)
        }
    }

    // All these queries describe the same captured samples.
    s := h.Snapshot()
    p99, err := s.Percentile(99)
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

## Configuration, resolution, and precision

`New(lowestDiscernible, highestTrackable int64, significantFigures int)` fixes
three settings for the lifetime of the histogram:

| Setting | Controls | Valid values |
| --- | --- | --- |
| `lowestDiscernible` | The smallest bucket width, in your chosen measurement units | At least `1` |
| `highestTrackable` | The largest accepted sample and the allocated range | At least twice `lowestDiscernible` |
| `significantFigures` | How finely larger values are distinguished | An `int` from `0` through `5` |

Invalid settings return `ErrInvalidArgument`, including combinations whose
initial bucket geometry cannot fit in a signed 64-bit integer. Zero figures is
an explicit coarse precision setting, not an automatic default.

Choose units first. For microsecond latencies, `1_000_000` means one second and
`New(1, 3_600_000_000, 3)` tracks up to one hour. Bounds, recorded values, and
correction intervals must use the same units; the library does not convert them.

### Base resolution: `lowestDiscernible`

**Resolution** is the bucket width: measurements in the same bucket are counted
together and cannot be distinguished afterward. The smallest width is the
largest power of two no greater than `lowestDiscernible`. This setting is not a
minimum accepted sample; zero and positive values below it remain valid.

For example, with three figures and a sufficiently large maximum:

| `lowestDiscernible` | Base bucket width | Bucket containing `0` | Bucket containing `1000` |
| --- | ---: | --- | --- |
| `1` | 1 | `0..0` | `1000..1000` |
| `1000` | 512 | `0..511` | `512..1023` |
| `1024` | 1024 | `0..1023` | `0..1023` |

Choose a power of two when you need an exact base width. A larger base width
sacrifices detail near zero and can reduce storage for a fixed maximum. Adding
significant figures cannot make buckets narrower than this base width.

### Relative precision: `significantFigures`

**Precision** describes the bucket width relative to the measurement's size.
Bucket widths grow in powers of two as values increase. More significant figures
delay that widening and preserve finer distinctions at larger values. This is
binary bucket quantization, not decimal rounding or a count of decimal places.

With `lowestDiscernible=1`, the relative difference between a positive recorded
value and either end of its bucket stays below the bound in the table. Each
extra figure makes that bound ten times smaller. With a coarser base resolution,
the base width dominates near zero; the relative precision bound applies once
values are large enough. For example, a base width of 512 still groups
`512..1023`, even with three figures, so it cannot provide 0.1% resolution near
`1000`. These bounds describe quantization error, not sampling error.

The following settings all use `New(1, 3_600_000_000, figures)`. Ranges are
inclusive; widths and bounds use the same units as recorded values.

| Figures | Relative quantization bound | Bucket containing `1_000_000` | Bucket width | Counts storage |
| ---: | ---: | --- | ---: | ---: |
| 0 | <100% | `524288..1048575` | 524,288 | 264 B |
| 1 | <10% | `983040..1015807` | 32,768 | 3.625 KiB |
| 2 | <1% | `999424..1003519` | 4,096 | 26 KiB |
| 3 | <0.1% | `999936..1000447` | 512 | 184 KiB |
| 4 | <0.01% | `1000000..1000031` | 32 | 2.375 MiB |
| 5 | <0.001% | `1000000..1000003` | 4 | 16 MiB |

These are upper bounds, not the error for every sample; actual error can be much
smaller or zero. For example, with base resolution one and three figures, every
integer through `2047` has its own bucket. Then `2048..2049` share a bucket of
width two, and `4096..4099` share a bucket of width four.

Counts storage is the counter-array length times eight bytes, excluding object
headers and allocator rounding. Storage depends on settings, not sample count.
Each retained snapshot owns another full counter array. Higher precision can
substantially increase memory and snapshot-copy costs, as well as the work for
bucket scans, resets, and merges. Ordinary and weighted recording remain
constant-time and allocation-free after construction.

### Accepted range: `highestTrackable`

Values in `[0, highestTrackable]` are accepted. The histogram does not grow;
recording above the configured limit returns `ErrOutOfRange`. Increasing this
limit reserves counters for larger values without changing the bucket width at
any already-supported value. Storage grows in steps as additional ranges are
needed, rather than in direct proportion to the maximum.

Reported bucket bounds can exceed the configured limit. For example,
`New(1, 2050, 3)` accepts `2050` and reports its bucket as `2050..2051`, but
recording `2051` still fails. The configured limit governs input validation;
query results describe complete quantization buckets.

### Interpreting query results

On a snapshot, `Min` returns the lowest occupied bucket's lower bound and `Max`
returns the highest occupied bucket's upper bound. `Mean` and population `StdDev`
use integer bucket midpoints rounded up. These estimate the original values;
counts remain exact. For example, a single recorded `1_000_000` with base
resolution one and three figures gives `Min=999936`, `Max=1000447`, and
`Mean=1000192`.

`Percentile` accepts finite percentiles from zero to 100. It uses HDR's
ceiling-rank calculation with a one-ULP adjustment to avoid floating-point jumps
at rank boundaries. Percentile zero returns `Min`; positive percentiles return
the selected bucket's upper bound; percentile 100 returns `Max`. Empty snapshots
return zero for all valid queries. Invalid percentiles still return an error on
an empty snapshot. `TotalCount` distinguishes empty snapshots from recorded zeros.

`Percentiles([]float64{99, 50, 99.9, 50})` computes several estimates in one scan.
It preserves request order and duplicates without changing the input slice. An
empty request returns `nil, nil`; any invalid percentile returns `nil` and an error.

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
            if err := h.Record(value); err != nil {
                panic(err)
            }
        }
    })
}
workers.Wait()
fmt.Println(h.Snapshot().TotalCount()) // 400
```

This snippet uses `fmt` and `sync` from the standard library. Complete runnable
examples are included in `example_test.go`.

The implementation uses `sync.Mutex`. Recording, reset, merge, and snapshot
capture each observe consistent state. All analytics live on `Snapshot`, so
multiple queries describe the same captured samples. Capture copies the full
counts array while holding the mutex and can delay writers. Queries and iteration
on an existing snapshot do not acquire histogram locks. Reuse a snapshot for
related queries; capture a new one when fresh measurements are needed.

`Snapshot.Buckets` yields `Bucket{From, To, Count}` values for occupied buckets in
ascending order, with inclusive bounds. Iterators are reusable and do not copy the
counts array. Caller code may record or reset the originating histogram during
iteration without changing the captured samples. A zero `Snapshot` is an empty
view, but it cannot be used as a merge source.

## Weighted recording, correction, and merging

`RecordN(value, count)` records multiple occurrences in constant time. Zero
counts are no-ops after argument validation; negative counts are invalid.

`RecordCorrected(value, interval)` accounts for missed observations
when sampling pauses during slow operations. For example, recording `100` with an
expected interval of `20` records `100, 80, 60, 40, 20`. An interval of zero records
only the original value; negative intervals fail. Use this only when the expected
sampling interval is known, and apply correction once per measurement stream.
The implementation groups generated observations by bucket, so even very large
correction ratios do not require a loop per synthetic observation.

`destination.Merge(source.Snapshot())` adds the source's captured observations.
All three constructor settings must match exactly, even when different settings
produce the same geometry or the source is empty. Capture and destination update
are separate synchronized steps. `Merge` reads the immutable snapshot directly,
allocates no memory, and never modifies the source.

A snapshot can be reused across merges; each call adds its captured observations
again. `h.Merge(h.Snapshot())` adds the captured distribution, preserving any
writes that occur between capture and merge. For recording across many workers,
each worker can own a histogram and merge its snapshot into a shared aggregate.

Every failed mutation leaves the destination unchanged. Counts use `int64`, with
overflow checked before recording, correction, or merging. Use `errors.Is` with:

| Error | Meaning |
| --- | --- |
| `ErrInvalidArgument` | Invalid configuration, negative count/interval, invalid percentile, nil merge source, or uninitialized histogram/snapshot |
| `ErrOutOfRange` | A sample is negative or exceeds the configured maximum |
| `ErrCountOverflow` | The resulting total would exceed `math.MaxInt64` |
| `ErrIncompatibleConfiguration` | Merge settings differ |

## Costs

Let **B** be the configured counts-array length, **S** the span between the first
and last occupied counters, **K** the number of buckets touched by a corrected
sequence, and **P** the number of requested percentiles. Costs exclude mutex
contention.

| Operation | Time | Additional storage |
| --- | --- | --- |
| `Record`, `RecordN` | O(1) | Zero allocations on success |
| `RecordCorrected` | O(K) | Zero allocations on success |
| Snapshot `TotalCount`, `Min`, `Max` | O(1) | O(1) |
| Snapshot `Percentile`, `Mean`, `StdDev`, `Buckets` | O(S) | O(1) |
| Snapshot `Percentiles` | O(P log P + S) | O(P) |
| `Reset` | O(S); O(1) when empty | Reuses allocated storage |
| `Snapshot` | O(B) | O(B) |
| `Merge` | O(source S) | Zero allocations |

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
Independent tests run in parallel; allocation tests stay serial because they
measure process-wide allocations and change `GOMAXPROCS`. Narrow inline
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
empty and populated reset/merge, snapshot allocation costs, snapshot iteration,
and querying several percentiles together or individually.

This implementation follows the bucket model developed by Gil Tene in
[HdrHistogram](https://github.com/HdrHistogram/HdrHistogram). It provides a new Go
API. Serialization, histogram logs, automatic resizing, floating-point samples,
and rolling windows are outside this version's scope.
