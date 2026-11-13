package hdrhistogram

import "math"

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

// merge requires compatible geometry and an immutable source.
func (d *histogramData) merge(source *histogramData) error {
	if source.total > math.MaxInt64-d.total {
		return ErrCountOverflow
	}
	if source.total == 0 {
		return nil
	}
	if d.total == 0 {
		copy(d.counts[source.minIndex:source.maxIndex+1], source.counts[source.minIndex:source.maxIndex+1])
	} else {
		for i := source.minIndex; i <= source.maxIndex; i++ {
			d.counts[i] += source.counts[i]
		}
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

func (d *histogramData) percentile(percentile float64) int64 {
	if d.total == 0 {
		return 0
	}
	if percentile == 0 {
		return d.min()
	}
	if percentile == 100 {
		return d.max()
	}
	rank := percentileRank(percentile, d.total)
	counts := d.counts[d.minIndex : d.maxIndex+1]
	var cumulative int64
	i := 0
	for ; i+8 <= len(counts); i += 8 {
		block := counts[i : i+8]
		count := block[0] + block[1] + block[2] + block[3] + block[4] + block[5] + block[6] + block[7]
		if cumulative+count >= rank {
			break
		}
		cumulative += count
	}
	for ; i < len(counts); i++ {
		cumulative += counts[i]
		if cumulative >= rank {
			_, to := d.geometry.bounds(d.minIndex + i)
			return to
		}
	}
	return d.max()
}

// percentileRank requires a valid positive percentile and a nonzero total.
func percentileRank(percentile float64, total int64) int64 {
	// HDR removes one ULP before rounding the rank up, avoiding a spurious
	// jump to the next bucket at floating-point percentile boundaries.
	p := math.Nextafter(percentile, math.Inf(-1))
	floatRank := math.Ceil(p * float64(total) / 100)
	rank := total
	// float64(MaxInt64) rounds to 1<<63; clamp before converting to int64.
	if floatRank < float64(total) {
		rank = max(1, int64(floatRank))
	}
	return rank
}
