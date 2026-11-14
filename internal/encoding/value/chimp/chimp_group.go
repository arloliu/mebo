package chimp

import (
	"encoding/binary"
	"math"
	"math/bits"
	"runtime"

	"github.com/arloliu/mebo/internal/arch"
	"github.com/arloliu/mebo/internal/encoding/internal/bitstream"
)

const (
	// GroupMinColumns is the fewest kernel-eligible columns a ColumnGroup needs before Decode uses its kernel.
	// A kernel step costs the same for one column or eight,
	// so a caller with fewer Chimp columns than this has no reason to build a group.
	GroupMinColumns = 4

	// chimpGroupMinLive is the fewest unfinished columns the kernel keeps running for.
	// Columns of a group may hold different counts; once fewer than this are left, the scalar loop finishes them.
	chimpGroupMinLive = 4

	// chimpGroupMinBitsPerValue is the density below which a group is left to the scalar loop:
	// the average encoded bits per value after the first, over the kernel-eligible columns.
	// The kernel pays one step per value, while the scalar loop skips a run of unchanged values (2 bits each) at once.
	chimpGroupMinBitsPerValue = 12

	// chimpGroupReadAhead is how many bytes of its own payload a column needs after its end to enter the kernel.
	// It is the kernel's read extent, 16 bytes at any byte offset up to the column's end, not a tuning value.
	chimpGroupReadAhead = 16

	// chimpGroupRows is the most steps one kernel call takes: the size of the scratch rows.
	chimpGroupRows = 64
)

// chimpGroupAVX512 enables the AVX-512 group decode kernel (chimp_decsimd_amd64.s) where the CPU supports it:
// AVX-512 F, BW and DQ. It is always false off amd64.
// GODEBUG=cpu.avx512dq=off or GODEBUG=cpu.avx512bw=off turns it off for a whole process.
// Tests clear it to force the scalar path, and must not run in parallel while they do.
var chimpGroupAVX512 = arch.X86HasAVX512DQ() && arch.X86HasAVX512BW()

// ColumnGroup is a group of up to eight Chimp columns to decode together.
// The columns may lie in different buffers, which is what lets one metric's columns from several blobs share a call.
//
// Column i is Payload[i][Off[i]:Off[i]+Len[i]] and holds Count[i] values, decoded into Dst[i].
// The caller fills lanes 0 to n-1 and calls Decode(n); every column must lie inside its payload.
// A ColumnGroup can be reused for the next group.
// It is large (about 5 KiB, most of it the kernel's scratch rows) and meant to live on the caller's stack.
type ColumnGroup struct {
	// Payload holds the buffer each column lies in.
	Payload [8][]byte
	// Off and Len locate each column inside its payload.
	Off, Len [8]int
	// Count is the number of values each column is asked for.
	Count [8]int
	// Dst is where each column's values go.
	Dst [8][]float64
	// Produced is set by Decode to what NumericChimpDecoder.DecodeAll returns for each column.
	Produced [8]int
	// Columns, Steps and Calls count the columns that entered the kernel, the steps it completed
	// and the kernel calls, summed over every Decode of this group.
	Columns, Steps, Calls int

	st   chimpGroupState
	rows [chimpGroupRows * 8]uint64
}

// GroupAdmission answers, before a ColumnGroup is filled, whether Decode would take a group of columns into its kernel.
// It applies the rules Decode applies, to where the columns lie and how many values they are asked for:
// it reads no byte of a payload and writes nothing.
// A caller adds the columns it would put in a group with Add and then asks Enters.
// The zero value is an empty group.
//
// The answer is a forecast. Decode checks every lane again, and it also needs GroupDecodeEnabled,
// which Enters does not ask: a group that Enters accepts may still be decoded by the scalar loop.
type GroupAdmission struct {
	// columns is the number of eligible columns added; bits and values are their encoded bits and their values,
	// each after the column's first value.
	columns, bits, values int
}

