// Package portable provides a deterministic, serializable Bloom filter for
// byte strings. Use bloom.Filter when persistence and cross-process sharing
// are not required and Go 1.27's maphash is preferred.
package portable

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash"
	"hash/crc32"
	"io"
	"math"
	"sync"
	"sync/atomic"

	"github.com/satorunooshie/bloomfilter/internal/core"
)

var (
	ErrInvalidCapacity = errors.New("portable: capacity must be greater than zero")
	ErrInvalidRate     = errors.New("portable: false-positive rate must be greater than zero and less than one")
	ErrInvalidData     = errors.New("portable: invalid serialized filter")
	ErrIncompatible    = errors.New("portable: filters have incompatible parameters")
)

const (
	headerSize       = 40
	formatVersion    = uint32(1)
	algorithmFNV1a64 = uint32(1)
)

var magic = [4]byte{'B', 'L', 'M', '1'}

var nextFilterID atomic.Uint64

var _ io.WriterTo = (*Filter)(nil)

// Filter is a deterministic byte-oriented Bloom filter. Its serialized form
// can be restored by another process or machine. The zero value is not ready
// for use; create filters with New or restore one with UnmarshalBinary or
// ReadFrom.
type Filter struct {
	noCopy  noCopy
	mergeID atomic.Uint64
	mu      sync.RWMutex
	words   []uint64
	bits    uint64
	k       uint64
}

// noCopy makes go vet flag accidental copies of a live Filter.
type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}

// New creates a portable filter for the expected capacity and false-positive
// rate. It uses the stable FNV-1a 64-bit hash from this package.
func New(capacity uint64, falsePositiveRate float64) (*Filter, error) {
	if capacity == 0 {
		return nil, ErrInvalidCapacity
	}
	if !(falsePositiveRate > 0 && falsePositiveRate < 1) || math.IsNaN(falsePositiveRate) {
		return nil, ErrInvalidRate
	}
	m, k, ok := core.Parameters(capacity, falsePositiveRate)
	if !ok {
		return nil, ErrInvalidCapacity
	}
	words := (m + 63) / 64
	f := &Filter{words: make([]uint64, int(words)), bits: words * 64, k: k}
	f.mergeID.Store(newFilterID())
	return f, nil
}

// Add inserts value into the filter.
func (f *Filter) Add(value []byte) {
	h1 := fnv64(value)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addHash(h1)
}

func (f *Filter) addHash(h1 uint64) {
	h2 := (h1 >> 32) | 1
	for i := uint64(0); i < f.k; i++ {
		index := core.Probe(h1, h2, i, f.bits)
		f.words[index>>6] |= uint64(1) << (index & 63)
	}
}

// Contains reports whether value may have been added. A false result is
// definitive; a true result may be a false positive.
func (f *Filter) Contains(value []byte) bool {
	h1 := fnv64(value)
	f.mu.RLock()
	defer f.mu.RUnlock()
	h2 := (h1 >> 32) | 1
	for i := uint64(0); i < f.k; i++ {
		index := core.Probe(h1, h2, i, f.bits)
		if f.words[index>>6]&(uint64(1)<<(index&63)) == 0 {
			return false
		}
	}
	return true
}

// Reset removes all values while retaining the filter parameters.
func (f *Filter) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	clear(f.words)
}

// Parameters describes the serialized filter's sizing parameters.
type Parameters struct {
	Bits          uint64
	HashFunctions uint64
}

// Parameters returns the number of bits and hash functions used by the filter.
func (f *Filter) Parameters() Parameters {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return Parameters{Bits: f.bits, HashFunctions: f.k}
}

// EstimateFalsePositiveRate estimates the false-positive rate after n Add
// calls using the standard Bloom filter approximation.
func (f *Filter) EstimateFalsePositiveRate(n uint64) float64 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return math.Pow(1-math.Exp(-float64(f.k)*float64(n)/float64(f.bits)), float64(f.k))
}

// Merge combines two portable filters with the same parameters.
func (f *Filter) Merge(other *Filter) error {
	if f == other {
		return nil
	}
	if other == nil {
		return ErrIncompatible
	}
	// Lock both filters in a stable process-local order. This avoids the
	// deadlock that would occur if two goroutines merged the same pair in
	// opposite directions, without serializing unrelated merges globally.
	fID := f.ensureMergeID()
	otherID := other.ensureMergeID()
	if fID < otherID {
		f.mu.Lock()
		defer f.mu.Unlock()
		other.mu.RLock()
		defer other.mu.RUnlock()
	} else {
		other.mu.RLock()
		defer other.mu.RUnlock()
		f.mu.Lock()
		defer f.mu.Unlock()
	}
	if f.bits != other.bits || f.k != other.k {
		return ErrIncompatible
	}
	for i := range f.words {
		f.words[i] |= other.words[i]
	}
	return nil
}

