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
	// DecodeErr is the raw error returned by the decode step itself
	// (NewNumericDecoder/NewTextDecoder, dec.Decode(), unpackMultiBlob, or
	// DecodeBlobSet) — nil when decoding succeeded. It is distinct from
	// Errors, which also accumulates post-decode field-mismatch messages
	// against the manifest: a "must reject" fixture whose manifest carries
	// no verifiable metrics (e.g. Metrics: nil) will always have a nonzero
	// Errors slice once decoded successfully (MetricCount mismatch), so
	// callers that need to know whether the *decode itself* failed — as
	// opposed to the decoded blob merely not matching the manifest — must
	// consult DecodeErr, not OK().
	DecodeErr error
}

// OK returns true if there are no verification errors.
func (r *VerifyResult) OK() bool { return len(r.Errors) == 0 }

// verifyNumericBorrowedImpl and verifyTextBorrowedImpl are set by
// verify_metricnames.go's init() (build tag "metricnames") to additionally
// exercise NewNumericDecoderBorrowed/NewTextDecoderBorrowed — new-to-v1.10.0
// zero-copy constructors that don't exist in a pre-v1.10.0 module. When
// building against an older module (e.g. v1.9.0, the OLD side of the compat
// matrix), these stay nil and Manifest.VerifyBorrowed is silently skipped:
// the borrowed decode path is an additional self-check the NEW binary can
// run on itself, not a cross-version wire-compat requirement — unlike
// main.go's namedSentinels, nothing about it needs to hold on a binary built
// before the symbols existed.
var (
	verifyNumericBorrowedImpl func(data []byte, m *Manifest) *VerifyResult
	verifyTextBorrowedImpl    func(data []byte, m *Manifest) *VerifyResult
)

// materializeByNameFallbackFixed is set true by verify_metricnames.go's
// init() (build tag "metricnames"), i.e. only when built against a
// v1.10.0+ module. MaterializedNumericBlob/MaterializedTextBlob's ByName
// accessors had no hash-based fallback for a blob with no names payload at
// all until the fix that shipped alongside this release — a pre-v1.10.0
// module (e.g. v1.9.0, the OLD side of the compat matrix) still has that
// gap. verifyNumericMetricMaterialized/verifyTextMetricMaterialized use
// this to tell "OLD's known, frozen Materialize() limitation" apart from a
// genuine not-found that should still fail the row on every version (a
// names-bearing blob, or an ID-keyed lookup — neither of those cases were
// ever broken).
var materializeByNameFallbackFixed bool

// materializeCollisionSafe is set true by verify_metricnames.go's init(),
// i.e. only when built against a v1.10.0+ module. Materialize() collapsed
// two distinct metrics sharing one hashed MetricID into one until the fix
// that shipped alongside this release; a pre-v1.10.0 module (the OLD side
// of the compat matrix) still does that. VerifyNumericBlob/VerifyTextBlob
// skip the Materialize()-based checks entirely for a Manifest.HasRealCollision
// scenario when this is false, rather than asserting collision-safe
// materialization a known-frozen OLD binary was never fixed to provide —
// the raw blob checks (verifyNumericMetric/verifyTextMetric, unaffected)
// already confirm the wire bytes and non-materialized resolution are
// correct on every version.
var materializeCollisionSafe bool

func (r *VerifyResult) addError(format string, args ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
}

