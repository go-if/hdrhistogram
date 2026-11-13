package hdrhistogram

import (
	"math"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func FuzzBucketMapping(f *testing.F) {
	f.Add(uint64(0), uint64(0), uint8(0))
	f.Add(uint64(2048), uint64(999), uint8(3))
	f.Add(uint64(math.MaxInt64), uint64(0), uint8(5))
	f.Add(uint64(math.MaxInt64), uint64(1<<44), uint8(5))
	f.Fuzz(func(t *testing.T, rawValue, rawLowest uint64, precision uint8) {
		value := int64(rawValue & math.MaxInt64)
		lowest := int64(rawLowest%(1<<45)) + 1
		figures := int(precision % 6)
		g, err := newGeometry(lowest, math.MaxInt64, figures)
		require.NoError(t, err)
		index := g.index(value)
		require.GreaterOrEqual(t, index, 0)
		require.Less(t, index, g.countsLen)
		from, to := g.bounds(index)
		wantFrom, wantTo := oracleBounds(value, lowest, figures)
		require.Equal(t, wantFrom, from)
		require.Equal(t, wantTo, to)
		require.Equal(t, index, g.index(from))
		require.Equal(t, index, g.index(to))
		midpoint := g.midpoint(index)
		require.GreaterOrEqual(t, midpoint, from)
		require.LessOrEqual(t, midpoint, to)
	})
}

func FuzzPercentilesAndCounts(f *testing.F) {
	f.Add([]byte{}, uint8(0), uint8(3))
	f.Add([]byte{0, 0, 1, 8, 0, 2, 255, 255, 7}, uint8(0), uint8(3))
	f.Add([]byte{0, 1, 1, 0, 2, 1}, uint8(4), uint8(0))
	f.Fuzz(func(t *testing.T, data []byte, low, precision uint8) {
		data = data[:min(len(data), 384)]
		lowest, figures := int64(low)+1, int(precision%4)
		h := newTestHistogram(t, lowest, 65535, figures)
		var samples []int64
		wantBuckets := make(map[int64]Bucket)
		for i := 0; i+2 < len(data); i += 3 {
			value, count := int64(data[i])<<8|int64(data[i+1]), int64(data[i+2]%8)
			record(t, h, value, count)
			if count == 0 {
				continue
			}
			from, to := oracleBounds(value, lowest, figures)
			b := wantBuckets[from]
			wantBuckets[from] = Bucket{From: from, To: to, Count: b.Count + count}
			for range count {
				samples = append(samples, value)
			}
		}
		snapshot := h.Snapshot()
		require.Equal(t, int64(len(samples)), snapshot.TotalCount(), "sample count not conserved")
		buckets := slices.Collect(snapshot.Buckets())
		require.Len(t, buckets, len(wantBuckets))
		for _, b := range buckets {
			require.Equal(t, wantBuckets[b.From], b)
		}
		slices.Sort(samples)
		ps := make([]float64, 101)
		for i := range ps {
			ps[i] = float64(100 - i)
		}
		percentiles, err := snapshot.Percentiles(ps)
		require.NoError(t, err)
		previous := int64(0)
		for p := range 101 {
			got, err := snapshot.Percentile(float64(p))
			require.NoError(t, err)
			require.GreaterOrEqual(t, got, previous, "nonmonotonic p%d", p)
			previous = got
			want := int64(0)
			if len(samples) > 0 {
				// Exact integer nearest-rank calculation on the original samples.
				rank := max(1, (p*len(samples)+99)/100)
				from, to := oracleBounds(samples[rank-1], lowest, figures)
				want = to
				if p == 0 {
					want = from
				}
			}
			require.Equal(t, want, got, "p%d", p)
			require.Equal(t, want, percentiles[100-p], "batch p%d", p)
		}
		require.NoError(t, snapshotProblem(h.Snapshot()))
	})
}

func FuzzCorrectedRecording(f *testing.F) {
	f.Add(uint16(4097), uint16(1), uint8(0), uint8(3))
	f.Add(uint16(0), uint16(0), uint8(0), uint8(0))
	f.Add(uint16(100), uint16(10), uint8(255), uint8(2))
	f.Fuzz(func(t *testing.T, rawValue, rawInterval uint16, rawLowest, precision uint8) {
		value, interval := int64(rawValue), int64(rawInterval)
		lowest, figures := int64(rawLowest)+1, int(precision%4)
		h := newTestHistogram(t, lowest, 65535, figures)
		want := newTestHistogram(t, lowest, 65535, figures)
		require.NoError(t, h.RecordCorrected(value, interval))
		record(t, want, value, 1)
		if interval > 0 {
			for v := value - interval; v >= interval; v -= interval {
				record(t, want, v, 1)
			}
		}
		require.Equal(t, want.Snapshot(), h.Snapshot(), "value=%d interval=%d lowest=%d figures=%d", value, interval, lowest, figures)
	})
}
