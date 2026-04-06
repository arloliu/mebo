//go:build !v2

package main

// When compiled without the v2 build tag (i.e., against v1.4.3 which lacks
// WithBlobLayoutV2 / WithSharedTimestamps), no V2 scenarios are registered.
// This file intentionally contains no init() so that the binary is identical
// to a v1.4.3-era build.
