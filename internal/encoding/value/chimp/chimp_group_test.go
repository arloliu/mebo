package chimp

import (
	"fmt"
	"go/parser"
	"go/token"
	"maps"
	"math"
	"math/bits"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/internal/encoding/internal/bitstream"
)

const (
	// chimpGroupDenseSlot is the first slot of generateChimpMixColumn's full-precision gauge:
	// every value changes, and none needs more than the kernel's 64-bit window.
	chimpGroupDenseSlot = 73

	// chimpGroupStressRuns is how many groups TestChimpGroup_GCAndStackMoves decodes in each variant,
	// and chimpGroupStressShortRuns how many under -short.
	chimpGroupStressRuns      = 4000
	chimpGroupStressShortRuns = 300
	// chimpGroupStressBytes is the room for one column and its read-ahead in chimpGroupDecodeOnStack's array.
	chimpGroupStressBytes = 2048
)

// chimpGroupForecasts counts the groups chimpGroupCheck compared with their GroupAdmission forecast,
// by the forecast: entering the kernel or not.
var chimpGroupForecasts = map[bool]int{}

// chimpGroupStep is the decoder's state after one value, as chimpGroupTrace records it.
type chimpGroupStep struct {
	value  float64
	bitPos int    // where the next value starts
	prev   uint64 // bits of this value
	stored int    // stored leading-zero count, 65 for none
	// wide is set when the value's header and payload exceed the 64-bit window.
	// The kernel does not take such a value: it leaves, and the scalar loop reads the payload with a second window.
	wide bool
}

// chimpGroupLane is one column of a test group, placed in a buffer of its own.
type chimpGroupLane struct {
	col   []byte // encoded bytes
	count int    // the count asked for
	cut   int    // bytes dropped from the column's end; they stay in the buffer after it
	tail  int    // bytes of the payload's length after the encoded bytes
	spare int    // bytes of the payload's capacity after its length
	pre   int    // bytes of the payload before the column
}

// chimpGroupKernelRuns reports whether the group decode kernel runs on this machine.
// Where it does not, a test still compares every result with DecodeAll, and skips what it pins about the kernel.
// MEBO_REQUIRE_AVX512=1 turns that skip into a failure:
// set it on a runner meant to execute the kernel, so its pins cannot pass by being skipped.
func chimpGroupKernelRuns(tb testing.TB) bool {
	tb.Helper()
	if chimpGroupAVX512 {
		return true
	}
	if os.Getenv("MEBO_REQUIRE_AVX512") == "1" {
		tb.Fatal("the Chimp group decode kernel cannot run on this machine, and MEBO_REQUIRE_AVX512=1")
	}

	return false
}

// chimpGroupRequireKernel skips t when the group decode kernel cannot run on this machine,
// unless MEBO_REQUIRE_AVX512=1, which turns the skip into a failure (chimpGroupKernelRuns).
func chimpGroupRequireKernel(t testing.TB) {
	t.Helper()
	if !chimpGroupKernelRuns(t) {
		t.Skip("the Chimp group decode kernel cannot run on this machine")
	}
}

// chimpGroupEncode returns col as a Chimp column.
func chimpGroupEncode(col []float64) []byte {
	enc := NewNumericChimpEncoder()
	defer enc.Finish()
	enc.WriteSlice(col)

	return slices.Clone(enc.Bytes())
}

// chimpGroupFill writes a pattern over dst that no decoded column holds,
// so a comparison of whole destinations shows every write, and any value a decoder took from the destination.
func chimpGroupFill(dst []float64) {
	for j := range dst {
		dst[j] = math.Float64frombits(0x7FF8DEADBEEF0000 + uint64(j))
	}
}

// chimpGroupSameBits reports whether a and b hold the same bit patterns.
func chimpGroupSameBits(a, b []float64) bool {
	return slices.EqualFunc(a, b, func(x, y float64) bool { return math.Float64bits(x) == math.Float64bits(y) })
}

