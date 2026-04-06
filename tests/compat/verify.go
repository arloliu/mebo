package main

import (
	"fmt"
	"math"
	"strings"

	"github.com/arloliu/mebo/blob"
)

// VerifyResult holds the outcome of one verification run.
type VerifyResult struct {
	ScenarioID string
	Errors     []string
}

// OK returns true if there are no verification errors.
func (r *VerifyResult) OK() bool { return len(r.Errors) == 0 }

func (r *VerifyResult) addError(format string, args ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
}

// VerifyNumericBlob decodes data using NewNumericDecoder and verifies all
// fields match the manifest exactly.
func VerifyNumericBlob(data []byte, m *Manifest) *VerifyResult {
	result := &VerifyResult{ScenarioID: m.ScenarioID}

	dec, err := blob.NewNumericDecoder(data)
	if err != nil {
		result.addError("NewNumericDecoder: %v", err)
		return result
	}

	nb, err := dec.Decode()
	if err != nil {
		result.addError("Decode: %v", err)
		return result
	}

	if nb.MetricCount() != len(m.Metrics) {
		result.addError("MetricCount: got %d, want %d", nb.MetricCount(), len(m.Metrics))
		// Continue checking as many metrics as we can.
	}

	for _, wantMetric := range m.Metrics {
		verifyNumericMetric(nb, wantMetric, m.UseMetricID, result)
	}
	return result
}

func verifyNumericMetric(nb blob.NumericBlob, wantMetric ManifestMetric, useMetricID bool, result *VerifyResult) {
	id := wantMetric.MetricID
	name := wantMetric.MetricName
	label := metricLabel(id, name, useMetricID)

	// Presence check.
	if useMetricID {
		if !nb.HasMetricID(id) {
			result.addError("%s: metric not found in blob", label)
			return
		}
	} else {
		if !nb.HasMetricName(name) {
			result.addError("%s: metric name not found in blob", label)
			return
		}
	}

	// Length check.
	var gotLen int
	if useMetricID {
		gotLen = nb.Len(id)
	} else {
		gotLen = nb.LenByName(name)
	}
	wantLen := len(wantMetric.DataPoints)
	if gotLen != wantLen {
		result.addError("%s: Len: got %d, want %d", label, gotLen, wantLen)
	}

	// Sequential iteration via All / AllByName.
	i := 0
	var iterErr string
	iterFn := func(idx int, dp blob.NumericDataPoint) bool {
		if i >= len(wantMetric.DataPoints) {
			iterErr = fmt.Sprintf("%s: iterator yielded more points than expected (index %d)", label, i)
			return false
		}
		want := wantMetric.DataPoints[i]
		if dp.Ts != want.Timestamp {
			result.addError("%s[%d]: Timestamp: got %d, want %d", label, i, dp.Ts, want.Timestamp)
		}
		gotBits := math.Float64bits(dp.Val)
		if gotBits != want.ValueBits {
			result.addError("%s[%d]: Value: got bits %x (%.6f), want bits %x (%.6f)",
				label, i, gotBits, dp.Val, want.ValueBits, bitsToFloat64(want.ValueBits))
		}
		if dp.Tag != want.Tag {
			result.addError("%s[%d]: Tag: got %q, want %q", label, i, dp.Tag, want.Tag)
		}
		i++
		return true
	}

	if useMetricID {
		for idx, dp := range nb.All(id) {
			if !iterFn(idx, dp) {
				break
			}
		}
	} else {
		for idx, dp := range nb.AllByName(name) {
			if !iterFn(idx, dp) {
				break
			}
		}
	}
	if iterErr != "" {
		result.addError("%s", iterErr)
	}
	if i < len(wantMetric.DataPoints) {
		result.addError("%s: iterator yielded only %d points, want %d", label, i, len(wantMetric.DataPoints))
	}

	// Spot-check random access at indices: 0, mid, last.
	checkIdxNumeric(nb, wantMetric, useMetricID, 0, result)
	if len(wantMetric.DataPoints) > 1 {
		checkIdxNumeric(nb, wantMetric, useMetricID, len(wantMetric.DataPoints)/2, result)
		checkIdxNumeric(nb, wantMetric, useMetricID, len(wantMetric.DataPoints)-1, result)
	}
}

func checkIdxNumeric(nb blob.NumericBlob, wantMetric ManifestMetric, useMetricID bool, idx int, result *VerifyResult) {
	if idx >= len(wantMetric.DataPoints) {
		return
	}
	id := wantMetric.MetricID
	name := wantMetric.MetricName
	label := metricLabel(id, name, useMetricID)
	want := wantMetric.DataPoints[idx]

	var gotTs int64
	var tsOK bool
	var gotVal float64
	var valOK bool

	if useMetricID {
		gotTs, tsOK = nb.TimestampAt(id, idx)
		gotVal, valOK = nb.ValueAt(id, idx)
	} else {
		gotTs, tsOK = nb.TimestampAtByName(name, idx)
		gotVal, valOK = nb.ValueAtByName(name, idx)
	}

	if !tsOK {
		result.addError("%s: TimestampAt(%d): not found", label, idx)
	} else if gotTs != want.Timestamp {
		result.addError("%s: TimestampAt(%d): got %d, want %d", label, idx, gotTs, want.Timestamp)
	}
	if !valOK {
		result.addError("%s: ValueAt(%d): not found", label, idx)
	} else if math.Float64bits(gotVal) != want.ValueBits {
		result.addError("%s: ValueAt(%d): got bits %x, want bits %x", label, idx, math.Float64bits(gotVal), want.ValueBits)
	}
}