// chimpGroupState is the kernel's state, one lane per column.
// The assembly reads the fields through their generated offsets and loads each as eight 64-bit lanes.
// base holds real pointers, so the collector keeps every lane's payload alive while the kernel reads it.
type chimpGroupState struct {
	base   [8]*byte  // first byte of the lane's column
	pos    [8]uint64 // bit position of the next value, counted from base
	end    [8]uint64 // one past the column's last bit
	prev   [8]uint64 // bits of the previous value
	stored [8]uint64 // stored leading-zero count, 65 for none
}

// GroupDecodeEnabled reports whether ColumnGroup.Decode can use its AVX-512 kernel on this CPU.
// When it is false, Decode still works and decodes every column with NumericChimpDecoder.DecodeAll.
func GroupDecodeEnabled() bool {
	return chimpGroupAVX512
}

// decodeAllFrom is the loop of NumericChimpDecoder.DecodeAll, entered at a given state:
// produced values are already in dst, the next value starts at bitPos,
// prevValue holds the bits of the last value and storedLeading the stored leading-zero count (65 for none).
// It returns the number of values in dst when the loop ends, as DecodeAll does.
//
// The loop is a copy, kept in step with DecodeAll by a test that resumes from every state DecodeAll passes through.
// DecodeAll serves many other paths and is not changed to share it.
func decodeAllFrom(data []byte, count int, dst []float64, produced, bitPos int, prevValue uint64, storedLeading int) int {
	totalBits := len(data) * 8
	prevFloat := math.Float64frombits(prevValue)
	_ = dst[count-1] // bounds-check elimination

	for produced < count {
		if bitPos+2 > totalBits {
			return produced
		}

		w := bitstream.PeekBits64(data, bitPos)

		switch w >> 62 {
		case 0: // Value unchanged: batch the whole run of 00 flag pairs in the window
			pairs := bits.LeadingZeros64(w) / 2
			run := min(pairs, count-produced, (totalBits-bitPos)/2)
			for range run {
				dst[produced] = prevFloat
				produced++
			}
			bitPos += run * 2
			storedLeading = 65

		case 1: // Trailing-zero optimized
			leading := chimpLeadingDecode[(w>>59)&0x07]

			sigBits := int(w>>53) & 0x3F
			if sigBits == 0 {
				sigBits = 64
			}

			trailingZeros := 64 - leading - sigBits
			if trailingZeros < 0 || bitPos+11+sigBits > totalBits {
				return produced
			}

			var meaningful uint64
			if sigBits <= 53 {
				meaningful = (w << 11) >> (64 - uint(sigBits))
			} else {
				meaningful = bitstream.PeekBits64(data, bitPos+11) >> (64 - uint(sigBits))
			}
			bitPos += 11 + sigBits

			prevValue ^= meaningful << uint(trailingZeros)
			prevFloat = math.Float64frombits(prevValue)
			dst[produced] = prevFloat
			produced++
			storedLeading = 65

		case 2: // Reuse previous leading
			if storedLeading > 64 {
				return produced
			}

			sigBits := 64 - storedLeading
			if bitPos+2+sigBits > totalBits {
				return produced
			}

			var meaningful uint64
			if sigBits <= 62 {
				meaningful = (w << 2) >> (64 - uint(sigBits))
			} else {
				meaningful = bitstream.PeekBits64(data, bitPos+2) >> (64 - uint(sigBits))
			}
			bitPos += 2 + sigBits

			prevValue ^= meaningful
			prevFloat = math.Float64frombits(prevValue)
			dst[produced] = prevFloat
			produced++

		default: // case 3: New leading
			leading := chimpLeadingDecode[(w>>59)&0x07]
			storedLeading = leading
			sigBits := 64 - leading

			if bitPos+5+sigBits > totalBits {
				return produced
			}

			var meaningful uint64
			if sigBits <= 59 {
				meaningful = (w << 5) >> (64 - uint(sigBits))
			} else {
				meaningful = bitstream.PeekBits64(data, bitPos+5) >> (64 - uint(sigBits))
			}
			bitPos += 5 + sigBits

			prevValue ^= meaningful
			prevFloat = math.Float64frombits(prevValue)
			dst[produced] = prevFloat
			produced++
		}
	}

	return produced
}

