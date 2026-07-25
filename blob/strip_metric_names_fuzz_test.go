package blob

import (
	"bytes"
	"testing"
	"unsafe"

	"github.com/arloliu/mebo/section"
)

// FuzzStripMetricNames drives strip over arbitrary bytes — especially mutated
// header offsets and names length prefixes — asserting the two hard invariants:
// strip never reads outside the declared extents (any out-of-range
// access panics and fails the fuzz), and every outcome is byte-atomic (the
// append form never mutates src; a successful strip is self-consistent).
//
// Beyond the whole-blob seeds below, testdata/fuzz/FuzzStripMetricNames/ carries
// a small structure-aware corpus: pre-mutated blobs hitting IndexOffset
// 32/33, an oversized/undersized names-length prefix, a names count that
// disagrees with MetricCount, an extent crossing into the index, and an
// offset-ordering violation. Go's testing package runs corpus files as regular
// subtests under plain `go test`, so this coverage does not require -fuzz.
func FuzzStripMetricNames(f *testing.F) {
	// Seed with a spread of valid names-bearing blobs and a few degenerate shapes.
	seeds := [][]byte{
		encodeNumericAt(f, []NumericEncoderOption{WithMetricNames()}, []numericMetricSpec{
			{name: "aaa.metric", points: 2, value: 1.0},
			{name: "bbb.metric", points: 1, value: 2.0},
		}),
		encodeNumericAt(f, []NumericEncoderOption{WithBlobLayoutV2(), WithMetricNames()}, []numericMetricSpec{
			{name: "zzz.metric", points: 2, value: 1.0},
			{name: "aaa.metric", points: 1, value: 2.0},
		}),
		encodeTextAt(f, nil, []textMetricSpec{
			{name: "t.one", values: []string{"a", "b"}},
			{name: "t.two", values: []string{"c"}},
		}),
		// A real collision pair (names load-bearing -> strip refuses).
		encodeNumericAt(f, nil, []numericMetricSpec{
			{name: cnA, points: 1, value: 1.0},
			{name: cnB, points: 1, value: 2.0},
		}),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	// Short/degenerate inputs.
	f.Add([]byte{})
	f.Add(make([]byte, section.HeaderSize))

	f.Fuzz(func(t *testing.T, data []byte) {
		// Append form must never mutate src, whatever the outcome.
		before := append([]byte(nil), data...)
		out, stripped, err := StripMetricNames(nil, data)
		if !bytes.Equal(before, data) {
			t.Fatal("StripMetricNames mutated src")
		}

		switch {
		case err != nil:
			if stripped {
				t.Fatal("stripped must be false on error")
			}
			// The error path returns dst unchanged (not unconditionally nil).
			// This target always calls StripMetricNames(nil, data), so dst is
			// nil and out must be nil too — see the dst-atomicity assertions in
			// strip_metric_names_malformed_test.go for the non-nil-dst case.
			if out != nil {
				t.Fatal("out must equal dst (nil here) on error")
			}
		case stripped:
			// A stripped blob must be shorter and carry no names flag; re-stripping
			// it must be a no-op (names already gone) — proving self-consistency.
			if len(out) >= len(data) {
				t.Fatalf("stripped output not shorter: in=%d out=%d", len(data), len(out))
			}
			out2, stripped2, err2 := StripMetricNames(nil, out)
			if err2 != nil {
				t.Fatalf("re-strip of stripped output errored: %v", err2)
			}
			if stripped2 {
				t.Fatal("re-strip removed names twice")
			}
			_ = out2
		default:
			// Refusal (load-bearing names or none present): out mirrors src.
			if !bytes.Equal(out, data) {
				t.Fatal("refused output must equal src")
			}
		}

		// In-place form operates on an independent copy; it must not panic and
		// must agree with the append form's decision.
		buf := append([]byte(nil), data...)
		outIP, strippedIP, errIP := StripMetricNamesInPlace(buf)
		if (errIP != nil) != (err != nil) {
			t.Fatalf("append/in-place error disagreement: append=%v inplace=%v", err, errIP)
		}
		if errIP != nil {
			if strippedIP {
				t.Fatal("strippedIP must be false on error")
			}
			// The error path returns buf unchanged: same backing array, bytes
			// untouched.
			if unsafe.SliceData(outIP) != unsafe.SliceData(buf) {
				t.Fatal("outIP must be the same backing array as buf on error, not a copy")
			}
			if !bytes.Equal(outIP, before) {
				t.Fatal("outIP must equal buf's original content on error")
			}
		}
		if errIP == nil && strippedIP != stripped {
			t.Fatalf("append/in-place stripped disagreement: append=%v inplace=%v", stripped, strippedIP)
		}
		if stripped && errIP == nil {
			if !bytes.Equal(out, outIP) {
				t.Fatal("append and in-place produced different output")
			}
		}
	})
}
