package hdrhistogram

import (
	"fmt"
	"iter"
	"math"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestHistogram(t testing.TB, lowest, highest int64, figures int) *Histogram {
	t.Helper()
	h, err := New(lowest, highest, figures)
	require.NoError(t, err)
	return h
}

func record(t testing.TB, h *Histogram, value, count int64) {
	t.Helper()
	require.NoError(t, h.RecordValues(value, count))
}

func unchanged(t *testing.T, h *Histogram, before *Snapshot) {
	t.Helper()
	require.Equal(t, before, h.Snapshot(), "operation changed histogram")
}

// oracleBounds computes ranges using division and doubling rather than the
// production bit-indexing or inverse-index functions.
func oracleBounds(value, lowest int64, figures int) (int64, int64) {
	if value < 0 {
		panic("oracle requires a nonnegative sample")
	}
	unit := int64(1)
	for unit <= lowest/2 {
		unit *= 2
	}
	resolution := uint64(2)
	for range figures {
		resolution *= 10
	}
	subCount := uint64(1)
	for subCount < resolution {
		subCount *= 2
	}
	width := unit
	for limit := subCount * uint64(unit); uint64(value) >= limit; limit *= 2 {
		width *= 2
	}
	from := value / width * width
	return from, from + (width - 1)
}

func TestNew(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name            string
		lowest, highest int64
		figures         int
		wantErr         error
	}
	tests := []testCase{
		{"zero lowest", 0, 100, 3, ErrInvalidArgument},
		{"negative lowest", -1, 100, 3, ErrInvalidArgument},
		{"highest too small", 1, 1, 3, ErrInvalidArgument},
		{"highest below twice lowest", 5, 9, 3, ErrInvalidArgument},
		{"negative highest", 1, -1, 3, ErrInvalidArgument},
		{"negative precision", 1, 100, -1, ErrInvalidArgument},
		{"excess precision", 1, 100, 6, ErrInvalidArgument},
		{"maximum lowest", math.MaxInt64, math.MaxInt64, 0, ErrInvalidArgument},
		{"lowest multiplication overflow", math.MaxInt64/2 + 1, math.MaxInt64, 0, ErrInvalidArgument},
		{"unrepresentable geometry", 1 << 61, 1 << 62, 1, ErrInvalidArgument},
		{"largest supported geometry", 1 << 61, math.MaxInt64, 0, nil},
	}
	for figures := range 6 {
		tests = append(tests, testCase{
			name:   fmt.Sprintf("precision %d", figures),
			lowest: 1, highest: 2, figures: figures,
			wantErr: nil,
		})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h, err := New(tc.lowest, tc.highest, tc.figures)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Nil(t, h)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, h)
			record(t, h, 0, 1)
			record(t, h, tc.highest, 1)
			assert.Equal(t, int64(2), h.TotalCount())
			_, upper := oracleBounds(tc.highest, tc.lowest, tc.figures)
			assert.Equal(t, upper, h.Max())
		})
	}
}

func TestGeometry(t *testing.T) {
	t.Parallel()

	values := []int64{0, 1, 2, 999, 1000, 2047, 2048, 2049, math.MaxInt64}
	for power := 1; power < 63; power++ {
		v := int64(1) << power
		values = append(values, v-1, v, v+1)
	}
	for figures := range 6 {
		for _, lowest := range []int64{1, 3, 1000, 1 << 20} {
			t.Run(fmt.Sprintf("precision%d/lowest%d", figures, lowest), func(t *testing.T) {
				t.Parallel()

				g, err := newGeometry(lowest, math.MaxInt64, figures)
				require.NoError(t, err)
				for _, value := range values {
					index := g.index(value)
					require.GreaterOrEqual(t, index, 0, "value=%d", value)
					require.Less(t, index, g.countsLen, "value=%d", value)
					from, to := g.bounds(index)
					wantFrom, wantTo := oracleBounds(value, lowest, figures)
					assert.Equal(t, wantFrom, from, "value=%d", value)
					assert.Equal(t, wantTo, to, "value=%d", value)
					assert.Equal(t, index, g.index(from), "value=%d", value)
					assert.Equal(t, index, g.index(to), "value=%d", value)
					if index > 0 {
						_, previousTo := g.bounds(index - 1)
						assert.Equal(t, from-1, previousTo, "gap or overlap before index %d", index)
					}
				}
			})
		}
	}
}

type queryView interface {
	TotalCount() int64
	Min() int64
	Max() int64
	Mean() float64
	StdDev() float64
	ValueAtPercentile(float64) (int64, error)
}

