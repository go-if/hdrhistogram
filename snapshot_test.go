package hdrhistogram

import (
	"fmt"
	"math"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPercentilesOrderingAndScanBoundaries(t *testing.T) {
	t.Parallel()

	for _, length := range []int{1, 7, 8, 9, 15, 16, 17, 23, 24, 25, 64} {
		for _, step := range []int64{1, 17} {
			t.Run(fmt.Sprintf("length%d/step%d", length, step), func(t *testing.T) {
				t.Parallel()

				h := newTestHistogram(t, 1, 10_000, 3)
				var samples []int64
				for i := range length {
					value, count := 5+int64(i)*step, int64(i%5+1)
					record(t, h, value, count)
					for range count {
						samples = append(samples, value)
					}
				}
				ps := []float64{100, 0, 50, 50, math.SmallestNonzeroFloat64}
				want := []int64{samples[len(samples)-1], samples[0], samples[(len(samples)-1)/2], samples[(len(samples)-1)/2], samples[0]}
				for i := range 101 {
					p := (i * 37) % 101
					ps = append(ps, float64(p))
					// Integer ranks over expanded observations are independent of
					// the production rank and counter scanning implementations.
					rank := max(1, (p*len(samples)+99)/100)
					want = append(want, samples[rank-1])
				}
				before := slices.Clone(ps)
				s := h.Snapshot()
				got, err := s.Percentiles(ps)
				require.NoError(t, err)
				require.Equal(t, want, got)
				assert.Equal(t, before, ps, "requests changed")
				for i, p := range ps {
					value, err := s.Percentile(p)
					require.NoError(t, err)
					require.Equal(t, want[i], value, "p%g", p)
				}
			})
		}
	}
}

func TestPercentilesEmpty(t *testing.T) {
	t.Parallel()

	h := newTestHistogram(t, 1, 100, 3)
	for _, tc := range []struct {
		name string
		view *Snapshot
	}{
		{"configured", h.Snapshot()},
		{"zero", new(Snapshot)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for _, ps := range [][]float64{nil, {}} {
				values, err := tc.view.Percentiles(ps)
				require.NoError(t, err)
				require.Nil(t, values)
			}
			values, err := tc.view.Percentiles([]float64{100, 0, 50, 50})
			require.NoError(t, err)
			assert.Equal(t, []int64{0, 0, 0, 0}, values)
			values, err = tc.view.Percentiles([]float64{0, math.NaN()})
			require.ErrorIs(t, err, ErrInvalidArgument)
			require.Nil(t, values)
		})
	}
}

func TestMergeCapturedSamples(t *testing.T) {
	t.Parallel()

	h := newTestHistogram(t, 1, 10_000, 3)
	record(t, h, 5, 2)
	record(t, h, 4096, 3)
	source := h.Snapshot()
	before := slices.Clone(source.data.counts)
	// A captured source adds only its samples, even when its origin changes.
	record(t, h, 100, 7)
	for range 2 {
		require.NoError(t, h.Merge(source))
	}
	assert.Equal(t, []Bucket{{5, 5, 6}, {100, 100, 7}, {4096, 4099, 9}}, slices.Collect(h.Snapshot().Buckets()))
	h.Reset()
	require.NoError(t, h.Merge(source))
	assert.Equal(t, source, h.Snapshot(), "merge after reset must copy exactly the captured samples")
	record(t, h, 9000, 1)
	h.Reset()
	record(t, h, 1, 1)
	assert.Equal(t, []Bucket{{1, 1, 1}}, slices.Collect(h.Snapshot().Buckets()))
	assert.Equal(t, before, source.data.counts, "source storage was modified or aliased")
	assert.Equal(t, int64(5), source.TotalCount())
}

//nolint:paralleltest // AllocsPerRun measures process-wide allocations and temporarily changes GOMAXPROCS.
func TestMergeAllocations(t *testing.T) {
	h := newTestHistogram(t, 1, 10_000, 3)
	record(t, h, 100, 2)
	source := h.Snapshot()
	for _, reset := range []bool{false, true} {
		allocations := testing.AllocsPerRun(1000, func() {
			if reset {
				h.Reset()
			}
			if err := h.Merge(source); err != nil {
				panic(err)
			}
		})
		assert.Zero(t, allocations, "merging a snapshot must not allocate")
	}
}
