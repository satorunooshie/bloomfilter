# bloomfilter

A small, fast Bloom filter for Go 1.27+.

The library provides concurrent in-memory filters for Go applications, plus a
portable filter for persistence and cross-process sharing. A small CLI for the
portable filter is included as well.

The in-memory `Add` and `Contains` hot paths perform zero allocations in the
built-in implementations, and the library itself has no external runtime
dependencies.

```sh
go get github.com/satorunooshie/bloomfilter
```

The module path is `github.com/satorunooshie/bloomfilter`; the package name in
Go source is `bloom`.

## Library

### Benchmarks

The library provides benchmarks for concurrent throughput, key distributions,
multiple capacities/FPRs, cache-line contention, and Reset behavior.

#### Prehashed concurrent operations

This comparison measures the filter operation itself: callers provide an
already-computed `uint64` hash. It compares concurrent implementations. Both
`HashFilter` and `blobloom.SyncFilter`
are safe for concurrent Add/Contains and have no Reset method, so their hot
paths have no Reset coordination.

| Operation | `HashFilter` (atomic, no Reset) | `blobloom.SyncFilter` (atomic, no Reset) |
| --- | ---: | ---: |
| Add | 3.42 ns/op | 4.90 ns/op |
| Contains | 3.03 ns/op | 3.52 ns/op |

#### Key-oriented operations (hashing included)

This comparison includes hashing in each operation. `Filter[T]` is the
key-based API in this library:

| Operation | `Filter[T]` (this library) | `bits-and-blooms` | `phrozen` |
| --- | ---: | ---: | ---: |
| Add | 20.90 ns/op | 32.98 ns/op | 26.06 ns/op |
| Contains | 19.25 ns/op | 22.39 ns/op | 25.98 ns/op |

For reference, the non-concurrent `blobloom.Filter` measured 3.53 ns/op for
Add and 3.38 ns/op for Contains. It provides `Clear()` but is not safe for
concurrent updates.

These results were measured on an Apple M4 Max with Go 1.27.1. Results vary by
CPU, Go version, and system load. Run the benchmarks yourself with:

```sh
# Main package benchmarks.
go test -run '^$' -bench='Benchmark(ContainsScenarios|Parallel|Reset)' -benchmem ./...

# Library comparison benchmarks.
cd comparison
go test -run '^$' -bench='BenchmarkComparison' -benchmem -count=5 .
```

The concurrent scaling benchmark measures `Filter[uint64].Contains` with
exactly 16, 32, or 64 worker goroutines. `GOMAXPROCS` is left at its runtime
default so goroutine concurrency is measured independently from CPU parallelism.
Each case uses 100% hit, 100% miss, or a 50/50 mixed workload:

```sh
go test -run '^$' -bench='^BenchmarkParallelScalingScenarios$' \
  -benchmem -benchtime=1s .
```

Reference results from an Apple M4 Max with Go 1.27.1 (`-benchtime=1s`):

| Worker goroutines | 100% hit | 100% miss | Mixed |
| ---: | ---: | ---: | ---: |
| 16 | 1.82 ns/op | 2.41 ns/op | 2.16 ns/op |
| 32 | 1.77 ns/op | 2.57 ns/op | 2.24 ns/op |
| 64 | 1.86 ns/op | 2.57 ns/op | 2.24 ns/op |

These are reference measurements only; results vary with CPU, Go version, and
system load. All scenarios measured 0 B/op and 0 allocs/op in this run.

### Native Go types

Comparable Go types work with `New` automatically:

```go
filter, err := bloom.New[int](100_000, 0.01)
```

For any other type, provide a `maphash.Hasher[T]`:

```go
type User struct {
	ID uint64
}

type UserHasher struct{}

func (UserHasher) Hash(h *maphash.Hash, user User) {
	maphash.WriteComparable(h, user.ID)
}

func (UserHasher) Equal(a, b User) bool {
	return a.ID == b.ID
}

filter, err := bloom.NewWithHasher[User](100_000, 0.01, UserHasher{})
```

Values are hashed in their native Go type, so callers do not need to convert
keys to strings or `[]byte` first. Bloom filters use `Hash`; `Equal` is required
by the `maphash.Hasher[T]` interface but is not used by this package.

### Usage

Pass keys directly with `Filter[T]`:

```go
filter, err := bloom.New[string](100_000, 0.01)
if err != nil {
	panic(err)
}

filter.Add("alice")
if filter.Contains("alice") {
	// The key may be present.
}
```

