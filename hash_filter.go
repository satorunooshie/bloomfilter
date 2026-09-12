package bloom

import (
	"math"
	"sync/atomic"

	"github.com/satorunooshie/bloomfilter/internal/core"
)

const (
	hashBlockBits  = uint64(512)
	hashBlockWords = uint32(16)
)

// HashFilter is a concurrent Bloom filter that accepts caller-provided,
// well-mixed 64-bit hash values. Unlike Filter, it does not hash keys and does
// not use maphash or sync.Pool on the operation hot path.
//
// Add and Contains may run concurrently. The zero value is not ready for use;
// create filters with NewHash. Hash values should be uniformly distributed
// across 64 bits. Passing raw, low-entropy integer values can produce a
// false-positive rate worse than the estimate. HashFilter intentionally has no
// Reset method; replace the filter from the outside when a new generation is
// needed.
type HashFilter struct {
	noCopy   noCopy
	words    []uint32
	bits     uint64
	blocks   uint32
	k        uint64
	k32      uint32
	capacity uint64
	rate     float64
}

// NewHash creates a Bloom filter for caller-provided 64-bit hash values.
func NewHash(capacity uint64, falsePositiveRate float64) (*HashFilter, error) {
	if capacity == 0 {
		return nil, ErrInvalidCapacity
	}
	if !(falsePositiveRate > 0 && falsePositiveRate < 1) || math.IsNaN(falsePositiveRate) {
		return nil, ErrInvalidRate
	}
	baseBits, _, ok := core.Parameters(capacity, falsePositiveRate)
	if !ok {
		return nil, ErrInvalidCapacity
	}
	m, k := fastHashParameters(capacity, falsePositiveRate, baseBits)
	blocks := (m + hashBlockBits - 1) / hashBlockBits
	wordCount := blocks * uint64(hashBlockWords)
	if blocks > uint64(^uint32(0)) || wordCount > uint64(^uint(0)>>1) {
		return nil, ErrInvalidCapacity
	}
	return &HashFilter{
		words:    make([]uint32, int(wordCount)),
		bits:     blocks * hashBlockBits,
		blocks:   uint32(blocks),
		k:        k,
		k32:      uint32(k),
		capacity: capacity,
		rate:     falsePositiveRate,
	}, nil
}

// fastHashParameters trades some additional bits for fewer probes. Blocked
// filters need a little more space than vanilla filters because each key's
// probes are constrained to one block. The correction values are the standard
// blocked-filter occupancy correction, indexed by ceil(-log2(p)/ln(2)).
func fastHashParameters(capacity uint64, rate float64, baseBits uint64) (uint64, uint64) {
	correction := []float64{1, 1, 2, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 16, 17, 18, 20, 21, 23, 25, 26, 28, 30, 32, 35, 38, 40, 44, 48, 51, 58, 64, 74, 90}
	index := int(math.Ceil(-math.Log2(rate) / math.Ln2))
	if index >= len(correction) {
		_, baseK, _ := core.Parameters(capacity, rate)
		return baseBits, baseK
	}
	bitsPerKey := correction[index]
	if rate <= 0.0001 {
		bitsPerKey *= 1.5
	}
	for k := uint64(2); k <= 16; k++ {
		probeCount := float64(k)
		if rate <= 0.0001 {
			// The occupancy correction is conservative for the lower FPR
			// range when one hash selects the block and the remaining hashes
			// select bits within it.
			probeCount--
		}
		blockedFPR := math.Pow(1-math.Exp(-probeCount/bitsPerKey), probeCount)
		if blockedFPR <= rate {
			mFloat := math.Ceil(float64(capacity) * bitsPerKey)
			if mFloat < math.Exp2(64) {
				return uint64(mFloat), k
			}
		}
	}
	_, k, _ := core.Parameters(capacity, rate)
	return baseBits, k
}

// Add inserts a caller-provided hash value into the filter.
func (f *HashFilter) Add(hash uint64) {
	f.addHash(hash)
}

func (f *HashFilter) addHash(hash uint64) {
	block := uint32((uint64(uint32(hash)) * uint64(f.blocks)) >> 32)
	base := block * uint32(hashBlockWords)
	h1, h2 := uint32(hash>>32), uint32(hash)
	for i := uint32(0); i < f.k32; i++ {
		position := h1 & uint32(hashBlockBits-1)
		word := base + position>>5
		atomic.OrUint32(&f.words[word], uint32(1)<<(position&31))
		h1 += h2
		h2 += i + 1
	}
}

// Contains reports whether a hash value may have been added to the filter.
func (f *HashFilter) Contains(hash uint64) bool {
	return f.containsHash(hash)
}

func (f *HashFilter) containsHash(hash uint64) bool {
	block := uint32((uint64(uint32(hash)) * uint64(f.blocks)) >> 32)
	base := block * uint32(hashBlockWords)
	h1, h2 := uint32(hash>>32), uint32(hash)
	for i := uint32(0); i < f.k32; i++ {
		position := h1 & uint32(hashBlockBits-1)
		word := base + position>>5
		if atomic.LoadUint32(&f.words[word])&(uint32(1)<<(position&31)) == 0 {
			return false
		}
		h1 += h2
		h2 += i + 1
	}
	return true
}

// Parameters returns the sizing decisions made by the filter.
func (f *HashFilter) Parameters() Parameters {
	return Parameters{
		Capacity:          f.capacity,
		Bits:              f.bits,
		HashFunctions:     f.k,
		FalsePositiveRate: f.rate,
	}
}

// EstimateFalsePositiveRate estimates the false-positive rate after n Add
// calls using the standard Bloom filter approximation.
func (f *HashFilter) EstimateFalsePositiveRate(n uint64) float64 {
	return math.Pow(1-math.Exp(-float64(f.k)*float64(n)/float64(f.bits)), float64(f.k))
}