// chimpGroupTrace is the loop of NumericChimpDecoder.DecodeAll,
// kept here as a copy that records the state after every value it decodes; entry k-1 is the state after k values.
// A run of unchanged values is recorded one value at a time, each two bits further, which is how the kernel walks it.
// It also classifies every value: wide or not.
func chimpGroupTrace(data []byte, count int) []chimpGroupStep {
	totalBits := len(data) * 8
	if count == 0 || totalBits < 64 {
		return nil
	}

	prevValue := bitstream.PeekBits64(data, 0)
	bitPos := 64
	storedLeading := 65
	trace := []chimpGroupStep{{value: math.Float64frombits(prevValue), bitPos: bitPos, prev: prevValue, stored: storedLeading}}

	for len(trace) < count {
		if bitPos+2 > totalBits {
			return trace
		}

		w := bitstream.PeekBits64(data, bitPos)
		wide := false

		switch w >> 62 {
		case 0:
			pairs := bits.LeadingZeros64(w) / 2
			run := min(pairs, count-len(trace), (totalBits-bitPos)/2)
			for range run {
				bitPos += 2
				trace = append(trace, chimpGroupStep{value: math.Float64frombits(prevValue), bitPos: bitPos, prev: prevValue, stored: 65})
			}
			storedLeading = 65

			continue

		case 1:
			leading := chimpLeadingDecode[(w>>59)&0x07]
			sigBits := int(w>>53) & 0x3F
			if sigBits == 0 {
				sigBits = 64
			}
			trailingZeros := 64 - leading - sigBits
			if trailingZeros < 0 || bitPos+11+sigBits > totalBits {
				return trace
			}
			var meaningful uint64
			if sigBits <= 53 {
				meaningful = (w << 11) >> (64 - uint(sigBits))
			} else {
				meaningful = bitstream.PeekBits64(data, bitPos+11) >> (64 - uint(sigBits))
				wide = true
			}
			bitPos += 11 + sigBits
			prevValue ^= meaningful << uint(trailingZeros)
			storedLeading = 65

		case 2:
			if storedLeading > 64 {
				return trace
			}
			sigBits := 64 - storedLeading
			if bitPos+2+sigBits > totalBits {
				return trace
			}
			var meaningful uint64
			if sigBits <= 62 {
				meaningful = (w << 2) >> (64 - uint(sigBits))
			} else {
				meaningful = bitstream.PeekBits64(data, bitPos+2) >> (64 - uint(sigBits))
				wide = true
			}
			bitPos += 2 + sigBits
			prevValue ^= meaningful

		default:
			leading := chimpLeadingDecode[(w>>59)&0x07]
			sigBits := 64 - leading
			if bitPos+5+sigBits > totalBits {
				return trace
			}
			storedLeading = leading
			var meaningful uint64
			if sigBits <= 59 {
				meaningful = (w << 5) >> (64 - uint(sigBits))
			} else {
				meaningful = bitstream.PeekBits64(data, bitPos+5) >> (64 - uint(sigBits))
				wide = true
			}
			bitPos += 5 + sigBits
			prevValue ^= meaningful
		}

		trace = append(trace, chimpGroupStep{
			value: math.Float64frombits(prevValue), bitPos: bitPos, prev: prevValue, stored: storedLeading, wide: wide,
		})
	}

	return trace
}

// chimpGroupWide returns how many values of the column the kernel does not take, among the count asked for.
func chimpGroupWide(data []byte, count int) int {
	wide := 0
	for _, s := range chimpGroupTrace(data, count) {
		if s.wide {
			wide++
		}
	}

	return wide
}

// chimpGroupDense returns full-precision gauge columns of n values, one per lane, each from its own seed.
// The kernel takes every value of them, which the function checks.
func chimpGroupDense(tb testing.TB, lanes, n int, seed int64) [][]float64 {
	tb.Helper()
	cols := make([][]float64, lanes)
	for i := range cols {
		cols[i] = generateChimpMixColumn(chimpGroupDenseSlot+i, n, seed+int64(i))
		require.Zero(tb, chimpGroupWide(chimpGroupEncode(cols[i]), n), "dense column %d holds a value the kernel does not take", i)
	}

	return cols
}

// chimpGroupCheck decodes the lanes as one ColumnGroup and compares every lane with DecodeAll on the same bytes:
// the count produced and the whole destination, bit for bit.
// Every destination is longer than its count by 0 to 2 values and starts as a pattern,
// so a write past the produced count shows, and so does a decode that depends on what the destination held.
// It also compares the group with what GroupAdmission forecast for it, from the same lanes before the decode:
// where the kernel can run, the kernel takes columns exactly when Enters said it would, and as many as Add counted.
// It returns the group, for its counters.
func chimpGroupCheck(tb testing.TB, name string, lanes []chimpGroupLane, rng *rand.Rand) *ColumnGroup {
	tb.Helper()
	g := new(ColumnGroup)
	var admission GroupAdmission
	for i, l := range lanes {
		buf := make([]byte, l.pre+len(l.col)+l.tail+l.spare)
		_, _ = rng.Read(buf)
		copy(buf[l.pre:], l.col)
		g.Payload[i] = buf[:l.pre+len(l.col)+l.tail]
		g.Off[i] = l.pre
		g.Len[i] = len(l.col) - l.cut
		g.Count[i] = l.count
		g.Dst[i] = make([]float64, l.count+i%3)
		chimpGroupFill(g.Dst[i])
		admission.Add(g.Payload[i], g.Off[i], g.Len[i], g.Count[i])
	}
	g.Decode(len(lanes))
	if chimpGroupKernelRuns(tb) {
		chimpGroupForecasts[admission.Enters()]++
		if admission.Enters() != (g.Columns > 0) || g.Columns > 0 && g.Columns != admission.columns {
			tb.Fatalf("%s: the kernel took %d columns; the forecast was %d columns, entering=%v",
				name, g.Columns, admission.columns, admission.Enters())
		}
	}

	for i := range lanes {
		want := make([]float64, len(g.Dst[i]))
		chimpGroupFill(want)
		n := NumericChimpDecoder{}.DecodeAll(g.Payload[i][g.Off[i]:g.Off[i]+g.Len[i]], g.Count[i], want)
		if g.Produced[i] != n {
			tb.Fatalf("%s: lane %d: produced %d, DecodeAll %d", name, i, g.Produced[i], n)
		}
		if !chimpGroupSameBits(g.Dst[i], want) {
			tb.Fatalf("%s: lane %d: destination differs from DecodeAll (count %d, produced %d)", name, i, g.Count[i], n)
		}
	}

	return g
}