// chimpColumnEligible reports whether a column may enter the kernel, by where it lies and what it is asked for:
// it is asked for at least two values, it lies inside its payload and has the 8 bytes of a first value,
// chimpGroupReadAhead bytes of the payload's length follow it,
// and it has at least one bit for each value after the first.
// A column asked for more than that comes up short whatever it holds, so the scalar loop takes it;
// the bound also ties the count, which an index entry may set to anything, to the column's length.
// payload is the payload's length, off and length locate the column in it, and count is the values asked for.
// Each subtraction comes after the check that keeps it from going negative, and no sum of off and length is formed.
// The bits of a column, (length-8)*8, and the sums a GroupAdmission keeps of them are taken to fit an int:
// length is at most the length of a payload in memory, which leaves room on a 64-bit build.
func chimpColumnEligible(payload, off, length, count int) bool {
	return count >= 2 &&
		off >= 0 && off <= payload &&
		length >= 8 && length <= payload-off &&
		payload-off-length >= chimpGroupReadAhead &&
		count-1 <= (length-8)*8
}

// Decode decodes lanes 0 to n-1, with n at most 8.
// For each lane it sets Produced[i] to what NumericChimpDecoder.DecodeAll(column, Count[i], Dst[i]) returns
// and leaves the same values in Dst[i][:Produced[i]], writing nothing past them.
// It never reads Dst.
//
// A lane can share the kernel when it asks for at least two values, its destination holds them,
// its column has at least 8 bytes, and 16 bytes of its own payload follow the column
// (counted to the payload's length, never its capacity).
// The kernel runs when GroupDecodeEnabled is true, at least GroupMinColumns lanes can share it,
// and their columns are dense enough to gain from it.
// Every other lane, and every value the kernel does not take, is decoded by the scalar loop of DecodeAll.
//
// Parameters:
//   - n: the number of lanes to decode, from 0 to 8; lanes n and above are left as they are
func (g *ColumnGroup) Decode(n int) {
	g.decodeWith(n, GroupMinColumns, chimpGroupMinLive, chimpGroupMinBitsPerValue)
}

// Add counts one column of the group, if it is one Decode could take into the kernel.
// Decode asks the same of a lane, and that its destination holds the values asked for, which Add cannot see.
//
// Parameters:
//   - payload: the buffer the column lies in; only its length is read
//   - off: where the column starts in payload
//   - length: the column's length in bytes
//   - count: the number of values the column is asked for
func (a *GroupAdmission) Add(payload []byte, off, length, count int) {
	if chimpColumnEligible(len(payload), off, length, count) {
		a.tally(length, count)
	}
}

// Enters reports whether the columns added so far would enter the kernel as one group:
// at least GroupMinColumns of them are eligible, and those are dense enough to gain from it.
//
// Returns:
//   - bool: true when Decode would use the kernel for the group, on a CPU that has it
func (a *GroupAdmission) Enters() bool {
	return a.entersWith(GroupMinColumns, chimpGroupMinBitsPerValue)
}

// decodeWith is Decode with its three thresholds as parameters:
// the fewest eligible lanes a group enters the kernel with, the fewest live lanes the kernel keeps running for,
// and the fewest bits per value after the first.
func (g *ColumnGroup) decodeWith(n, minColumns, minLive, minBits int) {
	var live [8]bool
	nLive := 0
	if chimpGroupAVX512 {
		live, nLive = g.admit(n, minColumns, minBits)
	}

	var done [8]int
	for i := range n {
		column := g.Payload[i][g.Off[i] : g.Off[i]+g.Len[i]]
		if !live[i] {
			g.Produced[i] = NumericChimpDecoder{}.DecodeAll(column, g.Count[i], g.Dst[i])

			continue
		}
		// The first value is stored whole and read here, as DecodeAll reads it; the lane enters the kernel behind it.
		first := binary.BigEndian.Uint64(column)
		g.Dst[i][0] = math.Float64frombits(first)
		done[i] = 1
		g.st.base[i] = &column[0]
		g.st.pos[i] = 64
		g.st.end[i] = uint64(len(column)) * 8
		g.st.prev[i] = first
		g.st.stored[i] = 65
	}
	if nLive == 0 {
		return
	}
	g.Columns += nLive
	g.run(&live, &done, nLive, minLive)
}

