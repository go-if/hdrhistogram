// Package hdrhistogram implements a thread-safe High Dynamic Range Histogram
// for nonnegative integer measurements such as latency in microseconds.
//
// A histogram allocates a fixed counts array according to its configured range
// and precision. Recording individual or weighted samples takes constant time
// and allocates no memory. Values are quantized into exponentially widening
// equivalence ranges; statistics and percentiles are estimates from those ranges.
// Significant figures control relative precision above the lowest discernible
// resolution, which is rounded down to a power of two. Bucket upper bounds can
// exceed the configured maximum, although recording above that maximum fails.
//
// All Histogram methods synchronize internally. A single query is consistent,
// but separate queries may observe different samples. Use Histogram.Snapshot
// for multiple queries on an immutable view. Snapshot creation and merges copy
// a counts array; histogram iteration also captures a snapshot immediately.
// Long queries hold a read lock and can delay recording. Per-worker histograms
// with identical settings can be merged to reduce recording contention.
//
// RecordCorrectedValue compensates for coordinated omission when the caller
// knows the expected sampling interval. Use consistent units for values and the
// interval, and apply correction only once to any measurement stream.
//
// Histograms must be constructed with New and must not be copied by value.
// The package does not resize histograms, serialize them, or attach physical
// units to samples. It uses the bucket model developed by Gil Tene:
// https://github.com/HdrHistogram/HdrHistogram.
package hdrhistogram