// chimpGroupVariants returns valid as it is, truncated and with single bits flipped.
func chimpGroupVariants(rng *rand.Rand, valid []byte) map[string][]byte {
	variants := map[string][]byte{"valid": valid}
	for k := range 6 {
		if len(valid) > 1 {
			variants["truncated_"+strconv.Itoa(k)] = valid[:rng.Intn(len(valid))]
		}
		flipped := slices.Clone(valid)
		flipped[rng.Intn(len(flipped))] ^= 1 << uint(rng.Intn(8))
		variants["flipped_"+strconv.Itoa(k)] = flipped
	}
	for n := range min(len(valid), 9) {
		variants["truncated_to_"+strconv.Itoa(n)] = valid[:n]
	}

	return variants
}

// chimpGroupDecodeOnStack copies the columns into an array declared in its own frame, each followed by its read-ahead,
// and decodes them as one group into dst.
// It hands the array and the group to nothing but Decode, and returns plain values,
// so the group stays on the goroutine's stack. The compiler moves the array to the heap all the same:
// Decode stores pointers into its payloads in the group (-gcflags=-m: "moved to heap: payloads").
//
//go:noinline
func chimpGroupDecodeOnStack(columns *[8][]byte, counts *[8]int, dst *[8][]float64) (produced [8]int, calls int) {
	var (
		payloads [8][chimpGroupStressBytes]byte
		g        ColumnGroup
	)
	for i := range payloads {
		n := copy(payloads[i][:], columns[i])
		g.Payload[i] = payloads[i][:n+chimpGroupReadAhead]
		g.Len[i], g.Count[i], g.Dst[i] = n, counts[i], dst[i]
	}
	g.Decode(len(payloads))

	return g.Produced, g.Calls
}

// chimpGroupDecodeOnHeap is chimpGroupDecodeOnStack with each column in a heap payload of its own,
// which nothing but the group references.
//
//go:noinline
func chimpGroupDecodeOnHeap(columns *[8][]byte, counts *[8]int, dst *[8][]float64) (produced [8]int, calls int) {
	var g ColumnGroup
	for i := range g.Payload {
		g.Payload[i] = make([]byte, len(columns[i])+chimpGroupReadAhead)
		n := copy(g.Payload[i], columns[i])
		g.Len[i], g.Count[i], g.Dst[i] = n, counts[i], dst[i]
	}
	g.Decode(len(g.Payload))

	return g.Produced, g.Calls
}

// chimpGroupGrowStack recurses through depth frames of about 1 KiB and returns,
// which leaves the goroutine's stack grown and mostly unused, so the next collection may shrink it.
//
//go:noinline
func chimpGroupGrowStack(depth int) int {
	var frame [1024]byte
	frame[depth%len(frame)] = byte(depth)
	if depth == 0 {
		return int(frame[0])
	}

	return chimpGroupGrowStack(depth-1) + int(frame[depth%len(frame)])
}

// TestChimpGroup_DecodeAllFrom pins decodeAllFrom, a copy of DecodeAll's loop, against DecodeAll itself.
// For every column and every number of values k that DecodeAll gets out of it,
// resuming from the state after k values must fill the same values and return the same count.
// The states come from chimpGroupTrace, which the test first checks against DecodeAll.
func TestChimpGroup_DecodeAllFrom(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	columns := chimpAtEdgeColumns()
	for slot := 0; slot < chimpMixSetColumns; slot += 9 {
		for _, n := range []int{30, 150} {
			columns[fmt.Sprintf("mix_slot%d_n%d", slot, n)] = generateChimpMixColumn(slot, n, int64(slot*1000+n))
		}
	}
	dense := chimpGroupDense(t, 7, 30, 5)

	states, wide, empty := 0, 0, 0
	for _, name := range slices.Sorted(maps.Keys(columns)) {
		col := columns[name]
		variants := chimpGroupVariants(rng, chimpGroupEncode(col))
		for _, variant := range slices.Sorted(maps.Keys(variants)) {
			data := variants[variant]
			for _, count := range []int{len(col), len(col) + 3} {
				want := make([]float64, count+2)
				chimpGroupFill(want)
				n := NumericChimpDecoder{}.DecodeAll(data, count, want)

				trace := chimpGroupTrace(data, count)
				require.Len(t, trace, n, "%s/%s count %d: the trace and DecodeAll disagree on the count", name, variant, count)
				for k, s := range trace {
					if wantBits := math.Float64bits(want[k]); math.Float64bits(s.value) != wantBits || s.prev != wantBits {
						t.Fatalf("%s/%s count %d: traced value %d differs from DecodeAll", name, variant, count, k)
					}
				}

				if n == 0 {
					// No value, so no state to resume from: as a lane of a group the column must come out as DecodeAll leaves it,
					// with nothing produced and the destination untouched.
					lanes := []chimpGroupLane{{col: data, count: count, tail: chimpGroupReadAhead}}
					for i := range dense {
						lanes = append(lanes, chimpGroupLane{col: chimpGroupEncode(dense[i]), count: len(dense[i]), tail: chimpGroupReadAhead})
					}
					g := chimpGroupCheck(t, name+"/"+variant, lanes, rng)
					require.Zero(t, g.Produced[0], "%s/%s count %d", name, variant, count)
					empty++

					continue
				}

				got := make([]float64, len(want))
				for k := 1; k <= n; k++ {
					chimpGroupFill(got)
					copy(got[:k], want[:k])
					s := trace[k-1]
					m := decodeAllFrom(data, count, got, k, s.bitPos, s.prev, s.stored)
					if m != n || !chimpGroupSameBits(got, want) {
						t.Fatalf("%s/%s count %d: resumed after %d values: produced %d, DecodeAll %d, same values: %v",
							name, variant, count, k, m, n, chimpGroupSameBits(got, want))
					}
					states++
					if s.wide {
						wide++
					}
				}
			}
		}
	}
	// The columns must reach the loop's second-window branches and the columns without a value.
	require.Greater(t, states, 100_000)
	require.Greater(t, wide, 1_000)
	require.Greater(t, empty, 100)
}

