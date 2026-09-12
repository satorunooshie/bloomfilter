// Package bloom provides a concurrent Bloom filter for Go 1.27 and later.
//
// The filter uses hash/maphash.Hasher, so callers can use
// maphash.ComparableHasher for comparable values or provide a hasher for any
// other type.
package bloom

import (
	"errors"
	"hash/maphash"
	"math"
	"math/bits"
	"sync"
	"sync/atomic"

	"github.com/satorunooshie/bloomfilter/internal/core"
)

var (
	ErrInvalidCapacity = errors.New("bloom: capacity must be greater than zero")
	ErrInvalidRate     = errors.New("bloom: false-positive rate must be greater than zero and less than one")
	ErrNilHasher       = errors.New("bloom: hasher must not be nil")
	ErrInvalidSeed     = errors.New("bloom: seed must be created by maphash.MakeSeed")
	ErrIncompatible    = errors.New("bloom: filters have incompatible parameters")
)

// Filter is a concurrent Bloom filter with a lock-free atomic bitset. The zero value is not ready
// for use; create filters with New, NewWithHasher, or NewWithSeed. It may
// contain false positives, but never returns a false negative for a value
// added to the same Filter. Add, Contains, and Reset may run concurrently.
//
// A Filter keeps a private random maphash seed. Consequently its bitset is
// intentionally not portable across processes; maphash.Seed cannot be
// serialized and reconstructed by callers.
type Filter[T any] struct {
	noCopy   noCopy
	words    []atomic.Uint64
	bits     uint64
	k        uint64
	capacity uint64
	rate     float64
	seed     maphash.Seed
	hasher   maphash.Hasher[T]
	hashPool sync.Pool
	epoch    atomic.Uint64
}

// noCopy makes go vet flag accidental copies of a live Filter.
type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}

// New creates a Bloom filter for capacity expected insertions and the target
// false-positive rate. It uses maphash.ComparableHasher, so T must be
// comparable.
func New[T comparable](capacity uint64, falsePositiveRate float64) (*Filter[T], error) {
	return NewWithHasher[T](capacity, falsePositiveRate, maphash.ComparableHasher[T]{})
}

// NewWithHasher creates a Bloom filter using hasher. The hasher's Hash method
// is used by Add and Contains; Equal is not needed by a Bloom filter but is
// part of maphash.Hasher's contract.
func NewWithHasher[T any](capacity uint64, falsePositiveRate float64, hasher maphash.Hasher[T]) (*Filter[T], error) {
	return newWithSeed(capacity, falsePositiveRate, hasher, maphash.MakeSeed())
}

// NewWithSeed is like NewWithHasher but uses seed. The seed must have been
// returned by maphash.MakeSeed or another Hash.Seed call. It is useful when multiple
// filters in the same process must use the same hash function. A maphash.Seed
// cannot be serialized and reconstructed, so this does not make filters
// portable across process restarts.
func NewWithSeed[T any](capacity uint64, falsePositiveRate float64, hasher maphash.Hasher[T], seed maphash.Seed) (*Filter[T], error) {
	return newWithSeed(capacity, falsePositiveRate, hasher, seed)
}

func newWithSeed[T any](capacity uint64, falsePositiveRate float64, hasher maphash.Hasher[T], seed maphash.Seed) (*Filter[T], error) {
	if capacity == 0 {
		return nil, ErrInvalidCapacity
	}
	if !(falsePositiveRate > 0 && falsePositiveRate < 1) || math.IsNaN(falsePositiveRate) {
		return nil, ErrInvalidRate
	}
	if hasher == nil {
		return nil, ErrNilHasher
	}
	var zeroSeed maphash.Seed
	if seed == zeroSeed {
		return nil, ErrInvalidSeed
	}

	m, k, ok := core.Parameters(capacity, falsePositiveRate)
	if !ok {
		return nil, ErrInvalidCapacity
	}
	wordCount := (m + 63) / 64

	f := &Filter[T]{
		words:    make([]atomic.Uint64, int(wordCount)),
		bits:     m,
		k:        k,
		capacity: capacity,
		rate:     falsePositiveRate,
		seed:     seed,
		hasher:   hasher,
	}
	f.hashPool.New = func() any { return new(maphash.Hash) }
	return f, nil
}