Use `HashFilter` when the application already has a high-quality 64-bit hash,
or when hash calculation must be controlled separately from the filter. It is
the low-level speed-oriented API:

```go
seed := maphash.MakeSeed()
var h maphash.Hash
h.SetSeed(seed)
h.WriteString("alice")
keyHash := h.Sum64()

filter, err := bloom.NewHash(100_000, 0.01)
if err != nil {
	panic(err)
}
filter.Add(keyHash)
filter.Contains(keyHash)
```

`HashFilter` does not hash keys, use `sync.Pool`, or provide `Reset`. To start a
new generation, create a new filter and replace the pointer externally if the
application needs that behavior. This is an application-level pattern, not a
separate exported filter type:

```go
var current atomic.Pointer[bloom.HashFilter]

next, _ := bloom.NewHash(100_000, 0.01)
current.Store(next)

current.Load().Add(keyHash)
```

The supplied hash must be well mixed and approximately uniform across 64 bits.
Passing raw sequential integers can cause poor block distribution and a much
higher false-positive rate.

Bloom filters do not support deletion. A `false` result means the value was
definitely not added; `true` means it may have been added and can be a false
positive. Choose `capacity` for the expected number of distinct insertions and
`falsePositiveRate` for the desired false-positive rate at that capacity.
Adding substantially more values than the configured capacity increases the
false-positive rate.

### Concurrency and Reset

`Filter[T]` and `HashFilter` both support concurrent `Add` and `Contains`.
Their bitsets use atomic operations and have no mutex on the operation hot path.
The package describes this as a lock-free atomic bitset; it does not make a
formal lock-free progress guarantee for the hash implementation or
`sync.Pool`.

`Filter[T].Reset` may run concurrently with `Add` and `Contains`. Operations
that overlap Reset are retried against the new generation, so an Add that
completes after Reset returns is retained.

`HashFilter` intentionally has no Reset coordination in its hot path. Replace
the filter instance externally when a new generation is needed. This keeps its
`Add` and `Contains` operations as fast as possible. If rotation is not needed,
use `HashFilter` directly.

`Filter` uses a process-local random `maphash.Seed`, so independently-created
filters cannot be merged. Use a shared seed when compatible filters must be
merged:

```go
seed := maphash.MakeSeed()

left, _ := bloom.NewWithSeed[string](100_000, 0.01,
	maphash.ComparableHasher[string]{}, seed)
right, _ := bloom.NewWithSeed[string](100_000, 0.01,
	maphash.ComparableHasher[string]{}, seed)

left.Merge(right)
```

The seed is process-local and cannot be persisted or reconstructed after a
restart. The zero value of either filter is not ready for use; use a
constructor.

### False-positive validation

The test suite fills filters to capacity and probes one million unseen keys for
target rates `0.1`, `0.01`, `0.001`, and `0.0001`. The key-oriented filter is
checked against a confidence interval around the expected Bloom estimate.
The HashFilter test uses a conservative upper bound because blocked filters
have different occupancy behavior.

```sh
go test -run 'FalsePositiveRate' -count=1 ./...
```

### Portable filters

When a filter must be persisted or shared across processes, use the
`portable` package:

```go
import "github.com/satorunooshie/bloomfilter/portable"

filter, err := portable.New(100_000, 0.01)
if err != nil {
	panic(err)
}

filter.Add([]byte("alice"))
data, err := filter.MarshalBinary()
```

`portable.Filter` uses a deterministic FNV-1a hash and supports:

- `MarshalBinary` / `UnmarshalBinary`
- `ReadFrom` / `WriteTo`
- `Merge`
- concurrent `Add`, `Contains`, and serialization

It is a separate hash space from the main `maphash`-based filter. Its
serialized format is versioned and checksummed. `ReadFrom` reads exactly one
filter representation and leaves following bytes in the reader untouched.

## Command-line interface

The repository includes a small line-oriented CLI for creating, inserting
into, and checking portable filters. It stores the filter in a file and reads
keys from standard input:

```sh
go run ./cmd/bloom create -capacity 100000 -rate 0.01 -file users.bloom
printf 'alice\nbob\n' | go run ./cmd/bloom insert -file users.bloom
printf 'alice\ncarol\n' | go run ./cmd/bloom check -file users.bloom
```

## License

See the repository license.
