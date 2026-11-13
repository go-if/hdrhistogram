package hdrhistogram

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func snapshotProblem(s *Snapshot) error {
	var total, first, last int64
	for bucket := range s.Buckets() {
		if bucket.Count <= 0 || bucket.From < 0 || bucket.To < bucket.From || (total > 0 && bucket.From <= last) {
			return fmt.Errorf("invalid or unordered bucket: %v", bucket)
		}
		if total == 0 {
			first = bucket.From
		}
		last = bucket.To
		if bucket.Count > math.MaxInt64-total {
			return errors.New("bucket counts overflow")
		}
		total += bucket.Count
	}
	if total != s.TotalCount() || first != s.Min() || last != s.Max() {
		return fmt.Errorf("inconsistent snapshot: sum/range=%d/%d..%d, queries=%d/%d..%d", total, first, last, s.TotalCount(), s.Min(), s.Max())
	}
	ps := []float64{100, 50, 0, 50}
	values, err := s.Percentiles(ps)
	if err != nil {
		return fmt.Errorf("query percentiles: %w", err)
	}
	for i, p := range ps {
		v, err := s.Percentile(p)
		if err != nil {
			return fmt.Errorf("query p%g: %w", p, err)
		}
		if v != values[i] || v < s.Min() || v > s.Max() {
			return fmt.Errorf("invalid percentile: p%g = %d outside [%d,%d]", p, v, s.Min(), s.Max())
		}
	}
	if math.IsNaN(s.Mean()) || math.IsNaN(s.StdDev()) || s.StdDev() < 0 {
		return errors.New("invalid statistics")
	}
	return nil
}

func waitWorkers(t *testing.T, wg *sync.WaitGroup) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		require.FailNow(t, "concurrent operations did not finish (possible deadlock)")
	}
}

func TestConcurrentRecordingAndSnapshots(t *testing.T) {
	t.Parallel()

	h := newTestHistogram(t, 1, 10_000, 3)
	const workers, repetitions = 8, 1000
	var wg sync.WaitGroup
	for worker := range workers {
		wg.Go(func() {
			for range repetitions {
				if !assert.NoError(t, h.RecordN(int64(worker), 2)) {
					return
				}
			}
		})
	}
	wg.Go(func() {
		for range 100 {
			if !assert.NoError(t, snapshotProblem(h.Snapshot())) {
				return
			}
		}
	})
	waitWorkers(t, &wg)
	assert.Equal(t, int64(workers*repetitions*2), h.Snapshot().TotalCount(), "lost samples")
	for bucket := range h.Snapshot().Buckets() {
		assert.Equal(t, int64(repetitions*2), bucket.Count, "per-worker count: %v", bucket)
	}
}

func TestConcurrentMixedOperations(t *testing.T) {
	t.Parallel()

	h := newTestHistogram(t, 1, 10_000, 3)
	donor := newTestHistogram(t, 1, 10_000, 3)
	record(t, donor, 4000, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	operations := []struct {
		name string
		run  func(int) error
	}{
		{"record", func(i int) error { return h.Record(int64(i)) }},
		{"correct", func(int) error { return h.RecordCorrected(4097, 100) }},
		{"reset", func(int) error { h.Reset(); return nil }},
		{"merge", func(int) error { return h.Merge(donor.Snapshot()) }},
		{"query", func(int) error { return snapshotProblem(h.Snapshot()) }},
		{"snapshot and iterate", func(int) error {
			if err := snapshotProblem(h.Snapshot()); err != nil {
				return err
			}
			for range h.Snapshot().Buckets() {
				// Iterator bodies may mutate the source histogram.
				h.Reset()
				break
			}
			return nil
		}},
	}
	for _, operation := range operations {
		wg.Go(func() {
			<-start
			for i := range 100 {
				if !assert.NoError(t, operation.run(i), operation.name) {
					return
				}
			}
		})
	}
	close(start)
	waitWorkers(t, &wg)
	require.NoError(t, snapshotProblem(h.Snapshot()))
}

func TestConcurrentMerges(t *testing.T) {
	t.Parallel()

	a := newTestHistogram(t, 1, 100, 3)
	b := newTestHistogram(t, 1, 100, 3)
	record(t, a, 10, 1)
	record(t, b, 20, 1)
	var wg sync.WaitGroup
	for _, tc := range []struct {
		name                string
		destination, source *Histogram
	}{{"forward", a, b}, {"reverse", b, a}, {"self", a, a}} {
		wg.Go(func() {
			for range 12 {
				if !assert.NoError(t, tc.destination.Merge(tc.source.Snapshot()), tc.name) {
					return
				}
			}
		})
	}
	waitWorkers(t, &wg)
	for _, h := range []*Histogram{a, b} {
		require.NoError(t, snapshotProblem(h.Snapshot()))
	}
}

func TestConcurrentSnapshotReaders(t *testing.T) {
	t.Parallel()

	h := newTestHistogram(t, 1, 10_000, 3)
	record(t, h, 50, 10)
	record(t, h, 5000, 20)
	s := h.Snapshot()
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 100 {
				if !assert.NoError(t, snapshotProblem(s)) {
					return
				}
			}
		})
	}
	wg.Go(func() {
		for range 100 {
			h.Reset()
			if !assert.NoError(t, h.Record(1)) {
				return
			}
		}
	})
	waitWorkers(t, &wg)
	assert.Equal(t, int64(30), s.TotalCount(), "snapshot changed with source")
}

func TestConcurrentSharedSnapshotMerges(t *testing.T) {
	t.Parallel()

	h := newTestHistogram(t, 1, 10_000, 3)
	donor := newTestHistogram(t, 1, 10_000, 3)
	record(t, donor, 1, 2)
	record(t, donor, 9000, 3)
	source := donor.Snapshot()
	const workers, repetitions = 4, 100
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for range repetitions {
				if !assert.NoError(t, h.Merge(source)) {
					return
				}
			}
		})
	}
	wg.Go(func() {
		for range repetitions {
			s := h.Snapshot()
			if err := snapshotProblem(s); err != nil {
				t.Error(err)
				return
			}
			if !assert.Zero(t, s.TotalCount()%5, "partially visible merge") {
				return
			}
			for bucket := range s.Buckets() {
				want := int64(2)
				if bucket.From != 1 {
					want = 3
				}
				if !assert.Equal(t, s.TotalCount()/5*want, bucket.Count) {
					return
				}
			}
		}
	})
	waitWorkers(t, &wg)
	assert.Equal(t, int64(workers*repetitions*5), h.Snapshot().TotalCount())
	assert.Equal(t, donor.Snapshot(), source)
}
