//go:build linux

package main

import "golang.org/x/sys/unix"

// cpuSetBits is the number of CPUs a unix.CPUSet holds (glibc's CPU_SETSIZE).
const cpuSetBits = 1024

// cpuAffinity returns the CPUs in this thread's scheduler affinity mask (sched_getaffinity);
// taskset sets it before exec and every thread inherits it.
func cpuAffinity() ([]int, error) {
	var set unix.CPUSet
	if err := unix.SchedGetaffinity(0, &set); err != nil {
		return nil, err
	}
	cpus := make([]int, 0, set.Count())
	for i := range cpuSetBits {
		if set.IsSet(i) {
			cpus = append(cpus, i)
		}
	}

	return cpus, nil
}