// Add inserts value into the filter.
func (f *Filter[T]) Add(value T) {
	for {
		epoch := f.epoch.Load()
		if epoch&1 != 0 {
			continue
		}
		h1, h2 := f.hash(value)
		f.addHash(h1, h2)
		if f.epoch.Load() == epoch {
			return
		}
	}
}

func (f *Filter[T]) addHash(h1, h2 uint64) {
	for i := uint64(0); i < f.k; i++ {
		index := core.Probe(h1, h2, i, f.bits)
		f.words[index>>6].Or(uint64(1) << (index & 63))
	}
}

// Contains reports whether value may have been added to the filter. false
// means value was definitely not added; true may be a false positive.
func (f *Filter[T]) Contains(value T) bool {
	for {
		epoch := f.epoch.Load()
		if epoch&1 != 0 {
			continue
		}
		h1, h2 := f.hash(value)
		result := f.containsHash(h1, h2)
		if f.epoch.Load() == epoch {
			return result
		}
	}
}

func (f *Filter[T]) containsHash(h1, h2 uint64) bool {
	for i := uint64(0); i < f.k; i++ {
		index := core.Probe(h1, h2, i, f.bits)
		if f.words[index>>6].Load()&(uint64(1)<<(index&63)) == 0 {
			return false
		}
	}
	return true
}

// Merge ORs other into f. Both filters must have identical sizing, seed, and
// hasher configuration. Go cannot inspect a maphash.Hasher's configuration,
// so callers must ensure that custom hashers are equivalent. Merge is safe to
// call concurrently with Add and Contains, but not with Reset on either
// filter.
func (f *Filter[T]) Merge(other *Filter[T]) error {
	if other == nil {
		return ErrIncompatible
	}
	if f == other {
		return nil
	}
	if f.bits != other.bits || f.k != other.k || f.seed != other.seed {
		return ErrIncompatible
	}
	for i := range f.words {
		word := other.words[i].Load()
		f.words[i].Or(word)
	}
	return nil
}

// Reset removes all values from the filter while retaining its size and hash
// seed.
func (f *Filter[T]) Reset() {
	for {
		epoch := f.epoch.Load()
		if epoch&1 != 0 || !f.epoch.CompareAndSwap(epoch, epoch+1) {
			continue
		}
		break
	}
	for i := range f.words {
		f.words[i].Store(0)
	}
	f.epoch.Store(f.epoch.Load() + 1)
}

// Seed returns the seed used by this filter. Keep it in memory if another
// filter must produce compatible bit positions.
func (f *Filter[T]) Seed() maphash.Seed { return f.seed }

// Parameters describes the sizing decisions made by the constructor.
type Parameters struct {
	Capacity          uint64
	Bits              uint64
	HashFunctions     uint64
	FalsePositiveRate float64
}

// Parameters returns the number of bits and hash functions used by the filter.
func (f *Filter[T]) Parameters() Parameters {
	return Parameters{
		Capacity:          f.capacity,
		Bits:              f.bits,
		HashFunctions:     f.k,
		FalsePositiveRate: f.rate,
	}
}

// EstimateFalsePositiveRate estimates the false-positive rate after n Add
// calls using the standard Bloom filter approximation.
func (f *Filter[T]) EstimateFalsePositiveRate(n uint64) float64 {
	return math.Pow(1-math.Exp(-float64(f.k)*float64(n)/float64(f.bits)), float64(f.k))
}

func (f *Filter[T]) hash(value T) (uint64, uint64) {
	h := f.hashPool.Get().(*maphash.Hash)
	h.SetSeed(f.seed)
	f.hasher.Hash(h, value)
	x := h.Sum64()
	h.Reset()
	f.hashPool.Put(h)
	return hashPair(x)
}

func hashPair(x uint64) (uint64, uint64) {
	// Two 32-bit halves provide the two streams used by double hashing.
	return x, bits.RotateLeft64(x, 32) | 1
}
