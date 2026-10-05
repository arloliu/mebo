package main

import (
	"errors"
	"fmt"
	"math/rand"

	"github.com/arloliu/mebo/blob"
	"github.com/arloliu/mebo/format"
)

// opFixtures holds what one timed operation prepared, and the checksum its body writes on every call.
// The body is the only reference to them, so they are released when the runner returns.
type opFixtures struct {
	blob       []byte
	decoded    blob.NumericBlob
	indices    []int
	checksum   float64
	tsChecksum int64
}

// encodeBlob encodes test data with the given combo and returns raw blob bytes.
// This is the shared encoding logic used by both benchmark and size measurement functions.
func encodeBlob(combo EncodingCombo, data *TestData) ([]byte, error) {
	opts := []blob.NumericEncoderOption{
		blob.WithTimestampEncoding(combo.TSEncoding),
		blob.WithTimestampCompression(format.CompressionNone),
		blob.WithValueEncoding(combo.ValEncoding),
		blob.WithValueCompression(format.CompressionNone),
	}
	if combo.SharedTS {
		opts = append(opts, blob.WithSharedTimestamps())
	}

	encoder, err := blob.NewNumericEncoder(
		data.StartTime,
		opts...,
	)
	if err != nil {
		return nil, err
	}

	numMetrics := len(data.MetricIDs)
	ppm := data.Config.PointsPerMetric

	for i := range numMetrics {
		metricID := data.MetricIDs[i]
		if err = encoder.StartMetricID(metricID, ppm); err != nil {
			return nil, err
		}

		for j := range ppm {
			idx := i*ppm + j
			if err = encoder.AddDataPoint(data.Timestamps[idx], data.Values[idx], ""); err != nil {
				return nil, err
			}
		}

		if err = encoder.EndMetric(); err != nil {
			return nil, err
		}
	}

	return encoder.Finish()
}

// decodeBlob creates a decoder and decodes the blob data.
func decodeBlob(blobData []byte) (blob.NumericBlob, error) {
	decoder, err := blob.NewNumericDecoder(blobData)
	if err != nil {
		return blob.NumericBlob{}, err
	}

	return decoder.Decode()
}

// measureEncodedSize encodes data with the given combo and returns the blob size.
func measureEncodedSize(combo EncodingCombo, data *TestData) (int, error) {
	blobData, err := encodeBlob(combo, data)
	if err != nil {
		return 0, err
	}

	return len(blobData), nil
}

// randomAccessPattern returns one uniformly random point index per metric,
// using a seed derived from the data's own seed so the pattern is reproducible
// across runs but distinct from the data-generation RNG stream.
//
// A single random index per metric (rather than a fixed first/middle/last
// probe) is deliberate: ValueAt/TimestampAt's cost for the sequential-decode
// encodings (Gorilla, Chimp, Delta, DeltaPacked) scales with the index itself,
// so cherry-picking index 0 would understate their real cost and always
// picking the last index would overstate it. A uniformly random index across
// the whole metric reports the realistic average a "random access" workload
// actually sees.
func randomAccessPattern(data *TestData) []int {
	rng := rand.New(rand.NewSource(data.Config.Seed + 1)) //nolint:gosec // seeded PRNG for reproducible benchmark access pattern
	ppm := data.Config.PointsPerMetric
	indices := make([]int, len(data.MetricIDs))
	for i := range indices {
		indices[i] = rng.Intn(ppm)
	}

	return indices
}

