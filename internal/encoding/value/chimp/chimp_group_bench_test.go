package chimp

import (
	"fmt"
	"math"
	"math/rand"
	"slices"
	"testing"
)

// chimpGroupBenchPoints is the number of values in every column of the unsupported= cells.
const chimpGroupBenchPoints = 35

// chimpGroupSweepDst is the destination length of the sweep: the longest column it builds, 35 + 64 values.
const chimpGroupSweepDst = chimpGroupBenchPoints + 64

// chimpGroupBenchColumn is one encoded column in a buffer of its own, followed by the kernel's read-ahead.
type chimpGroupBenchColumn struct {
	payload []byte
	length  int // bytes of the column
	count   int
}

// chimpGroupBenchGroup is eight columns to decode in one call.
type chimpGroupBenchGroup [8]chimpGroupBenchColumn

// chimpGroupSweepTuple is one side of a sweep cell: the three thresholds decodeWith takes.
type chimpGroupSweepTuple struct{ minColumns, minLive, minBits int }

// chimpGroupBenchEncode encodes col into a buffer of its own with read-ahead after it.
func chimpGroupBenchEncode(enc *NumericChimpEncoder, col []float64) chimpGroupBenchColumn {
	enc.WriteSlice(col)
	encoded := enc.Bytes()
	payload := make([]byte, len(encoded)+chimpGroupReadAhead)
	copy(payload, encoded)
	enc.Reset()
	enc.buf.B = enc.buf.B[:0]

	return chimpGroupBenchColumn{payload: payload, length: len(encoded), count: len(col)}
}

// chimpGroupBenchMix returns the monitoring mix as one metric's columns across blobs:
// every group holds eight columns of one kind of the mix, each from its own seed,
// with 30 to 40 values and a count that differs from blob to blob.
// It holds at least chimpMixFreshMin distinct points, more than a branch predictor memorizes.
func chimpGroupBenchMix(b *testing.B) []chimpGroupBenchGroup {
	b.Helper()
	const blobs = 288
	rng := rand.New(rand.NewSource(71))
	enc := NewNumericChimpEncoder()
	defer enc.Finish()

	counts := make([]int, blobs)
	for i := range counts {
		counts[i] = 30 + rng.Intn(11)
	}
	groups := make([]chimpGroupBenchGroup, 0, chimpMixSetColumns*blobs/8)
	points := 0
	// Kinds alternate from one group to the next, so a short run sees all of them.
	for first := 0; first < blobs; first += 8 {
		for slot := range chimpMixSetColumns {
			var group chimpGroupBenchGroup
			for i := range group {
				blob := first + i
				group[i] = chimpGroupBenchEncode(enc, generateChimpMixColumn(slot, counts[blob], int64(blob*chimpMixSetColumns+slot)))
				points += counts[blob]
			}
			groups = append(groups, group)
		}
	}
	if points < chimpMixFreshMin {
		b.Fatalf("the mix holds %d points, want at least %d", points, chimpMixFreshMin)
	}

	return groups
}

// chimpGroupBenchUnsupported returns groups of eight full-precision gauge columns of chimpGroupBenchPoints values,
// in which values after a column's first are replaced by random 64-bit patterns.
// A replaced value is almost always one the kernel does not take: its header and payload exceed the 64-bit window.
// In every lane of a group (allLanes) or in one lane of each group, every such value is replaced with probability share.
// It holds at least chimpMixFreshMin distinct points.
func chimpGroupBenchUnsupported(share float64, allLanes bool) []chimpGroupBenchGroup {
	rng := rand.New(rand.NewSource(73))
	enc := NewNumericChimpEncoder()
	defer enc.Finish()

	groups := make([]chimpGroupBenchGroup, (chimpMixFreshMin+8*chimpGroupBenchPoints-1)/(8*chimpGroupBenchPoints))
	for k := range groups {
		chosen := rng.Intn(8)
		for i := range groups[k] {
			col := generateChimpMixColumn(chimpGroupDenseSlot+i, chimpGroupBenchPoints, int64(k*8+i))
			if allLanes || i == chosen {
				for j := 1; j < len(col); j++ {
					if rng.Float64() < share {
						col[j] = math.Float64frombits(rng.Uint64())
					}
				}
			}
			groups[k][i] = chimpGroupBenchEncode(enc, col)
		}
	}

	return groups
}

// chimpGroupBenchLoad points g at the columns of group.
func chimpGroupBenchLoad(g *ColumnGroup, group *chimpGroupBenchGroup) {
	for i := range group {
		g.Payload[i], g.Off[i], g.Len[i], g.Count[i] = group[i].payload, 0, group[i].length, group[i].count
	}
}

