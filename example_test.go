package hdrhistogram_test

import (
	"fmt"
	"sync"
	"time"

	"github.com/go-if/hdrhistogram"
)

func Example() {
	// Values and bounds are expressed in microseconds.
	h, err := hdrhistogram.New(1, time.Hour.Microseconds(), 3)
	if err != nil {
		panic(err)
	}
	for _, latency := range []time.Duration{100 * time.Microsecond, 200 * time.Microsecond, 300 * time.Microsecond} {
		if err := h.Record(latency.Microseconds()); err != nil {
			panic(err)
		}
	}
	s := h.Snapshot()
	p99, err := s.Percentile(99)
	if err != nil {
		panic(err)
	}
	fmt.Printf("count=%d mean=%.0fµs p99=%dµs\n", s.TotalCount(), s.Mean(), p99)
	// Output: count=3 mean=200µs p99=300µs
}

func ExampleHistogram_Record() {
	h, err := hdrhistogram.New(1, 1_000_000, 3)
	if err != nil {
		panic(err)
	}
	var workers sync.WaitGroup
	for value := range int64(4) {
		workers.Go(func() {
			for range 100 {
				if err := h.Record(value); err != nil {
					panic(err)
				}
			}
		})
	}
	workers.Wait()
	fmt.Println(h.Snapshot().TotalCount())
	// Output: 400
}

func ExampleHistogram_RecordCorrected() {
	h, err := hdrhistogram.New(1, 1000, 3)
	if err != nil {
		panic(err)
	}
	if err := h.RecordCorrected(100, 20); err != nil {
		panic(err)
	}
	for b := range h.Snapshot().Buckets() {
		fmt.Printf("%d..%d: %d\n", b.From, b.To, b.Count)
	}
	// Output:
	// 20..20: 1
	// 40..40: 1
	// 60..60: 1
	// 80..80: 1
	// 100..100: 1
}

func ExampleHistogram_Merge() {
	a, err := hdrhistogram.New(1, 1000, 3)
	if err != nil {
		panic(err)
	}
	b, err := hdrhistogram.New(1, 1000, 3)
	if err != nil {
		panic(err)
	}
	if err := a.Record(10); err != nil {
		panic(err)
	}
	if err := b.RecordN(20, 2); err != nil {
		panic(err)
	}
	if err := a.Merge(b.Snapshot()); err != nil {
		panic(err)
	}
	fmt.Println(a.Snapshot().TotalCount(), b.Snapshot().TotalCount())
	// Output: 3 2
}

func ExampleNew_precision() {
	// More figures distinguish measurements more finely at larger values.
	for figures := range 6 {
		h, err := hdrhistogram.New(1, 3_600_000_000, figures)
		if err != nil {
			panic(err)
		}
		if err := h.Record(1_000_000); err != nil {
			panic(err)
		}
		s := h.Snapshot()
		fmt.Printf("figures=%d: %d..%d\n", figures, s.Min(), s.Max())
	}
	// Output:
	// figures=0: 524288..1048575
	// figures=1: 983040..1015807
	// figures=2: 999424..1003519
	// figures=3: 999936..1000447
	// figures=4: 1000000..1000031
	// figures=5: 1000000..1000003
}

func ExampleNew_resolution() {
	// The lowest discernible value is rounded down to a power of two.
	// It controls base resolution; smaller samples are still accepted.
	for _, lowest := range []int64{1, 1000, 1024} {
		h, err := hdrhistogram.New(lowest, 10_000, 3)
		if err != nil {
			panic(err)
		}
		if err := h.Record(1000); err != nil {
			panic(err)
		}
		s := h.Snapshot()
		fmt.Printf("lowest=%d: %d..%d\n", lowest, s.Min(), s.Max())
	}
	// Output:
	// lowest=1: 1000..1000
	// lowest=1000: 512..1023
	// lowest=1024: 0..1023
}