// VerifyTextBlob decodes data using NewTextDecoder and verifies all fields.
// Text values and real tags are stored in ManifestDataPoint.Tag using
// "textval|tag" encoding when tagsEnabled, or just "textval" otherwise.
func VerifyTextBlob(data []byte, m *Manifest) *VerifyResult {
	result := &VerifyResult{ScenarioID: m.ScenarioID}

	dec, err := blob.NewTextDecoder(data)
	if err != nil {
		result.addError("NewTextDecoder: %v", err)
		return result
	}

	tb, err := dec.Decode()
	if err != nil {
		result.addError("Decode: %v", err)
		return result
	}

	if tb.MetricCount() != len(m.Metrics) {
		result.addError("MetricCount: got %d, want %d", tb.MetricCount(), len(m.Metrics))
	}

	for _, wantMetric := range m.Metrics {
		verifyTextMetric(tb, wantMetric, m.UseMetricID, result)
	}
	return result
}

func verifyTextMetric(tb blob.TextBlob, wantMetric ManifestMetric, useMetricID bool, result *VerifyResult) {
	id := wantMetric.MetricID
	name := wantMetric.MetricName
	label := metricLabel(id, name, useMetricID)

	if useMetricID {
		if !tb.HasMetricID(id) {
			result.addError("%s: metric not found in blob", label)
			return
		}
	} else {
		if !tb.HasMetricName(name) {
			result.addError("%s: metric name not found in blob", label)
			return
		}
	}

	var gotLen int
	if useMetricID {
		gotLen = tb.Len(id)
	} else {
		gotLen = tb.LenByName(name)
	}
	if gotLen != len(wantMetric.DataPoints) {
		result.addError("%s: Len: got %d, want %d", label, gotLen, len(wantMetric.DataPoints))
	}

	i := 0
	var iterErr string
	iterFn := func(idx int, dp blob.TextDataPoint) bool {
		if i >= len(wantMetric.DataPoints) {
			iterErr = fmt.Sprintf("%s: iterator yielded more points than expected (index %d)", label, i)
			return false
		}
		want := wantMetric.DataPoints[i]
		if dp.Ts != want.Timestamp {
			result.addError("%s[%d]: Timestamp: got %d, want %d", label, i, dp.Ts, want.Timestamp)
		}
		// Decode the stored text value / tag from the Tag field.
		wantTextVal, wantTag := decodeTextManifestField(want.Tag)
		if dp.Val != wantTextVal {
			result.addError("%s[%d]: TextValue: got %q, want %q", label, i, dp.Val, wantTextVal)
		}
		if dp.Tag != wantTag {
			result.addError("%s[%d]: Tag: got %q, want %q", label, i, dp.Tag, wantTag)
		}
		i++
		return true
	}

	if useMetricID {
		for idx, dp := range tb.All(id) {
			if !iterFn(idx, dp) {
				break
			}
		}
	} else {
		for idx, dp := range tb.AllByName(name) {
			if !iterFn(idx, dp) {
				break
			}
		}
	}
	if iterErr != "" {
		result.addError("%s", iterErr)
	}
	if i < len(wantMetric.DataPoints) {
		result.addError("%s: iterator yielded only %d points, want %d", label, i, len(wantMetric.DataPoints))
	}
}

// decodeTextManifestField splits "textval|tag" → (textval, tag), or treats
// the whole string as textval when no "|" separator is present.
func decodeTextManifestField(stored string) (textVal, tag string) {
	idx := strings.IndexByte(stored, '|')
	if idx < 0 {
		return stored, ""
	}
	return stored[:idx], stored[idx+1:]
}

// VerifyBlobSet decodes a multi-blob payload and verifies the first two blobs
// as numeric, the third as text.
func VerifyBlobSet(data []byte, m *Manifest) *VerifyResult {
	result := &VerifyResult{ScenarioID: m.ScenarioID}

	blobs, err := unpackMultiBlob(data)
	if err != nil {
		result.addError("unpackMultiBlob: %v", err)
		return result
	}
	if len(blobs) < 3 {
		result.addError("expected 3 blobs in set, got %d", len(blobs))
		return result
	}

	// Decode the two numeric blobs and one text blob using DecodeBlobSet.
	bs, err := blob.DecodeBlobSet(blobs...)
	if err != nil {
		result.addError("DecodeBlobSet: %v", err)
		return result
	}

	// Verify all numeric metrics exist and iterate correctly.
	for _, wantMetric := range m.Metrics {
		var found bool
		for _, nb := range bs.NumericBlobs() {
			if nb.HasMetricID(wantMetric.MetricID) {
				sub := &VerifyResult{ScenarioID: m.ScenarioID}
				verifyNumericMetric(nb, wantMetric, true, sub)
				result.Errors = append(result.Errors, sub.Errors...)
				found = true
				break
			}
		}
		// Also check text metrics.
		if !found {
			for _, tb := range bs.TextBlobs() {
				if tb.HasMetricID(wantMetric.MetricID) {
					sub := &VerifyResult{ScenarioID: m.ScenarioID}
					verifyTextMetric(tb, wantMetric, true, sub)
					result.Errors = append(result.Errors, sub.Errors...)
					found = true
					break
				}
			}
		}
		if !found {
			result.addError("metric %d not found in any blob in set", wantMetric.MetricID)
		}
	}
	return result
}

func metricLabel(id uint64, name string, useMetricID bool) string {
	if useMetricID {
		return fmt.Sprintf("metric(%d)", id)
	}
	return fmt.Sprintf("metric(%q)", name)
}
