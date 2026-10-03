//go:build !(alp && alprle)

package main

// When compiled without both the alp and alprle build tags (i.e. against a release that lacks format.TypeALPRLE),
// no ALP-RLE scenarios are registered.
// This file intentionally contains no init() so the binary is identical to a build without ALP-RLE.