// TestChimpGroup_Engages pins when the kernel runs, for how many steps and in how many calls,
// so the comparisons with DecodeAll cannot pass with the kernel idle.
// Every figure follows from the rounds: a round runs until the first live lane has its count or 64 steps are done,
// and the kernel stops when fewer than four lanes are live.
func TestChimpGroup_Engages(t *testing.T) {
	chimpGroupRequireKernel(t)
	rng := rand.New(rand.NewSource(3))
	dense := chimpGroupDense(t, 8, 200, 99)
	group := func(counts []int, edit func(i int, l *chimpGroupLane)) []chimpGroupLane {
		lanes := make([]chimpGroupLane, len(counts))
		for i, c := range counts {
			lanes[i] = chimpGroupLane{col: chimpGroupEncode(dense[i][:c]), count: c, tail: chimpGroupReadAhead, pre: i * 3}
			if edit != nil {
				edit(i, &lanes[i])
			}
		}

		return lanes
	}
	expect := func(name string, lanes []chimpGroupLane, columns, steps, calls int) {
		t.Helper()
		g := chimpGroupCheck(t, name, lanes, rng)
		if g.Columns != columns || g.Steps != steps || g.Calls != calls {
			t.Fatalf("%s: the kernel took %d columns for %d steps in %d calls, want %d, %d and %d",
				name, g.Columns, g.Steps, g.Calls, columns, steps, calls)
		}
	}
	equal := []int{30, 30, 30, 30, 30, 30, 30, 30}

	// The first value of a column is read outside the kernel, so 30 values are 29 steps.
	expect("equal counts", group(equal, nil), 8, 29, 1)
	// Lanes complete at 30, 31, 33, 34 and 35 values, one round each; then three are left and the kernel stops.
	expect("counts differ", group([]int{38, 31, 40, 30, 35, 33, 34, 39}, nil), 8, 34, 5)
	// 199 steps are three rounds of 64 and one of 7.
	expect("more than one round of rows", group([]int{200, 200, 200, 200}, nil), 4, 199, 4)
	expect("four lanes", group([]int{30, 32, 34, 36}, nil), 4, 29, 1)
	expect("three lanes", group([]int{30, 32, 34}, nil), 0, 0, 0)
	expect("two-point columns", group([]int{2, 2, 2, 2, 2, 2, 2, 2}, nil), 8, 1, 1)
	expect("one-point columns stay out", group([]int{1, 30, 1, 30, 30, 30, 1, 30}, nil), 5, 29, 1)
	expect("one-point columns leave too few", group([]int{1, 30, 1, 30, 30, 1, 1, 1}, nil), 0, 0, 0)

	// Read-ahead: 16 bytes of the lane's own payload after the column, or the lane stays out.
	expect("one lane with 15 bytes of read-ahead", group(equal, func(i int, l *chimpGroupLane) {
		if i == 5 {
			l.tail = chimpGroupReadAhead - 1
		}
	}), 7, 29, 1)
	expect("no lane has read-ahead", group(equal, func(_ int, l *chimpGroupLane) { l.tail = 0 }), 0, 0, 0)
	expect("read-ahead on four lanes", group(equal, func(i int, l *chimpGroupLane) { l.tail = (i % 2) * chimpGroupReadAhead }), 4, 29, 1)
	// Bytes past the payload's length do not count, whatever its capacity.
	expect("one lane with its last byte of read-ahead in the capacity", group(equal, func(i int, l *chimpGroupLane) {
		if i == 5 {
			l.tail, l.spare = chimpGroupReadAhead-1, 64
		}
	}), 7, 29, 1)
	expect("read-ahead in the capacity only", group(equal, func(_ int, l *chimpGroupLane) { l.tail, l.spare = 0, 64 }), 0, 0, 0)

	// A lane cut short stops the kernel at its own end, however much of the buffer follows:
	// the kernel completes as many steps as DecodeAll gets values out of the cut column after the first.
	cutColumn := chimpGroupEncode(dense[2][:30])[:8+20]
	cutValues := NumericChimpDecoder{}.DecodeAll(cutColumn, 30, make([]float64, 30))
	require.Greater(t, cutValues, 2)
	require.Less(t, cutValues, 30)
	expect("lane cut short", group(equal, func(i int, l *chimpGroupLane) {
		if i == 2 {
			l.cut = len(l.col) - len(cutColumn)
		}
	}), 8, cutValues-1, 1)

	// The density rule: a group is left to the scalar loop
	// when its eligible columns average fewer than chimpGroupMinBitsPerValue bits per value after the first.
	// An unchanged value costs 2 bits.
	density := func(lanes []chimpGroupLane) float64 {
		encoded, values := 0, 0
		for _, l := range lanes {
			encoded += (len(l.col) - 8) * 8
			values += l.count - 1
		}

		return float64(encoded) / float64(values)
	}
	constant := func(i int, l *chimpGroupLane) {
		l.col = chimpGroupEncode(slices.Repeat([]float64{float64(i) + 0.5}, l.count))
	}
	expect("constant columns", group(equal, constant), 0, 0, 0)
	// Three constant lanes among dense ones do not pull the group under the threshold: the rule is per group.
	mostlyDense := group(equal, func(i int, l *chimpGroupLane) {
		if i < 3 {
			constant(i, l)
		}
	})
	require.Greater(t, density(mostlyDense), 2.0*chimpGroupMinBitsPerValue)
	expect("mostly dense", mostlyDense, 8, 29, 1)
	// A value that changes every sixth time is under the threshold, every third time over it,
	// by less than a factor of two on either side.
	changing := func(every int) func(int, *chimpGroupLane) {
		return func(i int, l *chimpGroupLane) {
			col := make([]float64, l.count)
			v := 100.25 + float64(i)
			for j := range col {
				if j%every == 0 {
					v *= 1.0001
				}
				col[j] = v
			}
			l.col = chimpGroupEncode(col)
			require.Zero(t, chimpGroupWide(l.col, l.count))
		}
	}
	counts31 := []int{31, 31, 31, 31, 31, 31, 31, 31}
	under, over := group(counts31, changing(6)), group(counts31, changing(3))
	require.Less(t, density(under), float64(chimpGroupMinBitsPerValue))
	require.Greater(t, density(under), chimpGroupMinBitsPerValue/2.0)
	require.GreaterOrEqual(t, density(over), float64(chimpGroupMinBitsPerValue))
	require.Less(t, density(over), 2.0*chimpGroupMinBitsPerValue)
	expect("one change in six", under, 0, 0, 0)
	expect("one change in three", over, 8, 30, 1)
}

