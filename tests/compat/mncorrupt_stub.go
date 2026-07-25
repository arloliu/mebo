//go:build !metricnames

package main

import "fmt"

// Without the metricnames build tag (e.g. building against v1.9.0, which
// lacks the v1.10.0 metric-names public API), the adversarial fixtures in
// mncorrupt_metricnames.go cannot be built or referenced. run_compat.sh only
// invokes `compat mncorrupt` against a binary built WITH -tags metricnames
// (see needs_metricnames_tag), so reaching this path indicates a harness
// wiring bug rather than an expected outcome.
func init() {
	mnCorruptImpl = func([]string) error {
		return fmt.Errorf("mncorrupt: unsupported — binary built without -tags metricnames")
	}
}
