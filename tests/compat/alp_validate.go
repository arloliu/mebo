//go:build alpvalidate

package main

// Built only against a v1.9.0+ module (build tag "alpvalidate"), the first
// release that validates ALP columns when a blob is opened; see verify.go's
// alpOpenValidation.
func init() {
	alpOpenValidation = true
}
