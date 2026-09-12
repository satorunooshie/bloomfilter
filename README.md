# bloomfilter

A small, fast Bloom filter for Go 1.27+.

The main package uses Go 1.27's generic `maphash.Hasher[T]` API. Values are
hashed in their native Go type, so callers do not need to convert keys to
strings or `[]byte` first.

```sh
go get github.com/satorunooshie/bloomfilter
```

## Basic use

```go
package main

import (
	"fmt"

	"github.com/satorunooshie/bloomfilter"
)

func main() {
	filter, err := bloom.New[string](100_000, 0.01)
	if err != nil {
		panic(err)
	}

	filter.Add("alice")
	fmt.Println(filter.Contains("alice")) // true
}
```

`Contains` is probabilistic:

- `false` means the value was definitely not added.
- `true` means the value may have been added and can be a false positive.

Choose `capacity` for the expected number of insertions and
`falsePositiveRate` for the target false-positive rate. A Bloom filter does
not support deletion.

## Native Go types

Comparable types work out of the box:

```go
filter, err := bloom.New[int](100_000, 0.01)
```

For any type, provide a `maphash.Hasher[T]`:

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

Bloom filters use `Hash`; `Equal` is required by the `maphash.Hasher[T]`
interface but is not used by this package.

## Concurrency

The main package is designed for concurrent in-memory workloads:

- `Add` and `Contains` are safe to call concurrently.
- The bitset uses atomic words and does not use a lock on the hot path.
- `Reset` may run concurrently; an `Add` racing with `Reset` may be cleared.

Each filter has a process-local `maphash.Seed`. Filters created independently
cannot be merged. Share a seed when filters must use the same bit positions:

```go
seed := maphash.MakeSeed()

left, _ := bloom.NewWithSeed[string](100_000, 0.01,
	maphash.ComparableHasher[string]{}, seed)
right, _ := bloom.NewWithSeed[string](100_000, 0.01,
	maphash.ComparableHasher[string]{}, seed)

left.Merge(right)
```

The seed is process-local and cannot be persisted or reconstructed after a
restart. The zero value of `Filter` is not ready for use; use a constructor.

## Portable filters

When a filter must be persisted or shared across processes, use the
`portable` package instead:

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

The repository also includes a small line-oriented CLI for portable filters:

```sh
go run ./cmd/bloom create -capacity 100000 -rate 0.01 -file users.bloom
printf 'alice\nbob\n' | go run ./cmd/bloom insert -file users.bloom
printf 'alice\ncarol\n' | go run ./cmd/bloom check -file users.bloom
```

## Benchmarks

Single-threaded comparison on an Apple M4 Max with Go 1.27.1. The comparison
uses the same `[]byte` input and reports zero allocations per operation.

| Operation | bloomfilter | bits-and-blooms | phrozen |
| --- | ---: | ---: | ---: |
| Add | 21.2 ns/op | 33.7 ns/op | 26.4 ns/op |
| Contains | 19.6 ns/op | 22.7 ns/op | 26.2 ns/op |

Run the comparison yourself with:

```sh
cd comparison
go test -run '^$' -bench='BenchmarkComparison' -benchmem -count=5 ./...
```

Results vary with the CPU, Go version, and system load.

## License

See the repository license.
