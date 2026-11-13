package hdrhistogram

import (
	"fmt"
	"math"
	"math/bits"
)

// geometry is immutable after construction. Buckets have exponentially wider
// ranges; their upper halves are stored without duplicating the preceding bucket.
type geometry struct {
	lowest, highest int64
	figures         int
	unitMagnitude   uint
	halfMagnitude   uint
	halfCount       int
	subBucketMask   uint64
	countsLen       int
}

func newGeometry(lowest, highest int64, figures int) (geometry, error) {
	if lowest < 1 || highest < 2 || lowest > highest/2 {
		return geometry{}, fmt.Errorf("%w: require 1 <= lowestDiscernible <= highestTrackable/2", ErrInvalidArgument)
	}
	if figures < 0 || figures > 5 {
		return geometry{}, fmt.Errorf("%w: significantFigures must be between 0 and 5", ErrInvalidArgument)
	}
	resolution := uint64(2)
	for range figures {
		resolution *= 10
	}
	unitMagnitude := uint(bits.Len64(uint64(lowest)) - 1)
	subMagnitude := uint(bits.Len64(resolution - 1))
	// Keep the initial sub-bucket geometry representable in a positive int64.
	// Later buckets can cover MaxInt64.
	if unitMagnitude+subMagnitude > 62 {
		return geometry{}, fmt.Errorf("%w: resolution and lowestDiscernible exceed representable geometry", ErrInvalidArgument)
	}
	subCount := uint64(1) << subMagnitude
	bucketCount := uint64(1)
	for limit := subCount << unitMagnitude; limit <= uint64(highest); limit <<= 1 {
		// limit never needs to exceed 1<<63 because highest <= MaxInt64.
		bucketCount++
	}
	length := (bucketCount + 1) * (subCount / 2)
	if length > uint64(math.MaxInt)/8 {
		return geometry{}, fmt.Errorf("%w: counts array is too large", ErrInvalidArgument)
	}
	return geometry{
		lowest: lowest, highest: highest, figures: figures,
		unitMagnitude: unitMagnitude,
		halfMagnitude: subMagnitude - 1,
		halfCount:     1 << (subMagnitude - 1),
		subBucketMask: (subCount - 1) << unitMagnitude,
		countsLen:     int(length),
	}, nil
}

func (g geometry) index(value int64) int {
	bucket := bits.Len64(uint64(value)|g.subBucketMask) - int(g.unitMagnitude+g.halfMagnitude+1) //nolint:gosec // G115: callers pass nonnegative values; validated geometry bounds the magnitude sum to 62.
	subBucket := int(uint64(value) >> (uint(bucket) + g.unitMagnitude))                          //nolint:gosec // G115: values are nonnegative, bucket indices are at most 62, and sub-buckets fit in 18 bits.
	return ((bucket + 1) << g.halfMagnitude) + subBucket - g.halfCount
}

func (g geometry) bounds(index int) (from, to int64) {
	bucket := (index >> g.halfMagnitude) - 1
	subBucket := (index & (g.halfCount - 1)) + g.halfCount
	if bucket < 0 {
		bucket = 0
		subBucket -= g.halfCount
	}
	shift := uint(bucket) + g.unitMagnitude
	lower := int64(subBucket) << shift
	// Valid indices give shift <= 62 and upper bounds <= MaxInt64. Subtract
	// before adding so the final bucket never forms MaxInt64+1 temporarily.
	width := int64(1) << shift
	return lower, lower + (width - 1)
}

func (g geometry) midpoint(index int) int64 {
	from, to := g.bounds(index)
	return from + (to-from)/2 + (to-from)%2
}

func (g geometry) compatible(other geometry) bool {
	return g.lowest == other.lowest && g.highest == other.highest && g.figures == other.figures
}
