package hash

import "github.com/cespare/xxhash/v2"

// ID computes the xxHash64 of the given string.
func ID(data string) uint64 {
	return xxhash.Sum64String(data)
}
