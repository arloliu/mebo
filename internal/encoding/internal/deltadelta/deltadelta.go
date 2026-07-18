// Package deltadelta provides the shared delta-of-delta batch backend.
package deltadelta

import "github.com/arloliu/mebo/internal/arch"

const (
	// ChunkSize bounds the stack buffer used by Delta and DeltaPacked batch paths.
	ChunkSize                    = 256
	deltaOfDeltaSIMDMinLenAVX2   = 64
	deltaOfDeltaSIMDMinLenAVX512 = 32
)

type deltaOfDeltaBackend uint8

type deltaOfDeltaKernel func(dst []int64, src []int64, prevTS int64, prevDelta int64) (lastTS int64, lastDelta int64)

const (
	deltaOfDeltaBackendScalar deltaOfDeltaBackend = iota
	deltaOfDeltaBackendAsmAVX2
	deltaOfDeltaBackendAsmAVX512
	deltaOfDeltaBackendArchSIMDAVX2
	deltaOfDeltaBackendArchSIMDAVX512
)

var deltaOfDeltaBackends = [...]deltaOfDeltaBackend{
	deltaOfDeltaBackendScalar,
	deltaOfDeltaBackendAsmAVX2,
	deltaOfDeltaBackendAsmAVX512,
	deltaOfDeltaBackendArchSIMDAVX2,
	deltaOfDeltaBackendArchSIMDAVX512,
}

var activeDeltaOfDeltaBackend deltaOfDeltaBackend = deltaOfDeltaBackendScalar

func init() {
	setActiveDeltaOfDeltaBackend(defaultDeltaOfDeltaBackend())
}

func defaultDeltaOfDeltaBackend() deltaOfDeltaBackend {
	if arch.X86ArchSIMDHasAVX512() {
		return deltaOfDeltaBackendArchSIMDAVX512
	}

	if arch.X86HasAVX512() {
		return deltaOfDeltaBackendAsmAVX512
	}

	if arch.X86HasAVX2() {
		return deltaOfDeltaBackendAsmAVX2
	}

	if arch.X86ArchSIMDHasAVX2() {
		return deltaOfDeltaBackendArchSIMDAVX2
	}

	return deltaOfDeltaBackendScalar
}

func deltaOfDeltaBackendName(backend deltaOfDeltaBackend) string {
	switch backend {
	case deltaOfDeltaBackendScalar:
		return "Scalar"
	case deltaOfDeltaBackendAsmAVX2:
		return "AsmAVX2"
	case deltaOfDeltaBackendAsmAVX512:
		return "AsmAVX512"
	case deltaOfDeltaBackendArchSIMDAVX2:
		return "ArchSIMDAVX2"
	case deltaOfDeltaBackendArchSIMDAVX512:
		return "ArchSIMDAVX512"
	default:
		return "Unknown"
	}
}

func deltaOfDeltaBackendSupported(backend deltaOfDeltaBackend) bool {
	switch backend {
	case deltaOfDeltaBackendScalar:
		return true
	case deltaOfDeltaBackendAsmAVX2:
		return arch.X86HasAVX2()
	case deltaOfDeltaBackendAsmAVX512:
		return arch.X86HasAVX512()
	case deltaOfDeltaBackendArchSIMDAVX2:
		return arch.X86ArchSIMDHasAVX2()
	case deltaOfDeltaBackendArchSIMDAVX512:
		return arch.X86ArchSIMDHasAVX512()
	default:
		return false
	}
}

func setActiveDeltaOfDeltaBackend(backend deltaOfDeltaBackend) {
	if backend == deltaOfDeltaBackendAsmAVX512 && arch.X86HasAVX512() {
		activeDeltaOfDeltaBackend = deltaOfDeltaBackendAsmAVX512

		return
	}

	if backend == deltaOfDeltaBackendArchSIMDAVX512 && arch.X86ArchSIMDHasAVX512() {
		activeDeltaOfDeltaBackend = deltaOfDeltaBackendArchSIMDAVX512

		return
	}

	if backend == deltaOfDeltaBackendAsmAVX2 && arch.X86HasAVX2() {
		activeDeltaOfDeltaBackend = deltaOfDeltaBackendAsmAVX2

		return
	}

	if backend == deltaOfDeltaBackendArchSIMDAVX2 && arch.X86ArchSIMDHasAVX2() {
		activeDeltaOfDeltaBackend = deltaOfDeltaBackendArchSIMDAVX2

		return
	}

	activeDeltaOfDeltaBackend = deltaOfDeltaBackendScalar
}