// chimpGroupBenchDecode times ColumnGroup.Decode, one group per operation, cycling through groups.
// It reports the time per column and the share of the columns that entered the kernel.
func chimpGroupBenchDecode(b *testing.B, groups []chimpGroupBenchGroup) {
	b.Helper()
	var g ColumnGroup
	for i := range g.Dst {
		g.Dst[i] = make([]float64, 40)
	}
	next, produced := 0, 0
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		chimpGroupBenchLoad(&g, &groups[next])
		g.Decode(8)
		produced += g.Produced[0]
		if next++; next == len(groups) {
			next = 0
		}
	}

	benchmarkFloatSink = float64(produced)
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/8, "ns/column")
	b.ReportMetric(float64(g.Columns)/float64(b.N)/8, "kernel-columns/column")
}

// chimpGroupBenchCensus decodes every group once and returns three figures,
// each over all values after the first of all columns:
// the rate of values the kernel does not take, the mean number of lanes per group that hold one,
// and the share of values the kernel handed to the scalar loop.
// The first two come from the classifier, the third from the kernel's step count.
// It fails b when the groups hold fewer than chimpMixFreshMin points, when a group stays out of the kernel,
// when a column comes out short,
// or when the kernel stops anywhere but before the first value the classifier says it does not take.
func chimpGroupBenchCensus(b *testing.B, groups []chimpGroupBenchGroup) (untaken, lanes, handedOff float64) {
	b.Helper()
	var g ColumnGroup
	for i := range g.Dst {
		g.Dst[i] = make([]float64, 40)
	}
	points, values, wideValues, wideLanes, scalarValues := 0, 0, 0, 0, 0
	for k := range groups {
		group := &groups[k]
		taken := math.MaxInt
		for i := range group {
			firstWide, wide := group[i].count, 0
			for j, s := range chimpGroupTrace(group[i].payload[:group[i].length], group[i].count) {
				if s.wide {
					firstWide = min(firstWide, j)
					wide++
				}
			}
			points += group[i].count
			values += group[i].count - 1
			wideValues += wide
			if wide > 0 {
				wideLanes++
			}
			taken = min(taken, firstWide-1)
		}

		steps := g.Steps
		chimpGroupBenchLoad(&g, group)
		g.Decode(8)
		steps = g.Steps - steps
		if g.Columns != (k+1)*8 {
			b.Fatalf("group %d: %d of its columns entered the kernel, want 8", k, g.Columns-k*8)
		}
		if steps != taken {
			b.Fatalf("group %d: the kernel completed %d steps, the classifier says %d", k, steps, taken)
		}
		for i := range group {
			if g.Produced[i] != group[i].count {
				b.Fatalf("group %d lane %d: %d values, want %d", k, i, g.Produced[i], group[i].count)
			}
			scalarValues += group[i].count - 1 - steps
		}
	}
	if points < chimpMixFreshMin {
		b.Fatalf("the groups hold %d points, want at least %d", points, chimpMixFreshMin)
	}

	return float64(wideValues) / float64(values), float64(wideLanes) / float64(len(groups)), float64(scalarValues) / float64(values)
}

// BenchmarkChimpGroup measures ColumnGroup.Decode on eight columns in eight buffers, on data that does not repeat.
//
// mix/kernel and mix/decodeall decode the monitoring mix, one metric's columns across blobs,
// with the group decode and with DecodeAll column by column.
// Groups of the mix's sparse kind stay out of the kernel, which kernel-columns/column shows.
//
// unsupported=<share>/<spread> decodes full-precision gauges in which values the kernel does not take are planted:
// in one lane of every group (onelane) or in all eight (alllanes),
// replacing that share of the lanes' values after the first.
// The share only says how a cell was built.
// What a cell holds is in the three figures it reports, all over the values after the first of every column:
// untaken/value, untaken-lanes/group, and handed-off/value, the part the scalar loop decoded after the kernel left.
func BenchmarkChimpGroup(b *testing.B) {
	b.Run("mix", func(b *testing.B) {
		groups := chimpGroupBenchMix(b)
		b.Run("kernel", func(b *testing.B) {
			chimpGroupRequireKernel(b)
			chimpGroupBenchDecode(b, groups)
		})
		b.Run("decodeall", func(b *testing.B) {
			dec := NewNumericChimpDecoder()
			dst := make([]float64, 40)
			next, produced := 0, 0
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				for i := range groups[next] {
					column := &groups[next][i]
					produced += dec.DecodeAll(column.payload[:column.length], column.count, dst)
				}
				if next++; next == len(groups) {
					next = 0
				}
			}

			benchmarkFloatSink = float64(produced)
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/8, "ns/column")
		})
	})

	for _, spread := range []string{"onelane", "alllanes"} {
		for _, share := range []float64{0.001, 0.01, 0.1, 1} {
			b.Run(fmt.Sprintf("unsupported=%g%%/%s", share*100, spread), func(b *testing.B) {
				chimpGroupRequireKernel(b)
				groups := chimpGroupBenchUnsupported(share, spread == "alllanes")
				untaken, lanes, handedOff := chimpGroupBenchCensus(b, groups)
				b.Logf("untaken/value %.5f, untaken-lanes/group %.4f, handed-off/value %.5f", untaken, lanes, handedOff)

				chimpGroupBenchDecode(b, groups)
				b.ReportMetric(untaken, "untaken/value")
				b.ReportMetric(lanes, "untaken-lanes/group")
				b.ReportMetric(handedOff, "handed-off/value")
			})
		}
	}
}

