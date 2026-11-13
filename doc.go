// Package hdrhistogram implements a thread-safe High Dynamic Range Histogram
// for nonnegative integer measurements such as latency in microseconds.
//
// A histogram allocates a fixed counts array according to its configured range
// and precision. Recording individual or weighted samples takes constant time
// and allocates no memory. Values are quantized into exponentially widening
// equivalence ranges; statistics and percentiles are estimates from those ranges.
//
// # Settings and precision
//
// New(lowestDiscernible, highestTrackable, significantFigures) uses int64 bounds
// and an int precision from 0 through 5. Zero figures selects coarse precision;
// it is not a default. Invalid settings return ErrInvalidArgument. Values,
// bounds, and correction intervals must all use the same measurement units.
//
// lowestDiscernible must be at least one. It sets the base bucket width, rounded
// down to a power of two, rather than a minimum accepted sample. For example,
// 1000 selects width 512: values 0..511 share one bucket and 512..1023 share the
// next. Increasing significantFigures cannot make buckets narrower than this
// base width. With lowestDiscernible=1, small integers have individual buckets.
//
// Significant figures control relative precision as bucket widths grow with
// larger values. With base width one, the relative difference between a positive
// sample and either bucket bound is below a bound that shrinks tenfold with each
// extra figure: 1 figure gives a bound of 10%, 2 gives 1%, 3 gives 0.1%, 4 gives
// 0.01%, and 5 gives 0.001%; 0 gives 100%. A coarser base width dominates near
// zero. These are quantization bounds, not guarantees about sampling error or
// decimal rounding. At base width one and three figures, 0..2047 are exact,
// 2048..2049 share a bucket, and 1_000_000 falls in 999936..1000447.
//
// highestTrackable must be at least twice lowestDiscernible. Values from zero
// through this maximum are accepted. Raising the maximum extends the allocated
// range without changing existing bucket widths. Bucket upper bounds can exceed
// the maximum: New(1, 2050, 3) groups 2050 in 2050..2051, but rejects recording
// 2051. Unrepresentable bucket geometry is also rejected during construction.
//
// Higher precision and a wider range require more memory. New(1, 3_600_000_000, 3)
// uses 184 KiB of counters; at 2 figures it uses 26 KiB, and at 5 it uses 16 MiB.
// These sizes exclude object headers and allocator rounding. Each snapshot copies
// the full array. More counters increase copying and potential scan costs;
// successful ordinary and weighted recording remain allocation-free.
//
// # Recording and snapshots
//
// All Histogram methods synchronize internally. Recording and snapshot capture
// use a mutex; analytics are available only on immutable snapshots. Capture
// copies the counts array while holding the mutex. Queries and iteration on a
// snapshot never acquire histogram locks or observe later mutations.
//
// Merge adds the samples from a compatible snapshot without copying its storage
// again. Reusing a snapshot adds the same samples on every merge. Per-worker
// histograms can reduce recording contention and contribute snapshots to a
// shared aggregate. Errors leave the destination histogram unchanged.
//
// RecordCorrected compensates for coordinated omission when the caller
// knows the expected sampling interval. Use consistent units for values and the
// interval, and apply correction only once to any measurement stream.
//
// Histograms must be constructed with New and must not be copied by value.
// The package does not resize histograms, serialize them, or attach physical
// units to samples. It uses the bucket model developed by Gil Tene:
// https://github.com/HdrHistogram/HdrHistogram.
package hdrhistogram
