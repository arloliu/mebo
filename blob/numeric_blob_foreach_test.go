package blob

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/endian"
	"github.com/arloliu/mebo/errs"
	"github.com/arloliu/mebo/format"
	"github.com/arloliu/mebo/internal/collisiontest"
	ienc "github.com/arloliu/mebo/internal/encoding"
	"github.com/arloliu/mebo/internal/hash"
	"github.com/arloliu/mebo/internal/pool"
	"github.com/arloliu/mebo/section"
)

// buildForEachTestBlob encodes 3 metrics × 50 points with the given encodings
// and tag mode, returning the decoded blob and metric IDs.
func buildForEachTestBlob(t *testing.T, tsEnc, valEnc format.EncodingType, withTags bool) (NumericBlob, []uint64) {
	t.Helper()

	startTime := time.Unix(1700000000, 0).UTC()
	opts := []NumericEncoderOption{
		WithTimestampEncoding(tsEnc),
		WithValueEncoding(valEnc),
	}
	if withTags {
		opts = append(opts, WithTagsEnabled(true))
	}

	encoder, err := NewNumericEncoder(startTime, opts...)
	require.NoError(t, err)

	const numMetrics = 3
	const points = 50

	metricIDs := make([]uint64, numMetrics)
	for m := range numMetrics {
		metricIDs[m] = uint64(1000 + m*7)
		require.NoError(t, encoder.StartMetricID(metricIDs[m], points))

		ts := startTime.UnixMicro()
		val := 100.0 * float64(m+1)
		for i := range points {
			ts += int64(time.Second/time.Microsecond) + int64(i%7)
			val *= 1.001
			tag := ""
			if withTags {
				tag = fmt.Sprintf("tag-%d-%d", m, i)
			}
			require.NoError(t, encoder.AddDataPoint(ts, val, tag))
		}

		require.NoError(t, encoder.EndMetric())
	}

	data, err := encoder.Finish()
	require.NoError(t, err)

	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	return blob, metricIDs
}

// TestNumericBlob_ForEach_MatchesAll verifies ForEach yields exactly the same
// (index, data point) sequence as All across every encoding combination, with
// and without tags.
func TestNumericBlob_ForEach_MatchesAll(t *testing.T) {
	tsEncodings := []format.EncodingType{format.TypeRaw, format.TypeDelta, format.TypeDeltaPacked}
	valEncodings := []format.EncodingType{format.TypeRaw, format.TypeGorilla, format.TypeChimp, format.TypeALP, format.TypeALPRLE}

	for _, tsEnc := range tsEncodings {
		for _, valEnc := range valEncodings {
			for _, withTags := range []bool{false, true} {
				name := fmt.Sprintf("%v_%v_tags=%v", tsEnc, valEnc, withTags)
				t.Run(name, func(t *testing.T) {
					blob, metricIDs := buildForEachTestBlob(t, tsEnc, valEnc, withTags)

					for _, id := range metricIDs {
						var wantIdx []int
						var want []NumericDataPoint
						for i, dp := range blob.All(id) {
							wantIdx = append(wantIdx, i)
							want = append(want, dp)
						}
						require.NotEmpty(t, want)

						var gotIdx []int
						var got []NumericDataPoint
						found := blob.ForEach(id, func(i int, dp NumericDataPoint) bool {
							gotIdx = append(gotIdx, i)
							got = append(got, dp)

							return true
						})

						require.True(t, found)
						require.Equal(t, wantIdx, gotIdx)
						require.Equal(t, want, got)
					}
				})
			}
		}
	}
}

func TestNumericBlob_ForEach_EarlyStop(t *testing.T) {
	blob, metricIDs := buildForEachTestBlob(t, format.TypeDelta, format.TypeGorilla, false)

	var got []NumericDataPoint
	found := blob.ForEach(metricIDs[0], func(i int, dp NumericDataPoint) bool {
		got = append(got, dp)

		return i < 9 // stop after 10 points
	})

	require.True(t, found, "early stop must still report the metric as found")
	require.Len(t, got, 10)

	// The early prefix matches All.
	want := make([]NumericDataPoint, 0, 10)
	for _, dp := range blob.All(metricIDs[0]) {
		want = append(want, dp)
		if len(want) == 10 {
			break
		}
	}
	require.Equal(t, want, got)
}

func TestNumericBlob_ForEach_DeltaPackedRawParity(t *testing.T) {
	pointCounts := []int{1, 2, 3, 4, 10, 30, 48, 50, 56, 60, 63, 64, 65, 80, 100, 200}

	for _, pointCount := range pointCounts {
		for _, bigEndian := range []bool{false, true} {
			for _, withTags := range []bool{false, true} {
				name := fmt.Sprintf("points=%d/big-endian=%t/tags=%t", pointCount, bigEndian, withTags)
				t.Run(name, func(t *testing.T) {
					blob, metricID := buildDeltaPackedRawForEachBlob(t, pointCount, bigEndian, withTags)
					wantIdx, wantPoints := collectAllDataPoints(blob, metricID)

					gotIdx, gotPoints, found := collectForEachDataPoints(blob, metricID, pointCount)
					require.True(t, found)
					require.Equal(t, wantIdx, gotIdx)
					require.Equal(t, wantPoints, gotPoints)

					stopAfter := max(1, (pointCount+1)/2)
					gotIdx, gotPoints, found = collectForEachDataPoints(blob, metricID, stopAfter)
					require.True(t, found, "early stop must still report the metric as found")
					require.Equal(t, wantIdx[:stopAfter], gotIdx)
					require.Equal(t, wantPoints[:stopAfter], gotPoints)
				})
			}
		}
	}
}

func TestNumericBlob_ForEach_DeltaPackedRawTruncatedParity(t *testing.T) {
	pointCounts := []int{1, 2, 3, 4, 10, 30, 48, 50, 56, 60, 63, 64, 65, 80, 100, 200}

	for _, pointCount := range pointCounts {
		for _, bigEndian := range []bool{false, true} {
			name := fmt.Sprintf("points=%d/big-endian=%t", pointCount, bigEndian)
			t.Run(name, func(t *testing.T) {
				blob, metricID := buildDeltaPackedRawForEachBlob(t, pointCount, bigEndian, false)
				entry, ok := blob.index.GetByID(metricID)
				require.True(t, ok)

				for keepBytes := 0; keepBytes < entry.TimestampLength; keepBytes++ {
					truncated := truncateForEachBlobPayload(blob, metricID, true, keepBytes)
					wantIdx, wantPoints := collectAllDataPoints(truncated, metricID)
					gotIdx, gotPoints, found := collectForEachDataPoints(truncated, metricID, pointCount)
					require.True(t, found, "timestamp bytes kept: %d", keepBytes)
					require.Equal(t, wantIdx, gotIdx, "timestamp bytes kept: %d", keepBytes)
					require.Equal(t, wantPoints, gotPoints, "timestamp bytes kept: %d", keepBytes)
				}

				for keepBytes := 0; keepBytes < entry.ValueLength; keepBytes++ {
					truncated := truncateForEachBlobPayload(blob, metricID, false, keepBytes)
					wantIdx, wantPoints := collectAllDataPoints(truncated, metricID)
					gotIdx, gotPoints, found := collectForEachDataPoints(truncated, metricID, pointCount)
					require.True(t, found, "value bytes kept: %d", keepBytes)
					require.Equal(t, wantIdx, gotIdx, "value bytes kept: %d", keepBytes)
					require.Equal(t, wantPoints, gotPoints, "value bytes kept: %d", keepBytes)
				}
			})
		}
	}
}

func TestNumericBlob_ForEach_NotFound(t *testing.T) {
	blob, _ := buildForEachTestBlob(t, format.TypeDelta, format.TypeGorilla, false)

	called := false
	found := blob.ForEach(99999, func(int, NumericDataPoint) bool {
		called = true

		return true
	})

	require.False(t, found)
	require.False(t, called)
}

func TestNumericBlob_ForEachByName(t *testing.T) {
	startTime := time.Unix(1700000000, 0).UTC()
	encoder, err := NewNumericEncoder(startTime)
	require.NoError(t, err)

	require.NoError(t, encoder.StartMetricName("cpu.usage", 3))
	base := startTime.UnixMicro()
	for i := range 3 {
		require.NoError(t, encoder.AddDataPoint(base+int64(i)*1000000, float64(i)+0.5, ""))
	}
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	want := make([]NumericDataPoint, 0, 3)
	for _, dp := range blob.AllByName("cpu.usage") {
		want = append(want, dp)
	}
	require.Len(t, want, 3)

	var got []NumericDataPoint
	found := blob.ForEachByName("cpu.usage", func(_ int, dp NumericDataPoint) bool {
		got = append(got, dp)

		return true
	})
	require.True(t, found)
	require.Equal(t, want, got)

	require.False(t, blob.ForEachByName("no.such.metric", func(int, NumericDataPoint) bool {
		return true
	}))
}

// TestNumericBlob_DeltaPackedRawMatchesColumnDecoders pins the bulk DeltaPacked + Raw loop against the column decoders:
// ForEach and All yield the rows the timestamp iterator yields, each with the value At reads (0 where it reads none),
// on complete columns, on every truncation of either column, and on both sides of the pool's size limit.
// The rows are also checked against FusedDeltaPackedEach, the per-point loop the bulk decode replaced.
func TestNumericBlob_DeltaPackedRawMatchesColumnDecoders(t *testing.T) {
	reference := func(blob NumericBlob, metricID uint64) []NumericDataPoint {
		entry, ok := blob.index.GetByID(metricID)
		require.True(t, ok)
		tsBytes, _ := safeSlice(blob.tsPayload, entry.TimestampOffset, entry.TimestampLength)
		valBytes, _ := safeSlice(blob.valPayload, entry.ValueOffset, entry.ValueLength)
		var tsDec ienc.TimestampDeltaPackedDecoder
		valDec := ienc.NewNumericRawUnsafeDecoder(blob.Engine())
		var points []NumericDataPoint
		i := 0
		for ts := range tsDec.All(tsBytes, entry.Count) {
			val, _ := valDec.At(valBytes, i, entry.Count)
			points = append(points, NumericDataPoint{Ts: ts, Val: val})
			i++
		}
		var fused []NumericDataPoint
		ienc.FusedDeltaPackedEach(tsBytes, entry.Count, 0, func(i int, ts int64) bool {
			val, _ := valDec.At(valBytes, i, entry.Count)
			fused = append(fused, NumericDataPoint{Ts: ts, Val: val})

			return true
		})
		require.Equal(t, points, fused, "the column iterator and the fused loop disagree")

		return points
	}
	requireMatches := func(t *testing.T, blob NumericBlob, metricID uint64, what string) {
		t.Helper()
		want := reference(blob, metricID)
		_, all := collectAllDataPoints(blob, metricID)
		_, each, _ := collectForEachDataPoints(blob, metricID, math.MaxInt)
		if len(want) == 0 {
			require.Empty(t, all, what)
			require.Empty(t, each, what)

			return
		}
		require.Equal(t, want, all, "All, %s", what)
		require.Equal(t, want, each, "ForEach, %s", what)
	}

	for _, pointCount := range []int{1, 2, 3, 4, 10, 30, 48, 50, 56, 60, 63, 64, 65, 80, 100, 200} {
		t.Run(fmt.Sprintf("points=%d", pointCount), func(t *testing.T) {
			blob, metricID := buildDeltaPackedRawForEachBlob(t, pointCount, endian.IsNativeBigEndian(), false)
			require.True(t, blob.sameByteOrder, "the bulk loop needs a blob in the host's byte order")
			requireMatches(t, blob, metricID, "complete")
			entry, ok := blob.index.GetByID(metricID)
			require.True(t, ok)
			for keep := 0; keep < entry.TimestampLength; keep++ {
				requireMatches(t, truncateForEachBlobPayload(blob, metricID, true, keep), metricID, fmt.Sprintf("timestamp bytes kept: %d", keep))
			}
			for keep := 0; keep < entry.ValueLength; keep++ {
				requireMatches(t, truncateForEachBlobPayload(blob, metricID, false, keep), metricID, fmt.Sprintf("value bytes kept: %d", keep))
			}
		})
	}

	for _, points := range []int{pool.MaxPooledDecodeInt64s, pool.MaxPooledDecodeInt64s + 1} {
		t.Run(fmt.Sprintf("points=%d", points), func(t *testing.T) {
			opts := []NumericEncoderOption{WithBlobLayoutV2(), WithTimestampEncoding(format.TypeDeltaPacked), WithValueEncoding(format.TypeRaw)}
			if endian.IsNativeBigEndian() {
				opts = append(opts, WithBigEndian())
			}
			blob := handleTestBlob(t, handleTestMetrics(1, points, handleTestIdentical), false, opts...)
			require.True(t, blob.sameByteOrder)
			requireMatches(t, blob, 1, "complete")
		})
	}
}

