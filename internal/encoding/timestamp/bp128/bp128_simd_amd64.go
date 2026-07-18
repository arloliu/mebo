//go:build amd64

package bp128

//go:noescape
func bp128PackBlockAVX512(dst *uint64, src *uint64, w int) (nwords int)

//go:noescape
func bp128UnpackBlockAVX512(dst *uint64, src *uint64, w int)