// chimpGroupSweepEntry returns groups of eight two-decimal gauge columns of chimpGroupBenchPoints values,
// in which each value after a column's first changes, by 1 to 30 hundredths, with probability change,
// and holds the previous value otherwise.
// The values stay between 79.50 and 120.50, inside one binade, so the kernel takes every one of them.
// It holds at least chimpMixFreshMin points.
func chimpGroupSweepEntry(change float64) []chimpGroupBenchGroup {
	rng := rand.New(rand.NewSource(79))
	enc := NewNumericChimpEncoder()
	defer enc.Finish()

	groups := make([]chimpGroupBenchGroup, (chimpMixFreshMin+8*chimpGroupBenchPoints-1)/(8*chimpGroupBenchPoints))
	col := make([]float64, chimpGroupBenchPoints)
	for k := range groups {
		for i := range groups[k] {
			cents := 9000 + rng.Intn(2001)
			for j := range col {
				if j > 0 && rng.Float64() < change {
					step := 1 + rng.Intn(30)
					if rng.Intn(2) == 0 {
						step = -step
					}
					cents += step
				}
				col[j] = float64(cents) / 100
			}
			groups[k][i] = chimpGroupBenchEncode(enc, col)
		}
	}

	return groups
}

// chimpGroupSweepCont returns groups of eight full-precision gauge columns, in which every value changes:
// the first 8-k lanes hold chimpGroupBenchPoints values and the last k lanes r more.
// It holds at least chimpMixFreshMin points.
func chimpGroupSweepCont(k, r int) []chimpGroupBenchGroup {
	enc := NewNumericChimpEncoder()
	defer enc.Finish()

	perGroup := 8*chimpGroupBenchPoints + k*r
	groups := make([]chimpGroupBenchGroup, (chimpMixFreshMin+perGroup-1)/perGroup)
	for x := range groups {
		for i := range groups[x] {
			n := chimpGroupBenchPoints
			if i >= 8-k {
				n += r
			}
			groups[x][i] = chimpGroupBenchEncode(enc, generateChimpMixColumn(chimpGroupDenseSlot+i, n, int64(x*8+i)))
		}
	}

	return groups
}

// chimpGroupSweepCheck decodes every group once with the thresholds of t,
// fails b when a column does not decode whole or differs from DecodeAll on the same column,
// and passes check what the group's decode added to Columns, Calls and Steps.
func chimpGroupSweepCheck(b *testing.B, groups []chimpGroupBenchGroup, t chimpGroupSweepTuple, check func(columns, calls, steps int) error) {
	b.Helper()
	var g ColumnGroup
	for i := range g.Dst {
		g.Dst[i] = make([]float64, chimpGroupSweepDst)
	}
	want := make([]float64, chimpGroupSweepDst)
	for k := range groups {
		columns, calls, steps := g.Columns, g.Calls, g.Steps
		chimpGroupBenchLoad(&g, &groups[k])
		g.decodeWith(8, t.minColumns, t.minLive, t.minBits)
		for i := range groups[k] {
			column := &groups[k][i]
			n := NumericChimpDecoder{}.DecodeAll(column.payload[:column.length], column.count, want)
			if n != column.count || g.Produced[i] != n || !chimpGroupSameBits(g.Dst[i][:n], want[:n]) {
				b.Fatalf("group %d lane %d: %d values, DecodeAll %d of %d, or they differ", k, i, g.Produced[i], n, column.count)
			}
		}
		if err := check(g.Columns-columns, g.Calls-calls, g.Steps-steps); err != nil {
			b.Fatalf("group %d: %v", k, err)
		}
	}
}

// chimpGroupSweepTime times decodeWith with the thresholds of t, one group per operation, cycling through groups,
// and reports the time per column.
func chimpGroupSweepTime(b *testing.B, groups []chimpGroupBenchGroup, t chimpGroupSweepTuple) {
	b.Helper()
	var g ColumnGroup
	for i := range g.Dst {
		g.Dst[i] = make([]float64, chimpGroupSweepDst)
	}
	next, produced := 0, 0
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		chimpGroupBenchLoad(&g, &groups[next])
		g.decodeWith(8, t.minColumns, t.minLive, t.minBits)
		produced += g.Produced[0]
		if next++; next == len(groups) {
			next = 0
		}
	}

	benchmarkFloatSink = float64(produced)
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/8, "ns/column")
}