func buildDeltaPackedRawForEachBlob(t *testing.T, pointCount int, bigEndian, withTags bool) (NumericBlob, uint64) {
	t.Helper()

	opts := []NumericEncoderOption{
		WithTimestampEncoding(format.TypeDeltaPacked),
		WithValueEncoding(format.TypeRaw),
		WithTagsEnabled(withTags),
	}
	if bigEndian {
		opts = append(opts, WithBigEndian())
	} else {
		opts = append(opts, WithLittleEndian())
	}

	startTime := time.Unix(1700000000, 0).UTC()
	encoder, err := NewNumericEncoder(startTime, opts...)
	require.NoError(t, err)

	const metricID = uint64(1234)
	require.NoError(t, encoder.StartMetricID(metricID, pointCount))
	for i := range pointCount {
		timestamp := startTime.UnixMicro() + int64(i)*1_000_000 + int64((i*i)%17)
		value := float64(i*19)/7 + 0.125
		tag := ""
		if withTags {
			tag = fmt.Sprintf("tag-%d", i%5)
		}
		require.NoError(t, encoder.AddDataPoint(timestamp, value, tag))
	}
	require.NoError(t, encoder.EndMetric())

	data, err := encoder.Finish()
	require.NoError(t, err)
	decoder, err := NewNumericDecoder(data)
	require.NoError(t, err)
	blob, err := decoder.Decode()
	require.NoError(t, err)

	return blob, metricID
}

func collectAllDataPoints(blob NumericBlob, metricID uint64) ([]int, []NumericDataPoint) {
	indices := make([]int, 0, blob.Len(metricID))
	points := make([]NumericDataPoint, 0, blob.Len(metricID))
	for index, point := range blob.All(metricID) {
		indices = append(indices, index)
		points = append(points, point)
	}

	return indices, points
}

func collectForEachDataPoints(blob NumericBlob, metricID uint64, stopAfter int) ([]int, []NumericDataPoint, bool) {
	indices := make([]int, 0, min(blob.Len(metricID), stopAfter))
	points := make([]NumericDataPoint, 0, min(blob.Len(metricID), stopAfter))
	found := blob.ForEach(metricID, func(index int, point NumericDataPoint) bool {
		indices = append(indices, index)
		points = append(points, point)

		return len(points) < stopAfter
	})

	return indices, points, found
}

func truncateForEachBlobPayload(blob NumericBlob, metricID uint64, timestamp bool, keepBytes int) NumericBlob {
	ord, _ := blob.index.getOrdinal(metricID)
	entry := blob.index.sorted[ord]
	blob.index.sorted = slices.Clone(blob.index.sorted)
	if timestamp {
		blob.tsPayload = blob.tsPayload[:entry.TimestampOffset+keepBytes]
		entry.TimestampLength = keepBytes
	} else {
		blob.valPayload = blob.valPayload[:entry.ValueOffset+keepBytes]
		entry.ValueLength = keepBytes
	}
	blob.index.sorted[ord] = entry

	return blob
}

// TestNumericBlob_ForEachColumnsCapturingCallbacksStayOnStack pins the blob's column loops:
// ForEachValues and ForEachTimestamps do not allocate for a callback literal built at the call site and capturing a local,
// because the loops call yield by name and escape analysis keeps the closure on the stack.
// The existing allocation test builds its closures outside the measured function, which hides a leak.
func TestNumericBlob_ForEachColumnsCapturingCallbacksStayOnStack(t *testing.T) {
	if raceEnabled {
		t.Skip("race detector adds allocations")
	}
	layouts := []struct {
		ts, val format.EncodingType
	}{
		{format.TypeRaw, format.TypeRaw},
		{format.TypeDelta, format.TypeGorilla},
		{format.TypeDeltaPacked, format.TypeChimp},
		{format.TypeDeltaPacked, format.TypeRaw},
		{format.TypeDeltaPacked, format.TypeALP},
		{format.TypeRaw, format.TypeALPRLE},
	}
	for _, layout := range layouts {
		for _, tagged := range []bool{false, true} {
			name := fmt.Sprintf("%s/%s/tags=%t", layout.ts, layout.val, tagged)
			blob, ids := buildForEachTestBlob(t, layout.ts, layout.val, tagged)
			id := ids[0]
			var sinkTS int64
			var sinkV float64
			allocs := testing.AllocsPerRun(50, func() {
				var sum int64
				blob.ForEachTimestamps(id, func(_ int, ts int64) bool { sum += ts; return true })
				sinkTS += sum
			})
			require.Zerof(t, allocs, "%s ForEachTimestamps", name)
			allocs = testing.AllocsPerRun(50, func() {
				var sum float64
				blob.ForEachValues(id, func(_ int, v float64) bool { sum += v; return true })
				sinkV += sum
			})
			require.Zerof(t, allocs, "%s ForEachValues", name)
			require.NotZero(t, sinkTS)
			require.NotZero(t, sinkV)
		}
	}
}

// TestNumericBlob_ForEachPointsCapturingCallbacksStayOnStack is the point-form companion of the column test above:
// ForEach does not allocate for a callback literal built at the call site and capturing a local,
// on every timestamp and value encoding pair, with and without tags.
// ALP and ALP-RLE values decode both columns into pooled scratch buffers, so they stay at zero too,
// and a tagged blob allows one allocation per point, the tag string the decoder copies out of the payload.
func TestNumericBlob_ForEachPointsCapturingCallbacksStayOnStack(t *testing.T) {
	if raceEnabled {
		t.Skip("race detector adds allocations")
	}
	tsEncs := []format.EncodingType{format.TypeRaw, format.TypeDelta, format.TypeDeltaPacked}
	valEncs := []format.EncodingType{format.TypeRaw, format.TypeGorilla, format.TypeChimp, format.TypeALP, format.TypeALPRLE}
	for _, tsEnc := range tsEncs {
		for _, valEnc := range valEncs {
			for _, tagged := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/tags=%t", tsEnc, valEnc, tagged), func(t *testing.T) {
					blob, ids := buildForEachTestBlob(t, tsEnc, valEnc, tagged)
					id := ids[0]
					want := 0.0
					if tagged {
						want += float64(blob.Len(id))
					}
					var sink int64
					allocs := testing.AllocsPerRun(50, func() {
						var sum int64
						blob.ForEach(id, func(_ int, dp NumericDataPoint) bool { sum += dp.Ts + int64(len(dp.Tag)); return true })
						sink += sum
					})
					require.InDelta(t, want, allocs, 0, "ForEach")
					require.NotZero(t, sink)
				})
			}
		}
	}
}

// TestNumericBlob_ForEachALPPooledColumns pins the pooled column buffers of the ALP point loop:
// it agrees with All on both sides of the pool's size limit, on a timestamp column cut short into a dirty buffer,
// under a callback that re-enters ForEach, and after a callback that panics.
func TestNumericBlob_ForEachALPPooledColumns(t *testing.T) {
	for _, valEnc := range []format.EncodingType{format.TypeALP, format.TypeALPRLE} {
		for _, points := range []int{pool.MaxPooledDecodeFloat64s, pool.MaxPooledDecodeFloat64s + 1} {
			t.Run(fmt.Sprintf("%s/%d", valEnc, points), func(t *testing.T) {
				blob := handleTestBlob(t, handleTestMetrics(1, points, handleTestIdentical), false,
					WithBlobLayoutV2(), WithTimestampEncoding(format.TypeDeltaPacked), WithValueEncoding(valEnc))
				wantIdx, want := collectAllDataPoints(blob, 1)
				require.Len(t, want, points)
				gotIdx, got, found := collectForEachDataPoints(blob, 1, math.MaxInt)
				require.True(t, found)
				require.Equal(t, wantIdx, gotIdx)
				require.Equal(t, want, got)
			})
		}

		t.Run(fmt.Sprintf("%s/truncated-into-dirty-buffers", valEnc), func(t *testing.T) {
			blob, ids := buildForEachTestBlob(t, format.TypeDelta, valEnc, true)
			id := ids[0]
			ord, _ := blob.index.getOrdinal(id)
			short := truncateForEachBlobPayload(blob, id, true, blob.index.sorted[ord].TimestampLength/2)
			for range 8 {
				dirtyTS := pool.GetDecodeInt64Slice(64)
				for i := range *dirtyTS {
					(*dirtyTS)[i] = -1
				}
				pool.PutDecodeInt64Slice(dirtyTS)
				dirtyVals := pool.GetDecodeFloat64Slice(64)
				for i := range *dirtyVals {
					(*dirtyVals)[i] = -1
				}
				pool.PutDecodeFloat64Slice(dirtyVals)
			}
			wantIdx, want := collectAllDataPoints(short, id)
			require.NotEmpty(t, want)
			require.Less(t, len(want), short.Len(id))
			gotIdx, got, _ := collectForEachDataPoints(short, id, math.MaxInt)
			require.Equal(t, wantIdx, gotIdx)
			require.Equal(t, want, got)
		})

		t.Run(fmt.Sprintf("%s/re-entrant-and-panicking-callbacks", valEnc), func(t *testing.T) {
			blob, ids := buildForEachTestBlob(t, format.TypeDeltaPacked, valEnc, false)
			id := ids[0]
			_, want := collectAllDataPoints(blob, id)

			var outer []NumericDataPoint
			blob.ForEach(id, func(i int, dp NumericDataPoint) bool {
				if i == 0 {
					_, inner, _ := collectForEachDataPoints(blob, id, math.MaxInt)
					require.Equal(t, want, inner, "nested ForEach")
				}
				outer = append(outer, dp)

				return true
			})
			require.Equal(t, want, outer, "outer ForEach after a nested one")

			require.Panics(t, func() {
				blob.ForEach(id, func(i int, _ NumericDataPoint) bool {
					if i == 3 {
						panic("callback")
					}

					return true
				})
			})
			_, after, _ := collectForEachDataPoints(blob, id, math.MaxInt)
			require.Equal(t, want, after, "ForEach after a panicking callback")
		})
	}
}

// The tests below compare the point walks with the encoding dispatch,
// which every walk ran before one could pair a value column with timestamps that are already decoded.
// Each one records what a walk does and compares it with the same record of a reference walk.

// How a recorded point walk ended.
const (
	pointWalkReturned = "returned"
	pointWalkStopped  = "stopped"
	pointWalkPanicked = "panicked"
)

// pointWalkTimestampEncodings and pointWalkValueEncodings are the encodings a numeric blob can be written with.
var (
	pointWalkTimestampEncodings = []format.EncodingType{format.TypeRaw, format.TypeDelta, format.TypeDeltaPacked}
	pointWalkValueEncodings     = []format.EncodingType{
		format.TypeRaw, format.TypeGorilla, format.TypeChimp, format.TypeALP, format.TypeALPRLE,
	}
)

// pointCall is one callback of a point walk.
// The value is kept as its bit pattern, so that NaN and negative zero compare by their bits.
type pointCall struct {
	idx int
	ts  int64
	val uint64
	tag string
}

// pointWalk is what a point walk did: its callbacks in order, how it ended, and the panic value if it panicked.
type pointWalk struct {
	calls []pointCall
	end   string
	fault string
}

// pointWalkPoolPoints is the number of leading elements recordPointWalk zeroes in the pooled value buffers before a walk.
// It is 0 outside the tests that call zeroPointWalkPools.
var pointWalkPoolPoints int

// pointWalkFunc is a point walk waiting for its callback.
type pointWalkFunc func(yield func(int, NumericDataPoint) bool)

// pointWalkSpec describes one blob of the point walk fixtures.
type pointWalkSpec struct {
	tsEnc, valEnc format.EncodingType
	shared        bool // the metrics have the same timestamps and the encoder stores them once
	v1            bool // the V1 layout, which cannot share timestamps
	extended      bool // 32-byte index entries, forced by one more metric whose value column passes 64 KiB
	bigEndian     bool
	tagged        bool
	names         []string // the metric names; "m0", "m1" and so on when nil
	metrics       int      // the number of metrics when names is nil
	points        int      // the points of every metric
}

// pointWalkWire is an uncompressed blob with 16-byte index entries, opened for editing its stored bytes.
type pointWalkWire struct {
	data   []byte
	header section.NumericHeader
	engine endian.EndianEngine
}

// Byte positions of the fields of a 16-byte index entry.
const (
	wireEntryCount      = 8
	wireEntryTimestamps = 10
	wireEntryValues     = 12
)

// pointWalkPaddingPoints is the size of the metric that forces 32-byte index entries.
const pointWalkPaddingPoints = 8200

// String names the blob in a failure message.
func (s pointWalkSpec) String() string {
	layout := "v2"
	if s.v1 {
		layout = "v1"
	}
	if s.extended {
		layout += "-extended"
	}
	order := "little"
	if s.bigEndian {
		order = "big"
	}

	return fmt.Sprintf("%s/%s/%s/shared=%t/%s/tags=%t/points=%d", s.tsEnc, s.valEnc, layout, s.shared, order, s.tagged, s.points)
}

