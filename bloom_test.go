package bloom

import (
	"fmt"
	"hash/maphash"
	"math"
	"runtime"
	"sync"
	"testing"
)

func TestFilterComparable(t *testing.T) {
	f, err := New[string](1000, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	f.Add("alice")
	if !f.Contains("alice") {
		t.Fatal("added value was not found")
	}
	if f.Contains("definitely-not-added") {
		t.Log("allowed false positive")
	}
}

func TestHashFilter(t *testing.T) {
	f, err := NewHash(1000, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	f.Add(42)
	if !f.Contains(42) {
		t.Fatal("added hash was not found")
	}
}

func TestHashFilterConcurrentAdd(t *testing.T) {
	f, err := NewHash(100_000, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	const (
		workers         = 16
		valuesPerWorker = 1_000
	)
	var wg sync.WaitGroup
	for worker := uint64(0); worker < workers; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := uint64(0); i < valuesPerWorker; i++ {
				f.Add((worker << 32) | i)
			}
		}()
	}
	wg.Wait()
	for worker := uint64(0); worker < workers; worker++ {
		for i := uint64(0); i < valuesPerWorker; i++ {
			if !f.Contains((worker << 32) | i) {
				t.Fatalf("concurrent Add lost hash worker=%d value=%d", worker, i)
			}
		}
	}
}

type user struct{ ID uint64 }

type userHasher struct{}

func (userHasher) Hash(h *maphash.Hash, u user) { maphash.WriteComparable(h, u.ID) }
func (userHasher) Equal(a, b user) bool         { return a.ID == b.ID }

func TestFilterCustomHasher(t *testing.T) {
	f, err := NewWithHasher[user](100, 0.01, userHasher{})
	if err != nil {
		t.Fatal(err)
	}
	f.Add(user{ID: 42})
	if !f.Contains(user{ID: 42}) {
		t.Fatal("custom-hashed value was not found")
	}
}

func TestFilterResetAndValidation(t *testing.T) {
	if _, err := New[string](0, 0.01); err != ErrInvalidCapacity {
		t.Fatalf("capacity error = %v", err)
	}
	if _, err := New[string](1, 1); err != ErrInvalidRate {
		t.Fatalf("rate error = %v", err)
	}
	var zeroSeed maphash.Seed
	if _, err := NewWithSeed[string](1, 0.01, maphash.ComparableHasher[string]{}, zeroSeed); err != ErrInvalidSeed {
		t.Fatalf("seed error = %v", err)
	}
	f, err := New[string](10, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	f.Add("x")
	f.Reset()
	if f.Contains("x") {
		t.Fatal("reset did not clear the filter")
	}
}

func TestFilterParameters(t *testing.T) {
	f, err := New[int](1000, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	p := f.Parameters()
	if p.Capacity != 1000 || p.Bits == 0 || p.HashFunctions == 0 || p.FalsePositiveRate != 0.01 {
		t.Fatalf("unexpected parameters: %+v", p)
	}
	if got := f.EstimateFalsePositiveRate(0); got != 0 {
		t.Fatalf("empty filter rate = %v", got)
	}
}

func TestFilterAdd(t *testing.T) {
	f, err := New[string](100, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	f.Add("a")
	f.Add("b")
	if !f.Contains("a") || !f.Contains("b") || f.Contains("missing") {
		t.Fatal("Add produced unexpected membership results")
	}
}

func TestFilterMerge(t *testing.T) {
	seed := maphash.MakeSeed()
	a, err := NewWithSeed[string](100, 0.01, maphash.ComparableHasher[string]{}, seed)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewWithSeed[string](100, 0.01, maphash.ComparableHasher[string]{}, seed)
	if err != nil {
		t.Fatal(err)
	}
	a.Add("a")
	b.Add("b")
	if err := a.Merge(b); err != nil {
		t.Fatal(err)
	}
	if !a.Contains("a") || !a.Contains("b") {
		t.Fatal("merge failed")
	}
	if err := a.Merge(a); err != nil {
		t.Fatal(err)
	}
}

func TestFilterSharedSeed(t *testing.T) {
	seed := maphash.MakeSeed()
	a, err := NewWithSeed[string](100, 0.01, maphash.ComparableHasher[string]{}, seed)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewWithSeed[string](100, 0.01, maphash.ComparableHasher[string]{}, seed)
	if err != nil {
		t.Fatal(err)
	}
	aHash1, aHash2 := a.hash("same-seed")
	bHash1, bHash2 := b.hash("same-seed")
	if aHash1 != bHash1 || aHash2 != bHash2 {
		t.Fatal("filters with the same seed produced different hashes")
	}
}

func BenchmarkAdd(b *testing.B) {
	f, err := New[string](100_000, 0.01)
	if err != nil {
		b.Fatal(err)
	}
	f.Contains("warmup")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.Add("benchmark-value")
	}
}

func BenchmarkContains(b *testing.B) {
	f, err := New[string](100_000, 0.01)
	if err != nil {
		b.Fatal(err)
	}
	f.Add("benchmark-value")
	f.Contains("warmup")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.Contains("benchmark-value")
	}
}

// TestObservedFalsePositiveRate exercises the complete filter at its requested
// capacity and checks the measured rate against the standard Bloom estimate.
// The interval is deliberately wide because the double-hashing streams are
// correlated rather than independent random functions.
func TestObservedFalsePositiveRate(t *testing.T) {
	const (
		capacity = 10_000
		queries  = 1_000_000
	)
	for _, rate := range []float64{0.1, 0.01, 0.001, 0.0001} {
		rate := rate
		t.Run(fmt.Sprintf("rate=%g", rate), func(t *testing.T) {
			f, err := New[uint64](capacity, rate)
			if err != nil {
				t.Fatal(err)
			}
			for i := uint64(0); i < capacity; i++ {
				f.Add(i)
			}
			falsePositives := 0
			for i := uint64(0); i < queries; i++ {
				// The high bit keeps every probe outside the inserted key set.
				if f.Contains(i | (uint64(1) << 63)) {
					falsePositives++
				}
			}
			observed := float64(falsePositives) / queries
			expected := f.EstimateFalsePositiveRate(capacity)
			margin := 8 * math.Sqrt(expected*(1-expected)/queries)
			if observed < expected-margin || observed > expected+margin {
				t.Fatalf("observed FPR=%g, expected=%g, wide CI=[%g, %g] (%d/%d)",
					observed, expected, expected-margin, expected+margin, falsePositives, queries)
			}
			t.Logf("requested=%g observed=%g expected=%g (%d/%d)", rate, observed, expected, falsePositives, queries)
		})
	}
}

func TestHashFilterObservedFalsePositiveRate(t *testing.T) {
	const (
		capacity = 10_000
		queries  = 1_000_000
	)
	mix := func(x uint64) uint64 {
		x += 0x9e3779b97f4a7c15
		x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
		x = (x ^ (x >> 27)) * 0x94d049bb133111eb
		return x ^ (x >> 31)
	}
	for _, rate := range []float64{0.1, 0.01, 0.001, 0.0001} {
		t.Run(fmt.Sprintf("rate=%g", rate), func(t *testing.T) {
			f, err := NewHash(capacity, rate)
			if err != nil {
				t.Fatal(err)
			}
			for i := uint64(0); i < capacity; i++ {
				f.Add(mix(i))
			}
			falsePositives := 0
			for i := uint64(capacity); i < capacity+queries; i++ {
				if f.Contains(mix(i)) {
					falsePositives++
				}
			}
			observed := float64(falsePositives) / queries
			if observed > rate*1.5+10/float64(queries) {
				t.Fatalf("observed FPR=%g exceeds target=%g (%d/%d)", observed, rate, falsePositives, queries)
			}
		})
	}
}

func BenchmarkContainsScenarios(b *testing.B) {
	for _, capacity := range []uint64{1_000, 100_000} {
		for _, rate := range []float64{0.1, 0.01, 0.001, 0.0001} {
			b.Run(fmt.Sprintf("capacity=%d/rate=%g", capacity, rate), func(b *testing.B) {
				f, err := New[uint64](capacity, rate)
				if err != nil {
					b.Fatal(err)
				}
				for i := uint64(0); i < capacity; i++ {
					f.Add(i)
				}
				b.Run("present", func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						f.Contains(1)
					}
				})
				b.Run("missing", func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						f.Contains(uint64(1) << 63)
					}
				})
				b.Run("random-unseen", func(b *testing.B) {
					var x uint64 = 0x9e3779b97f4a7c15
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						x ^= x << 7
						x ^= x >> 9
						f.Contains(x | (uint64(1) << 63))
					}
				})
			})
		}
	}
}