// MarshalBinary serializes parameters, the algorithm identifier, a checksum,
// and the bitset in a versioned format.
func (f *Filter) MarshalBinary() ([]byte, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	data := make([]byte, headerSize+len(f.words)*8)
	copy(data[:4], magic[:])
	binary.LittleEndian.PutUint32(data[4:8], formatVersion)
	binary.LittleEndian.PutUint32(data[8:12], algorithmFNV1a64)
	binary.LittleEndian.PutUint64(data[16:24], f.bits)
	binary.LittleEndian.PutUint64(data[24:32], f.k)
	for i, word := range f.words {
		binary.LittleEndian.PutUint64(data[headerSize+i*8:], word)
	}
	binary.LittleEndian.PutUint32(data[32:36], checksum(data[:headerSize], data[headerSize:]))
	return data, nil
}

// WriteTo writes the binary representation to w.
func (f *Filter) WriteTo(w io.Writer) (int64, error) {
	data, err := f.MarshalBinary()
	if err != nil {
		return 0, err
	}
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return int64(n), err
}

// UnmarshalBinary restores a filter serialized by MarshalBinary.
func (f *Filter) UnmarshalBinary(data []byte) error {
	if len(data) < headerSize || !bytes.Equal(data[:4], magic[:]) {
		return ErrInvalidData
	}
	m, k, wordCount, ok := parseHeader(data[:headerSize])
	if !ok || uint64(len(data)-headerSize) != m/8 {
		return ErrInvalidData
	}
	if binary.LittleEndian.Uint32(data[32:36]) != checksum(data[:headerSize], data[headerSize:]) {
		return ErrInvalidData
	}
	words := decodeWords(data[headerSize:], int(wordCount))
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureMergeID()
	f.bits, f.k, f.words = m, k, words
	return nil
}

// ReadFrom reads one binary representation from r.
//
// The representation is self-framing: its payload length is determined by
// the header. ReadFrom therefore consumes exactly one filter and leaves any
// following bytes in r untouched. It intentionally does not implement
// io.ReaderFrom, whose contract requires reading until EOF.
func (f *Filter) ReadFrom(r io.Reader) (int64, error) {
	header := make([]byte, headerSize)
	n, err := io.ReadFull(r, header)
	if err != nil {
		return int64(n), err
	}
	m, k, wordCount, ok := parseHeader(header)
	if !ok {
		return int64(n), ErrInvalidData
	}
	words := make([]uint64, int(wordCount))
	h := newChecksum(header)
	var encoded [8]byte
	for i := range words {
		nn, readErr := io.ReadFull(r, encoded[:])
		n += nn
		if readErr != nil {
			return int64(n), readErr
		}
		_, _ = h.Write(encoded[:])
		words[i] = binary.LittleEndian.Uint64(encoded[:])
	}
	if binary.LittleEndian.Uint32(header[32:36]) != h.Sum32() {
		return int64(n), ErrInvalidData
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureMergeID()
	f.bits, f.k, f.words = m, k, words
	return int64(n), nil
}

func newFilterID() uint64 {
	return nextFilterID.Add(1)
}

func (f *Filter) ensureMergeID() uint64 {
	if id := f.mergeID.Load(); id != 0 {
		return id
	}
	id := newFilterID()
	if f.mergeID.CompareAndSwap(0, id) {
		return id
	}
	return f.mergeID.Load()
}

func parseHeader(header []byte) (bits, hashFunctions, wordCount uint64, ok bool) {
	if len(header) != headerSize || !bytes.Equal(header[:4], magic[:]) {
		return 0, 0, 0, false
	}
	if binary.LittleEndian.Uint32(header[4:8]) != formatVersion || binary.LittleEndian.Uint32(header[8:12]) != algorithmFNV1a64 {
		return 0, 0, 0, false
	}
	if binary.LittleEndian.Uint32(header[12:16]) != 0 || binary.LittleEndian.Uint32(header[36:40]) != 0 {
		return 0, 0, 0, false
	}
	bits = binary.LittleEndian.Uint64(header[16:24])
	hashFunctions = binary.LittleEndian.Uint64(header[24:32])
	if bits < 64 || bits%64 != 0 || hashFunctions == 0 || hashFunctions > bits {
		return 0, 0, 0, false
	}
	wordCount = bits / 64
	maxInt := uint64(^uint(0) >> 1)
	if wordCount > maxInt/8 {
		return 0, 0, 0, false
	}
	return bits, hashFunctions, wordCount, true
}

func checksum(header, payload []byte) uint32 {
	h := newChecksum(header)
	_, _ = h.Write(payload)
	return h.Sum32()
}

func newChecksum(header []byte) hash.Hash32 {
	h := crc32.NewIEEE()
	_, _ = h.Write(header[:32])
	_, _ = h.Write(header[36:])
	return h
}

func decodeWords(data []byte, count int) []uint64 {
	words := make([]uint64, count)
	for i := range words {
		words[i] = binary.LittleEndian.Uint64(data[i*8:])
	}
	return words
}

func fnv64(data []byte) uint64 {
	const offset = uint64(14695981039346656037)
	const prime = uint64(1099511628211)
	h := offset
	for _, b := range data {
		h ^= uint64(b)
		h *= prime
	}
	return h
}
