//go:build metricnames

package main

import "github.com/arloliu/mebo/blob"

// This file registers the verifyNumericBorrowedImpl/verifyTextBorrowedImpl
// hooks declared in verify.go, mirroring the mnCorruptImpl indirection in
// mncorrupt_metricnames.go: NewNumericDecoderBorrowed/NewTextDecoderBorrowed
// are new-to-v1.10.0 symbols, so referencing them from an unconditionally
// compiled file would break the v1.9.0 build. This file only compiles under
// the "metricnames" build tag, i.e. only when the module being built against
// already has them.

func init() {
	verifyNumericBorrowedImpl = VerifyNumericBlobBorrowed
	verifyTextBorrowedImpl = VerifyTextBlobBorrowed
	materializeByNameFallbackFixed = true
	materializeCollisionSafe = true
}

// VerifyNumericBlobBorrowed mirrors VerifyNumericBlob but decodes via
// NewNumericDecoderBorrowed — the zero-copy path where the decoded blob's
// metric names alias the input buffer instead of owning independent copies.
// verify.go's default VerifyNumericBlob path only ever exercises the owning
// NewNumericDecoder; this is invoked alongside it (via Manifest.VerifyBorrowed)
// so at least one scenario per blob type actually exercises the borrowed
// constructor end-to-end, including through Materialize() (which must deep
// copy names so the materialized object stays owning even when the source
// blob is borrowed).
func VerifyNumericBlobBorrowed(data []byte, m *Manifest) *VerifyResult {
	result := &VerifyResult{ScenarioID: m.ScenarioID}

	dec, err := blob.NewNumericDecoderBorrowed(data)
	if err != nil {
		result.DecodeErr = err
		result.addError("NewNumericDecoderBorrowed: %v", err)
		return result
	}

	nb, err := dec.Decode()
	if err != nil {
		result.DecodeErr = err
		result.addError("Decode (borrowed): %v", err)
		return result
	}

	if nb.MetricCount() != len(m.Metrics) {
		result.addError("MetricCount: got %d, want %d", nb.MetricCount(), len(m.Metrics))
	}

	mat := nb.Materialize()
	if mat.MetricCount() != len(m.Metrics) {
		result.addError("Materialize: MetricCount: got %d, want %d", mat.MetricCount(), len(m.Metrics))
	}

	for _, wantMetric := range m.Metrics {
		verifyNumericMetric(nb, wantMetric, m.UseMetricID, result)
		verifyNumericMetricMaterialized(nb, mat, wantMetric, m.UseMetricID, result)
	}

	return result
}

// VerifyTextBlobBorrowed is the text-encoder counterpart of
// VerifyNumericBlobBorrowed, using NewTextDecoderBorrowed.
func VerifyTextBlobBorrowed(data []byte, m *Manifest) *VerifyResult {
	result := &VerifyResult{ScenarioID: m.ScenarioID}

	dec, err := blob.NewTextDecoderBorrowed(data)
	if err != nil {
		result.DecodeErr = err
		result.addError("NewTextDecoderBorrowed: %v", err)
		return result
	}

	tb, err := dec.Decode()
	if err != nil {
		result.DecodeErr = err
		result.addError("Decode (borrowed): %v", err)
		return result
	}

	if tb.MetricCount() != len(m.Metrics) {
		result.addError("MetricCount: got %d, want %d", tb.MetricCount(), len(m.Metrics))
	}

	mat := tb.Materialize()
	if mat.MetricCount() != len(m.Metrics) {
		result.addError("Materialize: MetricCount: got %d, want %d", mat.MetricCount(), len(m.Metrics))
	}

	for _, wantMetric := range m.Metrics {
		verifyTextMetric(tb, wantMetric, m.UseMetricID, result)
		verifyTextMetricMaterialized(tb, mat, wantMetric, m.UseMetricID, result)
	}

	return result
}
