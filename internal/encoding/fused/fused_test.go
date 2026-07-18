package fused

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arloliu/mebo/internal/encoding/metadata"
	"github.com/arloliu/mebo/internal/encoding/timestamp/delta"
	"github.com/arloliu/mebo/internal/encoding/timestamp/deltapacked"
	"github.com/arloliu/mebo/internal/encoding/value/chimp"
	"github.com/arloliu/mebo/internal/encoding/value/gorilla"
)

func TestFusedDeltaGorillaAll(t *testing.T) {
	// Prepare test data
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	timestamps := make([]int64, 10)
	values := make([]float64, 10)
	for i := range 10 {
		timestamps[i] = startTime.Add(time.Duration(i) * time.Minute).UnixMicro()
		values[i] = 100.0 + float64(i)*1.5
	}

	// Encode timestamps with delta encoder
	tsEncoder := delta.NewTimestampDeltaEncoder()
	tsEncoder.WriteSlice(timestamps)
	tsData := make([]byte, len(tsEncoder.Bytes()))
	copy(tsData, tsEncoder.Bytes())
	tsEncoder.Finish()

	// Encode values with gorilla encoder
	valEncoder := gorilla.NewNumericGorillaEncoder()
	valEncoder.WriteSlice(values)
	valData := make([]byte, len(valEncoder.Bytes()))
	copy(valData, valEncoder.Bytes())
	valEncoder.Finish()

	// Decode using fused decoder
	gotTS := make([]int64, 0, 10)
	gotVals := make([]float64, 0, 10)
	for ts, val := range FusedDeltaGorillaAll(tsData, valData, 10) {
		gotTS = append(gotTS, ts)
		gotVals = append(gotVals, val)
	}

	require.Equal(t, timestamps, gotTS)
	require.Equal(t, values, gotVals)
}

func TestFusedDeltaGorillaAll_ConstantValues(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	timestamps := make([]int64, 50)
	values := make([]float64, 50)
	for i := range 50 {
		timestamps[i] = startTime.Add(time.Duration(i) * time.Second).UnixMicro()
		values[i] = 42.0 // constant value
	}

	tsEncoder := delta.NewTimestampDeltaEncoder()
	tsEncoder.WriteSlice(timestamps)
	tsData := make([]byte, len(tsEncoder.Bytes()))
	copy(tsData, tsEncoder.Bytes())
	tsEncoder.Finish()

	valEncoder := gorilla.NewNumericGorillaEncoder()
	valEncoder.WriteSlice(values)
	valData := make([]byte, len(valEncoder.Bytes()))
	copy(valData, valEncoder.Bytes())
	valEncoder.Finish()

	gotTS := make([]int64, 0, 50)
	gotVals := make([]float64, 0, 50)
	for ts, val := range FusedDeltaGorillaAll(tsData, valData, 50) {
		gotTS = append(gotTS, ts)
		gotVals = append(gotVals, val)
	}

	require.Equal(t, timestamps, gotTS)
	require.Equal(t, values, gotVals)
}

func TestFusedDeltaGorillaAll_SinglePoint(t *testing.T) {
	ts := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMicro()
	val := 99.5

	tsEncoder := delta.NewTimestampDeltaEncoder()
	tsEncoder.Write(ts)
	tsData := make([]byte, len(tsEncoder.Bytes()))
	copy(tsData, tsEncoder.Bytes())
	tsEncoder.Finish()

	valEncoder := gorilla.NewNumericGorillaEncoder()
	valEncoder.Write(val)
	valData := make([]byte, len(valEncoder.Bytes()))
	copy(valData, valEncoder.Bytes())
	valEncoder.Finish()

	gotTS := make([]int64, 0, 1)
	gotVals := make([]float64, 0, 1)
	for ts, val := range FusedDeltaGorillaAll(tsData, valData, 1) {
		gotTS = append(gotTS, ts)
		gotVals = append(gotVals, val)
	}

	require.Equal(t, []int64{ts}, gotTS)
	require.Equal(t, []float64{val}, gotVals)
}

