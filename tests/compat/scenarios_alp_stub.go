//go:build !alp

package main

// When compiled without the alp build tag (i.e. against a release older than
// v1.8.0, which lacks format.TypeALP), no ALP scenarios are registered.
// This file intentionally contains no init() so the binary is identical to a
// pre-v1.8.0 build.