// recordPointWalk runs walk with a callback that records every call and returns false on call number stopAt.
// A stopAt of 0 never stops the walk.
// A panic of the walk is part of the record, so that a walk over a crafted column can be compared like any other.
func recordPointWalk(stopAt int, walk pointWalkFunc) (w pointWalk) {
	if pointWalkPoolPoints > 0 {
		// Two buffers are every buffer the pool can hold here: a walk takes one, and these two calls are the only other user.
		first, second := pool.GetDecodeFloat64Slice(pointWalkPoolPoints), pool.GetDecodeFloat64Slice(pointWalkPoolPoints)
		clear(*first)
		clear(*second)
		pool.PutDecodeFloat64Slice(first)
		pool.PutDecodeFloat64Slice(second)
	}
	w.end = pointWalkReturned
	defer func() {
		if r := recover(); r != nil {
			w.end, w.fault = pointWalkPanicked, fmt.Sprint(r)
		}
	}()
	walk(func(i int, dp NumericDataPoint) bool {
		w.calls = append(w.calls, pointCall{idx: i, ts: dp.Ts, val: math.Float64bits(dp.Val), tag: dp.Tag})
		if len(w.calls) == stopAt {
			w.end = pointWalkStopped

			return false
		}

		return true
	})

	return w
}

// zeroPointWalkPools makes the recorded walks of the calling test start from zeroed pooled value buffers,
// zeroed up to points values.
// An ALP-RLE column cut in memory can leave part of its decode buffer unwritten and yield what an earlier walk left there,
// so without this the encoding dispatch does not yield the same values twice on such a column.
// The pool keeps buffers per processor, so the test runs on one processor, and it starts from an empty pool.
func zeroPointWalkPools(t *testing.T, points int) {
	t.Helper()
	procs := runtime.GOMAXPROCS(1)
	// The first collection moves the pooled buffers aside and the second drops them.
	runtime.GC() //nolint:revive // collecting twice is the only way to empty a sync.Pool
	runtime.GC() //nolint:revive // collecting twice is the only way to empty a sync.Pool
	pointWalkPoolPoints = points
	t.Cleanup(func() {
		pointWalkPoolPoints = 0
		runtime.GOMAXPROCS(procs)
	})
}

// diffPointWalks describes the first difference between two recorded walks in one line, or returns "" when they agree.
func diffPointWalks(want, got pointWalk) string {
	for i := range min(len(want.calls), len(got.calls)) {
		if want.calls[i] != got.calls[i] {
			return fmt.Sprintf("call %d: want %+v, got %+v", i, want.calls[i], got.calls[i])
		}
	}
	if len(want.calls) != len(got.calls) || want.end != got.end || want.fault != got.fault {
		return fmt.Sprintf("want %d calls then %s %q, got %d calls then %s %q",
			len(want.calls), want.end, want.fault, len(got.calls), got.end, got.fault)
	}

	return ""
}

// pointWalkStops returns the call numbers a walk of n calls is stopped at:
// never (0), the first point, a middle one and the last.
func pointWalkStops(n int) []int {
	stops := []int{0, 1}
	for _, stop := range []int{(n + 1) / 2, n} {
		if stop > 1 && !slices.Contains(stops, stop) {
			stops = append(stops, stop)
		}
	}

	return stops
}

// requireSamePointWalk runs got and want with a callback that stops at call number stop,
// fails with one line naming the first difference, and returns what want did.
func requireSamePointWalk(t *testing.T, where string, stop int, want, got pointWalkFunc) pointWalk {
	t.Helper()
	wantWalk := recordPointWalk(stop, want)
	if diff := diffPointWalks(wantWalk, recordPointWalk(stop, got)); diff != "" {
		t.Fatalf("%s, stop at call %d: %s", where, stop, diff)
	}

	return wantWalk
}

// referenceForEachFromEntry is forEachFromEntry as it was before it paired a value column with decoded timestamps:
// the range checks, then the encoding dispatch.
// It stays as it was written, so that the comparison is against a fixed walk.
func referenceForEachFromEntry(b NumericBlob, entry section.NumericIndexEntry, yield func(int, NumericDataPoint) bool) {
	if entry.Count == 0 {
		return
	}

	tsBytes, tsOk := safeSlice(b.tsPayload, entry.TimestampOffset, entry.TimestampLength)
	valBytes, valOk := safeSlice(b.valPayload, entry.ValueOffset, entry.ValueLength)
	if !tsOk || !valOk {
		return
	}

	var tagBytes []byte
	if b.HasTag() && len(b.tagPayload) > 0 {
		var tagOk bool
		tagBytes, tagOk = safeSlice(b.tagPayload, entry.TagOffset, entry.TagLength)
		if !tagOk {
			return
		}
	}

	b.forEachDataPoint(tsBytes, valBytes, tagBytes, entry.Count, yield)
}

// requireBlobPointWalksAgree compares ForEach and ForEachByName on one metric with the reference walk of its entry,
// for a full walk and for a stop at the first, a middle and the last point,
// and checks that both report the metric as found.
func requireBlobPointWalksAgree(t *testing.T, fixture string, b NumericBlob, name string) {
	t.Helper()
	byName, ok := b.lookupMetricEntry(name)
	if !ok {
		t.Fatalf("%s: the blob does not hold metric %q", fixture, name)
	}
	id := byName.MetricID
	byID, _ := b.index.GetByID(id)

	walks := []struct {
		entryPoint string
		entry      section.NumericIndexEntry
		walk       func(yield func(int, NumericDataPoint) bool) bool
	}{
		{"ForEach", byID, func(yield func(int, NumericDataPoint) bool) bool { return b.ForEach(id, yield) }},
		{"ForEachByName", byName, func(yield func(int, NumericDataPoint) bool) bool { return b.ForEachByName(name, yield) }},
	}
	for _, w := range walks {
		where := fixture + " " + name + " " + w.entryPoint
		reference := func(yield func(int, NumericDataPoint) bool) { referenceForEachFromEntry(b, w.entry, yield) }
		stops := []int{0}
		for i := 0; i < len(stops); i++ {
			found := false
			want := requireSamePointWalk(t, where, stops[i], reference, func(yield func(int, NumericDataPoint) bool) {
				found = w.walk(yield)
			})
			if !found && want.end != pointWalkPanicked {
				t.Fatalf("%s, stop at call %d: returned false for a metric the blob holds", where, stops[i])
			}
			if i == 0 {
				stops = pointWalkStops(len(want.calls))
			}
		}
	}
}

// requirePointWalksAgree checks the point walks of one metric of b:
// the blob's two, and those of a handle on the metric, of a copy of it, and of a copy that was materialized.
func requirePointWalksAgree(t *testing.T, fixture string, b NumericBlob, name string) {
	t.Helper()
	requireBlobPointWalksAgree(t, fixture, b, name)

	h, ok := b.MetricByName(name)
	if !ok {
		t.Fatalf("%s: no handle on metric %q", fixture, name)
	}
	requireHandlePointWalksAgree(t, fixture+" "+name+" handle", &h, true)
	copied, materialized := h, h
	materialized.Materialize()
	requireHandlePointWalksAgree(t, fixture+" "+name+" materialized handle", &materialized, true)
	requireHandlePointWalksAgree(t, fixture+" "+name+" handle copied before Materialize", &copied, true)
}

// pointWalkSeries returns the points of the metric at ordinal m:
// timestamps 15 s apart with a jitter, shifted by the ordinal unless the metrics share them,
// values with two decimals that repeat on every third point, and tags that cycle over four hosts.
func pointWalkSeries(points, m int, shared bool) ([]int64, []float64, []string) {
	offset := int64(m)
	if shared {
		offset = 0
	}
	ts, vals, tags := handleTestSeries(points, offset, float64(m)*10)
	for i := range ts {
		ts[i] += int64(i * i % 17)
	}

	return ts, vals, tags
}

// encodePointWalkBlob encodes a blob as the spec describes it, uncompressed, with the given series per metric,
// and returns the stored bytes and the metric names.
// The metrics are started by name and the names are not stored, so each is found by name and by the hash of its name.
func encodePointWalkBlob(tb testing.TB, s pointWalkSpec, series func(m int) ([]int64, []float64, []string)) ([]byte, []string) {
	tb.Helper()
	opts := []NumericEncoderOption{
		WithTimestampEncoding(s.tsEnc), WithValueEncoding(s.valEnc),
		WithTimestampCompression(format.CompressionNone), WithValueCompression(format.CompressionNone),
		WithTagsEnabled(s.tagged), WithLittleEndian(),
	}
	if s.bigEndian {
		opts = append(opts, WithBigEndian())
	}
	if !s.v1 {
		opts = append(opts, WithBlobLayoutV2())
	}
	if s.shared {
		opts = append(opts, WithSharedTimestamps())
	}
	enc, err := NewNumericEncoder(time.UnixMicro(handleTestStart), opts...)
	require.NoError(tb, err)

	names := s.names
	if names == nil {
		for m := range s.metrics {
			names = append(names, "m"+strconv.Itoa(m))
		}
	}
	add := func(name string, ts []int64, vals []float64, tags []string) {
		require.NoError(tb, enc.StartMetricName(name, len(ts)))
		require.NoError(tb, enc.AddDataPoints(ts, vals, tags))
		require.NoError(tb, enc.EndMetric())
	}
	for m, name := range names {
		ts, vals, tags := series(m)
		add(name, ts, vals, tags)
	}
	if s.extended {
		// Values with no pattern take at least 8 bytes each in every value encoding.
		rng := rand.New(rand.NewSource(20261112))
		ts, vals, tags := pointWalkSeries(pointWalkPaddingPoints, len(names), false)
		for i := range vals {
			vals[i] = math.Float64frombits(rng.Uint64())
		}
		add(pointWalkPaddingName(names), ts, vals, tags)
	}
	data, err := enc.Finish()
	require.NoError(tb, err)

	return data, names
}

// pointWalkPaddingName returns a name whose ID sorts before the ID of one of names.
// An entry stores the distance from the previous entry's offsets, and the V2 layout orders entries by ID,
// so the long column forces 32-byte entries only when an entry follows it.
func pointWalkPaddingName(names []string) string {
	for i := 0; ; i++ {
		name := "padding" + strconv.Itoa(i)
		if slices.ContainsFunc(names, func(other string) bool { return hash.ID(name) < hash.ID(other) }) {
			return name
		}
	}
}

// decodePointWalkBlob decodes stored bytes.
func decodePointWalkBlob(data []byte) (NumericBlob, error) {
	dec, err := NewNumericDecoder(data)
	if err != nil {
		return NumericBlob{}, err
	}

	return dec.Decode()
}

// buildPointWalkBlob encodes the blob the spec describes with the series of pointWalkSeries and decodes it.
// It returns the stored bytes, the blob and the metric names.
func buildPointWalkBlob(tb testing.TB, s pointWalkSpec) ([]byte, NumericBlob, []string) {
	tb.Helper()
	data, names := encodePointWalkBlob(tb, s, func(m int) ([]int64, []float64, []string) {
		return pointWalkSeries(s.points, m, s.shared)
	})
	blob, err := decodePointWalkBlob(data)
	require.NoError(tb, err)

	return data, blob, names
}

// pointWalkOrdinal returns the ordinal of the entry ForEachByName resolves for name.
func pointWalkOrdinal(tb testing.TB, b NumericBlob, name string) int {
	tb.Helper()
	entry := b.index.entryByName(name)
	require.NotNil(tb, entry, "metric %q", name)
	for ord := range b.index.sorted {
		if &b.index.sorted[ord] == entry {
			return ord
		}
	}
	tb.Fatalf("metric %q has no ordinal", name)

	return -1
}

// pointWalkGroup returns the pre-decoded timestamps the metric's entry references, nil when it has none,
// and the entry's point count.
func pointWalkGroup(tb testing.TB, b NumericBlob, name string) ([]int64, int) {
	tb.Helper()
	entry := b.index.sorted[pointWalkOrdinal(tb, b, name)]

	return b.sharedTs.lookup(entry.TimestampOffset), entry.Count
}

// requirePointWalkGroup checks that the metric has a pre-decoded group of exactly its point count,
// which is what lets a walk pair the value column with it, or that it has none at all.
func requirePointWalkGroup(tb testing.TB, fixture string, b NumericBlob, name string, complete bool) {
	tb.Helper()
	group, count := pointWalkGroup(tb, b, name)
	if !complete {
		require.Nil(tb, group, "%s %s: pre-decoded group", fixture, name)

		return
	}
	require.NotNil(tb, group, "%s %s: pre-decoded group", fixture, name)
	require.Len(tb, group, count, "%s %s: pre-decoded group", fixture, name)
}

// cutPointWalkValues returns blob with the value column of the entry at ord cut to keep bytes,
// as a slice that keeps the payload's capacity or, when clamp is set, one whose capacity ends with it.
// The value payload ends at the cut, so the columns stored behind it fall outside it.
func cutPointWalkValues(blob NumericBlob, ord, keep int, clamp bool) NumericBlob {
	entry := blob.index.sorted[ord]
	blob.index.sorted = slices.Clone(blob.index.sorted)
	end := entry.ValueOffset + keep
	if clamp {
		blob.valPayload = blob.valPayload[:end:end]
	} else {
		blob.valPayload = blob.valPayload[:end]
	}
	entry.ValueLength = keep
	blob.index.sorted[ord] = entry

	return blob
}