func TestFusedDeltaGorillaAll_EarlyBreak(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	timestamps := make([]int64, 10)
	values := make([]float64, 10)
	for i := range 10 {
		timestamps[i] = startTime.Add(time.Duration(i) * time.Minute).UnixMicro()
		values[i] = float64(i)
	}

	tsEncoder := delta.NewTimestampDeltaEncoder()
	tsEncoder.WriteSlice(timestamps)
	tsData := make([]byte, len(tsEncoder.Bytes()))
	copy(tsData, tsEncoder.Bytes())
	tsEncoder.Finish()

	valEncoder := gorilla.NewNumericGorillaEncoder()
	valEncoder.WriteSlice(values)
	valData := make([]byte, len(valEncoder.Bytes()))
	copy(valData, valEncoder.Bytes())
	valEncoder.Finish()

	// Break after 3 items
	count := 0
	for range FusedDeltaGorillaAll(tsData, valData, 10) {
		count++
		if count == 3 {
			break
		}
	}

	require.Equal(t, 3, count)
}

func TestFusedDeltaGorillaAll_EmptyInputs(t *testing.T) {
	count := 0
	for range FusedDeltaGorillaAll(nil, nil, 0) {
		count++
	}
	require.Equal(t, 0, count)

	for range FusedDeltaGorillaAll([]byte{}, []byte{}, 0) {
		count++
	}
	require.Equal(t, 0, count)
}

func TestFusedDeltaGorillaTagAll(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	timestamps := make([]int64, 5)
	values := make([]float64, 5)
	tags := []string{"a", "b", "", "d", "e"}
	for i := range 5 {
		timestamps[i] = startTime.Add(time.Duration(i) * time.Minute).UnixMicro()
		values[i] = float64(i) * 2.0
	}

	tsEncoder := delta.NewTimestampDeltaEncoder()
	tsEncoder.WriteSlice(timestamps)
	tsData := make([]byte, len(tsEncoder.Bytes()))
	copy(tsData, tsEncoder.Bytes())
	tsEncoder.Finish()

	valEncoder := gorilla.NewNumericGorillaEncoder()
	valEncoder.WriteSlice(values)
	valData := make([]byte, len(valEncoder.Bytes()))
	copy(valData, valEncoder.Bytes())
	valEncoder.Finish()

	tagEncoder := metadata.NewTagEncoder(nil)
	tagEncoder.WriteSlice(tags)
	tagData := make([]byte, len(tagEncoder.Bytes()))
	copy(tagData, tagEncoder.Bytes())
	tagEncoder.Finish()

	var gotTS []int64
	var gotVals []float64
	var gotTags []string
	var gotIndices []int

	FusedDeltaGorillaTagAll(tsData, valData, tagData, 5, func(i int, ts int64, val float64, tag string) bool {
		gotIndices = append(gotIndices, i)
		gotTS = append(gotTS, ts)
		gotVals = append(gotVals, val)
		gotTags = append(gotTags, tag)

		return true
	})

	require.Equal(t, []int{0, 1, 2, 3, 4}, gotIndices)
	require.Equal(t, timestamps, gotTS)
	require.Equal(t, values, gotVals)
	require.Equal(t, tags, gotTags)
}

func TestFusedDeltaGorillaAll_VariedValues(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	timestamps := make([]int64, 20)
	values := []float64{
		0.0, 1.0, -1.0, math.MaxFloat64, math.SmallestNonzeroFloat64,
		math.Pi, math.E, 100.5, -100.5, 0.0,
		42.0, 42.0, 42.0, 42.0, 42.0, // run of identical
		99.9, 100.0, 100.1, 100.2, 100.3, // slowly changing
	}
	for i := range 20 {
		timestamps[i] = startTime.Add(time.Duration(i) * time.Second).UnixMicro()
	}

	tsEncoder := delta.NewTimestampDeltaEncoder()
	tsEncoder.WriteSlice(timestamps)
	tsData := make([]byte, len(tsEncoder.Bytes()))
	copy(tsData, tsEncoder.Bytes())
	tsEncoder.Finish()

	valEncoder := gorilla.NewNumericGorillaEncoder()
	valEncoder.WriteSlice(values)
	valData := make([]byte, len(valEncoder.Bytes()))
	copy(valData, valEncoder.Bytes())
	valEncoder.Finish()

	gotTS := make([]int64, 0, 20)
	gotVals := make([]float64, 0, 20)
	for ts, val := range FusedDeltaGorillaAll(tsData, valData, 20) {
		gotTS = append(gotTS, ts)
		gotVals = append(gotVals, val)
	}

	require.Equal(t, timestamps, gotTS)
	require.Equal(t, values, gotVals)
}