// TestChimpGroup_Admission pins GroupAdmission on either side of every rule it applies.
// A column is counted when it is asked for two values or more, lies inside its payload with the 8 bytes of a first value,
// has chimpGroupReadAhead bytes of the payload's length after it, and claims no more values than it has bits.
// A group enters with GroupMinColumns counted columns that average chimpGroupMinBitsPerValue bits a value after the first.
// The payloads are zero bytes of the right length: the forecast reads none of them.
func TestChimpGroup_Admission(t *testing.T) {
	const (
		length = 8 + 6                   // a first value and 48 bits
		count  = 1 + 48/12               // the values those bits carry at the threshold: four after the first
		room   = chimpGroupReadAhead     // bytes of the payload after the column
		pre    = 5                       // bytes of the payload before the column
		whole  = pre + length + room     // the length of a payload the column fits with its read-ahead
		atEdge = GroupMinColumns * 6 * 8 // bits of a group of GroupMinColumns such columns
	)
	require.Equal(t, 12, chimpGroupMinBitsPerValue, "the columns below sit on a threshold of 12 bits a value")
	// The kernel runs while chimpGroupMinLive lanes are live: a group that entered with fewer would never call it.
	require.GreaterOrEqual(t, GroupMinColumns, chimpGroupMinLive, "the entry minimum against the minimum the kernel runs for")
	require.Equal(t, atEdge, chimpGroupMinBitsPerValue*GroupMinColumns*(count-1), "the group sits on the threshold")

	columns := []struct {
		name                        string
		payload, off, length, count int
		spare                       int // capacity of the payload past its length
		counted                     bool
	}{
		{name: "a column with its read-ahead", payload: whole, off: pre, length: length, count: count, counted: true},
		{name: "at the start of its payload", payload: whole - pre, off: 0, length: length, count: count, counted: true},
		{name: "two values", payload: whole, off: pre, length: length, count: 2, counted: true},
		{name: "one value", payload: whole, off: pre, length: length, count: 1},
		{name: "no value", payload: whole, off: pre, length: length, count: 0},
		{name: "a negative count", payload: whole, off: pre, length: length, count: -3},
		{name: "8 bytes and two values", payload: pre + 8 + room, off: pre, length: 8, count: 2},
		{name: "9 bytes and two values", payload: pre + 9 + room, off: pre, length: 9, count: 2, counted: true},
		{name: "7 bytes", payload: pre + 7 + room, off: pre, length: 7, count: 2},
		{name: "a negative length", payload: whole, off: pre, length: -1, count: count},
		{name: "a negative offset", payload: whole, off: -1, length: length, count: count},
		{name: "an offset past the payload", payload: whole, off: whole + 1, length: length, count: count},
		{name: "a column past the payload", payload: whole, off: pre, length: whole - pre + 1, count: count},
		{name: "one byte of read-ahead missing", payload: whole - 1, off: pre, length: length, count: count},
		{name: "the last byte of read-ahead in the capacity", payload: whole - 1, off: pre, length: length, count: count, spare: 64},
		{name: "no read-ahead", payload: pre + length, off: pre, length: length, count: count},
		{name: "as many values as bits", payload: whole, off: pre, length: length, count: 1 + 48, counted: true},
		{name: "one value more than bits", payload: whole, off: pre, length: length, count: 2 + 48},
		{name: "the largest count", payload: whole, off: pre, length: length, count: math.MaxInt},
		{name: "the largest offset", payload: whole, off: math.MaxInt, length: length, count: count},
		{name: "the largest length", payload: whole, off: pre, length: math.MaxInt, count: count},
	}
	for _, c := range columns {
		t.Run(c.name, func(t *testing.T) {
			var a GroupAdmission
			a.Add(make([]byte, c.payload, c.payload+c.spare), c.off, c.length, c.count)
			if !c.counted {
				require.Zero(t, a, "the column was counted")

				return
			}
			require.Equal(t, GroupAdmission{columns: 1, bits: (c.length - 8) * 8, values: c.count - 1}, a)
		})
	}

	payload := make([]byte, whole)
	groups := []struct {
		name   string
		counts []int // one column of length bytes per count
		short  int   // bytes taken off the first column
		enters bool
	}{
		{name: "no column"},
		{name: "one column short", counts: slices.Repeat([]int{count}, GroupMinColumns-1)},
		{name: "on the threshold", counts: slices.Repeat([]int{count}, GroupMinColumns), enters: true},
		{name: "one value over", counts: append([]int{count + 1}, slices.Repeat([]int{count}, GroupMinColumns-1)...)},
		{name: "one byte under", counts: slices.Repeat([]int{count}, GroupMinColumns), short: 1},
		{name: "one value under", counts: append([]int{count - 1}, slices.Repeat([]int{count}, GroupMinColumns-1)...), enters: true},
		{name: "eight columns on the threshold", counts: slices.Repeat([]int{count}, 8), enters: true},
		{name: "a one-value column among too few", counts: append([]int{1}, slices.Repeat([]int{count}, GroupMinColumns-1)...)},
		{
			name:   "sparse columns among dense ones",
			counts: []int{count + 2, count + 2, count - 1, count - 1, count - 1, count - 1},
			enters: true,
		},
	}
	for _, c := range groups {
		t.Run(c.name, func(t *testing.T) {
			var a GroupAdmission
			for i, n := range c.counts {
				short := 0
				if i == 0 {
					short = c.short
				}
				a.Add(payload[:whole-short], pre, length-short, n)
			}
			require.Equal(t, c.enters, a.Enters())
		})
	}
}