// cutPointWalkTimestamps returns blob with the timestamp column of the entry at ord cut to keep bytes,
// as a slice that keeps the payload's capacity or, when clamp is set, one whose capacity ends with it.
// The entry keeps its offset, so the group decoded when the blob was opened still belongs to it.
// The timestamp payload ends at the cut, so the columns stored behind it fall outside it.
func cutPointWalkTimestamps(blob NumericBlob, ord, keep int, clamp bool) NumericBlob {
	entry := blob.index.sorted[ord]
	blob.index.sorted = slices.Clone(blob.index.sorted)
	end := entry.TimestampOffset + keep
	if clamp {
		blob.tsPayload = blob.tsPayload[:end:end]
	} else {
		blob.tsPayload = blob.tsPayload[:end]
	}
	entry.TimestampLength = keep
	blob.index.sorted[ord] = entry

	return blob
}

// openPointWalkWire copies the stored bytes of a blob with 16-byte index entries for editing.
func openPointWalkWire(tb testing.TB, data []byte) pointWalkWire {
	tb.Helper()
	header, err := section.ParseNumericHeader(data)
	require.NoError(tb, err)
	require.False(tb, header.Flag.IsV2Ext(), "the stored entries must be 16 bytes")

	return pointWalkWire{data: slices.Clone(data), header: header, engine: header.Flag.GetEndianEngine()}
}

// field returns the two stored bytes of a field of the entry at ord.
func (w pointWalkWire) field(ord, at int) []byte {
	off := int(w.header.IndexOffset) + ord*section.NumericIndexEntrySize + at

	return w.data[off : off+2]
}

// add adds delta to a stored field of the entry at ord.
// An offset field holds the distance from the previous entry's offset,
// so adding to it moves the start of this entry's column and of every column behind it.
func (w pointWalkWire) add(ord, at, delta int) {
	field := w.field(ord, at)
	w.engine.PutUint16(field, uint16(int(w.engine.Uint16(field))+delta))
}

// moveFirstValueEnd moves the end of the first entry's value column by delta bytes,
// and with it the start of the second entry's column;
// the columns behind that one stay where they are.
func (w pointWalkWire) moveFirstValueEnd(delta int) {
	w.add(1, wireEntryValues, delta)
	if w.header.MetricCount > 2 {
		w.add(2, wireEntryValues, -delta)
	}
}

// timestampPayload returns the stored timestamp payload.
func (w pointWalkWire) timestampPayload() []byte {
	return w.data[w.header.TimestampPayloadOffset:w.header.ValuePayloadOffset]
}

// TestNumericBlob_ForEachMatchesEncodingDispatch pins the point walks on well-formed blobs:
// every walk yields what the encoding dispatch yields for the entry's byte ranges,
// on every timestamp and value encoding pair, with and without shared timestamps,
// in the V1 layout and in the V2 layout with 16-byte and 32-byte index entries, in both byte orders.
// The walks are ForEach, ForEachByName and the handle's ForEach and ForEachRange.
func TestNumericBlob_ForEachMatchesEncodingDispatch(t *testing.T) {
	layouts := []struct{ v1, shared, extended bool }{
		{v1: true}, {}, {shared: true}, {extended: true}, {shared: true, extended: true},
	}
	for _, tsEnc := range pointWalkTimestampEncodings {
		for _, valEnc := range pointWalkValueEncodings {
			t.Run(fmt.Sprintf("%s/%s", tsEnc, valEnc), func(t *testing.T) {
				for _, layout := range layouts {
					for _, bigEndian := range []bool{false, true} {
						spec := pointWalkSpec{
							tsEnc: tsEnc, valEnc: valEnc, shared: layout.shared, v1: layout.v1, extended: layout.extended,
							bigEndian: bigEndian, metrics: 3, points: 37,
						}
						data, blob, names := buildPointWalkBlob(t, spec)
						header, err := section.ParseNumericHeader(data)
						require.NoError(t, err)
						require.Equal(t, layout.extended, header.Flag.IsV2Ext(), "%s: 32-byte index entries", spec)
						for _, name := range names {
							requirePointWalkGroup(t, spec.String(), blob, name, layout.shared)
							requirePointWalksAgree(t, spec.String(), blob, name)
						}
					}
				}
			})
		}
	}
}

// TestNumericBlob_ForEachMatchesEncodingDispatchAroundPoolLimit pins the point walks on columns of one point
// and of one point less than, exactly, and one point more than the decode pool keeps a buffer for,
// with and without shared timestamps, and on a blob with tags, whose walks never pair with decoded timestamps.
func TestNumericBlob_ForEachMatchesEncodingDispatchAroundPoolLimit(t *testing.T) {
	limit := pool.MaxPooledDecodeFloat64s
	for _, tsEnc := range pointWalkTimestampEncodings {
		for _, valEnc := range pointWalkValueEncodings {
			t.Run(fmt.Sprintf("%s/%s", tsEnc, valEnc), func(t *testing.T) {
				for _, points := range []int{1, limit - 1, limit, limit + 1} {
					for _, shared := range []bool{false, true} {
						spec := pointWalkSpec{tsEnc: tsEnc, valEnc: valEnc, shared: shared, metrics: 2, points: points}
						_, blob, names := buildPointWalkBlob(t, spec)
						requirePointWalkGroup(t, spec.String(), blob, names[0], shared)
						requireBlobPointWalksAgree(t, spec.String(), blob, names[0])
						h, ok := blob.MetricByName(names[0])
						require.True(t, ok)
						requireHandlePointWalksAgree(t, spec.String()+" handle", &h, points == 1)
					}
				}

				spec := pointWalkSpec{tsEnc: tsEnc, valEnc: valEnc, shared: true, tagged: true, metrics: 3, points: 37}
				_, blob, names := buildPointWalkBlob(t, spec)
				for _, name := range names {
					requirePointWalkGroup(t, spec.String(), blob, name, true)
					requirePointWalksAgree(t, spec.String(), blob, name)
				}
			})
		}
	}
}

// referenceForEachAcrossBlobs is forEachAcrossBlobs as it is, with every member walked by referenceForEachFromEntry.
func referenceForEachAcrossBlobs(blobs []NumericBlob, resolver setEntryResolver, yield func(int, NumericDataPoint) bool) bool {
	if yield == nil {
		return false
	}

	var (
		found       bool
		globalIndex int
		stopped     bool
	)

	adapter := func(_ int, dp NumericDataPoint) bool {
		if !yield(globalIndex, dp) {
			stopped = true
			return false
		}
		globalIndex++

		return true
	}

	for i := range blobs {
		entry, ok := resolver.resolve(&blobs[i])
		if !ok {
			continue
		}
		found = true
		referenceForEachFromEntry(blobs[i], entry, adapter)
		if stopped {
			break
		}
	}

	return found
}

// requireSetPointWalksAgree compares the ForEach and ForEachByName of a numeric blob set with the reference walk,
// for a full walk, for a stop at the first, a middle and the last point,
// and for a stop at the last point of every member of points points and at the first point of the next one.
func requireSetPointWalksAgree(t *testing.T, fixture string, set NumericBlobSet, name string, points int) {
	t.Helper()
	id := hash.ID(name)
	walks := []struct {
		entryPoint     string
		reference, got func(yield func(int, NumericDataPoint) bool) bool
	}{
		{
			"NumericBlobSet.ForEach",
			func(yield func(int, NumericDataPoint) bool) bool {
				return referenceForEachAcrossBlobs(set.blobs, set.resolverByID(id), yield)
			},
			func(yield func(int, NumericDataPoint) bool) bool { return set.ForEach(id, yield) },
		},
		{
			"NumericBlobSet.ForEachByName",
			func(yield func(int, NumericDataPoint) bool) bool {
				return referenceForEachAcrossBlobs(set.blobs, set.resolverByName(name), yield)
			},
			func(yield func(int, NumericDataPoint) bool) bool { return set.ForEachByName(name, yield) },
		},
	}
	for _, w := range walks {
		where := fixture + " " + name + " " + w.entryPoint
		stops := []int{0}
		for i := 0; i < len(stops); i++ {
			var wantFound, gotFound bool
			want := requireSamePointWalk(t, where, stops[i],
				func(yield func(int, NumericDataPoint) bool) { wantFound = w.reference(yield) },
				func(yield func(int, NumericDataPoint) bool) { gotFound = w.got(yield) })
			if wantFound != gotFound {
				t.Fatalf("%s, stop at call %d: want result %t, got %t", where, stops[i], wantFound, gotFound)
			}
			if i > 0 {
				continue
			}
			stops = pointWalkStops(len(want.calls))
			for boundary := points; boundary < len(want.calls); boundary += points {
				for _, stop := range []int{boundary, boundary + 1} {
					if !slices.Contains(stops, stop) {
						stops = append(stops, stop)
					}
				}
			}
		}
	}
}

// TestNumericBlobSet_ForEachMatchesEncodingDispatch pins the numeric blob set's ForEach and ForEachByName,
// which walk every member through the blob's forEachFromEntry and so pair value columns with shared timestamps too.
// The set shares timestamps in every member and has tags in one;
// the walks are compared for every value encoding, on a metric in every member and on metrics in some,
// then with the value column of one member cut in memory, and for a missing metric and a nil callback.
func TestNumericBlobSet_ForEachMatchesEncodingDispatch(t *testing.T) {
	const points = 20 // per member, as handleTestSet writes them
	zeroPointWalkPools(t, points)
	for _, valEnc := range pointWalkValueEncodings {
		t.Run(valEnc.String(), func(t *testing.T) {
			members := handleTestSetMembers()
			members[1].opts = []NumericEncoderOption{WithTagsEnabled(true)}
			bs, _ := handleTestSet(t, members, true,
				WithSharedTimestamps(), WithTimestampEncoding(format.TypeDeltaPacked), WithValueEncoding(valEnc))
			blobs := bs.NumericBlobs()
			set, err := NewNumericBlobSet(blobs)
			require.NoError(t, err)
			for _, name := range handleTestSetNames {
				requireSetPointWalksAgree(t, "set", set, name, points)
			}
			for i := range blobs {
				requirePointWalkGroup(t, fmt.Sprintf("member %d", i), blobs[i], "all", true)
			}

			cut := slices.Clone(blobs)
			ord := pointWalkOrdinal(t, cut[2], "all")
			cut[2] = cutPointWalkValues(cut[2], ord, cut[2].index.sorted[ord].ValueLength/2, true)
			cutSet, err := NewNumericBlobSet(cut)
			require.NoError(t, err)
			requireSetPointWalksAgree(t, "set with a cut member", cutSet, "all", points)

			calls := 0
			count := func(int, NumericDataPoint) bool {
				calls++

				return true
			}
			require.False(t, set.ForEach(hash.ID("no.such.metric"), count), "ForEach of a missing metric")
			require.False(t, set.ForEachByName("no.such.metric", count), "ForEachByName of a missing metric")
			require.Zero(t, calls)
			require.False(t, set.ForEach(hash.ID("all"), nil), "ForEach with a nil callback")
			require.False(t, set.ForEachByName("all", nil), "ForEachByName with a nil callback")
			require.True(t, set.ForEach(hash.ID("all"), count))
			require.Equal(t, 4*points, calls)
		})
	}
}