func TestFusedGorillaTagAll(t *testing.T) {
	values := []float64{1.0, 2.0, 3.0, 4.0, 5.0}
	tags := []string{"x", "y", "z", "", "w"}

	valEncoder := gorilla.NewNumericGorillaEncoder()
	valEncoder.WriteSlice(values)
	valData := make([]byte, len(valEncoder.Bytes()))
	copy(valData, valEncoder.Bytes())
	valEncoder.Finish()

	tagEncoder := metadata.NewTagEncoder(nil)
	tagEncoder.WriteSlice(tags)
	tagData := make([]byte, len(tagEncoder.Bytes()))
	copy(tagData, tagEncoder.Bytes())
	tagEncoder.Finish()

	var gotVals []float64
	var gotTags []string

	FusedGorillaTagAll(valData, tagData, 5, func(i int, val float64, tag string) bool {
		gotVals = append(gotVals, val)
		gotTags = append(gotTags, tag)
		return true
	})

	require.Equal(t, values, gotVals)
	require.Equal(t, tags, gotTags)
}

func TestFusedDeltaTagAll(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	timestamps := make([]int64, 5)
	tags := []string{"a", "b", "c", "", "e"}
	for i := range 5 {
		timestamps[i] = startTime.Add(time.Duration(i) * time.Minute).UnixMicro()
	}

	tsEncoder := delta.NewTimestampDeltaEncoder()
	tsEncoder.WriteSlice(timestamps)
	tsData := make([]byte, len(tsEncoder.Bytes()))
	copy(tsData, tsEncoder.Bytes())
	tsEncoder.Finish()

	tagEncoder := metadata.NewTagEncoder(nil)
	tagEncoder.WriteSlice(tags)
	tagData := make([]byte, len(tagEncoder.Bytes()))
	copy(tagData, tagEncoder.Bytes())
	tagEncoder.Finish()

	var gotTS []int64
	var gotTags []string

	FusedDeltaTagAll(tsData, tagData, 5, func(i int, ts int64, tag string) bool {
		gotTS = append(gotTS, ts)
		gotTags = append(gotTags, tag)
		return true
	})

	require.Equal(t, timestamps, gotTS)
	require.Equal(t, tags, gotTags)
}

func TestFusedDeltaPackedGorillaTagAll_PartialGroupAlignment(t *testing.T) {
	timestamps := []int64{1_000, 2_000, 3_100, 4_050, 5_250, 6_200, 7_600}
	values := []float64{1.5, -2.25, 3.75, 4.125, -5.5, 6.875, 7.25}
	tags := []string{"one", "two", "three", "four", "five", "six", "seven"}
	tsData, valData, tagData := encodeFusedDeltaPackedGorillaTagData(t, timestamps, values, tags)

	gotIndices := make([]int, 0, len(timestamps))
	gotTimestamps := make([]int64, 0, len(timestamps))
	gotValues := make([]float64, 0, len(values))
	gotTags := make([]string, 0, len(tags))
	FusedDeltaPackedGorillaTagAll(tsData, valData, tagData, len(timestamps), func(i int, ts int64, val float64, tag string) bool {
		gotIndices = append(gotIndices, i)
		gotTimestamps = append(gotTimestamps, ts)
		gotValues = append(gotValues, val)
		gotTags = append(gotTags, tag)

		return true
	})

	require.Equal(t, []int{0, 1, 2, 3, 4, 5, 6}, gotIndices)
	require.Equal(t, timestamps, gotTimestamps)
	require.Equal(t, values, gotValues)
	require.Equal(t, tags, gotTags)
}

func TestFusedDeltaPackedGorillaTagAll_MalformedStreamsStopAtValidData(t *testing.T) {
	timestamps := []int64{1_000, 2_000, 3_100, 4_050, 5_250, 6_200, 7_600}
	values := []float64{1.5, -2.25, 3.75, 4.125, -5.5, 6.875, 7.25}
	tags := []string{"one", "two", "three", "four", "five", "six", "seven"}
	tsData, valData, tagData := encodeFusedDeltaPackedGorillaTagData(t, timestamps, values, tags)

	tests := []struct {
		name      string
		tsData    []byte
		valData   []byte
		tagData   []byte
		wantCount int
	}{
		{
			name:      "packed timestamp stops before its incomplete final point",
			tsData:    tsData[:len(tsData)-1],
			valData:   valData,
			tagData:   tagData,
			wantCount: len(timestamps) - 1,
		},
		{
			name:      "value stops before its incomplete final point",
			tsData:    tsData,
			valData:   valData[:len(valData)-1],
			tagData:   tagData,
			wantCount: len(values) - 1,
		},
		{
			name:      "tag stops before its incomplete final point",
			tsData:    tsData,
			valData:   valData,
			tagData:   tagData[:len(tagData)-1],
			wantCount: len(tags) - 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotIndices := make([]int, 0, tt.wantCount)
			FusedDeltaPackedGorillaTagAll(tt.tsData, tt.valData, tt.tagData, len(timestamps), func(i int, _ int64, _ float64, _ string) bool {
				gotIndices = append(gotIndices, i)
				return true
			})

			require.Equal(t, tt.wantCount, len(gotIndices))
			require.Equal(t, makeRange(tt.wantCount), gotIndices)
		})
	}
}