// TestChimpGroup_AdmissionMatchesDecode pins a group whose columns the forecast and the decode once counted differently:
// GroupMinColumns columns on the density threshold, beside columns asked for one value more than they have bits.
// Neither counts the second kind, so the first kind enters the kernel; counted, the second kind would thin the group out.
func TestChimpGroup_AdmissionMatchesDecode(t *testing.T) {
	lanes := make([]chimpGroupLane, 8)
	for i := range lanes {
		lanes[i] = chimpGroupLane{col: make([]byte, 8), count: 2, tail: chimpGroupReadAhead}
		if i < GroupMinColumns {
			lanes[i] = chimpGroupLane{col: make([]byte, 8+6), count: 1 + 48/chimpGroupMinBitsPerValue, tail: chimpGroupReadAhead}
		}
	}
	g := chimpGroupCheck(t, "columns that claim too much", lanes, rand.New(rand.NewSource(1)))
	if chimpGroupKernelRuns(t) {
		require.Equal(t, GroupMinColumns, g.Columns, "columns in the kernel")
	}
}

// TestChimpGroup_KernelState calls the kernel itself and pins its contract:
// it completes the steps every lane can take and no more, row r holds the values of step r,
// it writes no row it did not complete,
// and the state it leaves is the state of DecodeAll's loop after the same number of values,
// which is what lets the scalar loop take over from it.
func TestChimpGroup_KernelState(t *testing.T) {
	chimpGroupRequireKernel(t)
	const sentinel = 0xA5A5A5A5A5A5A5A5
	rng := rand.New(rand.NewSource(41))
	dense := chimpGroupDense(t, 8, 80, 7)

	// wideAt returns a dense column whose value k is a random bit pattern.
	wideAt := func(lane, k int) []float64 {
		col := slices.Clone(dense[lane])
		col[k] = math.Float64frombits(rng.Uint64() | 1<<63)

		return col
	}
	cases := []struct {
		name string
		cols [][]float64
	}{
		{"every value taken", dense},
		{"a value wider than the window", append(slices.Clone(dense[:3]), append([][]float64{wideAt(3, 41)}, dense[4:]...)...)},
		{"a lane that ends", append(slices.Clone(dense[:6]), dense[6][:20], dense[7])},
		{"unchanged runs", append(slices.Clone(dense[:5]), slices.Repeat([]float64{2.5}, 80), dense[6], dense[7])},
	}

	for _, tc := range cases {
		var encoded [8][]byte
		var traces [8][]chimpGroupStep
		limit := chimpGroupRows
		for i, col := range tc.cols {
			column := chimpGroupEncode(col)
			// The kernel is not told a count: it walks to the lane's end, zero padding bits included.
			traces[i] = chimpGroupTrace(column, math.MaxInt32)
			taken := len(traces[i]) - 1
			for k, s := range traces[i] {
				if s.wide {
					taken = k - 1

					break
				}
			}
			limit = min(limit, taken)
			encoded[i] = append(column, make([]byte, chimpGroupReadAhead)...)
		}
		if tc.name == "every value taken" {
			require.Equal(t, chimpGroupRows, limit)
		} else if tc.name != "unchanged runs" {
			require.Less(t, limit, chimpGroupRows, tc.name)
		}

		for steps := range chimpGroupRows + 1 {
			var st chimpGroupState
			for i := range encoded {
				first := traces[i][0]
				st.base[i] = &encoded[i][0]
				st.pos[i] = uint64(first.bitPos)
				st.end[i] = uint64(len(encoded[i])-chimpGroupReadAhead) * 8
				st.prev[i], st.stored[i] = first.prev, uint64(first.stored)
			}
			before := st
			rows := slices.Repeat([]uint64{sentinel}, (steps+1)*8)

			got := chimpGroupKernel(&st, steps, &rows[0])
			require.Equal(t, min(steps, limit), got, "%s: steps completed of %d", tc.name, steps)
			for i := range st.base {
				// Same compares the pointers; Equal would compare the bytes they point to.
				require.Same(t, before.base[i], st.base[i], "%s: base of lane %d", tc.name, i)
			}
			require.Equal(t, before.end, st.end, tc.name)
			for i := range encoded {
				s := traces[i][got]
				require.Equal(t, uint64(s.bitPos), st.pos[i], "%s: lane %d position after %d steps", tc.name, i, got)
				require.Equal(t, s.prev, st.prev[i], "%s: lane %d previous value after %d steps", tc.name, i, got)
				require.Equal(t, uint64(s.stored), st.stored[i], "%s: lane %d stored leading after %d steps", tc.name, i, got)
				for r := range got {
					if rows[r*8+i] != traces[i][r+1].prev {
						t.Fatalf("%s: lane %d row %d of %d differs from DecodeAll", tc.name, i, r, got)
					}
				}
			}
			if slices.ContainsFunc(rows[got*8:], func(word uint64) bool { return word != sentinel }) {
				t.Fatalf("%s: a write past the %d completed rows of %d asked for", tc.name, got, steps)
			}
		}
	}
}

