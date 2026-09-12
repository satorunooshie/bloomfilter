module github.com/satorunooshie/bloomfilter/comparison

go 1.27.1

require (
	github.com/bits-and-blooms/bloom/v3 v3.7.1
	github.com/phrozen/bloom v0.2.0
	github.com/satorunooshie/bloomfilter v0.0.0
)

require github.com/bits-and-blooms/bitset v1.24.2 // indirect

replace github.com/satorunooshie/bloomfilter => ..
