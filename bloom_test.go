package bloom

import (
	"testing"

	"hash/maphash"
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
