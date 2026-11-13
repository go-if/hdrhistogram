package hdrhistogram

import (
	"errors"
	"math"
	"slices"
	"sync"
)

var (
	// ErrInvalidArgument reports invalid configuration, a negative count or
	// interval, an invalid percentile, a nil merge source, or an uninitialized
	// histogram or merge snapshot.
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
	mu   sync.Mutex
	data histogramData
}

// New creates a fixed-size histogram with a fixed range and precision.
//
// lowestDiscernible must be at least one and sets the smallest bucket width,
// rounded down to a power of two. For example, 1000 selects width 512, grouping
// values 0..511 together. Values below lowestDiscernible remain valid samples.
// highestTrackable must be at least twice lowestDiscernible; values outside
// [0, highestTrackable] cannot be recorded. Increasing this maximum reserves more
// counters without changing the resolution at already-supported values.
//
// significantFigures must be an int from 0 through 5. More figures give finer
// relative precision at larger values and use more memory. With base width one,
// 2 figures bound relative quantization error below 1%, 3 below 0.1%, and 4 below
// 0.01%. A coarser base width limits resolution near zero regardless of figures.
// Zero figures selects coarse precision, not an automatic default. See the
// package documentation for examples and the full precision range.
//
// Use the same units for bounds, samples, and correction intervals. Storage is
// allocated once according to these settings, independently of sample count;
// each snapshot copies that storage. Invalid settings or unrepresentable geometry
// return ErrInvalidArgument.
func New(lowestDiscernible, highestTrackable int64, significantFigures int) (*Histogram, error) {
	g, err := newGeometry(lowestDiscernible, highestTrackable, significantFigures)
	if err != nil {
		return nil, err
	}
	return &Histogram{
		mu: sync.Mutex{},
		data: histogramData{
			geometry: g,
			counts:   make([]int64, g.countsLen),
			total:    0,
			minIndex: g.countsLen,
			maxIndex: -1,
		},
	}, nil
}

// Record records one occurrence of value. Successful calls allocate no memory.
// Errors leave the histogram unchanged.
func (h *Histogram) Record(value int64) error {
	return h.RecordN(value, 1)
}

// RecordN records count occurrences of value in constant time without
// allocation. A zero count is a no-op after validation; a negative count is invalid.
// Errors leave the histogram unchanged.
func (h *Histogram) RecordN(value, count int64) error {
	if count < 0 {
		return ErrInvalidArgument
	}
	if err := h.data.validateValue(value); err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	index := h.data.geometry.index(value)
	h.mu.Lock()
	defer h.mu.Unlock()
	if count > math.MaxInt64-h.data.total {
		return ErrCountOverflow
	}
	h.data.add(index, count)
	return nil
}

// RecordCorrected records value and compensates for coordinated omission by
// recording value-interval, value-2*interval, and so on, while
// the generated value is at least interval. Zero disables correction;
// negative intervals are invalid. The entire operation is atomic and errors
// leave the histogram unchanged. Work is proportional to the number of occupied
// buckets in the generated sequence, not to the number of generated samples.
func (h *Histogram) RecordCorrected(value, interval int64) error {
	if interval < 0 {
		return ErrInvalidArgument
	}
	if err := h.data.validateValue(value); err != nil {
		return err
	}
	remaining := int64(1)
	if interval > 0 {
		remaining = max(1, value/interval)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if remaining > math.MaxInt64-h.data.total {
		return ErrCountOverflow
	}
	for {
		index := h.data.geometry.index(value)
		count := int64(1)
		if interval > 0 {
			from, _ := h.data.geometry.bounds(index)
			count = min(remaining, (value-from)/interval+1)
		}
		h.data.add(index, count)
		remaining -= count
		if remaining == 0 {
			return nil
		}
		// A remaining sample guarantees this product is <= value.
		value -= count * interval
	}
}

// Reset removes all samples while retaining configuration and allocated storage.
func (h *Histogram) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.data.total == 0 {
		return
	}
	clear(h.data.counts[h.data.minIndex : h.data.maxIndex+1])
	h.data.total = 0
	h.data.minIndex = len(h.data.counts)
	h.data.maxIndex = -1
}

// Merge adds the captured samples in source to h without modifying source or
// allocating memory. All three constructor settings must match exactly, even
// for an empty source. A nil or uninitialized source is invalid. Errors leave h
// unchanged. Repeated merges add the captured samples repeatedly.
func (h *Histogram) Merge(source *Snapshot) error {
	if source == nil {
		return ErrInvalidArgument
	}
	// Geometry is immutable, so checking it needs no lock.
	if h.data.geometry.countsLen == 0 || source.data.geometry.countsLen == 0 {
		return ErrInvalidArgument
	}
	if !h.data.geometry.compatible(source.data.geometry) {
		return ErrIncompatibleConfiguration
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.data.merge(&source.data)
}

// Snapshot returns an immutable, independent copy captured under the mutex.
// It copies the entire counts array, requiring O(configured bucket count) time
// and memory. Use it to make multiple queries against the same set of samples.
func (h *Histogram) Snapshot() *Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	data := h.data
	data.counts = slices.Clone(data.counts)
	return &Snapshot{data: data}
}