// admit picks the lanes that enter the kernel and returns them with their number.
// It returns none when fewer than minColumns lanes are eligible,
// or when the eligible columns average fewer than minBits bits per value after the first.
func (g *ColumnGroup) admit(n, minColumns, minBits int) ([8]bool, int) {
	var (
		live      [8]bool
		admission GroupAdmission
	)
	for i := range n {
		if g.eligible(i) {
			live[i] = true
			admission.tally(g.Len[i], g.Count[i])
		}
	}
	if !admission.entersWith(minColumns, minBits) {
		return [8]bool{}, 0
	}

	return live, admission.columns
}

// eligible reports whether lane i may enter the kernel:
// its destination holds the values it asks for, and its column passes chimpColumnEligible.
func (g *ColumnGroup) eligible(i int) bool {
	return len(g.Dst[i]) >= g.Count[i] && chimpColumnEligible(len(g.Payload[i]), g.Off[i], g.Len[i], g.Count[i])
}

// run takes the live lanes through the kernel in rounds and hands what is left to the scalar loop.
// done[i] is the number of values lane i has in its destination.
//
// A round runs the kernel for the fewest values any live lane still wants, at most chimpGroupRows,
// then completes the lanes that have their count: those are final, and Produced is set for them here.
// A slot that is not live mirrors a live one.
// With the same state it stops the kernel exactly when its source does, so it never costs a live lane a step,
// it is never a source of an address of its own, and its output column is ignored.
// The rounds end when fewer than minLive lanes are live, or when the kernel met a value it does not take.
// Every lane still live then continues in the scalar loop from its own slot, which no mirroring has touched.
func (g *ColumnGroup) run(live *[8]bool, done *[8]int, nLive, minLive int) {
	for nLive >= max(minLive, 1) {
		steps, source := chimpGroupRows, 0
		for i := range 8 {
			if live[i] {
				steps = min(steps, g.Count[i]-done[i])
				source = i
			}
		}
		for i := range 8 {
			if !live[i] {
				g.st.base[i], g.st.pos[i], g.st.end[i] = g.st.base[source], g.st.pos[source], g.st.end[source]
				g.st.prev[i], g.st.stored[i] = g.st.prev[source], g.st.stored[source]
			}
		}

		got := chimpGroupKernel(&g.st, steps, &g.rows[0])
		g.Calls++
		g.Steps += got
		for i := range 8 {
			if !live[i] {
				continue
			}
			dst := g.Dst[i][done[i] : done[i]+got]
			for r := range dst {
				dst[r] = math.Float64frombits(g.rows[r*8+i])
			}
			done[i] += got
			if done[i] == g.Count[i] {
				live[i] = false
				nLive--
				g.Produced[i] = g.Count[i]
			}
		}
		if got < steps {
			break // a lane met a value the kernel does not take: the scalar loop decides what it is
		}
	}
	// The kernel read through the pointers in g.st.base: the payloads stay reachable until its last call has returned.
	runtime.KeepAlive(g)

	for i := range 8 {
		if !live[i] {
			continue
		}
		column := g.Payload[i][g.Off[i] : g.Off[i]+g.Len[i]]
		g.Produced[i] = decodeAllFrom(column, g.Count[i], g.Dst[i][:g.Count[i]], done[i],
			int(g.st.pos[i]), g.st.prev[i], int(g.st.stored[i])) //nolint:gosec // a bit position inside the column, and 0..65
	}
}

// tally adds an eligible column of length bytes that is asked for count values.
func (a *GroupAdmission) tally(length, count int) {
	a.columns++
	a.bits += (length - 8) * 8
	a.values += count - 1
}

// entersWith is Enters with its two thresholds as parameters: the counted columns enter the kernel when
// there are at least minColumns of them, and they average at least minBits bits per value after the first.
func (a *GroupAdmission) entersWith(minColumns, minBits int) bool {
	return a.columns >= minColumns && a.bits >= minBits*a.values
}