type namedQueryView struct {
	name string
	view queryView
}

func queryViews(h *Histogram) []namedQueryView {
	return []namedQueryView{
		{name: "histogram", view: h},
		{name: "snapshot", view: h.Snapshot()},
	}
}

type percentileCase struct {
	percentile float64
	want       int64
}

func assertPercentiles(t *testing.T, view queryView, tests []percentileCase) {
	t.Helper()
	for _, tc := range tests {
		t.Run(fmt.Sprintf("p%g", tc.percentile), func(t *testing.T) {
			t.Parallel()

			got, err := view.ValueAtPercentile(tc.percentile)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func assertSampleStatistics(t *testing.T, view queryView, midpointSamples []int64) {
	t.Helper()
	var sum float64
	for _, v := range midpointSamples {
		sum += float64(v)
	}
	mean := sum / float64(len(midpointSamples))
	var squaredDeviations float64
	for _, v := range midpointSamples {
		d := float64(v) - mean
		squaredDeviations += d * d
	}
	stddev := math.Sqrt(squaredDeviations / float64(len(midpointSamples)))
	assert.Equal(t, int64(len(midpointSamples)), view.TotalCount())
	assert.InDelta(t, mean, view.Mean(), 1e-12*max(1, math.Abs(mean)), "mean")
	assert.InDelta(t, stddev, view.StdDev(), 1e-12*max(1, math.Abs(stddev)), "standard deviation")
}

type sample struct {
	value, count int64
}

func TestStatisticsAndPercentiles(t *testing.T) {
	t.Parallel()

	var uniform []sample
	var uniformMidpoints []int64
	for value := int64(1); value <= 100; value++ {
		uniform = append(uniform, sample{value, 1})
		uniformMidpoints = append(uniformMidpoints, value)
	}
	for _, tc := range []struct {
		name        string
		samples     []sample
		midpoints   []int64
		min, max    int64
		percentiles []percentileCase
	}{
		{
			name:    "uniform",
			samples: uniform, midpoints: uniformMidpoints, min: 1, max: 100,
			percentiles: []percentileCase{
				{0, 1}, {math.SmallestNonzeroFloat64, 1}, {0.1, 1},
				{1, 1}, {1.01, 2}, {50, 50}, {99, 99}, {99.01, 100}, {100, 100},
			},
		},
		{
			name:      "weighted with zeros",
			samples:   []sample{{0, 3}, {10, 2}, {100, 1}},
			midpoints: []int64{0, 0, 0, 10, 10, 100}, min: 0, max: 100,
			percentiles: []percentileCase{{0, 0}, {50, 0}, {75, 10}, {100, 100}},
		},
		{
			// Fixtures for the three-digit bucket overlap diagram by Gil Tene:
			// https://github.com/HdrHistogram/HdrHistogram/blob/master/src/main/java/org/HdrHistogram/AbstractHistogram.java
			// Bounds and midpoints are independent of the production indexing helpers.
			name:      "reference bucket boundaries",
			samples:   []sample{{0, 1}, {2047, 1}, {2048, 1}, {2049, 1}, {4095, 1}, {4096, 1}, {4099, 1}},
			midpoints: []int64{0, 2047, 2049, 2049, 4095, 4098, 4098}, min: 0, max: 4099,
			percentiles: []percentileCase{{0, 0}, {25, 2047}, {50, 2049}, {75, 4099}, {100, 4099}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newTestHistogram(t, 1, 10_000, 3)
			for _, s := range tc.samples {
				record(t, h, s.value, s.count)
			}
			for _, view := range queryViews(h) {
				t.Run(view.name, func(t *testing.T) {
					t.Parallel()

					assertSampleStatistics(t, view.view, tc.midpoints)
					assert.Equal(t, tc.min, view.view.Min())
					assert.Equal(t, tc.max, view.view.Max())
					assertPercentiles(t, view.view, tc.percentiles)
				})
			}
		})
	}
}

func TestReferenceBuckets(t *testing.T) {
	t.Parallel()

	h := newTestHistogram(t, 1, 10_000, 3)
	for _, value := range []int64{0, 2047, 2048, 2049, 4095, 4096, 4099} {
		record(t, h, value, 1)
	}
	want := []Bucket{{0, 0, 1}, {2047, 2047, 1}, {2048, 2049, 2}, {4094, 4095, 1}, {4096, 4099, 2}}
	require.Equal(t, want, slices.Collect(h.RecordedValues()))
}

func TestEmptyQueries(t *testing.T) {
	t.Parallel()

	h := newTestHistogram(t, 1, 100, 3)
	views := append(queryViews(h), namedQueryView{name: "zero snapshot", view: new(Snapshot)})
	for _, tc := range views {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Zero(t, tc.view.TotalCount())
			assert.Zero(t, tc.view.Min())
			assert.Zero(t, tc.view.Max())
			assert.Zero(t, tc.view.Mean())
			assert.Zero(t, tc.view.StdDev())
			assertPercentiles(t, tc.view, []percentileCase{{0, 0}, {50, 0}, {100, 0}})
		})
	}
}

func TestInvalidPercentiles(t *testing.T) {
	t.Parallel()

	for _, state := range []struct {
		name  string
		count int64
	}{{"empty", 0}, {"populated", 1}} {
		t.Run(state.name, func(t *testing.T) {
			t.Parallel()

			h := newTestHistogram(t, 1, 100, 3)
			record(t, h, 10, state.count)
			for _, view := range queryViews(h) {
				t.Run(view.name, func(t *testing.T) {
					t.Parallel()

					for _, tc := range []struct {
						name       string
						percentile float64
					}{
						{"negative", -1}, {"above maximum", 101}, {"NaN", math.NaN()},
						{"positive infinity", math.Inf(1)}, {"negative infinity", math.Inf(-1)},
					} {
						t.Run(tc.name, func(t *testing.T) {
							t.Parallel()

							_, err := view.view.ValueAtPercentile(tc.percentile)
							require.ErrorIs(t, err, ErrInvalidArgument)
						})
					}
				})
			}
		})
	}
}

func TestQuantizationAndStrictMaximum(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name            string
		lowest, highest int64
		values          []int64
		min, max        int64
		mean            float64
	}{
		{"base resolution", 1000, 10_000, []int64{0, 511}, 0, 511, 256},
		{"rounded upper bound", 1, 2050, []int64{2050}, 2050, 2051, 2051},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newTestHistogram(t, tc.lowest, tc.highest, 3)
			for _, value := range tc.values {
				record(t, h, value, 1)
			}
			assert.Equal(t, tc.min, h.Min())
			assert.Equal(t, tc.max, h.Max())
			assert.InDelta(t, tc.mean, h.Mean(), 1e-12)
			assert.Zero(t, h.StdDev())
			before := h.Snapshot()
			require.ErrorIs(t, h.RecordValue(tc.highest+1), ErrOutOfRange)
			unchanged(t, h, before)
		})
	}
}

