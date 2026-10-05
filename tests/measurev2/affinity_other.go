//go:build !linux

package main

import "errors"

// cpuAffinity is only implemented on Linux; elsewhere the affinity is recorded as unknown.
func cpuAffinity() ([]int, error) {
	return nil, errors.New("CPU affinity is only read on Linux")
}
