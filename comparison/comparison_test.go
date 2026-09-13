package comparison_test

import (
	"bytes"
	"hash/maphash"
	"testing"

	bitsbloom "github.com/bits-and-blooms/bloom/v3"
	blobloom "github.com/greatroar/blobloom"
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
	b.Run("satorunooshie/bloomfilter-hash", func(b *testing.B) {
		f, err := bloom.NewHash(comparisonCapacity, comparisonRate)
		if err != nil {
			b.Fatal(err)
		}
		const valueHash = uint64(0x6f3c2a1e9d4b7085)
		f.Add(valueHash)
		b.ReportAllocs()
		for b.Loop() {
			f.Add(valueHash)
		}
	})

	b.Run("greatroar/blobloom", func(b *testing.B) {
		f := blobloom.NewOptimized(blobloom.Config{
			Capacity: comparisonCapacity,
			FPRate:   comparisonRate,
		})
		// Blobloom intentionally accepts a caller-provided 64-bit hash rather
		// than hashing []byte itself. Keep the hash outside the timed operation
		// so this measures the filter operation, as its API is designed for it.
		const valueHash = uint64(0x6f3c2a1e9d4b7085)
		f.Add(valueHash)
		b.ReportAllocs()
		for b.Loop() {
			f.Add(valueHash)
		}
	})
	b.Run("greatroar/blobloom-sync", func(b *testing.B) {
		f := blobloom.NewSyncOptimized(blobloom.Config{
			Capacity: comparisonCapacity,
			FPRate:   comparisonRate,
		})
		const valueHash = uint64(0x6f3c2a1e9d4b7085)
		f.Add(valueHash)
		b.ReportAllocs()
		for b.Loop() {
			f.Add(valueHash)
		}
	})

	b.Run("satorunooshie/bloomfilter", func(b *testing.B) {
		f, err := bloom.NewWithHasher[[]byte](comparisonCapacity, comparisonRate, comparisonBytesHasher{})
		if err != nil {
			b.Fatal(err)
		}
		f.Add(comparisonValue) // warm the hash pool
		b.ReportAllocs()
		for b.Loop() {
			f.Add(comparisonValue)
		}
	})

	b.Run("bits-and-blooms/bloom", func(b *testing.B) {
		f := bitsbloom.NewWithEstimates(comparisonCapacity, comparisonRate)
		b.ReportAllocs()
		for b.Loop() {
			f.Add(comparisonValue)
		}
	})

	b.Run("phrozen/bloom", func(b *testing.B) {
		f := phrozenbloom.NewFilterFromProbability(comparisonCapacity, comparisonRate)
		b.ReportAllocs()
		for b.Loop() {
			f.Add(comparisonValue)
		}
	})
}

func BenchmarkComparisonContains(b *testing.B) {
	b.Run("satorunooshie/bloomfilter-hash", func(b *testing.B) {
		f, err := bloom.NewHash(comparisonCapacity, comparisonRate)
		if err != nil {
			b.Fatal(err)
		}
		const valueHash = uint64(0x6f3c2a1e9d4b7085)
		f.Add(valueHash)
		b.ReportAllocs()
		for b.Loop() {
			f.Contains(valueHash)
		}
	})

	b.Run("greatroar/blobloom", func(b *testing.B) {
		f := blobloom.NewOptimized(blobloom.Config{
			Capacity: comparisonCapacity,
			FPRate:   comparisonRate,
		})
		const valueHash = uint64(0x6f3c2a1e9d4b7085)
		f.Add(valueHash)
		b.ReportAllocs()
		for b.Loop() {
			f.Has(valueHash)
		}
	})
	b.Run("greatroar/blobloom-sync", func(b *testing.B) {
		f := blobloom.NewSyncOptimized(blobloom.Config{
			Capacity: comparisonCapacity,
			FPRate:   comparisonRate,
		})
		const valueHash = uint64(0x6f3c2a1e9d4b7085)
		f.Add(valueHash)
		b.ReportAllocs()
		for b.Loop() {
			f.Has(valueHash)
		}
	})

	b.Run("satorunooshie/bloomfilter", func(b *testing.B) {
		f, err := bloom.NewWithHasher[[]byte](comparisonCapacity, comparisonRate, comparisonBytesHasher{})
		if err != nil {
			b.Fatal(err)
		}
		f.Add(comparisonValue)
		b.ReportAllocs()
		for b.Loop() {
			f.Contains(comparisonValue)
		}
	})

	b.Run("bits-and-blooms/bloom", func(b *testing.B) {
		f := bitsbloom.NewWithEstimates(comparisonCapacity, comparisonRate)
		f.Add(comparisonValue)
		b.ReportAllocs()
		for b.Loop() {
			f.Test(comparisonValue)
		}
	})

	b.Run("phrozen/bloom", func(b *testing.B) {
		f := phrozenbloom.NewFilterFromProbability(comparisonCapacity, comparisonRate)
		f.Add(comparisonValue)
		b.ReportAllocs()
		for b.Loop() {
			f.Contains(comparisonValue)
		}
	})
}
