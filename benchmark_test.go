package hdrhistogram

import (
	"testing"

	"github.com/stretchr/testify/require"
)

var benchmarkHistogram *Histogram

func BenchmarkNew(b *testing.B) {
	for _, tc := range []struct {
		name    string
		figures int
	}{{"Figures1", 1}, {"Figures3", 3}, {"Figures5", 5}} {
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

func BenchmarkRecordValue(b *testing.B) {
	h := newTestHistogram(b, 1, 3_600_000_000, 3)
	b.ReportAllocs()
	for b.Loop() {
		if err := h.RecordValue(12345); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRecordValues(b *testing.B) {
	h := newTestHistogram(b, 1, 3_600_000_000, 3)
	b.ReportAllocs()
	for b.Loop() {
		if err := h.RecordValues(12345, 100); err != nil {
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
			if err := h.RecordValue(12345); err != nil {
				b.Error(err)
				return
			}
		}
	})
}

func BenchmarkRecordCorrectedValue(b *testing.B) {
	h := newTestHistogram(b, 1, 3_600_000_000, 3)
	b.ReportAllocs()
	for b.Loop() {
		if err := h.RecordCorrectedValue(100_000_000, 10_000); err != nil {
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

func BenchmarkValueAtPercentile(b *testing.B) {
	h := benchmarkPopulated(b)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := h.ValueAtPercentile(99); err != nil {
			b.Fatal(err)
		}
	}
}

var benchmarkSnapshot *Snapshot

func BenchmarkSnapshot(b *testing.B) {
	h := benchmarkPopulated(b)
	b.ReportAllocs()
	for b.Loop() {
		benchmarkSnapshot = h.Snapshot()
	}
}

var benchmarkStatistic float64

func BenchmarkMean(b *testing.B) {
	h := benchmarkPopulated(b)
	b.ReportAllocs()
	for b.Loop() {
		benchmarkStatistic = h.Mean()
	}
}

func BenchmarkStdDev(b *testing.B) {
	h := benchmarkPopulated(b)
	b.ReportAllocs()
	for b.Loop() {
		benchmarkStatistic = h.StdDev()
	}
}

func BenchmarkReset(b *testing.B) {
	h := benchmarkPopulated(b)
	b.ReportAllocs()
	// Reset clears the full counts array even when it is already empty. Reuse
	// the array to measure clearing and locking without timing sample recording.
	for b.Loop() {
		h.Reset()
	}
}

func BenchmarkMerge(b *testing.B) {
	source := benchmarkPopulated(b)
	destination := newTestHistogram(b, 1, 3_600_000_000, 3)
	b.ReportAllocs()
	for b.Loop() {
		if err := destination.Merge(source); err != nil {
			b.Fatal(err)
		}
	}
	require.Equal(b, int64(b.N)*source.TotalCount(), destination.TotalCount(), "merged count")
}

func BenchmarkRecordedValues(b *testing.B) {
	h := benchmarkPopulated(b)
	snapshot := h.Snapshot()
	for _, tc := range []struct {
		name        string
		useSnapshot bool
	}{
		// Histogram iteration includes capturing a new snapshot on every call.
		{"Histogram", false},
		{"Snapshot", true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			var count int64
			// Dispatch outside the timed loop. Calling an iterator through a
			// function value would add escaping closures to the measurement.
			if tc.useSnapshot {
				for b.Loop() {
					count = 0
					for bucket := range snapshot.RecordedValues() {
						count += bucket.Count
					}
				}
			} else {
				for b.Loop() {
					count = 0
					for bucket := range h.RecordedValues() {
						count += bucket.Count
					}
				}
			}
			require.Equal(b, h.TotalCount(), count, "iterated count")
		})
	}
}