func TestRecordValues(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                             string
		initial, value, count, wantTotal int64
		wantErr                          error
	}{
		{"single", 0, 5, 1, 1, nil},
		{"weighted", 2, 100, 3, 5, nil},
		{"negative value", 2, -1, 1, 2, ErrOutOfRange},
		{"value above maximum", 2, 101, 1, 2, ErrOutOfRange},
		{"negative count", 2, 5, -1, 2, ErrInvalidArgument},
		{"invalid value with zero count", 2, 101, 0, 2, ErrOutOfRange},
		{"zero count", 2, 0, 0, 2, nil},
		{"reach maximum count", 2, 100, math.MaxInt64 - 2, math.MaxInt64, nil},
		{"overflow new bin", math.MaxInt64, 0, 1, math.MaxInt64, ErrCountOverflow},
		{"overflow existing bin", math.MaxInt64, 5, 1, math.MaxInt64, ErrCountOverflow},
		{"overflow maximum value", math.MaxInt64, 100, 1, math.MaxInt64, ErrCountOverflow},
		{"zero count at capacity", math.MaxInt64, 100, 0, math.MaxInt64, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newTestHistogram(t, 1, 100, 3)
			record(t, h, 5, tc.initial)
			before := h.Snapshot()
			err := h.RecordValues(tc.value, tc.count)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.wantTotal, h.TotalCount())
			if tc.wantErr != nil || tc.count == 0 {
				unchanged(t, h, before)
			}
		})
	}
}

func TestPercentilesLargeCount(t *testing.T) {
	t.Parallel()

	h := newTestHistogram(t, 1, 100, 3)
	record(t, h, 10, math.MaxInt64-1)
	record(t, h, 100, 1)
	assertPercentiles(t, h, []percentileCase{
		{0, 10}, {math.SmallestNonzeroFloat64, 10}, {50, 10},
		{math.Nextafter(100, 0), 10}, {100, 100},
	})
}

