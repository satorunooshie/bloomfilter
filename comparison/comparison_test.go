package comparison_test

import (
	"bytes"
	"hash/maphash"
	"testing"

	bitsbloom "github.com/bits-and-blooms/bloom/v3"
	phrozenbloom "github.com/phrozen/bloom"
	bloom "github.com/satorunooshie/bloomfilter"
)

const comparisonCapacity = 100_000

const comparisonRate = 0.01

var comparisonValue = []byte("a stable benchmark key")

type comparisonBytesHasher struct{}

func (comparisonBytesHasher) Hash(h *maphash.Hash, value []byte) {
	_, _ = h.Write(value)
}

func (comparisonBytesHasher) Equal(a, b []byte) bool { return bytes.Equal(a, b) }

func BenchmarkComparisonAdd(b *testing.B) {
	b.Run("satorunooshie/bloomfilter", func(b *testing.B) {
		f, err := bloom.NewWithHasher[[]byte](comparisonCapacity, comparisonRate, comparisonBytesHasher{})
		if err != nil {
			b.Fatal(err)
		}
		f.Add(comparisonValue) // warm the hash pool
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			f.Add(comparisonValue)
		}
	})

	b.Run("bits-and-blooms/bloom", func(b *testing.B) {
		f := bitsbloom.NewWithEstimates(comparisonCapacity, comparisonRate)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			f.Add(comparisonValue)
		}
	})

	b.Run("phrozen/bloom", func(b *testing.B) {
		f := phrozenbloom.NewFilterFromProbability(comparisonCapacity, comparisonRate)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			f.Add(comparisonValue)
		}
	})
}

func BenchmarkComparisonContains(b *testing.B) {
	b.Run("satorunooshie/bloomfilter", func(b *testing.B) {
		f, err := bloom.NewWithHasher[[]byte](comparisonCapacity, comparisonRate, comparisonBytesHasher{})
		if err != nil {
			b.Fatal(err)
		}
		f.Add(comparisonValue)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			f.Contains(comparisonValue)
		}
	})

	b.Run("bits-and-blooms/bloom", func(b *testing.B) {
		f := bitsbloom.NewWithEstimates(comparisonCapacity, comparisonRate)
		f.Add(comparisonValue)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			f.Test(comparisonValue)
		}
	})

	b.Run("phrozen/bloom", func(b *testing.B) {
		f := phrozenbloom.NewFilterFromProbability(comparisonCapacity, comparisonRate)
		f.Add(comparisonValue)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			f.Contains(comparisonValue)
		}
	})
}