// TestNumericBlob_ForEachWrappers pins what ForEach and ForEachByName do around the walk of an entry,
// on a blob whose walks pair the value column with the shared timestamps:
// a missing metric and a nil callback return false without a call,
// an ID two names collide on walks the first entry while each name walks its own,
// and an entry without points reports the metric as found and calls nothing.
func TestNumericBlob_ForEachWrappers(t *testing.T) {
	spec := pointWalkSpec{tsEnc: format.TypeDeltaPacked, valEnc: format.TypeChimp, shared: true, metrics: 3, points: 9}
	_, blob, names := buildPointWalkBlob(t, spec)
	requirePointWalkGroup(t, spec.String(), blob, names[0], true)

	calls := 0
	count := func(int, NumericDataPoint) bool {
		calls++

		return true
	}
	require.False(t, blob.ForEach(hash.ID("no.such.metric"), count), "ForEach of a missing metric")
	require.False(t, blob.ForEachByName("no.such.metric", count), "ForEachByName of a missing metric")
	require.Zero(t, calls, "calls for a missing metric")
	require.False(t, blob.ForEach(hash.ID(names[0]), nil), "ForEach with a nil callback")
	require.False(t, blob.ForEachByName(names[0], nil), "ForEachByName with a nil callback")
	require.True(t, blob.ForEach(hash.ID(names[0]), count), "ForEach of a metric the blob holds")
	require.Equal(t, 9, calls)
	require.True(t, blob.ForEachByName(names[0], func(int, NumericDataPoint) bool { return false }), "ForEachByName stopped at once")

	t.Run("two names with one ID", func(t *testing.T) {
		spec := pointWalkSpec{
			tsEnc: format.TypeDeltaPacked, valEnc: format.TypeChimp, shared: true, points: 9,
			names: []string{collisiontest.NameA, collisiontest.NameB},
		}
		_, blob, names := buildPointWalkBlob(t, spec)
		require.Len(t, blob.index.sorted, 2)
		require.Equal(t, blob.index.sorted[0].MetricID, blob.index.sorted[1].MetricID)
		for _, name := range names {
			requirePointWalkGroup(t, spec.String(), blob, name, true)
			requirePointWalksAgree(t, spec.String(), blob, name)
		}
		// By ID the walk is the first entry's, by name each name's own: the two value columns differ.
		byID := recordPointWalk(0, func(yield func(int, NumericDataPoint) bool) { blob.ForEach(collisiontest.CollisionID, yield) })
		first := recordPointWalk(0, func(yield func(int, NumericDataPoint) bool) {
			referenceForEachFromEntry(blob, blob.index.sorted[0], yield)
		})
		second := recordPointWalk(0, func(yield func(int, NumericDataPoint) bool) {
			referenceForEachFromEntry(blob, blob.index.sorted[1], yield)
		})
		require.Empty(t, diffPointWalks(first, byID), "ForEach by the shared ID")
		require.NotEmpty(t, diffPointWalks(first, second), "the two metrics must differ")
		byName := recordPointWalk(0, func(yield func(int, NumericDataPoint) bool) { blob.ForEachByName(collisiontest.NameB, yield) })
		require.Empty(t, diffPointWalks(second, byName), "ForEachByName of the second name")
	})

	t.Run("entry without points", func(t *testing.T) {
		empty := blob
		empty.index.sorted = slices.Clone(blob.index.sorted)
		ord := pointWalkOrdinal(t, empty, names[1])
		empty.index.sorted[ord].Count = 0
		calls = 0
		require.True(t, empty.ForEach(hash.ID(names[1]), count))
		require.True(t, empty.ForEachByName(names[1], count))
		require.Zero(t, calls)
		for _, name := range names {
			requirePointWalksAgree(t, spec.String()+" with an empty entry", empty, name)
		}
	})
}

// shortenPointWalkTimestamps sets one stored byte of the timestamp payload to 0xFF, from the end backwards,
// until the blob still decodes and the shared group of the first metric holds fewer timestamps than the metric has points.
// A Delta column ends in an unfinished varint then,
// and a DeltaPacked column in a group that claims more bytes than follow.
func shortenPointWalkTimestamps(tb testing.TB, w pointWalkWire, name string) NumericBlob {
	tb.Helper()
	payload := w.timestampPayload()
	for at := len(payload) - 1; at >= 0; at-- {
		saved := payload[at]
		payload[at] = 0xFF
		blob, err := decodePointWalkBlob(w.data)
		if err == nil {
			if group, count := pointWalkGroup(tb, blob, name); group != nil && len(group) < count {
				return blob
			}
		}
		payload[at] = saved
	}
	tb.Fatal("no stored byte shortens the shared group")

	return NumericBlob{}
}

// TestNumericBlob_ForEachShortStreams pins the point walks on blobs the decoder accepts although a column decodes short:
// a shared group with fewer timestamps than the metrics have points, a Gorilla or Chimp column that ends early,
// and both in one blob.
// Raw timestamps cannot decode short and Raw, ALP and ALP-RLE values cannot be cut on the wire:
// the decoder rejects those blobs, which TestNumericBlob_CutColumnsAreRejectedAtDecode pins.
func TestNumericBlob_ForEachShortStreams(t *testing.T) {
	const metrics, cut = 3, 5
	for _, tsEnc := range []format.EncodingType{format.TypeDelta, format.TypeDeltaPacked} {
		for _, valEnc := range pointWalkValueEncodings {
			xor := valEnc == format.TypeGorilla || valEnc == format.TypeChimp
			for _, bigEndian := range []bool{false, true} {
				spec := pointWalkSpec{tsEnc: tsEnc, valEnc: valEnc, shared: true, bigEndian: bigEndian, metrics: metrics, points: 37}
				t.Run(spec.String(), func(t *testing.T) {
					data, _, names := buildPointWalkBlob(t, spec)
					check := func(what string, blob NumericBlob) {
						for _, name := range names {
							requirePointWalksAgree(t, spec.String()+" "+what, blob, name)
						}
					}

					short := shortenPointWalkTimestamps(t, openPointWalkWire(t, data), names[0])
					check("short group", short)
					if !xor {
						return
					}

					w := openPointWalkWire(t, data)
					w.moveFirstValueEnd(-cut)
					blob, err := decodePointWalkBlob(w.data)
					require.NoError(t, err)
					for _, name := range names {
						requirePointWalkGroup(t, spec.String(), blob, name, true)
					}
					check("short value stream", blob)
					first := recordPointWalk(0, func(yield func(int, NumericDataPoint) bool) {
						referenceForEachFromEntry(blob, blob.index.sorted[0], yield)
					})
					require.Less(t, len(first.calls), 37, "the cut value stream must end early")

					check("short group and short value stream", shortenPointWalkTimestamps(t, w, names[0]))
				})
			}
		}
	}
}

// TestNumericBlob_ForEachOnAlteredBlobs alters one stored byte behind the header at a time,
// an index field, a shared table entry or a payload byte,
// and pins the point walks on every altered blob the decoder still accepts.
// It reaches accepted blobs the tests around it do not build by hand,
// such as a value stream that decodes to other values or a group that holds other timestamps.
func TestNumericBlob_ForEachOnAlteredBlobs(t *testing.T) {
	for _, tsEnc := range pointWalkTimestampEncodings {
		for _, valEnc := range pointWalkValueEncodings {
			t.Run(fmt.Sprintf("%s/%s", tsEnc, valEnc), func(t *testing.T) {
				for _, bigEndian := range []bool{false, true} {
					spec := pointWalkSpec{tsEnc: tsEnc, valEnc: valEnc, shared: true, bigEndian: bigEndian, metrics: 2, points: 9}
					data, _, names := buildPointWalkBlob(t, spec)
					accepted := 0
					for at := section.HeaderSize; at < len(data); at++ {
						for _, flip := range []byte{0x01, 0x80, 0xff} {
							altered := slices.Clone(data)
							altered[at] ^= flip
							blob, err := decodePointWalkBlob(altered)
							if err != nil {
								continue
							}
							accepted++
							for _, name := range names {
								// A metric whose stored ID was altered is no longer found by its name.
								h, ok := blob.MetricByName(name)
								if !ok {
									continue
								}
								fixture := fmt.Sprintf("%s byte %d xor %#x", spec, at, flip)
								requireBlobPointWalksAgree(t, fixture, blob, name)
								requireHandlePointWalksAgree(t, fixture+" "+name+" handle", &h, false)
								h.Materialize()
								requireHandlePointWalksAgree(t, fixture+" "+name+" materialized handle", &h, false)
							}
						}
					}
					require.Positive(t, accepted, "%s: altered blobs the decoder accepts", spec)
				}
			})
		}
	}
}

// TestNumericBlob_ForEachGroupOfAnotherLength pins the point walks of a metric whose pre-decoded group is not its size:
// an empty group, one that is a timestamp short, and one that is a timestamp long.
// The decoder leaves an empty or short group when the first entry at a shared offset has fewer points than a later one;
// here the group is replaced in memory, and the walks must not read it.
func TestNumericBlob_ForEachGroupOfAnotherLength(t *testing.T) {
	const points = 9
	for _, valEnc := range pointWalkValueEncodings {
		spec := pointWalkSpec{tsEnc: format.TypeDeltaPacked, valEnc: valEnc, shared: true, metrics: 2, points: points}
		_, blob, names := buildPointWalkBlob(t, spec)
		requirePointWalkGroup(t, spec.String(), blob, names[0], true)
		whole := blob.sharedTs.first
		for _, length := range []int{0, points - 1, points + 1} {
			group := whole
			group.ts = append(slices.Clone(whole.ts), 0)[:length]
			crafted := blob
			crafted.sharedTs = &sharedTimestamps{first: group, groups: []sharedTimestampGroup{group}}
			for _, name := range names {
				got, count := pointWalkGroup(t, crafted, name)
				require.NotNil(t, got)
				require.NotEqual(t, count, len(got))
				requirePointWalksAgree(t, fmt.Sprintf("%s with a group of %d timestamps", spec, length), crafted, name)
			}
		}
	}
}

// TestNumericBlob_ForEachCutValueColumns pins the point walks on a value column shortened in memory after decoding,
// beside a complete shared group: every length from nothing to the whole column, for each value encoding,
// as a slice that keeps the payload's capacity and as one clamped to its length.
// The decoders trust a column that passed the checks at open, so a walk over a cut ALP column may panic,
// and it must then panic at the same call as the encoding dispatch does.
func TestNumericBlob_ForEachCutValueColumns(t *testing.T) {
	const points = 21
	zeroPointWalkPools(t, points)
	for _, tsEnc := range pointWalkTimestampEncodings {
		for _, valEnc := range pointWalkValueEncodings {
			t.Run(fmt.Sprintf("%s/%s", tsEnc, valEnc), func(t *testing.T) {
				for _, bigEndian := range []bool{false, true} {
					spec := pointWalkSpec{tsEnc: tsEnc, valEnc: valEnc, shared: true, bigEndian: bigEndian, metrics: 3, points: points}
					_, blob, names := buildPointWalkBlob(t, spec)
					name := names[1]
					requirePointWalkGroup(t, spec.String(), blob, name, true)
					ord := pointWalkOrdinal(t, blob, name)
					for keep := 0; keep <= blob.index.sorted[ord].ValueLength; keep++ {
						for _, clamp := range []bool{false, true} {
							fixture := fmt.Sprintf("%s value bytes kept=%d clamped=%t", spec, keep, clamp)
							requirePointWalksAgree(t, fixture, cutPointWalkValues(blob, ord, keep, clamp), name)
						}
					}
				}
			})
		}
	}
}

