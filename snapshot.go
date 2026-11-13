package hdrhistogram

import (
	"iter"
	"math"
)

// Bucket describes an occupied equivalence range, inclusive at both ends.
// Count is the number of samples quantized into that range.
type Bucket struct {
	From  int64
	To    int64
	Count int64
}

// Snapshot is an immutable histogram view, safe for concurrent queries and
// iteration. Obtain one with Histogram.Snapshot. Its zero value is an empty view.
type Snapshot struct {
	data histogramData
}

// TotalCount returns the number of samples captured in the snapshot.
func (s *Snapshot) TotalCount() int64 { return s.data.total }

// Min returns the first occupied bucket's lower bound, or zero when empty.
func (s *Snapshot) Min() int64 { return s.data.min() }

// Max returns the last occupied bucket's upper bound, or zero when empty.
func (s *Snapshot) Max() int64 { return s.data.max() }

// Mean estimates the arithmetic mean using integer bucket midpoints rounded up,
// or returns zero when empty.
func (s *Snapshot) Mean() float64 { return s.data.mean() }

// StdDev estimates the population standard deviation using bucket midpoints,
// or returns zero when empty.
func (s *Snapshot) StdDev() float64 { return s.data.stdDev() }

// ValueAtPercentile has the same percentile semantics as Histogram.ValueAtPercentile.
func (s *Snapshot) ValueAtPercentile(percentile float64) (int64, error) {
	return s.data.valueAtPercentile(percentile)
}

// RecordedValues returns a reusable iterator over occupied buckets in ascending
// order. Iteration does not copy the counts array and never holds a lock.
func (s *Snapshot) RecordedValues() iter.Seq[Bucket] {
	return func(yield func(Bucket) bool) {
		if s.data.total == 0 {
			return
		}
		for i := s.data.minIndex; i <= s.data.maxIndex; i++ {
			if count := s.data.counts[i]; count != 0 {
				from, to := s.data.geometry.bounds(i)
				if !yield(Bucket{From: from, To: to, Count: count}) {
					return
				}
			}
		}
	}
}

// histogramData has no synchronization of its own. A Histogram guards it with a
// mutex; a Snapshot owns a detached copy that is never mutated.
type histogramData struct {
	geometry geometry
	counts   []int64
	total    int64
	minIndex int
	maxIndex int
}

func (d *histogramData) validateValue(value int64) error {
	if d.geometry.countsLen == 0 {
		return ErrInvalidArgument
	}
	if value < 0 || value > d.geometry.highest {
		return ErrOutOfRange
	}
	return nil
}

// add requires a positive count and a checked total. Since counts are nonnegative
// and sum to total, checking total also prevents overflow in every individual bin.
func (d *histogramData) add(index int, count int64) {
	d.counts[index] += count
	d.total += count
	d.minIndex = min(d.minIndex, index)
	d.maxIndex = max(d.maxIndex, index)
}

// merge also supports source == d: each bin is read once, and total is changed last.
func (d *histogramData) merge(source *histogramData) error {
	if source.total > math.MaxInt64-d.total {
		return ErrCountOverflow
	}
	if source.total == 0 {
		return nil
	}
	for i := source.minIndex; i <= source.maxIndex; i++ {
		d.counts[i] += source.counts[i]
	}
	d.minIndex = min(d.minIndex, source.minIndex)
	d.maxIndex = max(d.maxIndex, source.maxIndex)
	d.total += source.total
	return nil
}

func (d *histogramData) min() int64 {
	if d.total == 0 {
		return 0
	}
	from, _ := d.geometry.bounds(d.minIndex)
	return from
}

func (d *histogramData) max() int64 {
	if d.total == 0 {
		return 0
	}
	_, to := d.geometry.bounds(d.maxIndex)
	return to
}

func (d *histogramData) mean() float64 {
	if d.total == 0 {
		return 0
	}
	var mean float64
	for i := d.minIndex; i <= d.maxIndex; i++ {
		if count := d.counts[i]; count != 0 {
			mean += float64(d.geometry.midpoint(i)) * (float64(count) / float64(d.total))
		}
	}
	return mean
}

func (d *histogramData) stdDev() float64 {
	if d.total == 0 {
		return 0
	}
	mean := d.mean()
	var variance float64
	for i := d.minIndex; i <= d.maxIndex; i++ {
		if count := d.counts[i]; count != 0 {
			deviation := float64(d.geometry.midpoint(i)) - mean
			variance += deviation * deviation * (float64(count) / float64(d.total))
		}
	}
	return math.Sqrt(variance)
}

func (d *histogramData) valueAtPercentile(percentile float64) (int64, error) {
	if math.IsNaN(percentile) || percentile < 0 || percentile > 100 {
		return 0, ErrInvalidArgument
	}
	if d.total == 0 {
		return 0, nil
	}
	if percentile == 0 {
		return d.min(), nil
	}
	if percentile == 100 {
		return d.max(), nil
	}
	// HDR removes one ULP before rounding the rank up, avoiding a spurious
	// jump to the next bucket at floating-point percentile boundaries.
	p := math.Nextafter(percentile, math.Inf(-1))
	floatRank := math.Ceil(p * float64(d.total) / 100)
	rank := d.total
	// float64(MaxInt64) rounds to 1<<63; clamp before converting to int64.
	if floatRank < float64(d.total) {
		rank = max(1, int64(floatRank))
	}
	var cumulative int64
	for i := d.minIndex; i <= d.maxIndex; i++ {
		cumulative += d.counts[i]
		if cumulative >= rank {
			_, to := d.geometry.bounds(i)
			return to, nil
		}
	}
	return d.max(), nil
}
