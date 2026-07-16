package encoding

// PoC: ALP-v2 per-vector compression ratio measurement.
//
// Background: today's ALP (TypeALP = 0x6) picks ONE (e,f) exponent pair and
// ONE scheme (main/RD/raw) for an entire column. The open design question:
// would splitting each column into independent 1024-value VECTORS, each with
// its own (e,f)/scheme choice and its own header, win enough compression
// ratio on real mixed data to justify a new wire format (encoding type 0x7)?
// The decision rule (2026-07-16): pursue a vectored format only if per-vector
// wins by >= 5% aggregate on real mixed data. This file answers that with a
// number; it does NOT wire anything — no format change, no production code
// touched.
//
// ---- cost model ----
//
// Whole-column ACTUAL: alpEncodeSlice(column, eng) — the exact same production
// NumericALPEncoder path TestNumericALP_Ratio (numeric_alp_test.go) uses, i.e.
// the real encoder's byte count for the column as ONE unit.
//
// Per-vector ESTIMATE: split the column into ceil(n/1024) vectors of at most
// 1024 values, then run alpEncodeSlice on EACH vector slice INDEPENDENTLY and
// sum the byte counts. This is deliberately NOT a hand-derived bit formula:
// calling the real encoder per vector exercises the actual alpBestEF search,
// the actual main/RD/raw selection (encodeColumn), and the actual bit packer,
// once per vector instead of once per column — so "estimate" here means
// "what today's format produces if invoked once per 1024-value vector", and
// every vector's header is the real [scheme][e][f][width][nExc][min] (main) or
// [scheme][rbw][codeBits][nDict][nExc][dict] (RD) or [scheme][raw floats] (raw)
// layout, not an approximation of it. Vectors that would choose RD/raw are
// therefore costed with the exact RD/raw bytes the real encoder produces for
// that vector slice.
//
// Known bias, stated deliberately:
// this reuses today's COLUMN-level field widths unmodified as the per-vector
// header (nExc: 32-bit, exception pos: 32-bit, min: 64-bit) rather than
// right-sizing them for a real ALP-v2 header capped at 1024 values (nExc/pos
// would fit in ~10-11 bits there). This keeps the per-value/per-exception cost
// model identical on both sides (96 bits per exception: 32-bit pos + 64-bit
// value) — but it does mean the per-vector
// ESTIMATE is somewhat PESSIMISTIC relative to a hand-tuned v2 header: if
// per-vector still wins by >=5% under this header cost, a real v2 header would
// likely win by more; if it falls short, a tighter header could still close
// some of the gap and would need a follow-up measurement before concluding
// the question is closed on a knife's edge.
//
// No extra cross-vector framing is added: the number of vectors per column is
// implicit from the column's record count (already known from the index) and a
// fixed 1024 vector size, so a real ALP-v2 column needs no extra byte for vector
// count beyond what's summed here.
//
// ---- datasets ----
//
// tests/measurev2 is a separate Go module (its own go.mod) that imports mebo's
// root module, so internal/encoding cannot import it back (would be a module
// cycle) — its loaders are not importable. Instead this mirrors its realistic
// profile catalog (tests/measurev2/types.go Profiles(), generator.go
// GenerateProfile) locally, VALUE-generation only (timestamps aren't ALP's
// domain). Two of the profile's ValueKinds ("gauge", already covered by this
// package's existing genALPColumns helper in numeric_alp_packbits_test.go, and
// the "counter"/"sparse" kinds added below) reproduce GenerateProfile's exact
// per-point formulas. Note that regular_scrape_60s and bursty_scrape use the
// SAME value-generation formula and decimals as decimal_gauge_2dp in the real
// corpus too — IntervalMs and BurstyGaps only perturb timestamps, never Values.
// Their reported byte counts still differ row-to-row here because each profile
// below is seeded independently (distinct random-walk realizations over 8500
// steps diverge substantially, not because the underlying value distribution
// differs); they are kept as separate rows for 1:1 traceability against the
// corpus's named profiles.
//
// Columns are generated at 8500 points each (8 full 1024-vectors + one 308-value
// partial tail vector) so the comparison actually exercises multiple vectors per
// column, including the partial-tail case a real vectored format would hit at
// every column boundary.
//
// This test only measures and logs; it PASSES regardless of which side wins —
// the >=5% decision is made by a human reading the table, not by this harness.
//
// Run:
//
//	go test ./internal/encoding/ -run 'POCALPv2' -v

import (
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/arloliu/mebo/endian"
)

// alpv2VectorSize is the per-vector split point this PoC evaluates (the
// candidate vector size for a vectored ALP format).
const alpv2VectorSize = 1024

// alpv2Profile names a realistic value-generation shape mirrored from
// tests/measurev2/types.go's Profiles() catalog (decimals/valueKind only —
// IntervalMs/BurstyGaps affect timestamps, which are outside ALP's domain).
type alpv2Profile struct {
	name     string
	decimals int
	kind     string // "gauge" | "counter" | "sparse"
}

// alpv2Profiles mirrors tests/measurev2/types.go Profiles() verbatim (names,
// decimals, value kinds).
func alpv2Profiles() []alpv2Profile {
	return []alpv2Profile{
		{"decimal_gauge_2dp", 2, "gauge"},
		{"decimal_gauge_4dp", 4, "gauge"},
		{"counter", 0, "counter"},
		{"sparse_constant", 2, "sparse"},
		{"regular_scrape_60s", 2, "gauge"},
		{"bursty_scrape", 2, "gauge"},
		{"worst_case", -1, "gauge"},
	}
}

