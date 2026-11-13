package hdrhistogram

import (
	"cmp"
	"iter"
	"math"
	"slices"
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
// This bound can exceed highestTrackable because of quantization.
func (s *Snapshot) Max() int64 { return s.data.max() }

// Mean estimates the arithmetic mean using integer bucket midpoints rounded up,
// or returns zero when empty.
func (s *Snapshot) Mean() float64 { return s.data.mean() }

// StdDev estimates the population standard deviation using bucket midpoints,
// or returns zero when empty.
func (s *Snapshot) StdDev() float64 { return s.data.stdDev() }

// Percentile returns an HDR percentile estimate. p must be finite and within
// [0, 100]. Zero returns Min; positive percentiles return an occupied bucket's
// upper bound, with 100 returning Max. Empty snapshots return zero. Invalid
// percentiles return ErrInvalidArgument even when the snapshot is empty.
func (s *Snapshot) Percentile(p float64) (int64, error) {
	if !validPercentile(p) {
		return 0, ErrInvalidArgument
	}
	return s.data.percentile(p), nil
}

// Percentiles returns estimates with the same semantics as Percentile, in the
// order requested, including duplicates. It does not modify or retain ps. It
// sorts requests internally and scans the counts once. An empty request returns
// nil, nil; any invalid percentile returns nil, ErrInvalidArgument.
func (s *Snapshot) Percentiles(ps []float64) ([]int64, error) {
	for _, p := range ps {
		if !validPercentile(p) {
			return nil, ErrInvalidArgument
		}
	}
	if len(ps) == 0 {
		return nil, nil
	}
	values := make([]int64, len(ps))
	if s.data.total == 0 {
		return values, nil
	}
	order := make([]int, len(ps))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int { return cmp.Compare(ps[a], ps[b]) })
	index := s.data.minIndex
	var cumulative int64
	for _, request := range order {
		p := ps[request]
		if p == 0 {
			values[request] = s.Min()
			continue
		}
		if p == 100 {
			values[request] = s.Max()
			continue
		}
		rank := percentileRank(p, s.data.total)
		for cumulative < rank {
			cumulative += s.data.counts[index]
			index++
		}
		_, values[request] = s.data.geometry.bounds(index - 1)
	}
	return values, nil
}

func validPercentile(p float64) bool {
	return !math.IsNaN(p) && p >= 0 && p <= 100
}

// Buckets returns a reusable iterator over occupied buckets in ascending
// order. Iteration does not copy the counts array and never holds a lock.
func (s *Snapshot) Buckets() iter.Seq[Bucket] {
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