// TestNumericBlob_ForEachReadsDecodedTimestampsWhenBytesAreCut pins the point walks of a metric
// whose timestamp bytes were cut in memory after its timestamps were decoded.
// The decoded timestamps are a shared group decoded when the blob was opened,
// or the timestamps Materialize decoded on a handle whose values stay encoded.
// The walks pair the value column with the decoded timestamps and yield every point, as ForEachTimestamps does.
// A walk that reads the cut bytes ends where they do,
// or, on Raw timestamps beside Raw, Gorilla or Chimp values, yields every point with timestamps that are not the stored ones.
// A walk the callback stops at the first, a middle or the last point makes the same calls up to the stop.
// All and AllByName keep decoding the timestamp bytes.
// No stored blob reaches this state, because the decoded timestamps come from those bytes.
func TestNumericBlob_ForEachReadsDecodedTimestampsWhenBytesAreCut(t *testing.T) {
	const points = 21
	// cutWalk is one point walk of the cut metric: the walk, the walk as it was before, the timestamp walk beside it,
	// and the iterator of the same entry, nil where the entry point has none.
	type cutWalk struct {
		entryPoint    string
		got, previous pointWalkFunc
		timestamps    func(yield func(int, int64) bool)
		all           pointWalkFunc
	}
	tests := []struct {
		name          string
		tsEnc, valEnc format.EncodingType
		shared        bool // a shared group on a blob; otherwise a handle after Materialize
	}{
		{"shared Delta timestamps, Chimp values", format.TypeDelta, format.TypeChimp, true},
		{"shared Delta timestamps, ALP values", format.TypeDelta, format.TypeALP, true},
		{"shared DeltaPacked timestamps, Chimp values", format.TypeDeltaPacked, format.TypeChimp, true},
		{"shared DeltaPacked timestamps, ALP values", format.TypeDeltaPacked, format.TypeALP, true},
		{"shared Raw timestamps, Chimp values", format.TypeRaw, format.TypeChimp, true},
		{"shared Raw timestamps, Raw values", format.TypeRaw, format.TypeRaw, true},
		{"shared Raw timestamps, ALP values", format.TypeRaw, format.TypeALP, true},
		{"materialized Delta timestamps, ALP values", format.TypeDelta, format.TypeALP, false},
	}
	for _, tt := range tests {
		for _, clamp := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, clamped=%t", tt.name, clamp), func(t *testing.T) {
				spec := pointWalkSpec{tsEnc: tt.tsEnc, valEnc: tt.valEnc, shared: tt.shared, metrics: 3, points: points}
				_, blob, names := buildPointWalkBlob(t, spec)
				name := names[1]

				// want is the walk of the uncut metric by the encoding dispatch, which decodes both columns from their bytes.
				// kept is the number of timestamps the cut bytes decode to with the column decoder.
				var (
					want  pointWalk
					kept  int
					walks []cutWalk
				)
				decoded := make([]int64, points)
				if tt.shared {
					requirePointWalkGroup(t, spec.String(), blob, name, true)
					ord := pointWalkOrdinal(t, blob, name)
					entry := blob.index.sorted[ord]
					want = recordPointWalk(0, func(yield func(int, NumericDataPoint) bool) {
						referenceForEachFromEntry(blob, entry, yield)
					})

					keep := entry.TimestampLength / 2
					cut := cutPointWalkTimestamps(blob, ord, keep, clamp)
					requirePointWalkGroup(t, spec.String(), cut, name, true)
					id := entry.MetricID
					byID, ok := cut.index.GetByID(id)
					require.True(t, ok)
					require.Equal(t, keep, byID.TimestampLength, "timestamp bytes of the entry found by ID")
					byName, ok := cut.lookupMetricEntry(name)
					require.True(t, ok)
					require.Equal(t, keep, byName.TimestampLength, "timestamp bytes of the entry found by name")
					tsBytes, ok := safeSlice(cut.tsPayload, byID.TimestampOffset, byID.TimestampLength)
					require.True(t, ok, "the cut timestamp range lies in the payload")
					kept = cut.decodeTimestampsSlice(tsBytes, points, decoded)

					set, err := NewNumericBlobSet([]NumericBlob{cut})
					require.NoError(t, err)
					h, ok := cut.MetricByName(name)
					require.True(t, ok)
					requirePartTimestampColumn(t, spec.String(), &h)
					require.Nil(t, h.first.values, "the handle's values stay encoded")
					walks = []cutWalk{
						{
							"NumericBlob.ForEach",
							func(yield func(int, NumericDataPoint) bool) { cut.ForEach(id, yield) },
							func(yield func(int, NumericDataPoint) bool) { referenceForEachFromEntry(cut, byID, yield) },
							func(yield func(int, int64) bool) { cut.ForEachTimestamps(id, yield) },
							pointWalkFunc(cut.All(id)),
						},
						{
							"NumericBlob.ForEachByName",
							func(yield func(int, NumericDataPoint) bool) { cut.ForEachByName(name, yield) },
							func(yield func(int, NumericDataPoint) bool) { referenceForEachFromEntry(cut, byName, yield) },
							func(yield func(int, int64) bool) { cut.ForEachTimestampsByName(name, yield) },
							pointWalkFunc(cut.AllByName(name)),
						},
						{
							"NumericBlobSet.ForEach",
							func(yield func(int, NumericDataPoint) bool) { set.ForEach(id, yield) },
							func(yield func(int, NumericDataPoint) bool) {
								referenceForEachAcrossBlobs(set.blobs, set.resolverByID(id), yield)
							},
							func(yield func(int, int64) bool) { set.ForEachTimestamps(id, yield) },
							nil,
						},
						{
							"NumericMetric.ForEach",
							h.ForEach,
							func(yield func(int, NumericDataPoint) bool) { referenceHandleForEach(&h, yield) },
							h.ForEachTimestamps,
							nil,
						},
						{
							"NumericMetric.ForEachRange",
							func(yield func(int, NumericDataPoint) bool) { h.ForEachRange(0, h.Len(), yield) },
							func(yield func(int, NumericDataPoint) bool) { referenceHandleForEachRange(&h, 0, h.Len(), yield) },
							func(yield func(int, int64) bool) { h.ForEachTimestampsRange(0, h.Len(), yield) },
							nil,
						},
					}
				} else {
					h, ok := blob.MetricByName(name)
					require.True(t, ok)
					require.Nil(t, h.first.timestampColumn(), "decoded timestamps before Materialize")
					want = recordPointWalk(0, func(yield func(int, NumericDataPoint) bool) { referenceHandleForEach(&h, yield) })

					h.Materialize()
					require.Nil(t, h.first.shared)
					require.Nil(t, h.first.values, "the values stay encoded")
					requirePartTimestampColumn(t, spec.String(), &h)
					keep := len(h.first.tsBytes) / 2
					if clamp {
						h.first.tsBytes = h.first.tsBytes[:keep:keep]
					} else {
						h.first.tsBytes = h.first.tsBytes[:keep]
					}
					kept = blob.decodeTimestampsSlice(h.first.tsBytes, points, decoded)
					walks = []cutWalk{
						{
							"NumericMetric.ForEach",
							h.ForEach,
							func(yield func(int, NumericDataPoint) bool) { referenceHandleForEach(&h, yield) },
							h.ForEachTimestamps,
							nil,
						},
						{
							"NumericMetric.ForEachRange",
							func(yield func(int, NumericDataPoint) bool) { h.ForEachRange(0, h.Len(), yield) },
							func(yield func(int, NumericDataPoint) bool) { referenceHandleForEachRange(&h, 0, h.Len(), yield) },
							func(yield func(int, int64) bool) { h.ForEachTimestampsRange(0, h.Len(), yield) },
							nil,
						},
					}
				}
				require.Len(t, want.calls, points, "points of the uncut metric")
				require.Equal(t, pointWalkReturned, want.end)
				require.Less(t, kept, points, "timestamps the cut bytes decode to")
				// The walk as it was ends where the column decoder does on the cut bytes.
				// Beside Raw, Gorilla or Chimp values it reads each Raw timestamp by index, a missing one as 0, and yields every point.
				previousPoints := kept
				if tt.tsEnc == format.TypeRaw && tt.valEnc != format.TypeALP && tt.valEnc != format.TypeALPRLE {
					previousPoints = points
				}

				for _, w := range walks {
					got := recordPointWalk(0, w.got)
					require.Empty(t, diffPointWalks(want, got), "%s: the walk against the uncut metric", w.entryPoint)
					for _, stop := range pointWalkStops(points)[1:] {
						stopped := pointWalk{calls: want.calls[:stop], end: pointWalkStopped}
						require.Empty(t, diffPointWalks(stopped, recordPointWalk(stop, w.got)),
							"%s stopped at call %d: the walk against the uncut metric", w.entryPoint, stop)
					}

					timestamps := make([]int64, 0, points)
					w.timestamps(func(_ int, ts int64) bool {
						timestamps = append(timestamps, ts)

						return true
					})
					require.Len(t, timestamps, points, "%s: timestamps of the timestamp walk", w.entryPoint)
					for i, call := range got.calls {
						require.Equal(t, timestamps[i], call.ts, "%s: timestamp of point %d", w.entryPoint, i)
					}

					// This documents the difference: the walk as it was reads the cut bytes and does not yield the metric's points.
					previous := recordPointWalk(0, w.previous)
					require.Equal(t, pointWalkReturned, previous.end, "%s: the walk as it was", w.entryPoint)
					require.Len(t, previous.calls, previousPoints, "%s: points of the walk as it was", w.entryPoint)
					require.NotEmpty(t, diffPointWalks(want, previous), "%s: the walk as it was against the uncut metric", w.entryPoint)
					if w.all != nil {
						// All keeps decoding the timestamp bytes, so it yields what the walk as it was yields.
						require.Empty(t, diffPointWalks(previous, recordPointWalk(0, w.all)), "%s: All against the walk as it was", w.entryPoint)
					}
				}
			})
		}
	}
}

// TestNumericBlob_ForEachRawValueLengths pins the point walks of a two-point metric with Raw values
// whose value range was set in memory to a length that is too short, too long or not a multiple of 8 bytes,
// beside a complete shared group, in both byte orders.
// In the host's byte order a range that is not a multiple of 8 bytes reads every value as 0,
// and in the other order a value is read wherever its 8 bytes are present.
func TestNumericBlob_ForEachRawValueLengths(t *testing.T) {
	for _, tsEnc := range pointWalkTimestampEncodings {
		for _, bigEndian := range []bool{false, true} {
			spec := pointWalkSpec{tsEnc: tsEnc, valEnc: format.TypeRaw, shared: true, bigEndian: bigEndian, metrics: 2, points: 2}
			t.Run(spec.String(), func(t *testing.T) {
				_, blob, names := buildPointWalkBlob(t, spec)
				name := names[0]
				requirePointWalkGroup(t, spec.String(), blob, name, true)
				ord := pointWalkOrdinal(t, blob, name)

				// Three values, so that a range longer than two values still lies inside the payload.
				var values []byte
				for _, v := range []float64{1.5, -2.25, 3.125} {
					values = blob.Engine().AppendUint64(values, math.Float64bits(v))
				}
				blob.valPayload = values
				blob.index.sorted = slices.Clone(blob.index.sorted)
				blob.index.sorted[ord].ValueOffset = 0
				blob.index.sorted[ord].ValueLength = len(values)

				for _, length := range []int{0, 7, 8, 9, 15, 16, 17, 24} {
					for _, clamp := range []bool{false, true} {
						fixture := fmt.Sprintf("%s value range of %d bytes clamped=%t", spec, length, clamp)
						requirePointWalksAgree(t, fixture, cutPointWalkValues(blob, ord, length, clamp), name)
					}
				}
			})
		}
	}
}

// replacePointWalkValues returns blob with the value column of the entry at ord replaced by column,
// which becomes the end of the value payload, so the columns stored behind it fall outside it.
func replacePointWalkValues(blob NumericBlob, ord int, column []byte) NumericBlob {
	entry := blob.index.sorted[ord]
	blob.index.sorted = slices.Clone(blob.index.sorted)
	blob.valPayload = append(slices.Clone(blob.valPayload[:entry.ValueOffset]), column...)
	entry.ValueLength = len(column)
	blob.index.sorted[ord] = entry

	return blob
}

// alpCutSeries are value series whose ALP or ALP-RLE column takes each layout the codec has:
// the main layout with exceptions, the RD layout with exceptions, the raw layout,
// and runs over a main column with exceptions.
// A series without values stands for the raw layout, which the test writes by hand:
// the encoder picks it for short columns only.
func alpCutSeries() []struct {
	name   string
	valEnc format.EncodingType
	scheme byte
	values func(points int) []float64
} {
	// irregular marks the points that become exceptions of the main layout: a value with no short decimal form.
	irregular := func(i int) bool { return i%1000 == 999 }

	return []struct {
		name   string
		valEnc format.EncodingType
		scheme byte
		values func(points int) []float64
	}{
		{"main", format.TypeALP, 0, func(points int) []float64 {
			vals := make([]float64, points)
			for i := range vals {
				vals[i] = 100 + float64(i%977)/100
				if irregular(i) || i == points-2 {
					vals[i] = math.Pi * float64(i)
				}
			}

			return vals
		}},
		{"rd", format.TypeALP, 1, func(points int) []float64 {
			vals := make([]float64, points)
			for i := range vals {
				vals[i] = math.Sqrt(float64(i + 2))
				if irregular(i) || i == points-2 {
					vals[i] = 1e200 / float64(i+1)
				}
			}

			return vals
		}},
		{"raw", format.TypeALP, 2, nil},
		{"runs", format.TypeALPRLE, 3, func(points int) []float64 {
			vals := make([]float64, points)
			for i := range vals {
				run := i / 4
				vals[i] = 100 + float64(run%977)/100
				if irregular(run) || run == (points-2)/4 {
					vals[i] = math.Pi * float64(run)
				}
			}

			return vals
		}},
	}
}

// alpColumnCuts returns lengths to cut an ALP column of count values to: one inside each region of its layout.
// A plain column has a header, packed values and an exception sidecar,
// and a runs column has its own header and a bitmap in front of a plain column of one value per run.
// It fails when the column has no sidecar, or when the regions do not add up to the column.
func alpColumnCuts(tb testing.TB, column []byte, count int, engine endian.EndianEngine) []int {
	tb.Helper()
	cuts := []int{1, len(column) - 1}
	at := 0
	if column[0] == 3 {
		bitmap := (count + 7) / 8
		cuts = append(cuts, 3, 5+bitmap/2)
		at, count = 5+bitmap, int(engine.Uint32(column[1:5]))
	}

	end := 0
	switch column[at] {
	case 0:
		// Scheme, exponent, factor, width, exception count, minimum,
		// then the packed values and exceptions of 12 bytes.
		packed := (count*int(column[at+3]) + 7) / 8
		exceptions := int(engine.Uint32(column[at+4 : at+8]))
		require.Positive(tb, exceptions, "the main layout needs an exception sidecar")
		sidecar := at + 16 + packed
		cuts = append(cuts, at+8, at+16+packed/2, sidecar+2, sidecar+exceptions*6)
		end = sidecar + exceptions*12
	case 1:
		// Scheme, right width, code width, dictionary size, exception count,
		// then the dictionary, the codes, the right parts and exceptions of 6 bytes.
		codes := at + 8 + 2*int(column[at+3])
		left := (count*int(column[at+2]) + 7) / 8
		right := (count*int(column[at+1]) + 7) / 8
		exceptions := int(engine.Uint32(column[at+4 : at+8]))
		require.Positive(tb, exceptions, "the RD layout needs an exception sidecar")
		sidecar := codes + left + right
		cuts = append(cuts, at+5, codes+left/2, codes+left+right/2, sidecar+1, sidecar+exceptions*3)
		end = sidecar + exceptions*6
	case 2:
		cuts = append(cuts, at+1+count*4)
		end = at + 1 + count*8
	default:
		tb.Fatalf("unknown ALP layout %d", column[at])
	}
	require.Equal(tb, len(column), end, "the regions of the column")
	slices.Sort(cuts)

	return slices.Compact(cuts)
}

