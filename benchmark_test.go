package hdrhistogram

import (
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	benchmarkHistogram   *Histogram
	benchmarkSnapshot    *Snapshot
	benchmarkStatistic   float64
	benchmarkPercentile  int64
	benchmarkPercentiles []int64
)

func BenchmarkNew(b *testing.B) {
	for _, tc := range []struct {
		name    string
		figures int
	}{{"Precision1", 1}, {"Precision3", 3}, {"Precision5", 5}} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				h, err := New(1, 3_600_000_000, tc.figures)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkHistogram = h
			}
		})
	}
}

func BenchmarkRecord(b *testing.B) {
	h := newTestHistogram(b, 1, 3_600_000_000, 3)
	b.ReportAllocs()
	for b.Loop() {
		if err := h.Record(12345); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRecordN(b *testing.B) {
	h := newTestHistogram(b, 1, 3_600_000_000, 3)
	b.ReportAllocs()
	for b.Loop() {
		if err := h.RecordN(12345, 100); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRecordParallel(b *testing.B) {
	h := newTestHistogram(b, 1, 3_600_000_000, 3)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := h.Record(12345); err != nil {
				b.Error(err)
				return
			}
		}
	})
}

func BenchmarkRecordCorrected(b *testing.B) {
	h := newTestHistogram(b, 1, 3_600_000_000, 3)
	b.ReportAllocs()
	for b.Loop() {
		if err := h.RecordCorrected(100_000_000, 10_000); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkPopulated(b *testing.B) *Histogram {
	b.Helper()
	h := newTestHistogram(b, 1, 3_600_000_000, 3)
	for value := int64(1); value < 3_600_000_000; value *= 2 {
		record(b, h, value, 10)
	}
	return h
}

func BenchmarkPercentile(b *testing.B) {
	s := benchmarkPopulated(b).Snapshot()
	b.ReportAllocs()
	for b.Loop() {
		value, err := s.Percentile(99)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkPercentile = value
	}
}

func BenchmarkPercentiles(b *testing.B) {
	s := benchmarkPopulated(b).Snapshot()
	ps := []float64{50, 90, 95, 99, 99.9}
	b.Run("Together", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			values, err := s.Percentiles(ps)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkPercentiles = values
		}
	})
	b.Run("Individually", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for _, p := range ps {
				value, err := s.Percentile(p)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkPercentile = value
			}
		}
	})
}

func BenchmarkSnapshot(b *testing.B) {
	h := benchmarkPopulated(b)
	b.ReportAllocs()
	for b.Loop() {
		benchmarkSnapshot = h.Snapshot()
	}
}

func BenchmarkMean(b *testing.B) {
	s := benchmarkPopulated(b).Snapshot()
	b.ReportAllocs()
	for b.Loop() {
		benchmarkStatistic = s.Mean()
	}
}

func BenchmarkStdDev(b *testing.B) {
	s := benchmarkPopulated(b).Snapshot()
	b.ReportAllocs()
	for b.Loop() {
		benchmarkStatistic = s.StdDev()
	}
}

func BenchmarkReset(b *testing.B) {
	b.Run("Empty", func(b *testing.B) {
		h := newTestHistogram(b, 1, 3_600_000_000, 3)
		b.ReportAllocs()
		for b.Loop() {
			h.Reset()
		}
	})
	b.Run("Populated", func(b *testing.B) {
		h := benchmarkPopulated(b)
		s := h.Snapshot()
		b.ReportAllocs()
		for b.Loop() {
			b.StopTimer()
			if err := h.Merge(s); err != nil {
				b.Fatal(err)
			}
			b.StartTimer()
			h.Reset()
		}
	})
}

func BenchmarkMerge(b *testing.B) {
	source := benchmarkPopulated(b).Snapshot()
	b.Run("Populated", func(b *testing.B) {
		destination := newTestHistogram(b, 1, 3_600_000_000, 3)
		b.ReportAllocs()
		for b.Loop() {
			if err := destination.Merge(source); err != nil {
				b.Fatal(err)
			}
		}
		require.Equal(b, int64(b.N)*source.TotalCount(), destination.Snapshot().TotalCount(), "merged count")
	})
	b.Run("Empty", func(b *testing.B) {
		destination := newTestHistogram(b, 1, 3_600_000_000, 3)
		b.ReportAllocs()
		for b.Loop() {
			b.StopTimer()
			destination.Reset()
			b.StartTimer()
			if err := destination.Merge(source); err != nil {
				b.Fatal(err)
			}
		}
		require.Equal(b, source.TotalCount(), destination.Snapshot().TotalCount(), "merged count")
	})
}

func BenchmarkBuckets(b *testing.B) {
	snapshot := benchmarkPopulated(b).Snapshot()
	b.ReportAllocs()
	var count int64
	for b.Loop() {
		count = 0
		for bucket := range snapshot.Buckets() {
			count += bucket.Count
		}
	}
	require.Equal(b, snapshot.TotalCount(), count, "iterated count")
}