// prepareOp builds one operation's fixtures outside any timing and returns the body to time.
// As before the runner interface, encode prepares nothing (its body builds the blob),
// decode pre-encodes the blob, and iterate, ValueAt and TimestampAt pre-encode and pre-decode it.
// The body keeps the checks the benchmarks always had and writes a checksum into the fixtures on every call,
// so tests can confirm it did the complete work without timing it.
func prepareOp(op operation, combo EncodingCombo, data *TestData) (*opFixtures, func() error, error) {
	fx := &opFixtures{}
	if op == opEncode {
		return fx, encodeBody(fx, combo, data), nil
	}

	var err error
	if fx.blob, err = encodeBlob(combo, data); err != nil {
		return nil, nil, fmt.Errorf("encoding %s: %w", combo.Label, err)
	}
	if op == opDecode {
		return fx, decodeBody(fx, data), nil
	}

	if fx.decoded, err = decodeBlob(fx.blob); err != nil {
		return nil, nil, fmt.Errorf("decoding %s: %w", combo.Label, err)
	}

	switch op {
	case opIterSeq:
		return fx, iterBody(fx, data), nil
	case opValueAt:
		fx.indices = randomAccessPattern(data)

		return fx, valueAtBody(fx, data), nil
	case opTimestampAt:
		fx.indices = randomAccessPattern(data)

		return fx, timestampAtBody(fx, data), nil
	case opEncode, opDecode:
		return nil, nil, fmt.Errorf("operation %s already handled", op)
	default:
		return nil, nil, fmt.Errorf("unknown operation %s", op)
	}
}

// encodeBody encodes the whole data set into a new blob.
func encodeBody(fx *opFixtures, combo EncodingCombo, data *TestData) func() error {
	return func() error {
		blobData, err := encodeBlob(combo, data)
		if err != nil {
			return err
		}

		// Prevent compiler from optimizing away
		if len(blobData) == 0 {
			return errors.New("empty blob")
		}
		fx.checksum = float64(len(blobData))

		return nil
	}
}

// decodeBody opens the pre-encoded blob and checks its metric count.
func decodeBody(fx *opFixtures, data *TestData) func() error {
	blobData := fx.blob

	return func() error {
		numericBlob, err := decodeBlob(blobData)
		if err != nil {
			return err
		}

		if numericBlob.MetricCount() != len(data.MetricIDs) {
			return fmt.Errorf("metric count mismatch: got %d, want %d", numericBlob.MetricCount(), len(data.MetricIDs))
		}
		fx.checksum = float64(numericBlob.MetricCount())

		return nil
	}
}

// iterBody iterates every point of every metric of the pre-decoded blob, summing the values.
func iterBody(fx *opFixtures, data *TestData) func() error {
	numericBlob := fx.decoded
	metricIDs := data.MetricIDs

	return func() error {
		totalValue := 0.0
		for _, metricID := range metricIDs {
			for _, dp := range numericBlob.All(metricID) {
				totalValue += dp.Val
			}
		}

		// Prevent compiler from optimizing away
		fx.checksum = totalValue

		return nil
	}
}

// valueAtBody looks up one value per metric at the fixed random indices.
func valueAtBody(fx *opFixtures, data *TestData) func() error {
	numericBlob := fx.decoded
	metricIDs := data.MetricIDs
	indices := fx.indices

	return func() error {
		var total float64
		for i, metricID := range metricIDs {
			val, ok := numericBlob.ValueAt(metricID, indices[i])
			if !ok {
				return fmt.Errorf("ValueAt failed for metric %d index %d", metricID, indices[i])
			}
			total += val
		}

		// Prevent compiler from optimizing away
		fx.checksum = total

		return nil
	}
}

// timestampAtBody looks up one timestamp per metric at the same indices as valueAtBody.
func timestampAtBody(fx *opFixtures, data *TestData) func() error {
	numericBlob := fx.decoded
	metricIDs := data.MetricIDs
	indices := fx.indices

	return func() error {
		var total int64
		for i, metricID := range metricIDs {
			ts, ok := numericBlob.TimestampAt(metricID, indices[i])
			if !ok {
				return fmt.Errorf("TimestampAt failed for metric %d index %d", metricID, indices[i])
			}
			total += ts
		}

		// Prevent compiler from optimizing away
		fx.tsChecksum = total

		return nil
	}
}