// TestChimpGroup_Differential compares random groups with DecodeAll, lane by lane, over the whole destination:
// undamaged columns of 30 to 40 values, and columns short of read-ahead, cut short, over-counted, under-counted
// and bit-flipped, from every kind of the monitoring mix, the edge columns and random bit patterns.
func TestChimpGroup_Differential(t *testing.T) {
	rng := rand.New(rand.NewSource(29))
	columns, steps := 0, 0
	entering, staying := chimpGroupForecasts[true], chimpGroupForecasts[false]
	run := func(name string, lanes []chimpGroupLane) {
		t.Helper()
		g := chimpGroupCheck(t, name, lanes, rng)
		columns += g.Columns
		steps += g.Steps
	}

	// mix[slot][lane] is a column of the mix's kind `slot`, as one metric has it in eight blobs.
	var mix [chimpMixSetColumns][8][]float64
	var pool [][]float64
	for slot := range mix {
		for lane := range mix[slot] {
			mix[slot][lane] = generateChimpMixColumn(slot, 45, int64(slot*8+lane))
			pool = append(pool, mix[slot][lane])
		}
	}
	edge := chimpAtEdgeColumns()
	for _, name := range slices.Sorted(maps.Keys(edge)) {
		pool = append(pool, edge[name])
	}

	for round := range 3000 {
		n := 8
		if round%5 == 0 {
			n = 1 + rng.Intn(8)
		}
		// Every other round is one metric's undamaged columns, so whole columns pass through the kernel.
		clean := round%2 == 1
		slot := rng.Intn(len(mix))
		lanes := make([]chimpGroupLane, n)
		for i := range lanes {
			col := pool[rng.Intn(len(pool))]
			count := 1 + rng.Intn(min(len(col), 45))
			if clean {
				col = mix[slot][i]
				count = 30 + rng.Intn(11)
			}
			l := chimpGroupLane{col: chimpGroupEncode(col[:count]), count: count, tail: chimpGroupReadAhead + rng.Intn(4), pre: rng.Intn(9)}
			edit := rng.Intn(12)
			if clean {
				edit = -1
			}
			switch edit {
			case 0: // too little read-ahead, down to none
				l.tail = rng.Intn(chimpGroupReadAhead)
				l.spare = rng.Intn(2) * chimpGroupReadAhead
			case 1: // cut short, down to nothing
				l.cut = rng.Intn(len(l.col) + 1)
			case 2: // more values asked for than encoded
				l.count += 1 + rng.Intn(6)
			case 3: // flipped bits
				for range 1 + rng.Intn(3) {
					l.col[rng.Intn(len(l.col))] ^= 1 << uint(rng.Intn(8))
				}
			case 4: // fewer values asked for than encoded
				l.count = 1 + rng.Intn(l.count)
			default: // left as it is
			}
			lanes[i] = l
		}
		run("round "+strconv.Itoa(round), lanes)
	}

	// Lanes without a count or without bytes.
	full := chimpGroupEncode(pool[0])
	run("empty", []chimpGroupLane{{}, {col: full, count: 0, tail: chimpGroupReadAhead}, {col: full, count: 30, cut: len(full)}, {}})

	t.Logf("kernel: %d columns, %d steps", columns, steps)
	if !chimpGroupKernelRuns(t) {
		return
	}
	// chimpGroupCheck compared the kernel's use with the forecast of GroupAdmission in every clean round,
	// and in the other rounds whose lanes claim no more values than they have bits.
	// 82 of the mix's 100 kinds enter, so at least half of the 1,500 clean rounds do and at least a tenth do not.
	entering, staying = chimpGroupForecasts[true]-entering, chimpGroupForecasts[false]-staying
	t.Logf("forecasts compared: %d groups entering, %d staying out", entering, staying)
	if entering < 750 || staying < 150 {
		t.Fatalf("the forecast was compared for %d groups entering and %d staying out: too few", entering, staying)
	}
	// 1,500 rounds are clean, each of eight columns of at least 30 values, and 82 of the mix's 100 kinds are dense enough.
	// The floor is two thirds of that.
	if columns < 6500 || steps < 23000 {
		t.Fatalf("the kernel took %d columns for %d steps: the rounds did not exercise it", columns, steps)
	}
}