func TestCorrectedRecording(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		value, interval int64
	}{
		{"zero without correction", 0, 0}, {"zero with correction", 0, 1},
		{"below interval", 5, 10}, {"equal interval", 10, 10},
		{"partial interval", 25, 10}, {"exact intervals", 100, 10},
		{"bucket boundary", 2049, 1}, {"nondivisible bucket widths", 4097, 3},
		{"disabled", 10_000, 0}, {"maximum interval", 10_000, math.MaxInt64},
	}
	for figures := range 6 {
		for _, lowest := range []int64{1, 1000} {
			t.Run(fmt.Sprintf("precision%d/lowest%d", figures, lowest), func(t *testing.T) {
				t.Parallel()

				for _, tc := range tests {
					t.Run(tc.name, func(t *testing.T) {
						t.Parallel()

						h := newTestHistogram(t, lowest, 10_000, figures)
						want := newTestHistogram(t, lowest, 10_000, figures)
						record(t, h, 7, 2)
						record(t, want, 7, 2)
						require.NoError(t, h.RecordCorrectedValue(tc.value, tc.interval))
						record(t, want, tc.value, 1)
						if tc.interval > 0 {
							for value := tc.value - tc.interval; value >= tc.interval; value -= tc.interval {
								record(t, want, value, 1)
							}
						}
						require.Equal(t, want.Snapshot(), h.Snapshot())
					})
				}
			})
		}
	}
}

func TestCorrectedRecordingHugeRatio(t *testing.T) {
	t.Parallel()

	// This represents over nine quintillion samples using only 63 occupied bins.
	h := newTestHistogram(t, 1, math.MaxInt64, 0)
	require.NoError(t, h.RecordCorrectedValue(math.MaxInt64, 1))
	assert.Equal(t, int64(math.MaxInt64), h.TotalCount())
	assert.Equal(t, int64(1), h.Min())
	assert.Equal(t, int64(math.MaxInt64), h.Max())
	for bucket := range h.RecordedValues() {
		require.Equal(t, bucket.To-bucket.From+1, bucket.Count, "one sample per integer: %v", bucket)
	}
	before := h.Snapshot()
	require.ErrorIs(t, h.RecordCorrectedValue(100, 10), ErrCountOverflow)
	unchanged(t, h, before)
}

func TestCorrectedRecordingErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name            string
		value, interval int64
		wantErr         error
	}{
		{"negative interval", 100, -1, ErrInvalidArgument},
		{"negative value", -1, 10, ErrOutOfRange},
		{"value above maximum", 101, 10, ErrOutOfRange},
		{"generated count overflow", 100, 10, ErrCountOverflow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newTestHistogram(t, 1, 100, 3)
			record(t, h, 0, math.MaxInt64-5)
			before := h.Snapshot()
			require.ErrorIs(t, h.RecordCorrectedValue(tc.value, tc.interval), tc.wantErr)
			unchanged(t, h, before)
		})
	}
}

func TestSnapshotsIterationAndReset(t *testing.T) {
	t.Parallel()

	h := newTestHistogram(t, 1, 10_000, 3)
	record(t, h, 100, 2)
	record(t, h, 4096, 3)
	snapshot := h.Snapshot()
	iterator := h.RecordedValues()
	want := []Bucket{{100, 100, 2}, {4096, 4099, 3}}
	storage := &h.data.counts[0]
	h.Reset()
	assert.Same(t, storage, &h.data.counts[0], "reset must reuse storage")
	assert.Zero(t, h.TotalCount())
	assert.Empty(t, slices.Collect(h.RecordedValues()))
	record(t, h, 5, 1)
	assert.Equal(t, int64(5), h.Min())
	assert.Equal(t, int64(5), h.Max())
	for _, tc := range []struct {
		name   string
		values iter.Seq[Bucket]
	}{{"captured histogram iterator", iterator}, {"snapshot iterator", snapshot.RecordedValues()}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for range 2 {
				require.Equal(t, want, slices.Collect(tc.values), "iterator must be reusable")
			}
		})
	}
	iterations := 0
	for range iterator {
		iterations++
		// Reentrant mutations must not deadlock or affect the captured data.
		h.Reset()
		record(t, h, 20, 1)
		break
	}
	assert.Equal(t, 1, iterations)
	assert.Equal(t, int64(5), snapshot.TotalCount())
	assert.Empty(t, slices.Collect(new(Snapshot).RecordedValues()))
}