// BenchmarkChimpGroupSweep measures where the kernel of ColumnGroup starts to pay, for setting its three thresholds.
// Every cell has two sides that decode the same groups through decodeWith and differ only in the thresholds.
//
// entry/change=<p>%/lanes=<n> decodes groups of eight two-decimal gauge columns of 35 values,
// in which p% of the values change and the rest hold.
// The first n lanes are eligible for the kernel; the others lack read-ahead and go to DecodeAll in the same call.
// side=kernel lets any eligible lane enter and keeps the kernel running down to one live lane;
// side=scalar lets no group enter. bits/value is the density the bit threshold is compared with,
// over the eligible columns.
//
// cont/k=<k>/r=<r> decodes groups of eight eligible full-precision gauge columns, in which every value changes:
// 8-k lanes hold 35 values and k lanes 35+r.
// Both sides enter the kernel with eight lanes for 34 steps;
// then side=continue keeps the k lanes in the kernel for their last r values,
// and side=handoff hands them to the scalar loop.
func BenchmarkChimpGroupSweep(b *testing.B) {
	chimpGroupRequireKernel(b)

	b.Run("entry", func(b *testing.B) {
		for _, change := range []int{0, 2, 5, 10, 15, 20, 25, 33, 50, 100} {
			b.Run(fmt.Sprintf("change=%d%%", change), func(b *testing.B) {
				all := chimpGroupSweepEntry(float64(change) / 100)
				for lanes := 1; lanes <= 8; lanes++ {
					b.Run(fmt.Sprintf("lanes=%d", lanes), func(b *testing.B) {
						groups := slices.Clone(all)
						var admission GroupAdmission
						for k := range groups {
							for i := lanes; i < 8; i++ {
								groups[k][i].payload = groups[k][i].payload[:groups[k][i].length]
							}
							for i := range groups[k] {
								admission.Add(groups[k][i].payload, 0, groups[k][i].length, groups[k][i].count)
							}
						}
						if admission.columns != lanes*len(groups) {
							b.Fatalf("%d eligible columns, want %d", admission.columns, lanes*len(groups))
						}
						bitsPerValue := float64(admission.bits) / float64(admission.values)

						sides := []struct {
							name  string
							tuple chimpGroupSweepTuple
							check func(columns, calls, steps int) error
						}{
							{"kernel", chimpGroupSweepTuple{1, 1, 0}, func(columns, calls, steps int) error {
								if columns != lanes || calls == 0 || steps == 0 {
									return fmt.Errorf("%d columns entered the kernel for %d calls and %d steps, want %d columns and some of each", columns, calls, steps, lanes)
								}

								return nil
							}},
							{"scalar", chimpGroupSweepTuple{9, 9, 0}, func(columns, calls, steps int) error {
								if columns != 0 || calls != 0 || steps != 0 {
									return fmt.Errorf("%d columns entered the kernel for %d calls and %d steps, want none", columns, calls, steps)
								}

								return nil
							}},
						}
						for _, side := range sides {
							b.Run("side="+side.name, func(b *testing.B) {
								chimpGroupSweepCheck(b, groups, side.tuple, side.check)
								chimpGroupSweepTime(b, groups, side.tuple)
								b.ReportMetric(bitsPerValue, "bits/value")
							})
						}
					})
				}
			})
		}
	})

	b.Run("cont", func(b *testing.B) {
		for k := 1; k <= 7; k++ {
			for _, r := range []int{4, 8, 16, 32, 64} {
				b.Run(fmt.Sprintf("k=%d/r=%d", k, r), func(b *testing.B) {
					groups := chimpGroupSweepCont(k, r)
					sides := []struct {
						name  string
						tuple chimpGroupSweepTuple
						steps int
					}{
						{"continue", chimpGroupSweepTuple{1, 1, 0}, chimpGroupBenchPoints - 1 + r},
						{"handoff", chimpGroupSweepTuple{1, 8, 0}, chimpGroupBenchPoints - 1},
					}
					for _, side := range sides {
						b.Run("side="+side.name, func(b *testing.B) {
							chimpGroupSweepCheck(b, groups, side.tuple, func(columns, _, steps int) error {
								if columns != 8 || steps != side.steps {
									return fmt.Errorf("%d columns entered the kernel for %d steps, want 8 columns and %d steps", columns, steps, side.steps)
								}

								return nil
							})
							chimpGroupSweepTime(b, groups, side.tuple)
						})
					}
				})
			}
		}
	})
}