// TestNumericBlob_ForEachCutALPColumns pins the point walks on ALP and ALP-RLE columns shortened in memory,
// at the largest column the decode pool keeps a buffer for and at one point more, beside a complete shared group.
// Each layout is cut inside its header, its packed values and its exception sidecar,
// and the runs layout inside its nested column too.
// The encoding dispatch decodes the whole value column before the first callback, so it faults before any call;
// past the pool's limit the value loop streams instead and calls back before it reaches the fault,
// which is why a walk pairs the value column with decoded timestamps only up to that limit.
func TestNumericBlob_ForEachCutALPColumns(t *testing.T) {
	limit := pool.MaxPooledDecodeFloat64s
	zeroPointWalkPools(t, limit)
	for _, series := range alpCutSeries() {
		for _, points := range []int{limit, limit + 1} {
			t.Run(fmt.Sprintf("%s/%d", series.name, points), func(t *testing.T) {
				spec := pointWalkSpec{tsEnc: format.TypeDeltaPacked, valEnc: series.valEnc, shared: true, metrics: 2, points: points}
				data, names := encodePointWalkBlob(t, spec, func(m int) ([]int64, []float64, []string) {
					ts, vals, tags := pointWalkSeries(points, m, true)
					if series.values != nil {
						vals = series.values(points)
					}

					return ts, vals, tags
				})
				blob, err := decodePointWalkBlob(data)
				require.NoError(t, err)
				name := names[0]
				ord := pointWalkOrdinal(t, blob, name)
				if series.values == nil {
					column := []byte{2}
					for i := range points {
						column = blob.Engine().AppendUint64(column, math.Float64bits(float64(i)+0.5))
					}
					blob = replacePointWalkValues(blob, ord, column)
				}
				entry := blob.index.sorted[ord]
				column := blob.valPayload[entry.ValueOffset : entry.ValueOffset+entry.ValueLength]
				require.Equal(t, series.scheme, column[0], "the layout of the column")
				requirePointWalkGroup(t, spec.String(), blob, name, true)

				streamsBeforeFault := 0
				decoder := ienc.NewNumericALPDecoder(blob.Engine())
				for _, cut := range alpColumnCuts(t, column, points, blob.Engine()) {
					for _, clamp := range []bool{false, true} {
						fixture := fmt.Sprintf("%s %s value bytes kept=%d clamped=%t", spec, series.name, cut, clamp)
						crafted := cutPointWalkValues(blob, ord, cut, clamp)
						requireBlobPointWalksAgree(t, fixture, crafted, name)
						h, ok := crafted.MetricByName(name)
						require.True(t, ok)
						requireHandlePointWalksAgree(t, fixture+" handle", &h, false)

						dispatch := recordPointWalk(0, func(yield func(int, NumericDataPoint) bool) {
							referenceForEachFromEntry(crafted, crafted.index.sorted[ord], yield)
						})
						stream := recordPointWalk(0, func(yield func(int, NumericDataPoint) bool) {
							decoder.Each(crafted.valPayload[entry.ValueOffset:], points, 0, func(i int, v float64) bool {
								return yield(i, NumericDataPoint{Val: v})
							})
						})
						if dispatch.end == pointWalkPanicked && len(dispatch.calls) == 0 && len(stream.calls) > 0 {
							streamsBeforeFault++
						}
					}
				}
				require.Positive(t, streamsBeforeFault, "a cut on which the streaming value loop calls back before it faults")
			})
		}
	}
}

// timestampColumn is a byte string handed to the timestamp decoders as a column of count timestamps.
type timestampColumn struct {
	name   string
	column []byte
	count  int
}

// appendTestVarint appends v as a varint of exactly width bytes, padded with continuation bytes,
// or in its shortest form when width is 0.
func appendTestVarint(dst []byte, v uint64, width int) []byte {
	if width == 0 {
		return binary.AppendUvarint(dst, v)
	}
	for i := range width {
		b := byte(v & 0x7f)
		v >>= 7
		if i < width-1 {
			b |= 0x80
		}
		dst = append(dst, b)
	}

	return dst
}

// testZigZag maps a signed step to the unsigned value the timestamp columns store.
func testZigZag(v int64) uint64 {
	return uint64(v<<1) ^ uint64(v>>63)
}

// deltaTestColumn writes a Delta column by hand: the first timestamp, then each step as a zigzag varint of width bytes,
// in its shortest form when width is 0.
// The first step is the delta and the others are differences of deltas.
func deltaTestColumn(first int64, steps []int64, width int) []byte {
	column := binary.AppendUvarint(nil, uint64(first))
	for _, step := range steps {
		column = appendTestVarint(column, testZigZag(step), width)
	}

	return column
}

// packedTestColumn writes a DeltaPacked column by hand: the first timestamp and the first step as varints,
// then the other steps in groups of four behind a control byte that holds a 2-bit width tag per lane.
// controls gives the control bytes of the groups in turn, starting over when it runs out,
// and a group of fewer than four steps keeps the tags of its unused lanes as well.
// Without controls every step takes the narrowest lane that holds it.
func packedTestColumn(first int64, steps []int64, controls ...byte) []byte {
	column := binary.AppendUvarint(nil, uint64(first))
	if len(steps) == 0 {
		return column
	}
	column = binary.AppendUvarint(column, testZigZag(steps[0]))
	for group, rest := 0, steps[1:]; len(rest) > 0; group, rest = group+1, rest[min(4, len(rest)):] {
		lanes := rest[:min(4, len(rest))]
		var tags byte
		if len(controls) > 0 {
			tags = controls[group%len(controls)]
		} else {
			for lane, step := range lanes {
				tag := byte(0)
				for zz := testZigZag(step); tag < 3 && zz >= 1<<(8<<tag); {
					tag++
				}
				tags |= tag << (2 * lane)
			}
		}
		column = append(column, tags)
		for lane, step := range lanes {
			var wide [8]byte
			binary.LittleEndian.PutUint64(wide[:], testZigZag(step))
			column = append(column, wide[:1<<(tags>>(2*lane)&3)]...)
		}
	}

	return column
}

// longTestSteps returns n steps for a long column: mostly small differences, with a 4-byte and an 8-byte one now and then.
func longTestSteps(n int) []int64 {
	steps := make([]int64, n)
	steps[0] = 1_000_000
	for i := 1; i < n; i++ {
		switch {
		case i%7 == 3:
			steps[i] = 1 << 20
		case i%11 == 5:
			steps[i] = -(1 << 40)
		default:
			steps[i] = int64(i%5) - 2
		}
	}

	return steps
}

// timestampTestColumns returns hand-written columns of the given encoding, each a well-formed column of its count:
// for Delta every varint length, varints padded to every width, a tenth varint byte that overflows, and sums that wrap;
// for DeltaPacked every control byte on a full group and on a group of one, two and three steps,
// every control byte in one column long enough for the SIMD kernels, and sums that wrap;
// for Raw plain 8-byte values;
// and for each a column of one point less than, exactly, and one point more than the decode pool keeps a buffer for.
func timestampTestColumns(tsEnc format.EncodingType) []timestampColumn {
	limit := pool.MaxPooledDecodeInt64s
	sizes := []int{limit - 1, limit, limit + 1}
	wrapping := []int64{math.MaxInt64, math.MaxInt64, math.MinInt64, -1, math.MaxInt64, 1 << 62, 1 << 62, 1 << 62}
	var columns []timestampColumn
	add := func(name string, column []byte, count int) {
		columns = append(columns, timestampColumn{name: name, column: column, count: count})
	}

	switch tsEnc {
	case format.TypeDelta:
		lengths := []int64{
			1_000_000, 0, 1, -1, 63, -64, 64, 8191, -8192, 8192, 1 << 20, -(1 << 27), 1 << 34, -(1 << 41),
			1 << 48, -(1 << 55), 1 << 62, math.MinInt64, math.MaxInt64, 3,
		}
		small := []int64{5, 0, -3, 7, 0, 0, 1}
		add("steps of every varint length", deltaTestColumn(handleTestStart, lengths, 0), len(lengths)+1)
		add("sums that wrap", deltaTestColumn(math.MaxInt64-5, wrapping, 0), len(wrapping)+1)
		for width := 2; width <= 10; width++ {
			add(fmt.Sprintf("steps padded to %d bytes", width), deltaTestColumn(handleTestStart, small, width), len(small)+1)
			padded := appendTestVarint(nil, uint64(handleTestStart), max(width, 8))
			add(fmt.Sprintf("first timestamp padded to %d bytes", max(width, 8)), append(padded, deltaTestColumn(0, small, 0)[1:]...), len(small)+1)
		}
		// A tenth byte above 1 does not fit 64 bits, in a step and in the first timestamp.
		for _, tenth := range []byte{0x01, 0x02, 0x7f, 0x81} {
			overflow := append(slices.Repeat([]byte{0xff}, 9), tenth)
			column := append(deltaTestColumn(handleTestStart, small[:3], 0), overflow...)
			add(fmt.Sprintf("step with tenth byte %#x", tenth), append(column, deltaTestColumn(0, small, 0)[1:]...), len(small)+5)
			add(fmt.Sprintf("first timestamp with tenth byte %#x", tenth), append(overflow, deltaTestColumn(0, small, 0)[1:]...), len(small)+1)
		}
		for _, size := range sizes {
			add(fmt.Sprintf("%d points", size), deltaTestColumn(handleTestStart, longTestSteps(size-1), 0), size)
		}
	case format.TypeDeltaPacked:
		// Steps that fill a lane of each width, and small steps in lanes wider than they need.
		filling := []int64{100, 30000, 1 << 30, 1 << 60}
		every := make([]byte, 256)
		filled, small := []int64{1_000_000}, []int64{1_000_000}
		for control := range 256 {
			every[control] = byte(control)
			full := []int64{1_000_000}
			for lane := range 4 {
				full = append(full, filling[control>>(2*lane)&3])
				small = append(small, int64(lane-control%3))
			}
			filled = append(filled, full[1:]...)
			add(fmt.Sprintf("control byte %#x", control), packedTestColumn(handleTestStart, full, every[control]), 6)
			for lanes := 1; lanes <= 3; lanes++ {
				add(fmt.Sprintf("control byte %#x on a group of %d", control, lanes),
					packedTestColumn(handleTestStart, full[:1+lanes], every[control]), 2+lanes)
			}
		}
		add("every control byte", packedTestColumn(handleTestStart, filled, every...), len(filled)+1)
		add("every control byte over small steps", packedTestColumn(handleTestStart, small, every...), len(small)+1)
		add("one point", packedTestColumn(handleTestStart, nil), 1)
		add("two points", packedTestColumn(handleTestStart, []int64{1_000_000}), 2)
		add("sums that wrap", packedTestColumn(math.MaxInt64-5, wrapping), len(wrapping)+1)
		for _, size := range sizes {
			add(fmt.Sprintf("%d points", size), packedTestColumn(handleTestStart, longTestSteps(size-1)), size)
		}
	case format.TypeRaw:
		for _, size := range append([]int{1, 2, 5}, sizes...) {
			column := make([]byte, 0, size*8)
			for i := range size {
				column = binary.BigEndian.AppendUint64(column, uint64(handleTestStart+int64(i)*977))
			}
			add(fmt.Sprintf("%d points", size), column, size)
		}
	case format.TypeGorilla, format.TypeChimp, format.TypeALP, format.TypeALPRLE:
		// Value encodings have no timestamp column.
		fallthrough
	default:
	}

	return columns
}

// timestampTestVariants returns the byte strings derived from one column: the column itself,
// read as its own count, as one point less and as one point more; the column with bytes behind it;
// and the column cut short, at every length for a short column and at a few lengths for a long one.
// The column of every control byte is also read as fewer points,
// around the 64 steps the SIMD kernels start at and near its end,
// so that its last group is a full one and one of one, two and three steps.
func timestampTestVariants(c timestampColumn) []timestampColumn {
	variants := []timestampColumn{c}
	counts := []int{c.count - 1, c.count + 1}
	if c.count > 256 && c.count < 2048 {
		for count := 62; count <= 72; count++ {
			counts = append(counts, count)
		}
		counts = append(counts, c.count-5, c.count-4, c.count-3, c.count-2)
	}
	for _, count := range counts {
		variants = append(variants, timestampColumn{fmt.Sprintf("%s read as %d points", c.name, count), c.column, count})
	}
	for _, trailing := range [][]byte{{0x00}, {0x80}, {0xff, 0xff, 0xff}, make([]byte, 7), make([]byte, 8), make([]byte, 9), make([]byte, 16)} {
		name := fmt.Sprintf("%s with %d trailing bytes %#x", c.name, len(trailing), trailing[0])
		variants = append(variants, timestampColumn{name, append(slices.Clone(c.column), trailing...), c.count})
	}
	for keep := range len(c.column) {
		// A long column is cut at every 97th length, and at each of its first and last 40.
		if len(c.column) <= 256 || keep < 40 || keep >= len(c.column)-40 || keep%97 == 0 {
			variants = append(variants, timestampColumn{fmt.Sprintf("%s cut to %d bytes", c.name, keep), c.column[:keep:keep], c.count})
		}
	}

	return variants
}