func setDeltaOfDeltaBackendForTest(backend deltaOfDeltaBackend) func() {
	previousBackend := activeDeltaOfDeltaBackend

	setActiveDeltaOfDeltaBackend(backend)

	return func() {
		activeDeltaOfDeltaBackend = previousBackend
	}
}

// IntoActive writes delta-of-delta values for src into dst using the active backend.
// It returns the timestamp and delta to carry into the next batch.
func IntoActive(dst []int64, src []int64, prevTS int64, prevDelta int64) (lastTS int64, lastDelta int64) {
	switch activeDeltaOfDeltaBackend {
	case deltaOfDeltaBackendAsmAVX2:
		return deltaOfDeltaIntoASMAVX2(dst, src, prevTS, prevDelta)
	case deltaOfDeltaBackendAsmAVX512:
		return deltaOfDeltaIntoASMAVX512(dst, src, prevTS, prevDelta)
	case deltaOfDeltaBackendArchSIMDAVX2:
		return deltaOfDeltaIntoArchSIMDAVX2(dst, src, prevTS, prevDelta)
	case deltaOfDeltaBackendArchSIMDAVX512:
		return deltaOfDeltaIntoArchSIMDAVX512(dst, src, prevTS, prevDelta)
	case deltaOfDeltaBackendScalar:
		return deltaOfDeltaIntoScalar(dst, src, prevTS, prevDelta)
	}

	return deltaOfDeltaIntoScalar(dst, src, prevTS, prevDelta)
}

func deltaOfDeltaKernelForBackend(backend deltaOfDeltaBackend) deltaOfDeltaKernel {
	if backend == deltaOfDeltaBackendAsmAVX2 && deltaOfDeltaBackendSupported(backend) {
		return deltaOfDeltaIntoASMAVX2
	}

	if backend == deltaOfDeltaBackendAsmAVX512 && deltaOfDeltaBackendSupported(backend) {
		return deltaOfDeltaIntoASMAVX512
	}

	if backend == deltaOfDeltaBackendArchSIMDAVX2 && deltaOfDeltaBackendSupported(backend) {
		return deltaOfDeltaIntoArchSIMDAVX2
	}

	if backend == deltaOfDeltaBackendArchSIMDAVX512 && deltaOfDeltaBackendSupported(backend) {
		return deltaOfDeltaIntoArchSIMDAVX512
	}

	return deltaOfDeltaIntoScalar
}

// ShouldUse reports whether the active backend is beneficial for count values.
func ShouldUse(count int) bool {
	return activeDeltaOfDeltaBackend != deltaOfDeltaBackendScalar && count >= deltaOfDeltaSIMDMinLenForBackend(activeDeltaOfDeltaBackend)
}

func deltaOfDeltaSIMDMinLenForBackend(backend deltaOfDeltaBackend) int {
	switch backend {
	case deltaOfDeltaBackendScalar:
		return 1 << 30
	case deltaOfDeltaBackendAsmAVX512, deltaOfDeltaBackendArchSIMDAVX512:
		return deltaOfDeltaSIMDMinLenAVX512
	case deltaOfDeltaBackendAsmAVX2, deltaOfDeltaBackendArchSIMDAVX2:
		return deltaOfDeltaSIMDMinLenAVX2
	}

	return 1 << 30
}

func deltaOfDeltaIntoScalar(dst []int64, src []int64, prevTS int64, prevDelta int64) (lastTS int64, lastDelta int64) {
	for i, ts := range src {
		delta := ts - prevTS
		dst[i] = delta - prevDelta
		prevTS = ts
		prevDelta = delta
	}

	return prevTS, prevDelta
}
