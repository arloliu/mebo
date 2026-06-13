package encoding

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
	tsEncoder := NewTimestampDeltaEncoder()
	tsEncoder.WriteSlice(timestamps)
	tsData := make([]byte, len(tsEncoder.Bytes()))
	copy(tsData, tsEncoder.Bytes())
	tsEncoder.Finish()

	// Encode values with gorilla encoder
	valEncoder := NewNumericGorillaEncoder()
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

	tsEncoder := NewTimestampDeltaEncoder()
	tsEncoder.WriteSlice(timestamps)
	tsData := make([]byte, len(tsEncoder.Bytes()))
	copy(tsData, tsEncoder.Bytes())
	tsEncoder.Finish()

	valEncoder := NewNumericGorillaEncoder()
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

	tsEncoder := NewTimestampDeltaEncoder()
	tsEncoder.Write(ts)
	tsData := make([]byte, len(tsEncoder.Bytes()))
	copy(tsData, tsEncoder.Bytes())
	tsEncoder.Finish()

	valEncoder := NewNumericGorillaEncoder()
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

	tsEncoder := NewTimestampDeltaEncoder()
	tsEncoder.WriteSlice(timestamps)
	tsData := make([]byte, len(tsEncoder.Bytes()))
	copy(tsData, tsEncoder.Bytes())
	tsEncoder.Finish()

	valEncoder := NewNumericGorillaEncoder()
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

	tsEncoder := NewTimestampDeltaEncoder()
	tsEncoder.WriteSlice(timestamps)
	tsData := make([]byte, len(tsEncoder.Bytes()))
	copy(tsData, tsEncoder.Bytes())
	tsEncoder.Finish()

	valEncoder := NewNumericGorillaEncoder()
	valEncoder.WriteSlice(values)
	valData := make([]byte, len(valEncoder.Bytes()))
	copy(valData, valEncoder.Bytes())
	valEncoder.Finish()

	tagEncoder := NewTagEncoder(nil)
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

	tsEncoder := NewTimestampDeltaEncoder()
	tsEncoder.WriteSlice(timestamps)
	tsData := make([]byte, len(tsEncoder.Bytes()))
	copy(tsData, tsEncoder.Bytes())
	tsEncoder.Finish()

	valEncoder := NewNumericGorillaEncoder()
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

	valEncoder := NewNumericGorillaEncoder()
	valEncoder.WriteSlice(values)
	valData := make([]byte, len(valEncoder.Bytes()))
	copy(valData, valEncoder.Bytes())
	valEncoder.Finish()

	tagEncoder := NewTagEncoder(nil)
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

	tsEncoder := NewTimestampDeltaEncoder()
	tsEncoder.WriteSlice(timestamps)
	tsData := make([]byte, len(tsEncoder.Bytes()))
	copy(tsData, tsEncoder.Bytes())
	tsEncoder.Finish()

	tagEncoder := NewTagEncoder(nil)
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

// TestChimpValState_SetCount verifies that SetCount bounds an unbounded Next()
// drain to exactly the logical count, preventing trailing padding zeros in the
// final byte from being misread as extra unchanged ("00" flag pair) values.
// This mirrors GorillaValState.SetCount.
func TestChimpValState_SetCount(t *testing.T) {
	// Constant values: the first is stored in full (64 bits); each repeat is a
	// 2-bit "00" flag pair. With few points the final byte holds padding zeros
	// that look like additional unchanged pairs.
	values := []float64{42.0, 42.0}

	valEncoder := NewNumericChimpEncoder()
	valEncoder.WriteSlice(values)
	valData := make([]byte, len(valEncoder.Bytes()))
	copy(valData, valEncoder.Bytes())
	valEncoder.Finish()

	t.Run("SetCount bounds the drain", func(t *testing.T) {
		st, ok := NewChimpValState(valData)
		require.True(t, ok)
		st.SetCount(len(values))

		got := []float64{st.Val()}
		for st.Next() {
			got = append(got, st.Val())
		}

		require.Equal(t, values, got)
	})

	t.Run("unbounded drain over-produces phantom values", func(t *testing.T) {
		st, ok := NewChimpValState(valData)
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
// the Chimp test. With the cap living in the Next() wrapper, the bulk fused
// loops (which call decodeGorillaValue directly) stay free of per-value cost.
func TestGorillaValState_SetCount(t *testing.T) {
	// Constant values: first stored in full (64 bits), each repeat is a single
	// "0" control bit; the final byte's padding zeros look like more unchanged
	// values to an unbounded drain.
	values := []float64{42.0, 42.0}

	valEncoder := NewNumericGorillaEncoder()
	valEncoder.WriteSlice(values)
	valData := make([]byte, len(valEncoder.Bytes()))
	copy(valData, valEncoder.Bytes())
	valEncoder.Finish()

	t.Run("SetCount bounds the drain", func(t *testing.T) {
		st, ok := NewGorillaValState(valData)
		require.True(t, ok)
		st.SetCount(len(values))

		got := []float64{st.Val()}
		for st.Next() {
			got = append(got, st.Val())
		}

		require.Equal(t, values, got)
	})

	t.Run("unbounded drain over-produces phantom values", func(t *testing.T) {
		st, ok := NewGorillaValState(valData)
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
