//go:build !metricnames

package main

// When compiled without the metricnames build tag (e.g. against v1.9.0,
// which lacks WithMetricNames / WithoutMetricNames / StripMetricNames /
// the borrowed decoder constructors), no metric-names-specific scenarios are
// registered. This file intentionally contains no init() so the binary is
// identical to what a pre-v1.10.0 build would produce.