// TestChimpGroup_StateLayout pins what the kernel's memory safety rests on in the Go code:
// the lane bases are pointers the collector sees, every field is eight 64-bit lanes as the assembly loads it,
// and none of the files that build or pass the state reaches for unsafe.
func TestChimpGroup_StateLayout(t *testing.T) {
	state := reflect.TypeOf(chimpGroupState{})
	want := []struct {
		name string
		typ  reflect.Type
	}{
		{"base", reflect.TypeOf([8]*byte{})},
		{"pos", reflect.TypeOf([8]uint64{})},
		{"end", reflect.TypeOf([8]uint64{})},
		{"prev", reflect.TypeOf([8]uint64{})},
		{"stored", reflect.TypeOf([8]uint64{})},
	}
	require.Equal(t, len(want), state.NumField())
	for i, w := range want {
		f := state.Field(i)
		require.Equal(t, w.name, f.Name)
		require.Equal(t, w.typ, f.Type, "chimpGroupState.%s", w.name)
		if runtime.GOARCH == "amd64" {
			// The kernel addresses the fields at these offsets; a 32-bit build has narrower pointers and no kernel.
			require.Equal(t, uintptr(i*64), f.Offset, "chimpGroupState.%s", w.name)
		}
	}

	for _, file := range []string{"chimp_group.go", "chimp_decsimd_amd64.go", "chimp_decsimd_noasm.go"} {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		require.NoError(t, err)
		for _, imp := range parsed.Imports {
			require.NotEqual(t, `"unsafe"`, imp.Path.Value, "%s imports unsafe", file)
		}
	}
}

// TestChimpGroup_GCAndStackMoves decodes groups of eight columns of 130 to 200 values, several kernel calls each,
// while two other goroutines allocate and run the collector in a loop, at GOGC=1.
// The group, with the lane bases and the kernel's rows, lies on the goroutine's stack:
// before each Decode the goroutine recurses deep and returns, so its stack is grown and due to shrink,
// and a shrink moves the group.
// In the first variant the columns are copied into an array of the decoding frame, which a base held as an integer
// would leave on the stack; with the bases held as pointers the compiler moves it to the heap.
// In the second each payload is a heap object of its own, which nothing but the group references.
// Every result is compared with DecodeAll on heap copies of the columns.
// The test is supplementary: it cannot show that a stack moved, or a collection ran, between two kernel calls.
func TestChimpGroup_GCAndStackMoves(t *testing.T) {
	chimpGroupRequireKernel(t)
	runs := chimpGroupStressRuns
	if testing.Short() {
		runs = chimpGroupStressShortRuns
	}

	var (
		columns      [8][]byte
		counts       [8]int
		want         [8][]float64
		wantProduced [8]int
	)
	for i, col := range chimpGroupDense(t, 8, 200, 61) {
		counts[i] = 130 + 10*i
		columns[i] = chimpGroupEncode(col[:counts[i]])
		require.LessOrEqual(t, len(columns[i])+chimpGroupReadAhead, chimpGroupStressBytes, "column %d", i)
		want[i] = make([]float64, counts[i]+1)
		chimpGroupFill(want[i])
		wantProduced[i] = NumericChimpDecoder{}.DecodeAll(slices.Clone(columns[i]), counts[i], want[i])
		require.Equal(t, counts[i], wantProduced[i], "column %d", i)
	}

	defer debug.SetGCPercent(debug.SetGCPercent(1))
	var (
		stop atomic.Bool
		wg   sync.WaitGroup
	)
	for range 2 {
		wg.Go(func() {
			garbage := make([][]byte, 64)
			for n := 0; !stop.Load(); n++ {
				garbage[n%len(garbage)] = make([]byte, 512+n%4096)
				if n%8 == 0 {
					runtime.GC()
				}
			}
		})
	}
	defer func() {
		stop.Store(true)
		wg.Wait()
	}()

	for _, variant := range []struct {
		name   string
		decode func(columns *[8][]byte, counts *[8]int, dst *[8][]float64) ([8]int, int)
	}{
		{"payloads in an array of the frame", chimpGroupDecodeOnStack},
		{"payloads on the heap", chimpGroupDecodeOnHeap},
	} {
		var dst [8][]float64
		for i := range dst {
			dst[i] = make([]float64, counts[i]+1)
		}
		for run := range runs {
			for i := range dst {
				chimpGroupFill(dst[i])
			}
			chimpGroupGrowStack(64 + run%64)
			produced, calls := variant.decode(&columns, &counts, &dst)
			if produced != wantProduced || calls < 2 {
				t.Fatalf("%s, run %d: produced %v in %d kernel calls, want %v in two or more", variant.name, run, produced, calls, wantProduced)
			}
			for i := range dst {
				if !chimpGroupSameBits(dst[i], want[i]) {
					t.Fatalf("%s, run %d: lane %d differs from DecodeAll", variant.name, run, i)
				}
			}
		}
	}
}
