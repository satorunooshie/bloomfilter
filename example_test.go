package bloom_test

import (
	"fmt"
	"hash/maphash"

	"github.com/satorunooshie/bloomfilter"
)

func ExampleNew() {
	filter, err := bloom.New[string](1000, 0.01)
	if err != nil {
		panic(err)
	}
	filter.Add("alice")
	fmt.Println(filter.Contains("alice"))
	// Output: true
}

type account struct{ ID uint64 }

type accountHasher struct{}

func (accountHasher) Hash(h *maphash.Hash, a account) {
	maphash.WriteComparable(h, a.ID)
}

// Equal is required by maphash.Hasher, but Bloom filters only need Hash.
func (accountHasher) Equal(a, b account) bool { return a.ID == b.ID }

func ExampleNewWithHasher() {
	filter, err := bloom.NewWithHasher[account](1000, 0.01, accountHasher{})
	if err != nil {
		panic(err)
	}
	filter.Add(account{ID: 42})
	fmt.Println(filter.Contains(account{ID: 42}))
	// Output: true
}