func TestFusedDeltaPackedGorillaTagAll_CallbackShortCircuit(t *testing.T) {
	timestamps := []int64{1_000, 2_000, 3_100, 4_050, 5_250, 6_200, 7_600}
	values := []float64{1.5, -2.25, 3.75, 4.125, -5.5, 6.875, 7.25}
	tags := []string{"one", "two", "three", "four", "five", "six", "seven"}
	tsData, valData, tagData := encodeFusedDeltaPackedGorillaTagData(t, timestamps, values, tags)

	const stopAfter = 3
	callbackCount := 0
	FusedDeltaPackedGorillaTagAll(tsData, valData, tagData, len(timestamps), func(i int, ts int64, val float64, tag string) bool {
		callbackCount++
		require.Equal(t, callbackCount-1, i)
		require.Equal(t, timestamps[i], ts)
		require.Equal(t, values[i], val)
		require.Equal(t, tags[i], tag)

		return callbackCount < stopAfter
	})

	require.Equal(t, stopAfter, callbackCount, "no callback may run after short-circuiting")
}

func encodeFusedDeltaPackedGorillaTagData(t *testing.T, timestamps []int64, values []float64, tags []string) (tsData, valData, tagData []byte) {
	t.Helper()
	require.Equal(t, len(timestamps), len(values))
	require.Equal(t, len(timestamps), len(tags))

	tsEncoder := deltapacked.NewTimestampDeltaPackedEncoder()
	tsEncoder.WriteSlice(timestamps)
	tsData = append([]byte(nil), tsEncoder.Bytes()...)
	tsEncoder.Finish()

	valEncoder := gorilla.NewNumericGorillaEncoder()
	valEncoder.WriteSlice(values)
	valData = append([]byte(nil), valEncoder.Bytes()...)
	valEncoder.Finish()

	tagEncoder := metadata.NewTagEncoder(nil)
	tagEncoder.WriteSlice(tags)
	tagData = append([]byte(nil), tagEncoder.Bytes()...)
	tagEncoder.Finish()

	return tsData, valData, tagData
}

func makeRange(count int) []int {
	values := make([]int, count)
	for i := range values {
		values[i] = i
	}

	return values
}

// TestChimpValState_SetCount verifies that SetCount bounds an unbounded Next()
// drain to exactly the logical count, preventing trailing padding zeros in the
// final byte from being misread as extra unchanged ("00" flag pair) values.
// This mirrors GorillaValState.SetCount.
func TestChimpValState_SetCount(t *testing.T) {
	// Constant values: the first is stored in full (64 bits); each repeat is a
	// 2-bit "00" flag pair. With few points the final byte holds padding zeros
	// that look like additional unchanged pairs.
	values := []float64{42.0, 42.0}

	valEncoder := chimp.NewNumericChimpEncoder()
	valEncoder.WriteSlice(values)
	valData := make([]byte, len(valEncoder.Bytes()))
	copy(valData, valEncoder.Bytes())
	valEncoder.Finish()

	t.Run("SetCount bounds the drain", func(t *testing.T) {
		st, ok := chimp.NewChimpValState(valData)
		require.True(t, ok)
		st.SetCount(len(values))

		got := []float64{st.Val()}
		for st.Next() {
			got = append(got, st.Val())
		}

		require.Equal(t, values, got)
	})

	t.Run("unbounded drain over-produces phantom values", func(t *testing.T) {
		st, ok := chimp.NewChimpValState(valData)
		require.True(t, ok)
		// No SetCount: remaining defaults to math.MaxInt, so padding zeros leak
		// in as phantom unchanged values.

		n := 1 // first value already available via Val()
		for st.Next() {
			n++
			if n > 100 { // safety cap against a runaway loop
				break
			}
		}

		require.Greater(t, n, len(values),
			"unbounded drain should read padding zeros as phantom unchanged values")
	})
}

