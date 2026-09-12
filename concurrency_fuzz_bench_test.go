package bloom

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"testing"
)

// TestFilterConcurrentOperations is intentionally suitable for -race. Reset
// exercises the generation retry path while readers and writers are active.
func TestFilterConcurrentOperations(t *testing.T) {
	f, err := New[uint64](10_000, 0.01)
	if err != nil {
		t.Fatal(err)
	}

	const workers = 8
	var wg sync.WaitGroup
	var workersWG sync.WaitGroup
	var ready sync.WaitGroup
	start := make(chan struct{})
	workersDone := make(chan struct{})

	for worker := range uint64(workers) {
		worker := worker
		ready.Add(1)
		wg.Add(1)
		workersWG.Add(1)
		go func() {
			defer wg.Done()
			defer workersWG.Done()
			ready.Done()
			<-start
			for i := range uint64(5_000) {
				value := worker<<32 | i
				f.Add(value)
				f.Contains(value)
			}
		}()
	}
	ready.Wait()
	close(start)
	go func() {
		workersWG.Wait()
		close(workersDone)
	}()
	wg.Go(func() {
		for {
			select {
			case <-workersDone:
				return
			default:
				f.Reset()
			}
		}
	})
	wg.Wait()
}

func TestHashFilterConcurrentOperations(t *testing.T) {
	f, err := NewHash(10_000, 0.01)
	if err != nil {
		t.Fatal(err)
	}

	const workers = 8
	var wg sync.WaitGroup
	for worker := range uint64(workers) {
		worker := worker
		wg.Go(func() {
			for i := range uint64(5_000) {
				value := mixHash(worker<<32 | i)
				f.Add(value)
				f.Contains(value)
			}
		})
	}
	wg.Wait()
}

// FuzzFilterStateMachine checks membership, false-negative prevention, and
// Reset semantics. Every two input bytes describe one operation, so fuzzing
// explores arbitrary Add/Contains/Reset sequences without relying on timing.
func FuzzFilterStateMachine(f *testing.F) {
	for _, seed := range [][]byte{nil, []byte("bloom"), []byte{0, 1, 2, 3, 4, 5, 6, 7}} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 4096 {
			input = input[:4096]
		}
		filter, err := New[uint64](256, 0.01)
		if err != nil {
			t.Fatal(err)
		}
		seen := make(map[uint64]struct{})
		for offset := 0; offset+1 < len(input); offset += 2 {
			value := binary.LittleEndian.Uint16(input[offset : offset+2])
			switch input[offset] % 3 {
			case 0:
				filter.Add(uint64(value))
				seen[uint64(value)] = struct{}{}
			case 1:
				if _, added := seen[uint64(value)]; added && !filter.Contains(uint64(value)) {
					t.Fatalf("false negative for %d", value)
				}
			case 2:
				filter.Reset()
				seen = make(map[uint64]struct{})
			}
		}
	})
}

// TestMeasuredFalsePositiveRateProperty checks observed FPR against the
// configured target with a generous statistical margin. The deterministic
// input mixer avoids accidentally testing sequential, low-entropy hashes.
func TestMeasuredFalsePositiveRateProperty(t *testing.T) {
	const (
		capacity = 5_000
		queries  = 250_000
	)
	for _, rate := range []float64{0.1, 0.01, 0.001, 0.0001} {
		t.Run(fmt.Sprintf("rate=%g", rate), func(t *testing.T) {
			filter, err := NewHash(capacity, rate)
			if err != nil {
				t.Fatal(err)
			}
			for i := range uint64(capacity) {
				filter.Add(mixHash(i))
			}
			falsePositives := 0
			for i := uint64(capacity); i < capacity+queries; i++ {
				if filter.Contains(mixHash(i)) {
					falsePositives++
				}
			}
			observed := float64(falsePositives) / queries
			expected := filter.EstimateFalsePositiveRate(capacity)
			// HashFilter uses blocked probing, so the vanilla Bloom estimate is
			// useful as a diagnostic but is not the contract being tested here.
			margin := 8*math.Sqrt(rate*(1-rate)/queries) + 5/float64(queries)
			upperBound := rate + margin
			if observed > upperBound {
				t.Fatalf("observed FPR=%g, target=%g, upper bound=%g, vanilla estimate=%g (%d/%d)", observed, rate, upperBound, expected, falsePositives, queries)
			}
		})
	}
}

func mixHash(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func BenchmarkParallelScalingScenarios(b *testing.B) {
	const (
		capacity = 100_000
		rate     = 0.01
	)
	for _, goroutines := range []int{16, 32, 64} {
		for _, scenario := range []string{"hit", "miss", "mixed"} {
			b.Run(fmt.Sprintf("goroutines=%d/%s", goroutines, scenario), func(b *testing.B) {
				filter, err := New[uint64](capacity, rate)
				if err != nil {
					b.Fatal(err)
				}
				for i := range uint64(capacity) {
					filter.Add(i)
				}
				b.ReportAllocs()
				benchmarkWorkers(b, goroutines, func(i uint64) {
					value := i % capacity
					if scenario == "miss" || scenario == "mixed" && i&1 != 0 {
						value |= uint64(1) << 63
					}
					filter.Contains(value)
				})
			})
		}
	}
}

// benchmarkWorkers measures exactly b.N operations using exactly workers
// goroutines. GOMAXPROCS is intentionally left unchanged so this benchmark
// varies application concurrency independently from runtime CPU parallelism.
func benchmarkWorkers(b *testing.B, workers int, operation func(uint64)) {
	var ready, wg sync.WaitGroup
	start := make(chan struct{})
	ready.Add(workers)
	wg.Add(workers)
	for worker := range workers {
		first := uint64(b.N) * uint64(worker) / uint64(workers)
		last := uint64(b.N) * uint64(worker+1) / uint64(workers)
		go func(first, last uint64) {
			defer wg.Done()
			ready.Done()
			<-start
			for i := first; i < last; i++ {
				operation(i)
			}
		}(first, last)
	}
	ready.Wait()
	b.ResetTimer()
	close(start)
	wg.Wait()
}