// VerifyNumericBlob decodes data using NewNumericDecoder and verifies all
// fields match the manifest exactly, through both the raw blob's accessors
// and Materialize()'s O(1) random-access representation. The two are built
// by independent code paths (Materialize has its own ordinal/name
// resolution), and a real cross-version regression has previously existed
// where Materialize collapsed two distinct metrics sharing a hashed
// MetricID while the raw blob read them apart correctly — verifying only
// the raw path would not have caught it.
func VerifyNumericBlob(data []byte, m *Manifest) *VerifyResult {
	result := &VerifyResult{ScenarioID: m.ScenarioID}

	dec, err := blob.NewNumericDecoder(data)
	if err != nil {
		result.DecodeErr = err
		result.addError("NewNumericDecoder: %v", err)
		return result
	}

	nb, err := dec.Decode()
	if err != nil {
		result.DecodeErr = err
		result.addError("Decode: %v", err)
		return result
	}

	if nb.MetricCount() != len(m.Metrics) {
		result.addError("MetricCount: got %d, want %d", nb.MetricCount(), len(m.Metrics))
		// Continue checking as many metrics as we can.
	}

	if m.WantNamesPayload && !nb.HasMetricNames() {
		result.addError("HasMetricNames: got false, want true (scenario requires a names payload)")
	}

	skipMaterialize := m.HasRealCollision && !materializeCollisionSafe
	var mat blob.MaterializedNumericBlob
	if !skipMaterialize {
		mat = nb.Materialize()
		if mat.MetricCount() != len(m.Metrics) {
			result.addError("Materialize: MetricCount: got %d, want %d", mat.MetricCount(), len(m.Metrics))
		}
	}

	for _, wantMetric := range m.Metrics {
		verifyNumericMetric(nb, wantMetric, m.UseMetricID, result)
		if !skipMaterialize {
			verifyNumericMetricMaterialized(nb, mat, wantMetric, m.UseMetricID, result)
		}
	}

	if m.VerifyBorrowed && verifyNumericBorrowedImpl != nil {
		br := verifyNumericBorrowedImpl(data, m)
		if br.DecodeErr != nil && result.DecodeErr == nil {
			result.DecodeErr = br.DecodeErr
		}
		for _, e := range br.Errors {
			result.addError("Borrowed: %s", e)
		}
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

// verifyNumericMetricMaterialized checks the same field-by-field contract as
// verifyNumericMetric, but through Materialize()'s O(1) accessors — both the
// whole-blob mat and the standalone single-metric MaterializeMetric(ByName)
// path, which resolves entries independently of the whole-blob one.
func verifyNumericMetricMaterialized(nb blob.NumericBlob, mat blob.MaterializedNumericBlob, wantMetric ManifestMetric, useMetricID bool, result *VerifyResult) {
	id := wantMetric.MetricID
	name := wantMetric.MetricName
	label := metricLabel(id, name, useMetricID)
	want := wantMetric.DataPoints

	// toleratedOldGap is true only for the precise shape that was actually
	// broken pre-v1.10.0: a name-mode lookup against a blob with no names
	// payload at all, verified on a binary built without the fix. It gates
	// ONLY the whole-blob mat.* accessors below — the standalone
	// nb.MaterializeMetric(ByName) checks further down resolve through the
	// raw blob's index first and were never affected, so they must stay
	// unconditional on every version.
	toleratedOldGap := !useMetricID && !nb.HasMetricNames() && !materializeByNameFallbackFixed

	var present bool
	var gotCount int
	if useMetricID {
		present = mat.HasMetricID(id)
		gotCount = mat.DataPointCount(id)
	} else {
		present = mat.HasMetricName(name)
		gotCount = mat.DataPointCountByName(name)
	}
	switch {
	case !present && toleratedOldGap:
		// Known pre-v1.10.0 limitation, not a regression: Materialize()'s
		// ByName accessors had no hash-based fallback for a blob with no
		// names payload at all until the fix that shipped alongside this
		// release (see blob/numeric_blob_material.go's ordinalByName). A
		// binary built against an older module (e.g. v1.9.0, the OLD side
		// of the compat matrix) still has that gap baked in — its raw blob
		// accessors (verifyNumericMetric, checked separately) already
		// resolve this metric correctly via hash-only membership, so the
		// wire bytes are fine; only OLD's whole-blob Materialize() itself is
		// missing the fallback. See materializeByNameFallbackFixed.
	case !present:
		result.addError("%s: Materialize: metric not found", label)
	default:
		if gotCount != len(want) {
			result.addError("%s: Materialize: DataPointCount: got %d, want %d", label, gotCount, len(want))
		}

		for i, wantDP := range want {
			var gotTs int64
			var tsOK bool
			var gotVal float64
			var valOK bool
			var gotTag string
			var tagOK bool
			if useMetricID {
				gotTs, tsOK = mat.TimestampAt(id, i)
				gotVal, valOK = mat.ValueAt(id, i)
				gotTag, tagOK = mat.TagAt(id, i)
			} else {
				gotTs, tsOK = mat.TimestampAtByName(name, i)
				gotVal, valOK = mat.ValueAtByName(name, i)
				gotTag, tagOK = mat.TagAtByName(name, i)
			}
			if !tsOK || !valOK || !tagOK {
				result.addError("%s: Materialize[%d]: accessor returned not-found", label, i)
				continue
			}
			if gotTs != wantDP.Timestamp {
				result.addError("%s: Materialize[%d]: Timestamp: got %d, want %d", label, i, gotTs, wantDP.Timestamp)
			}
			if gotBits := math.Float64bits(gotVal); gotBits != wantDP.ValueBits {
				result.addError("%s: Materialize[%d]: Value: got bits %x, want bits %x", label, i, gotBits, wantDP.ValueBits)
			}
			if gotTag != wantDP.Tag {
				result.addError("%s: Materialize[%d]: Tag: got %q, want %q", label, i, gotTag, wantDP.Tag)
			}
		}
	}

	// The standalone single-metric materialization path always runs,
	// regardless of toleratedOldGap — see the comment above.
	var single blob.MaterializedNumericMetric
	var ok bool
	if useMetricID {
		single, ok = nb.MaterializeMetric(id)
	} else {
		single, ok = nb.MaterializeMetricByName(name)
	}
	if !ok {
		result.addError("%s: MaterializeMetric: not found", label)
		return
	}
	if single.Len() != len(want) {
		result.addError("%s: MaterializeMetric: Len: got %d, want %d", label, single.Len(), len(want))
	}
	for i, wantDP := range want {
		gotTs, tsOK := single.TimestampAt(i)
		gotVal, valOK := single.ValueAt(i)
		gotTag, tagOK := single.TagAt(i)
		if !tsOK || !valOK || !tagOK {
			result.addError("%s: MaterializeMetric[%d]: accessor returned not-found", label, i)
			continue
		}
		if gotTs != wantDP.Timestamp {
			result.addError("%s: MaterializeMetric[%d]: Timestamp: got %d, want %d", label, i, gotTs, wantDP.Timestamp)
		}
		if gotBits := math.Float64bits(gotVal); gotBits != wantDP.ValueBits {
			result.addError("%s: MaterializeMetric[%d]: Value: got bits %x, want bits %x", label, i, gotBits, wantDP.ValueBits)
		}
		if gotTag != wantDP.Tag {
			result.addError("%s: MaterializeMetric[%d]: Tag: got %q, want %q", label, i, gotTag, wantDP.Tag)
		}
	}
}

// VerifyTextBlob decodes data using NewTextDecoder and verifies all fields.
// Text values and real tags are stored in ManifestDataPoint.Tag using
// "textval|tag" encoding when tagsEnabled, or just "textval" otherwise.
func VerifyTextBlob(data []byte, m *Manifest) *VerifyResult {
	result := &VerifyResult{ScenarioID: m.ScenarioID}

	dec, err := blob.NewTextDecoder(data)
	if err != nil {
		result.DecodeErr = err
		result.addError("NewTextDecoder: %v", err)
		return result
	}

	tb, err := dec.Decode()
	if err != nil {
		result.DecodeErr = err
		result.addError("Decode: %v", err)
		return result
	}

	if tb.MetricCount() != len(m.Metrics) {
		result.addError("MetricCount: got %d, want %d", tb.MetricCount(), len(m.Metrics))
	}

	if m.WantNamesPayload && !tb.HasMetricNames() {
		result.addError("HasMetricNames: got false, want true (scenario requires a names payload)")
	}

	skipMaterialize := m.HasRealCollision && !materializeCollisionSafe
	var mat blob.MaterializedTextBlob
	if !skipMaterialize {
		mat = tb.Materialize()
		if mat.MetricCount() != len(m.Metrics) {
			result.addError("Materialize: MetricCount: got %d, want %d", mat.MetricCount(), len(m.Metrics))
		}
	}

	for _, wantMetric := range m.Metrics {
		verifyTextMetric(tb, wantMetric, m.UseMetricID, result)
		if !skipMaterialize {
			verifyTextMetricMaterialized(tb, mat, wantMetric, m.UseMetricID, result)
		}
	}

	if m.VerifyBorrowed && verifyTextBorrowedImpl != nil {
		br := verifyTextBorrowedImpl(data, m)
		if br.DecodeErr != nil && result.DecodeErr == nil {
			result.DecodeErr = br.DecodeErr
		}
		for _, e := range br.Errors {
			result.addError("Borrowed: %s", e)
		}
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

// verifyTextMetricMaterialized checks the same field-by-field contract as
// verifyTextMetric, but through Materialize()'s O(1) accessors — both the
// whole-blob mat and the standalone single-metric MaterializeMetric(ByName)
// path. Text values and real tags are packed into ManifestDataPoint.Tag the
// same way verifyTextMetric decodes them.
func verifyTextMetricMaterialized(tb blob.TextBlob, mat blob.MaterializedTextBlob, wantMetric ManifestMetric, useMetricID bool, result *VerifyResult) {
	id := wantMetric.MetricID
	name := wantMetric.MetricName
	label := metricLabel(id, name, useMetricID)
	want := wantMetric.DataPoints

	// toleratedOldGap — see the identical variable in
	// verifyNumericMetricMaterialized for the full explanation. It gates
	// ONLY the whole-blob mat.* accessors below, never the standalone
	// tb.MaterializeMetric(ByName) checks further down.
	toleratedOldGap := !useMetricID && !tb.HasMetricNames() && !materializeByNameFallbackFixed

	var present bool
	var gotCount int
	if useMetricID {
		present = mat.HasMetricID(id)
		gotCount = mat.DataPointCount(id)
	} else {
		present = mat.HasMetricName(name)
		gotCount = mat.DataPointCountByName(name)
	}
	switch {
	case !present && toleratedOldGap:
		// Known pre-v1.10.0 limitation, not a regression.
	case !present:
		result.addError("%s: Materialize: metric not found", label)
	default:
		if gotCount != len(want) {
			result.addError("%s: Materialize: DataPointCount: got %d, want %d", label, gotCount, len(want))
		}

		for i, wantDP := range want {
			wantTextVal, wantTag := decodeTextManifestField(wantDP.Tag)

			var gotTs int64
			var tsOK bool
			var gotVal string
			var valOK bool
			var gotTag string
			var tagOK bool
			if useMetricID {
				gotTs, tsOK = mat.TimestampAt(id, i)
				gotVal, valOK = mat.ValueAt(id, i)
				gotTag, tagOK = mat.TagAt(id, i)
			} else {
				gotTs, tsOK = mat.TimestampAtByName(name, i)
				gotVal, valOK = mat.ValueAtByName(name, i)
				gotTag, tagOK = mat.TagAtByName(name, i)
			}
			if !tsOK || !valOK || !tagOK {
				result.addError("%s: Materialize[%d]: accessor returned not-found", label, i)
				continue
			}
			if gotTs != wantDP.Timestamp {
				result.addError("%s: Materialize[%d]: Timestamp: got %d, want %d", label, i, gotTs, wantDP.Timestamp)
			}
			if gotVal != wantTextVal {
				result.addError("%s: Materialize[%d]: TextValue: got %q, want %q", label, i, gotVal, wantTextVal)
			}
			if gotTag != wantTag {
				result.addError("%s: Materialize[%d]: Tag: got %q, want %q", label, i, gotTag, wantTag)
			}
		}
	}

	// The standalone single-metric materialization path always runs,
	// regardless of toleratedOldGap.
	var single blob.MaterializedTextMetric
	var ok bool
	if useMetricID {
		single, ok = tb.MaterializeMetric(id)
	} else {
		single, ok = tb.MaterializeMetricByName(name)
	}
	if !ok {
		result.addError("%s: MaterializeMetric: not found", label)
		return
	}
	if single.Len() != len(want) {
		result.addError("%s: MaterializeMetric: Len: got %d, want %d", label, single.Len(), len(want))
	}
	for i, wantDP := range want {
		wantTextVal, wantTag := decodeTextManifestField(wantDP.Tag)

		gotTs, tsOK := single.TimestampAt(i)
		gotVal, valOK := single.ValueAt(i)
		gotTag, tagOK := single.TagAt(i)
		if !tsOK || !valOK || !tagOK {
			result.addError("%s: MaterializeMetric[%d]: accessor returned not-found", label, i)
			continue
		}
		if gotTs != wantDP.Timestamp {
			result.addError("%s: MaterializeMetric[%d]: Timestamp: got %d, want %d", label, i, gotTs, wantDP.Timestamp)
		}
		if gotVal != wantTextVal {
			result.addError("%s: MaterializeMetric[%d]: TextValue: got %q, want %q", label, i, gotVal, wantTextVal)
		}
		if gotTag != wantTag {
			result.addError("%s: MaterializeMetric[%d]: Tag: got %q, want %q", label, i, gotTag, wantTag)
		}
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
		result.DecodeErr = err
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
		result.DecodeErr = err
		result.addError("DecodeBlobSet: %v", err)
		return result
	}

	// Verify all numeric metrics exist and iterate correctly, both via the
	// raw blob accessors and Materialize() (see verifyNumericMetricMaterialized).
	// A member is located by ID or by name depending on m.UseMetricID, the
	// same switch VerifyNumericBlob/VerifyTextBlob use — blobset-v1-mixed
	// (ID mode) and any names-bearing blobset scenario (name mode, e.g. a
	// cross-member hash collision) both go through this same path.
	//
	// skipMaterialize mirrors VerifyNumericBlob/VerifyTextBlob: a member
	// blob carrying a genuine within-blob collision hits the same
	// pre-v1.10.0 Materialize() gap on an OLD binary (see
	// materializeCollisionSafe's doc comment). No current blobset scenario
	// sets HasRealCollision (blobset-v1-mn-collision's collision is only
	// cross-member — no single member blob has two entries for one ID — so
	// each member's own Materialize() is unaffected), but this stays
	// correct if one ever does.
	skipMaterialize := m.HasRealCollision && !materializeCollisionSafe
	for _, wantMetric := range m.Metrics {
		var found bool
		for _, nb := range bs.NumericBlobs() {
			present := nb.HasMetricID(wantMetric.MetricID)
			if !m.UseMetricID {
				present = nb.HasMetricName(wantMetric.MetricName)
			}
			if present {
				sub := &VerifyResult{ScenarioID: m.ScenarioID}
				verifyNumericMetric(nb, wantMetric, m.UseMetricID, sub)
				if !skipMaterialize {
					verifyNumericMetricMaterialized(nb, nb.Materialize(), wantMetric, m.UseMetricID, sub)
				}
				if m.WantNamesPayload && !nb.HasMetricNames() {
					sub.addError("%s: member HasMetricNames: got false, want true", metricLabel(wantMetric.MetricID, wantMetric.MetricName, m.UseMetricID))
				}
				result.Errors = append(result.Errors, sub.Errors...)
				found = true
				break
			}
		}
		// Also check text metrics.
		if !found {
			for _, tb := range bs.TextBlobs() {
				present := tb.HasMetricID(wantMetric.MetricID)
				if !m.UseMetricID {
					present = tb.HasMetricName(wantMetric.MetricName)
				}
				if present {
					sub := &VerifyResult{ScenarioID: m.ScenarioID}
					verifyTextMetric(tb, wantMetric, m.UseMetricID, sub)
					if !skipMaterialize {
						verifyTextMetricMaterialized(tb, tb.Materialize(), wantMetric, m.UseMetricID, sub)
					}
					if m.WantNamesPayload && !tb.HasMetricNames() {
						sub.addError("%s: member HasMetricNames: got false, want true", metricLabel(wantMetric.MetricID, wantMetric.MetricName, m.UseMetricID))
					}
					result.Errors = append(result.Errors, sub.Errors...)
					found = true
					break
				}
			}
		}
		if !found {
			result.addError("%s: not found in any blob in set", metricLabel(wantMetric.MetricID, wantMetric.MetricName, m.UseMetricID))
		}

		// The checks above only ever call member-blob (NumericBlob/TextBlob)
		// accessors — they never exercise BlobSet's own ByName resolution
		// (blob_set_identity.go, entirely new this release), which is the
		// actual code that has to get a cross-member collision right. Run it
		// too, for name-mode scenarios, so a broken set-level resolver can't
		// pass just because each member individually reads back fine.
		if found && !m.UseMetricID {
			verifyBlobSetMetricByNameAtSetLevel(bs, wantMetric, result)
		}
	}
	return result
}

// verifyBlobSetMetricByNameAtSetLevel checks wantMetric through BlobSet's own
// ByName accessors (AllNumericsByName/AllTextsByName, spot ValueAt/TimestampAt/
// TagAtByName, MetricLenByName, MaterializeNumericMetricByName/
// MaterializeTextMetricByName) — as opposed to verifyNumericMetric/
// verifyTextMetric above, which only ever look at one already-located member
// blob directly. skipMaterialize mirrors the same OLD-tolerance semantics as
// VerifyNumericBlob/VerifyTextBlob's skip of that name (see
// materializeCollisionSafe): whether it's actually needed here is confirmed
// empirically, the same way it was for the single-blob case.
func verifyBlobSetMetricByNameAtSetLevel(bs blob.BlobSet, wantMetric ManifestMetric, result *VerifyResult) {
	name := wantMetric.MetricName
	label := fmt.Sprintf("blobset-byname(%q)", name)
	want := wantMetric.DataPoints

	// Per-metric, not scenario-wide: a blobset scenario can mix a genuinely
	// colliding metric (needs OLD tolerance) with ordinary, unaffected ones
	// (must stay strict) — see ManifestMetric.IsCrossMemberCollision's doc
	// comment for why this can't be a single flag on the whole scenario.
	skipMaterialize := wantMetric.IsCrossMemberCollision && !materializeCollisionSafe

	switch {
	case bs.IsNumericMetricByName(name):
		if gotLen := bs.MetricLenByName(name); gotLen != len(want) {
			result.addError("%s: MetricLenByName: got %d, want %d", label, gotLen, len(want))
		}

		i := 0
		for idx, dp := range bs.AllNumericsByName(name) {
			if i >= len(want) {
				result.addError("%s: AllNumericsByName yielded more points than expected (index %d)", label, idx)
				break
			}
			w := want[i]
			if dp.Ts != w.Timestamp {
				result.addError("%s[%d]: AllNumericsByName: Timestamp: got %d, want %d", label, i, dp.Ts, w.Timestamp)
			}
			if gotBits := math.Float64bits(dp.Val); gotBits != w.ValueBits {
				result.addError("%s[%d]: AllNumericsByName: Value: got bits %x, want bits %x", label, i, gotBits, w.ValueBits)
			}
			if dp.Tag != w.Tag {
				result.addError("%s[%d]: AllNumericsByName: Tag: got %q, want %q", label, i, dp.Tag, w.Tag)
			}
			i++
		}
		if i < len(want) {
			result.addError("%s: AllNumericsByName yielded only %d points, want %d", label, i, len(want))
		}

		for _, idx := range spotIndices(len(want)) {
			w := want[idx]
			gotTs, tsOK := bs.TimestampAtByName(name, idx)
			gotVal, valOK := bs.NumericValueAtByName(name, idx)
			gotTag, tagOK := bs.TagAtByName(name, idx)
			if !tsOK || !valOK || !tagOK {
				result.addError("%s: TimestampAtByName/NumericValueAtByName/TagAtByName(%d): not found", label, idx)
				continue
			}
			if gotTs != w.Timestamp {
				result.addError("%s: TimestampAtByName(%d): got %d, want %d", label, idx, gotTs, w.Timestamp)
			}
			if gotBits := math.Float64bits(gotVal); gotBits != w.ValueBits {
				result.addError("%s: NumericValueAtByName(%d): got bits %x, want bits %x", label, idx, gotBits, w.ValueBits)
			}
			if gotTag != w.Tag {
				result.addError("%s: TagAtByName(%d): got %q, want %q", label, idx, gotTag, w.Tag)
			}
		}

		if !skipMaterialize {
			single, ok := bs.MaterializeNumericMetricByName(name)
			if !ok {
				result.addError("%s: MaterializeNumericMetricByName: not found", label)
				return
			}
			if single.Len() != len(want) {
				result.addError("%s: MaterializeNumericMetricByName: Len: got %d, want %d", label, single.Len(), len(want))
			}
			for i, w := range want {
				gotTs, tsOK := single.TimestampAt(i)
				gotVal, valOK := single.ValueAt(i)
				if !tsOK || !valOK {
					result.addError("%s: MaterializeNumericMetricByName[%d]: not found", label, i)
					continue
				}
				if gotTs != w.Timestamp {
					result.addError("%s: MaterializeNumericMetricByName[%d]: Timestamp: got %d, want %d", label, i, gotTs, w.Timestamp)
				}
				if gotBits := math.Float64bits(gotVal); gotBits != w.ValueBits {
					result.addError("%s: MaterializeNumericMetricByName[%d]: Value: got bits %x, want bits %x", label, i, gotBits, w.ValueBits)
				}
			}
		}

	case bs.IsTextMetricByName(name):
		if gotLen := bs.MetricLenByName(name); gotLen != len(want) {
			result.addError("%s: MetricLenByName: got %d, want %d", label, gotLen, len(want))
		}

		i := 0
		for idx, dp := range bs.AllTextsByName(name) {
			if i >= len(want) {
				result.addError("%s: AllTextsByName yielded more points than expected (index %d)", label, idx)
				break
			}
			w := want[i]
			wantTextVal, wantTag := decodeTextManifestField(w.Tag)
			if dp.Ts != w.Timestamp {
				result.addError("%s[%d]: AllTextsByName: Timestamp: got %d, want %d", label, i, dp.Ts, w.Timestamp)
			}
			if dp.Val != wantTextVal {
				result.addError("%s[%d]: AllTextsByName: TextValue: got %q, want %q", label, i, dp.Val, wantTextVal)
			}
			if dp.Tag != wantTag {
				result.addError("%s[%d]: AllTextsByName: Tag: got %q, want %q", label, i, dp.Tag, wantTag)
			}
			i++
		}
		if i < len(want) {
			result.addError("%s: AllTextsByName yielded only %d points, want %d", label, i, len(want))
		}

		for _, idx := range spotIndices(len(want)) {
			w := want[idx]
			wantTextVal, wantTag := decodeTextManifestField(w.Tag)
			gotTs, tsOK := bs.TimestampAtByName(name, idx)
			gotVal, valOK := bs.TextValueAtByName(name, idx)
			gotTag, tagOK := bs.TagAtByName(name, idx)
			if !tsOK || !valOK || !tagOK {
				result.addError("%s: TimestampAtByName/TextValueAtByName/TagAtByName(%d): not found", label, idx)
				continue
			}
			if gotTs != w.Timestamp {
				result.addError("%s: TimestampAtByName(%d): got %d, want %d", label, idx, gotTs, w.Timestamp)
			}
			if gotVal != wantTextVal {
				result.addError("%s: TextValueAtByName(%d): got %q, want %q", label, idx, gotVal, wantTextVal)
			}
			if gotTag != wantTag {
				result.addError("%s: TagAtByName(%d): got %q, want %q", label, idx, gotTag, wantTag)
			}
		}

		if !skipMaterialize {
			single, ok := bs.MaterializeTextMetricByName(name)
			if !ok {
				result.addError("%s: MaterializeTextMetricByName: not found", label)
				return
			}
			if single.Len() != len(want) {
				result.addError("%s: MaterializeTextMetricByName: Len: got %d, want %d", label, single.Len(), len(want))
			}
			for i, w := range want {
				wantTextVal, _ := decodeTextManifestField(w.Tag)
				gotTs, tsOK := single.TimestampAt(i)
				gotVal, valOK := single.ValueAt(i)
				if !tsOK || !valOK {
					result.addError("%s: MaterializeTextMetricByName[%d]: not found", label, i)
					continue
				}
				if gotTs != w.Timestamp {
					result.addError("%s: MaterializeTextMetricByName[%d]: Timestamp: got %d, want %d", label, i, gotTs, w.Timestamp)
				}
				if gotVal != wantTextVal {
					result.addError("%s: MaterializeTextMetricByName[%d]: TextValue: got %q, want %q", label, i, gotVal, wantTextVal)
				}
			}
		}

	default:
		result.addError("%s: not found via BlobSet-level ByName resolution (neither numeric nor text)", label)
	}
}

// spotIndices returns {0, mid, last} for a length-n sequence, deduplicated
// and only including in-range indices — mirrors checkIdxNumeric's spot-check
// positions but as a reusable slice for a set-level accessor loop.
func spotIndices(n int) []int {
	if n == 0 {
		return nil
	}
	idx := []int{0}
	if n > 1 {
		idx = append(idx, n/2, n-1)
	}
	out := idx[:0:0] //nolint:staticcheck // build a deduped copy without reusing idx's backing array
	seen := make(map[int]struct{}, len(idx))
	for _, i := range idx {
		if _, dup := seen[i]; !dup {
			seen[i] = struct{}{}
			out = append(out, i)
		}
	}
	return out
}

func metricLabel(id uint64, name string, useMetricID bool) string {
	if useMetricID {
		return fmt.Sprintf("metric(%d)", id)
	}
	return fmt.Sprintf("metric(%q)", name)
}