func TestMerge(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                string
		destination, source []sample
		self                bool
		want                []Bucket
	}{
		{
			name:        "populated",
			destination: []sample{{10, 2}}, source: []sample{{5, 3}, {4096, 4}},
			self: false,
			want: []Bucket{{5, 5, 3}, {10, 10, 2}, {4096, 4099, 4}},
		},
		{name: "empty source", destination: []sample{{10, 2}}, source: nil, self: false, want: []Bucket{{10, 10, 2}}},
		{name: "empty destination", destination: nil, source: []sample{{5, 3}, {4096, 4}}, self: false, want: []Bucket{{5, 5, 3}, {4096, 4099, 4}}},
		{
			name:        "self doubles counts",
			destination: []sample{{5, 3}, {10, 2}, {4096, 4}}, source: nil, self: true,
			want: []Bucket{{5, 5, 6}, {10, 10, 4}, {4096, 4099, 8}},
		},
		{name: "empty self", destination: nil, source: nil, self: true, want: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			destination := newTestHistogram(t, 1, 10_000, 3)
			source := newTestHistogram(t, 1, 10_000, 3)
			for _, s := range tc.destination {
				record(t, destination, s.value, s.count)
			}
			for _, s := range tc.source {
				record(t, source, s.value, s.count)
			}
			if tc.self {
				source = destination
			}
			beforeSource := source.Snapshot()
			require.NoError(t, destination.Merge(source))
			if !tc.self {
				unchanged(t, source, beforeSource)
			}
			assert.Equal(t, tc.want, slices.Collect(destination.RecordedValues()))
			var total int64
			for _, bucket := range tc.want {
				total += bucket.Count
			}
			assert.Equal(t, total, destination.TotalCount())
			require.NoError(t, snapshotProblem(destination.Snapshot()))
		})
	}
}

func TestMergeIncompatible(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name            string
		lowest, highest int64
		figures         int
	}{
		{"lowest differs", 4, 100, 3},
		{"highest differs", 2, 101, 3},
		{"precision differs", 2, 100, 2},
		// Equal effective geometry is insufficient: constructor settings must match.
		{"same geometry but different lowest", 3, 100, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newTestHistogram(t, 2, 100, 3)
			record(t, h, 10, 1)
			before := h.Snapshot()
			source := newTestHistogram(t, tc.lowest, tc.highest, tc.figures)
			require.ErrorIs(t, h.Merge(source), ErrIncompatibleConfiguration)
			unchanged(t, h, before)
		})
	}
}

func TestMergeOverflow(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		self bool
	}{{"separate source", false}, {"self", true}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newTestHistogram(t, 1, 100, 3)
			record(t, h, 10, math.MaxInt64-1)
			source := newTestHistogram(t, 1, 100, 3)
			record(t, source, 20, 2)
			if tc.self {
				source = h
			}
			before := h.Snapshot()
			require.ErrorIs(t, h.Merge(source), ErrCountOverflow)
			unchanged(t, h, before)
		})
	}
}

func TestInvalidHistogramArguments(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		run  func(uninitialized, valid *Histogram) error
	}{
		{"record on zero value", func(h, _ *Histogram) error { return h.RecordValue(0) }},
		{"correct on zero value", func(h, _ *Histogram) error { return h.RecordCorrectedValue(0, 1) }},
		{"self merge on zero value", func(h, _ *Histogram) error { return h.Merge(h) }},
		{"merge into zero value", func(h, valid *Histogram) error { return h.Merge(valid) }},
		{"merge zero value source", func(h, valid *Histogram) error { return valid.Merge(h) }},
		{"merge nil source", func(_, valid *Histogram) error { return valid.Merge(nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var h Histogram
			valid := newTestHistogram(t, 1, 100, 3)
			record(t, valid, 10, 1)
			before, beforeZero := valid.Snapshot(), h.Snapshot()
			require.ErrorIs(t, tc.run(&h, valid), ErrInvalidArgument)
			unchanged(t, valid, before)
			unchanged(t, &h, beforeZero)
		})
	}
}

//nolint:paralleltest // AllocsPerRun measures process-wide allocations and temporarily changes GOMAXPROCS.
func TestRecordingAllocations(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*Histogram) error
	}{
		{"single", func(h *Histogram) error { return h.RecordValue(12345) }},
		{"weighted", func(h *Histogram) error { return h.RecordValues(12345, 100) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHistogram(t, 1, 1_000_000, 3)
			allocations := testing.AllocsPerRun(1000, func() {
				if err := tc.run(h); err != nil {
					panic(err)
				}
			})
			assert.Zero(t, allocations, "successful recording must not allocate")
		})
	}
}