func BenchmarkParallelRunParallel(b *testing.B) {
	const (
		capacity = 100_000
		rate     = 0.01
	)
	for _, goroutines := range []int{1, 2, 4, 8, 16} {
		b.Run(fmt.Sprintf("goroutines=%d", goroutines), func(b *testing.B) {
			previous := runtime.GOMAXPROCS(goroutines)
			defer runtime.GOMAXPROCS(previous)
			f, err := New[uint64](capacity, rate)
			if err != nil {
				b.Fatal(err)
			}
			for i := uint64(0); i < capacity; i++ {
				f.Add(i)
			}
			b.SetParallelism(1)
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				var x uint64
				for pb.Next() {
					x++
					f.Contains(x | (uint64(1) << 63))
				}
			})
		})
	}
}

func BenchmarkParallelContention(b *testing.B) {
	for _, goroutines := range []int{1, 2, 4, 8, 16} {
		b.Run(fmt.Sprintf("goroutines=%d", goroutines), func(b *testing.B) {
			previous := runtime.GOMAXPROCS(goroutines)
			defer runtime.GOMAXPROCS(previous)
			f, err := New[uint64](100_000, 0.01)
			if err != nil {
				b.Fatal(err)
			}
			b.SetParallelism(1)
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					f.Add(0) // all workers OR the same words/cache lines
				}
			})
		})
	}
}

func BenchmarkReset(b *testing.B) {
	b.Run("Filter", func(b *testing.B) {
		f, err := New[uint64](100_000, 0.01)
		if err != nil {
			b.Fatal(err)
		}
		for i := uint64(0); i < 100_000; i++ {
			f.Add(i)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			f.Reset()
		}
	})

}

func BenchmarkAddWithConcurrentReset(b *testing.B) {
	b.Run("Filter", func(b *testing.B) {
		f, err := New[uint64](100_000, 0.01)
		if err != nil {
			b.Fatal(err)
		}
		add, reset := f.Add, f.Reset
		done := make(chan struct{})
		var resetWG sync.WaitGroup
		resetWG.Add(1)
		go func() {
			defer resetWG.Done()
			for {
				select {
				case <-done:
					return
				default:
					reset()
				}
			}
		}()
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			var x uint64
			for pb.Next() {
				x++
				add(x)
			}
		})
		b.StopTimer()
		close(done)
		resetWG.Wait()
	})
}