// TestGorillaValState_SetCount verifies that SetCount bounds an unbounded
// Next() drain to exactly the logical count, the wrapper-level counterpart of
// the Chimp test. The bounded state is reserved for callers that need it;
// fused loops use GorillaCursor and do not check or decrement a value count.
func TestGorillaValState_SetCount(t *testing.T) {
	// Constant values: first stored in full (64 bits), each repeat is a single
	// "0" control bit; the final byte's padding zeros look like more unchanged
	// values to an unbounded drain.
	values := []float64{42.0, 42.0}

	valEncoder := gorilla.NewNumericGorillaEncoder()
	valEncoder.WriteSlice(values)
	valData := make([]byte, len(valEncoder.Bytes()))
	copy(valData, valEncoder.Bytes())
	valEncoder.Finish()

	t.Run("SetCount bounds the drain", func(t *testing.T) {
		st, ok := gorilla.NewGorillaValState(valData)
		require.True(t, ok)
		st.SetCount(len(values))

		got := []float64{st.Val()}
		for st.Next() {
			got = append(got, st.Val())
		}

		require.Equal(t, values, got)
	})

	t.Run("unbounded drain over-produces phantom values", func(t *testing.T) {
		st, ok := gorilla.NewGorillaValState(valData)
		require.True(t, ok)
		// No SetCount: remaining defaults to math.MaxInt.

		n := 1 // first value already available via Val()
		for st.Next() {
			n++
			if n > 100 { // safety cap against a runaway loop
				break
			}
		}

		require.Greater(t, n, len(values),
			"unbounded drain should read padding zeros as phantom unchanged values")
	})
}

// genFusedBenchStream encodes one measurev2-shaped metric stream.
func genFusedBenchStream(points int, seed int64) (tsData, valData []byte) {
	rng := rand.New(rand.NewSource(seed))
	start := time.Unix(1700000000, 0).UTC()

	intervalUs := int64(time.Second / time.Microsecond)
	ts := start.UnixMicro()
	val := 50.0 + rng.Float64()*50.0
	timestamps := make([]int64, 0, points)
	values := make([]float64, 0, points)
	for range points {
		jitter := int64(float64(intervalUs) * 0.001 * (rng.Float64()*2 - 1))
		ts += intervalUs + jitter
		val *= 1 + 0.005*(rng.Float64()*2-1)
		timestamps = append(timestamps, ts)
		values = append(values, val)
	}

	tsEncoder := delta.NewTimestampDeltaEncoder()
	tsEncoder.WriteSlice(timestamps)
	tsData = append([]byte(nil), tsEncoder.Bytes()...)
	tsEncoder.Finish()

	valEncoder := gorilla.NewNumericGorillaEncoder()
	valEncoder.WriteSlice(values)
	valData = append([]byte(nil), valEncoder.Bytes()...)
	valEncoder.Finish()

	return tsData, valData
}

func genFusedChimpBenchStream(points int, seed int64) (tsData, valData []byte) {
	tsData, gorillaData := genFusedBenchStream(points, seed)
	decoder := gorilla.NewNumericGorillaDecoder()
	values := make([]float64, 0, points)
	for value := range decoder.All(gorillaData, points) {
		values = append(values, value)
	}

	encoder := chimp.NewNumericChimpEncoder()
	encoder.WriteSlice(values)
	valData = append([]byte(nil), encoder.Bytes()...)
	encoder.Finish()

	return tsData, valData
}

func BenchmarkFusedDeltaGorillaEach(b *testing.B) {
	tsData, valData := genFusedBenchStream(200, 42)

	b.ReportAllocs()
	for b.Loop() {
		var sink int64
		var vsink float64
		FusedDeltaGorillaEach(tsData, valData, 200, func(_ int, ts int64, val float64) bool {
			sink += ts
			vsink += val

			return true
		})
		if sink == 0 && vsink == 0 {
			b.Fatal("no data")
		}
	}
}

func BenchmarkFusedDeltaChimpEach(b *testing.B) {
	tsData, valData := genFusedChimpBenchStream(200, 42)

	b.ReportAllocs()
	for b.Loop() {
		var sink int64
		var vsink float64
		FusedDeltaChimpEach(tsData, valData, 200, func(_ int, ts int64, val float64) bool {
			sink += ts
			vsink += val

			return true
		})
		if sink == 0 && vsink == 0 {
			b.Fatal("no data")
		}
	}
}

func BenchmarkFusedDeltaGorillaAll(b *testing.B) {
	tsData, valData := genFusedBenchStream(200, 42)

	b.ReportAllocs()
	for b.Loop() {
		var sink int64
		var vsink float64
		for ts, val := range FusedDeltaGorillaAll(tsData, valData, 200) {
			sink += ts
			vsink += val
		}
		if sink == 0 && vsink == 0 {
			b.Fatal("no data")
		}
	}
}
