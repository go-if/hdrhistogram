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
		if err := h.RecordValue(latency.Microseconds()); err != nil {
			panic(err)
		}
	}
	s := h.Snapshot()
	p99, err := s.ValueAtPercentile(99)
	if err != nil {
		panic(err)
	}
	fmt.Printf("count=%d mean=%.0fµs p99=%dµs\n", s.TotalCount(), s.Mean(), p99)
	// Output: count=3 mean=200µs p99=300µs
}

func ExampleHistogram_RecordValue() {
	h, err := hdrhistogram.New(1, 1_000_000, 3)
	if err != nil {
		panic(err)
	}
	var workers sync.WaitGroup
	for value := range int64(4) {
		workers.Go(func() {
			for range 100 {
				if err := h.RecordValue(value); err != nil {
					panic(err)
				}
			}
		})
	}
	workers.Wait()
	fmt.Println(h.TotalCount())
	// Output: 400
}

func ExampleHistogram_RecordCorrectedValue() {
	h, err := hdrhistogram.New(1, 1000, 3)
	if err != nil {
		panic(err)
	}
	if err := h.RecordCorrectedValue(100, 20); err != nil {
		panic(err)
	}
	for b := range h.RecordedValues() {
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
	if err := a.RecordValue(10); err != nil {
		panic(err)
	}
	if err := b.RecordValues(20, 2); err != nil {
		panic(err)
	}
	if err := a.Merge(b); err != nil {
		panic(err)
	}
	fmt.Println(a.TotalCount(), b.TotalCount())
	// Output: 3 2
}