// alpv2GenCounterColumns mirrors tests/measurev2 GenerateProfile's "counter"
// ValueKind: a monotonic integer-valued random walk (step 1..10 per point).
// Values are integer-valued regardless of the profile's decimals field,
// matching production exactly (the counter branch there never calls quantize).
func alpv2GenCounterColumns(nCols, nPts int, seed int64) [][]float64 {
	rng := rand.New(rand.NewSource(seed))
	cols := make([][]float64, nCols)
	for c := range cols {
		col := make([]float64, nPts)
		cur := 100.0 + float64(c)*10.0
		for j := range col {
			step := math.Floor(rng.Float64()*10) + 1
			cur += step
			col[j] = cur
		}
		cols[c] = col
	}

	return cols
}

// alpv2GenSparseColumns mirrors tests/measurev2 GenerateProfile's "sparse"
// ValueKind: long runs of an identical quantized value, punctuated by a rare
// (5% per point) small step.
func alpv2GenSparseColumns(nCols, nPts, decimals int, seed int64) [][]float64 {
	rng := rand.New(rand.NewSource(seed))
	scale := math.Pow(10, float64(decimals))
	cols := make([][]float64, nCols)
	for c := range cols {
		col := make([]float64, nPts)
		cur := 100.0 + float64(c)*10.0
		for j := range col {
			if rng.Float64() < 0.05 {
				cur += (rng.Float64()*2.0 - 1.0) * 1.0
			}
			col[j] = math.Round(cur*scale) / scale
		}
		cols[c] = col
	}

	return cols
}

// alpv2GenColumns generates nCols independent columns of nPts values each for
// profile p: "gauge" reuses this package's existing genALPColumns helper
// (numeric_alp_packbits_test.go), which already reproduces GenerateProfile's
// gauge random walk; "counter"/"sparse" use the two generators above.
func alpv2GenColumns(p alpv2Profile, nCols, nPts int, seed int64) [][]float64 {
	switch p.kind {
	case "counter":
		return alpv2GenCounterColumns(nCols, nPts, seed)
	case "sparse":
		return alpv2GenSparseColumns(nCols, nPts, p.decimals, seed)
	default: // "gauge"
		return genALPColumns(nCols, nPts, p.decimals, seed)
	}
}

// alpv2SchemeCounts tallies how many vectors of a column picked each ALP
// scheme, for the report's "vectors main/rd/raw" column.
type alpv2SchemeCounts struct {
	main, rd, raw int
}

// alpv2EstimateColumnBytes splits values into ceil(n/1024) vectors and runs
// the real production encoder (alpEncodeSlice) independently on each vector
// slice, summing the resulting byte counts — see the file-level comment for
// why this IS the estimate rather than an approximation of it.
func alpv2EstimateColumnBytes(values []float64, eng endian.EndianEngine) (int, alpv2SchemeCounts) {
	var total int
	var sc alpv2SchemeCounts
	for i := 0; i < len(values); i += alpv2VectorSize {
		end := i + alpv2VectorSize
		if end > len(values) {
			end = len(values)
		}
		data := alpEncodeSlice(values[i:end], eng)
		total += len(data)
		switch data[0] {
		case alpSchemeMain:
			sc.main++
		case alpSchemeRD:
			sc.rd++
		case alpSchemeRaw:
			sc.raw++
		default:
		}
	}

	return total, sc
}

// TestPOCALPv2VectorRatio measures whole-column ACTUAL (today's format) vs
// per-vector ESTIMATE (a hypothetical vectored ALP-v2),
// aggregated over the realistic profile catalog mirrored from tests/measurev2.
// See the file-level comment for the cost model and its documented biases.
func TestPOCALPv2VectorRatio(t *testing.T) {
	const (
		numCols  = 10
		numPts   = 8500 // 8 full 1024-vectors + one 308-value partial tail
		seedBase = int64(20260716)
	)
	eng := endian.GetLittleEndianEngine()

	t.Logf("%-20s %5s %6s | %13s %13s | %8s | %s",
		"profile", "cols", "n/col", "actual(B)", "estimate(B)", "delta%", "vectors main/rd/raw")
	t.Logf("%s", strings.Repeat("-", 96))

	var aggActual, aggEstimate int64

	for i, p := range alpv2Profiles() {
		cols := alpv2GenColumns(p, numCols, numPts, seedBase+int64(i)*1009)

		var actual, estimate int64
		var sc alpv2SchemeCounts
		for _, col := range cols {
			actual += int64(len(alpEncodeSlice(col, eng)))
			e, s := alpv2EstimateColumnBytes(col, eng)
			estimate += int64(e)
			sc.main += s.main
			sc.rd += s.rd
			sc.raw += s.raw
		}

		delta := 100 * (float64(estimate) - float64(actual)) / float64(actual)
		t.Logf("%-20s %5d %6d | %12dB %12dB | %+7.2f%% | %d/%d/%d",
			p.name, numCols, numPts, actual, estimate, delta, sc.main, sc.rd, sc.raw)

		aggActual += actual
		aggEstimate += estimate
	}

	aggDelta := 100 * (float64(aggEstimate) - float64(aggActual)) / float64(aggActual)
	t.Logf("%s", strings.Repeat("-", 96))
	t.Logf("AGGREGATE actual=%dB estimate=%dB delta=%+.2f%% (negative = per-vector ESTIMATE smaller = per-vector wins)",
		aggActual, aggEstimate, aggDelta)
	t.Logf("Decision rule: pursue a vectored format only if per-vector wins by >= 5%% aggregate (i.e. delta <= -5.00%%). Observed: %+.2f%%.",
		aggDelta)
}
