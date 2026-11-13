package hdrhistogram

import (
	"errors"
	"iter"
	"math"
	"slices"
	"sync"
)

var (
	// ErrInvalidArgument reports invalid configuration, a negative count or
	// interval, an invalid percentile, a nil merge source, or an uninitialized histogram.
	ErrInvalidArgument = errors.New("hdrhistogram: invalid argument")
	// ErrOutOfRange reports a value outside [0, highestTrackable].
	ErrOutOfRange = errors.New("hdrhistogram: value out of range")
	// ErrCountOverflow reports an operation that would exceed MaxInt64 samples.
	ErrCountOverflow = errors.New("hdrhistogram: count overflow")
	// ErrIncompatibleConfiguration reports a merge with different constructor settings.
	ErrIncompatibleConfiguration = errors.New("hdrhistogram: incompatible configuration")
)

// Histogram records nonnegative integer samples with configurable precision.
// All methods are safe for concurrent use. Construct it with New; the zero value
// is not ready for recording. A Histogram must not be copied by value.
type Histogram struct {
	mu   sync.RWMutex
	data histogramData
}

// New creates a fixed-size histogram. lowestDiscernible controls resolution and
// is rounded down to a power of two internally; values below it, including zero,
// remain valid samples. highestTrackable must be at least twice lowestDiscernible.
// significantFigures must be between 0 and 5, inclusive. Unrepresentable geometry
// returns ErrInvalidArgument. Storage is allocated once, according to the range
// and precision, independently of the number of samples recorded.
func New(lowestDiscernible, highestTrackable int64, significantFigures int) (*Histogram, error) {
	g, err := newGeometry(lowestDiscernible, highestTrackable, significantFigures)
	if err != nil {
		return nil, err
	}
	return &Histogram{
		mu: sync.RWMutex{},
		data: histogramData{
			geometry: g,
			counts:   make([]int64, g.countsLen),
			total:    0,
			minIndex: g.countsLen,
			maxIndex: -1,
		},
	}, nil
}

// RecordValue records one occurrence of value. Successful calls allocate no memory.
func (h *Histogram) RecordValue(value int64) error {
	return h.RecordValues(value, 1)
}

// RecordValues records count occurrences of value in constant time without
// allocation. A zero count is a no-op after validation; a negative count is invalid.
// Errors leave the histogram unchanged.
func (h *Histogram) RecordValues(value, count int64) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if count < 0 {
		return ErrInvalidArgument
	}
	if err := h.data.validateValue(value); err != nil {
		return err
	}
	if count > math.MaxInt64-h.data.total {
		return ErrCountOverflow
	}
	if count != 0 {
		h.data.add(h.data.geometry.index(value), count)
	}
	return nil
}

// RecordCorrectedValue records value and compensates for coordinated omission by
// recording value-expectedInterval, value-2*expectedInterval, and so on, while
// the generated value is at least expectedInterval. Zero disables correction;
// negative intervals are invalid. The entire operation is atomic and errors
// leave the histogram unchanged. Work is proportional to the number of occupied
// buckets in the generated sequence, not to the number of generated samples.
func (h *Histogram) RecordCorrectedValue(value, expectedInterval int64) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if expectedInterval < 0 {
		return ErrInvalidArgument
	}
	if err := h.data.validateValue(value); err != nil {
		return err
	}
	remaining := int64(1)
	if expectedInterval > 0 {
		remaining = max(1, value/expectedInterval)
	}
	if remaining > math.MaxInt64-h.data.total {
		return ErrCountOverflow
	}
	for {
		index := h.data.geometry.index(value)
		count := int64(1)
		if expectedInterval > 0 {
			from, _ := h.data.geometry.bounds(index)
			count = min(remaining, (value-from)/expectedInterval+1)
		}
		h.data.add(index, count)
		remaining -= count
		if remaining == 0 {
			return nil
		}
		// A remaining sample guarantees this product is <= value.
		value -= count * expectedInterval
	}
}

// Reset removes all samples while retaining configuration and allocated storage.
func (h *Histogram) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	clear(h.data.counts)
	h.data.total = 0
	h.data.minIndex = len(h.data.counts)
	h.data.maxIndex = -1
}

// Merge adds a consistent snapshot of other to h. All three constructor settings
// must match exactly. A self-merge atomically doubles the counts. Errors leave h
// unchanged. Merges never hold locks on two histograms at the same time.
func (h *Histogram) Merge(other *Histogram) error {
	if other == nil {
		return ErrInvalidArgument
	}
	// Geometry is immutable, so checking it needs no lock.
	if h.data.geometry.countsLen == 0 || other.data.geometry.countsLen == 0 {
		return ErrInvalidArgument
	}
	if !h.data.geometry.compatible(other.data.geometry) {
		return ErrIncompatibleConfiguration
	}
	if h == other {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.data.merge(&h.data)
	}
	source := other.Snapshot()
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.data.merge(&source.data)
}

// Snapshot returns an immutable, independent copy captured under a read lock.
// It copies the entire counts array, requiring O(configured bucket count) time
// and memory. Use it to make multiple queries against the same set of samples.
func (h *Histogram) Snapshot() *Snapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	data := h.data
	data.counts = slices.Clone(data.counts)
	return &Snapshot{data: data}
}

// RecordedValues returns an iterator over occupied buckets in ascending order.
// It captures a snapshot immediately, before iteration begins. No histogram lock
// is held while yielding to caller code. The iterator can be reused.
func (h *Histogram) RecordedValues() iter.Seq[Bucket] {
	return h.Snapshot().RecordedValues()
}

// TotalCount returns the number of samples, including generated correction samples.
func (h *Histogram) TotalCount() int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.data.total
}

// Min returns the first occupied bucket's lower bound, or zero when empty.
func (h *Histogram) Min() int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.data.min()
}

// Max returns the last occupied bucket's upper bound, or zero when empty.
// This bound can exceed highestTrackable because of quantization.
func (h *Histogram) Max() int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.data.max()
}

// Mean estimates the arithmetic mean using bucket midpoints, or returns zero
// when empty. Midpoints are rounded up to an integer.
func (h *Histogram) Mean() float64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.data.mean()
}

// StdDev estimates the population standard deviation using bucket midpoints,
// or returns zero when empty.
func (h *Histogram) StdDev() float64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.data.stdDev()
}

// ValueAtPercentile returns an HDR percentile estimate. percentile must be finite
// and within [0, 100]. Zero returns Min; positive percentiles return an occupied
// bucket's upper bound, with 100 returning Max. Empty histograms return zero.
// Invalid percentiles return ErrInvalidArgument even when the histogram is empty.
func (h *Histogram) ValueAtPercentile(percentile float64) (int64, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.data.valueAtPercentile(percentile)
}
