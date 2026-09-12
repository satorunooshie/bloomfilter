package portable

import (
	"bytes"
	"testing"
)

func TestRoundTripAndMerge(t *testing.T) {
	a, err := New(1000, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	a.Add([]byte("alice"))
	data, err := a.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	b := new(Filter)
	if err := b.UnmarshalBinary(data); err != nil {
		t.Fatal(err)
	}
	if !b.Contains([]byte("alice")) {
		t.Fatal("round trip lost value")
	}

	c, err := New(1000, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	c.Add([]byte("bob"))
	if err := b.Merge(c); err != nil {
		t.Fatal(err)
	}
	if !b.Contains([]byte("bob")) {
		t.Fatal("merge lost value")
	}
}

func TestInvalidSerializedData(t *testing.T) {
	f := new(Filter)
	if err := f.UnmarshalBinary([]byte("bad")); err != ErrInvalidData {
		t.Fatalf("error = %v", err)
	}
}

func TestChecksumDetectsCorruption(t *testing.T) {
	f, err := New(100, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	f.Add([]byte("value"))
	data, err := f.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 1
	if err := new(Filter).UnmarshalBinary(data); err != ErrInvalidData {
		t.Fatalf("corrupt data error = %v", err)
	}
}

func TestChecksumDetectsHeaderCorruption(t *testing.T) {
	f, err := New(100, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	data, err := f.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	// Changing k does not change the payload length, so this specifically
	// verifies that the checksum covers the header as well as the bitset.
	data[24]++
	if err := new(Filter).UnmarshalBinary(data); err != ErrInvalidData {
		t.Fatalf("corrupt header error = %v", err)
	}
}

func TestReaderWriter(t *testing.T) {
	f, err := New(100, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	f.Add([]byte("reader-writer"))
	var buf bytes.Buffer
	n, err := f.WriteTo(&buf)
	if err != nil || n != int64(buf.Len()) {
		t.Fatalf("WriteTo: n=%d err=%v", n, err)
	}
	restored := new(Filter)
	n, err = restored.ReadFrom(&buf)
	if err != nil || n == 0 || !restored.Contains([]byte("reader-writer")) {
		t.Fatalf("ReadFrom: n=%d err=%v", n, err)
	}
}