// timestampWalkerPoints is the longest column TestNumericBlob_TimestampDecodersAgree reads.
const timestampWalkerPoints = pool.MaxPooledDecodeInt64s + 2

// timestampWalker walks a timestamp column with the point loop of one encoding pair and byte order.
// It holds a blob with the pair's settings and no payload,
// and value bytes that are complete for any column the test reads.
type timestampWalker struct {
	name   string
	blob   NumericBlob
	valEnc format.EncodingType
	values []byte
}

// newTimestampWalker takes the settings from an encoded blob and its value bytes from the same blob:
// Raw values, of which a walk uses the first count,
// and a Gorilla or Chimp stream, which a walk reads until it has its points.
// For ALP and ALP-RLE it writes a column in the raw layout, which holds its values at fixed offsets.
func newTimestampWalker(tb testing.TB, tsEnc, valEnc format.EncodingType, bigEndian bool) timestampWalker {
	tb.Helper()
	spec := pointWalkSpec{tsEnc: tsEnc, valEnc: valEnc, bigEndian: bigEndian, metrics: 1, points: timestampWalkerPoints}
	_, blob, _ := buildPointWalkBlob(tb, spec)
	entry := blob.index.sorted[0]
	w := timestampWalker{
		name:   fmt.Sprintf("%s values, big-endian=%t", valEnc, bigEndian),
		blob:   NumericBlob{blobBase: blob.blobBase},
		valEnc: valEnc,
		values: blob.valPayload[entry.ValueOffset : entry.ValueOffset+entry.ValueLength],
	}
	if valEnc == format.TypeALP || valEnc == format.TypeALPRLE {
		w.values = []byte{2}
		for i := range timestampWalkerPoints {
			w.values = blob.Engine().AppendUint64(w.values, math.Float64bits(float64(i)))
		}
	}

	return w
}

// diff walks column beside the value bytes with the encoding dispatch.
// It describes in one line how the walk differs from yielding the decoded timestamps in order,
// or returns "" when it yields them.
func (w timestampWalker) diff(column []byte, decoded []int64) string {
	count := len(decoded)
	values := w.values
	switch w.valEnc {
	case format.TypeRaw:
		values = values[:count*8]
	case format.TypeALP, format.TypeALPRLE:
		values = values[:1+count*8]
	case format.TypeGorilla, format.TypeChimp, format.TypeDelta, format.TypeDeltaPacked:
		// The stream is read until the walk has its points.
		fallthrough
	default:
	}

	walk := recordPointWalk(0, func(yield func(int, NumericDataPoint) bool) {
		w.blob.forEachDataPoint(column, values, nil, count, yield)
	})
	if len(walk.calls) != count || walk.end != pointWalkReturned {
		return fmt.Sprintf("the column decodes to %d timestamps, the walk made %d calls then %s %q",
			count, len(walk.calls), walk.end, walk.fault)
	}
	for i, call := range walk.calls {
		if call.idx != i || call.ts != decoded[i] {
			return fmt.Sprintf("call %d: the column decodes to timestamp %d, the walk yielded index %d timestamp %d",
				i, decoded[i], call.idx, call.ts)
		}
	}

	return ""
}

// TestNumericBlob_TimestampDecodersAgree pins what lets a point walk use timestamps decoded ahead of it:
// the column decoder fills a shared group and the slice of Materialize,
// and whenever it returns as many timestamps as the metric has points,
// the encoding dispatch over the same bytes, beside a complete value column, yields those timestamps.
// Every value encoding in both byte orders takes part, because the pair picks the loop that walks the timestamps:
// a per-point decoder, an indexed read, or a bulk decode up to the size the decode pool keeps a buffer for.
// The bulk DeltaPacked decoder picks a scalar, AVX2 or AVX-512 kernel from the CPU,
// so the test runs once per kernel the machine has.
func TestNumericBlob_TimestampDecodersAgree(t *testing.T) {
	// The CPU features are read when the process starts, so the other kernels run in a process of their own:
	// without AVX-512 VBMI the decoder picks its AVX2 kernel, and without AVX2 its scalar loop.
	// Only the DeltaPacked decoder has kernels, so those processes run its columns alone.
	const kernelEnv = "MEBO_TEST_TIMESTAMP_DECODER_KERNEL"
	otherKernel := os.Getenv(kernelEnv) != ""
	if !otherKernel {
		var wg sync.WaitGroup
		defer wg.Wait()
		for _, off := range []string{"cpu.avx512vbmi=off", "cpu.avx512vbmi=off,cpu.avx2=off"} {
			wg.Go(func() {
				cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
				cmd.Env = append(os.Environ(), kernelEnv+"="+off, "GODEBUG="+off)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Errorf("with GODEBUG=%s: %v: %s", off, err, out)
				}
			})
		}
	}

	for _, tsEnc := range pointWalkTimestampEncodings {
		if otherKernel && tsEnc != format.TypeDeltaPacked {
			continue
		}
		t.Run(tsEnc.String(), func(t *testing.T) {
			var walkers []timestampWalker
			for _, bigEndian := range []bool{false, true} {
				for _, valEnc := range pointWalkValueEncodings {
					walkers = append(walkers, newTimestampWalker(t, tsEnc, valEnc, bigEndian))
				}
			}

			complete := 0
			var decoded []int64
			for _, column := range timestampTestColumns(tsEnc) {
				for _, in := range timestampTestVariants(column) {
					if in.count < 1 || in.count > timestampWalkerPoints {
						continue
					}
					decoded = slices.Grow(decoded[:0], in.count)[:in.count]
					for _, w := range walkers {
						if w.blob.decodeTimestampsSlice(in.column, in.count, decoded) != in.count {
							continue
						}
						complete++
						if diff := w.diff(in.column, decoded); diff != "" {
							t.Fatalf("%s beside %s: %s", in.name, w.name, diff)
						}
					}
				}
			}
			require.Positive(t, complete, "columns that decode in full")
		})
	}
}

// TestNumericBlob_CutColumnsAreRejectedAtDecode pins that no blob arrives with a column cut as the tests above cut it:
// the decoder rejects a blob whose stored index gives a column such a length, with the error it returned before.
// A Gorilla or Chimp column and a Delta or DeltaPacked group may end early in an accepted blob;
// TestNumericBlob_ForEachShortStreams covers those.
func TestNumericBlob_CutColumnsAreRejectedAtDecode(t *testing.T) {
	const metrics = 3
	// build returns the stored bytes of a blob and its first index entry.
	build := func(t *testing.T, spec pointWalkSpec, values func(points int) []float64) ([]byte, section.NumericIndexEntry) {
		t.Helper()
		data, _ := encodePointWalkBlob(t, spec, func(m int) ([]int64, []float64, []string) {
			ts, vals, tags := pointWalkSeries(spec.points, m, spec.shared)
			if values != nil {
				vals = values(spec.points)
			}

			return ts, vals, tags
		})
		blob, err := decodePointWalkBlob(data)
		require.NoError(t, err)

		return data, blob.index.sorted[0]
	}
	countError := func(entry section.NumericIndexEntry, tsLength, valLength int) string {
		return fmt.Sprintf("%v: metric ID %d has count %d but %d timestamp, %d value and 0 tag bytes",
			errs.ErrInvalidNumOfDataPoints, entry.MetricID, entry.Count, tsLength, valLength)
	}

	t.Run("raw value range", func(t *testing.T) {
		for _, bigEndian := range []bool{false, true} {
			spec := pointWalkSpec{tsEnc: format.TypeDeltaPacked, valEnc: format.TypeRaw, shared: true, bigEndian: bigEndian, metrics: metrics, points: 2}
			data, entry := build(t, spec, nil)
			for _, length := range []int{0, 7, 8, 9, 15, 17, 24} {
				w := openPointWalkWire(t, data)
				w.moveFirstValueEnd(length - entry.ValueLength)
				_, err := decodePointWalkBlob(w.data)
				require.ErrorIs(t, err, errs.ErrInvalidNumOfDataPoints, "%s with %d value bytes", spec, length)
				require.EqualError(t, err, countError(entry, entry.TimestampLength, length), "%s with %d value bytes", spec, length)
			}
		}
	})

	t.Run("cut ALP column", func(t *testing.T) {
		for _, series := range alpCutSeries() {
			spec := pointWalkSpec{tsEnc: format.TypeDeltaPacked, valEnc: series.valEnc, shared: true, metrics: metrics, points: 37}
			if series.values == nil {
				// The encoder writes the raw layout for a column of one point.
				spec.points = 1
			}
			data, entry := build(t, spec, series.values)
			for _, cut := range []int{1, entry.ValueLength / 2, entry.ValueLength - 1} {
				w := openPointWalkWire(t, data)
				w.moveFirstValueEnd(-cut)
				_, err := decodePointWalkBlob(w.data)
				require.ErrorIs(t, err, errs.ErrInvalidALPColumn, "%s %s cut by %d bytes", spec, series.name, cut)
				require.ErrorContains(t, err, fmt.Sprintf("metric ID %d", entry.MetricID), "%s %s cut by %d bytes", spec, series.name, cut)
			}
		}
	})

	t.Run("empty value column", func(t *testing.T) {
		for _, valEnc := range []format.EncodingType{format.TypeGorilla, format.TypeChimp} {
			spec := pointWalkSpec{tsEnc: format.TypeDeltaPacked, valEnc: valEnc, shared: true, metrics: metrics, points: 9}
			data, entry := build(t, spec, nil)
			w := openPointWalkWire(t, data)
			w.moveFirstValueEnd(-entry.ValueLength)
			_, err := decodePointWalkBlob(w.data)
			require.EqualError(t, err, countError(entry, entry.TimestampLength, 0), spec.String())
		}
	})

	t.Run("timestamp range shorter than the count", func(t *testing.T) {
		for _, tsEnc := range pointWalkTimestampEncodings {
			spec := pointWalkSpec{tsEnc: tsEnc, valEnc: format.TypeChimp, metrics: metrics, points: 9}
			data, entry := build(t, spec, nil)
			// One byte short of a Raw column, and one byte less than a byte per point otherwise.
			length := entry.TimestampLength - 1
			if tsEnc != format.TypeRaw {
				length = entry.Count - 1
			}
			w := openPointWalkWire(t, data)
			w.add(1, wireEntryTimestamps, length-entry.TimestampLength)
			w.add(2, wireEntryTimestamps, entry.TimestampLength-length)
			_, err := decodePointWalkBlob(w.data)
			require.EqualError(t, err, countError(entry, length, entry.ValueLength), spec.String())
		}
	})

	t.Run("shared group members of different counts", func(t *testing.T) {
		spec := pointWalkSpec{tsEnc: format.TypeDeltaPacked, valEnc: format.TypeChimp, shared: true, metrics: metrics, points: 9}
		data, _ := build(t, spec, nil)
		w := openPointWalkWire(t, data)
		w.add(0, wireEntryCount, 1)
		_, err := decodePointWalkBlob(w.data)
		require.ErrorIs(t, err, errs.ErrInvalidSharedTimestampTable)
	})
}

// TestNumericBlob_ForEachOverSharedTimestampsAllocatesNothing pins the allocations of the walks
// that pair a value column with shared timestamps:
// none for Gorilla and Chimp values,
// and none for ALP and ALP-RLE values once the decode pool holds a buffer, up to the largest column it keeps one for.
// The callback is a literal built inside the measured function and captures a local,
// so a callback that escaped would count.
func TestNumericBlob_ForEachOverSharedTimestampsAllocatesNothing(t *testing.T) {
	if raceEnabled {
		t.Skip("race detector adds allocations")
	}
	for _, tsEnc := range pointWalkTimestampEncodings {
		for _, valEnc := range []format.EncodingType{format.TypeGorilla, format.TypeChimp, format.TypeALP, format.TypeALPRLE} {
			for _, points := range []int{37, pool.MaxPooledDecodeFloat64s} {
				spec := pointWalkSpec{tsEnc: tsEnc, valEnc: valEnc, shared: true, metrics: 2, points: points}
				_, blob, names := buildPointWalkBlob(t, spec)
				name := names[0]
				id := hash.ID(name)
				requirePointWalkGroup(t, spec.String(), blob, name, true)

				var sink float64
				allocs := testing.AllocsPerRun(20, func() {
					var sum float64
					blob.ForEach(id, func(_ int, dp NumericDataPoint) bool { sum += dp.Val + float64(dp.Ts); return true })
					sink += sum
				})
				require.Zerof(t, allocs, "%s ForEach", spec)
				allocs = testing.AllocsPerRun(20, func() {
					var sum float64
					blob.ForEachByName(name, func(_ int, dp NumericDataPoint) bool { sum += dp.Val + float64(dp.Ts); return true })
					sink += sum
				})
				require.Zerof(t, allocs, "%s ForEachByName", spec)
				require.NotZero(t, sink)
			}
		}
	}
}
