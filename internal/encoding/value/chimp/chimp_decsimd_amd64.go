//go:build amd64

package chimp

// chimpGroupDecodeAVX512Asm is the AVX-512 kernel behind ColumnGroup.Decode (needs AVX-512 F, BW and DQ).
// It takes up to steps values from each of the eight lanes of st, writes one row of eight values per step to out,
// which must hold steps rows, and returns the number of steps it completed;
// st.pos, st.prev and st.stored then hold the state after those steps.
// It stops before a step that any lane cannot take, and reads 16 bytes at any byte offset up to a lane's end.
// It reads through the pointers in st.base, which is all the collector needs to see:
// the assembly says why no address it derives from them can go stale.
//
//go:noescape
func chimpGroupDecodeAVX512Asm(st *chimpGroupState, steps int, out *uint64) int

// chimpGroupKernel runs the group decode kernel; the caller has checked chimpGroupAVX512.
func chimpGroupKernel(st *chimpGroupState, steps int, out *uint64) int {
	return chimpGroupDecodeAVX512Asm(st, steps, out)
}
