package regression

import (
	"errors"
	"time"

	"github.com/arloliu/mebo/blob"
	"github.com/arloliu/mebo/errs"
	"github.com/arloliu/mebo/format"
)

// measureResult bundles the measured PPM/BPP arrays and the integer PPM list.
type measureResult struct {
	PPM    []float64
	BPP    []float64
	PPMInt []int
}

func chunkAndMeasureWithConfig(blobs []blob.NumericBlob, cfg AnalyzeConfig) (measureResult, error) {
	// This helper unions metrics through a map[uint64] and re-encodes via
	// StartMetricID, which silently collapses a hash collision (two distinct
	// names sharing one ID) into a single series — data loss, since a hash
	// collision can't be represented by an ID-keyed map. Reject collided
	// input here, at the shared seam. Because Analyze/AnalyzeWithOptions pass the
	// whole blob slice while AnalyzeEach* pass one blob at a time, this single
	// guard rejects within-blob collisions on all four entry points and
	// cross-member collisions only on the aggregating (whole-slice) callers.
	if err := checkNoCollision(blobs); err != nil {
		return measureResult{}, err
	}

	// Build set
	set, err := blob.NewNumericBlobSet(blobs)
	if err != nil {
		return measureResult{}, err
	}

	metricIDs := collectMetricIDs(set)
	if len(metricIDs) == 0 {
		return measureResult{}, errors.New("no metrics in input blobs")
	}

	// Materialize counts per metric to compute average points per metric
	totalPoints := 0
	perMetricCounts := make(map[uint64]int, len(metricIDs))
	for _, id := range metricIDs {
		m, ok := set.MaterializeMetric(id)
		if !ok {
			continue
		}
		c := len(m.Timestamps)
		if c == 0 {
			continue
		}
		perMetricCounts[id] = c
		totalPoints += c
	}

	if totalPoints == 0 || len(perMetricCounts) == 0 {
		return measureResult{}, errors.New("no data points available for analysis")
	}

	avgPPM := totalPoints / len(perMetricCounts)
	capPPM := improvedPPMCap(avgPPM, perMetricCounts)
	if capPPM <= 0 {
		return measureResult{}, errors.New("invalid PPM cap computed")
	}

	testPPMs := calculateTestPoints(capPPM)
	if len(testPPMs) == 0 {
		return measureResult{}, errors.New("no test PPMs derived")
	}

	// Pre-materialize all metrics
	materialized := make(map[uint64]blob.MaterializedNumericMetric, len(perMetricCounts))
	for id := range perMetricCounts {
		m, ok := set.MaterializeMetric(id)
		if ok {
			materialized[id] = m
		}
	}

	ppmValues := make([]float64, 0, len(testPPMs))
	bppValues := make([]float64, 0, len(testPPMs))
	ppmsInt := make([]int, 0, len(testPPMs))

	// For each target PPM, create multiple blobs via chunking and compute total encoded size and points
	for _, ppm := range testPPMs {
		if ppm <= 0 {
			continue
		}

		totalEncodedBytes, totalPointsForPPM, encErr := encodeAllChunksForPPM(materialized, ppm, cfg.TimestampEncoding, cfg.ValueEncoding, cfg.TimestampCompression, cfg.ValueCompression)
		if encErr != nil {
			return measureResult{}, encErr
		}
		if totalPointsForPPM == 0 {
			continue
		}

		ppmValues = append(ppmValues, float64(ppm))
		bppValues = append(bppValues, float64(totalEncodedBytes)/float64(totalPointsForPPM))
		ppmsInt = append(ppmsInt, ppm)
	}

	res := measureResult{PPM: ppmValues, BPP: bppValues, PPMInt: ppmsInt}

	return res, nil
}

// checkNoCollision returns errs.ErrCollisionNotSupported when the given blobs
// carry a hash collision this ID-keyed aggregator cannot represent:
//   - a within-blob collision (the same MetricID appears on two entries of one
//     blob — necessarily two distinct names), or
//   - a cross-member collision (the same MetricID bound to DIFFERENT names in
//     different member blobs).
//
// Ordinary repetition of the same metric (same name → same ID) across members
// is the intended cross-window merge and is accepted. Names-free collisions are
// intrinsically indistinguishable and are not detected here.
func checkNoCollision(blobs []blob.NumericBlob) error {
	idToName := make(map[uint64]string)

	for i := range blobs {
		b := &blobs[i]
		ids := b.MetricIDs()
		names := b.MetricNames()
		hasNames := len(names) == len(ids)

		localSeen := make(map[uint64]struct{}, len(ids))
		for j, id := range ids {
			// Within-blob duplicate ID == collision (a valid ID-mode blob cannot
			// repeat an ID; only a name-mode collision produces two entries).
			if _, dup := localSeen[id]; dup {
				return errs.ErrCollisionNotSupported
			}
			localSeen[id] = struct{}{}

			// Cross-member: same ID, different name.
			if hasNames {
				if prev, ok := idToName[id]; ok {
					if prev != names[j] {
						return errs.ErrCollisionNotSupported
					}
				} else {
					idToName[id] = names[j]
				}
			}
		}
	}

	return nil
}

// collectMetricIDs returns the union of metric IDs across all blobs in the set.
func collectMetricIDs(set blob.NumericBlobSet) []uint64 {
	idSet := make(map[uint64]struct{})
	for _, b := range set.Blobs() {
		for _, id := range b.MetricIDs() {
			idSet[id] = struct{}{}
		}
	}
	ids := make([]uint64, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}

	return ids
}

// calculateTestPoints mirrors tests/measure/CalculateTestPoints to choose PPMs.
func calculateTestPoints(maxPoints int) []int {
	standard := []int{1, 2, 5, 10, 20, 50, 100, 150, 200, 500, 1000, 2000, 5000}
	var out []int
	for _, p := range standard {
		if p <= maxPoints {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		if maxPoints > 0 {
			return []int{maxPoints}
		}

		return nil
	}
	if out[len(out)-1] != maxPoints {
		last := out[len(out)-1]
		if maxPoints > last && float64(maxPoints)/float64(last) > 1.2 {
			out = append(out, maxPoints)
		}
	}

	return out
}

// improvedPPMCap returns a conservative PPM cap based on average and p90 of per-metric counts.
// It returns min(average, p90) to avoid many partial chunks while still using representative sizes.
func improvedPPMCap(avg int, counts map[uint64]int) int {
	if len(counts) == 0 {
		return avg
	}

	// Build slice and sort to compute p90
	arr := make([]int, 0, len(counts))
	for _, c := range counts {
		arr = append(arr, c)
	}
	// simple insertion sort (counts size typically modest)
	for i := 1; i < len(arr); i++ {
		v := arr[i]
		j := i - 1
		for j >= 0 && arr[j] > v {
			arr[j+1] = arr[j]
			j--
		}
		arr[j+1] = v
	}

	// p90 index (ceil(0.9*n)-1)
	n := len(arr)
	idx := (9*n + 9) / 10
	if idx <= 0 {
		idx = 1
	}
	if idx > n {
		idx = n
	}
	p90 := arr[idx-1]
	if p90 <= 0 {
		p90 = 1
	}

	if avg <= 0 {
		return p90
	}
	if avg < p90 {
		return avg
	}

	return p90
}

// encodeAllChunksForPPM encodes all metrics into multiple blobs for a given PPM
// and returns the total encoded bytes and total points across those blobs.
func encodeAllChunksForPPM(materialized map[uint64]blob.MaterializedNumericMetric, ppm int, tsEnc, valEnc format.EncodingType, tsComp, valComp format.CompressionType) (totalBytes int, totalPoints int, err error) {
	// Determine max chunks across all metrics for this PPM
	maxChunks := 0
	for _, m := range materialized {
		c := len(m.Timestamps)
		chunks := (c + ppm - 1) / ppm
		if chunks > maxChunks {
			maxChunks = chunks
		}
	}

	for chunkIdx := 0; chunkIdx < maxChunks; chunkIdx++ {
		// Initialize encoder for this synthetic blob
		startTS := time.Now()
		for _, m := range materialized {
			start := chunkIdx * ppm
			if start < len(m.Timestamps) {
				startTS = time.UnixMicro(m.Timestamps[start]).UTC()
				break
			}
		}

		enc, encErr := blob.NewNumericEncoder(startTS,
			blob.WithTimestampEncoding(tsEnc),
			blob.WithValueEncoding(valEnc),
			blob.WithTimestampCompression(tsComp),
			blob.WithValueCompression(valComp),
			blob.WithTagsEnabled(false),
		)
		if encErr != nil {
			return 0, 0, encErr
		}

		pointsInThisBlob := 0
		for id, m := range materialized {
			start := chunkIdx * ppm
			if start >= len(m.Timestamps) {
				continue
			}
			end := min(start+ppm, len(m.Timestamps))

			count := end - start
			if count <= 0 {
				continue
			}
			if err = enc.StartMetricID(id, count); err != nil {
				return 0, 0, err
			}
			if err = enc.AddDataPoints(m.Timestamps[start:end], m.Values[start:end], nil); err != nil {
				return 0, 0, err
			}
			if err = enc.EndMetric(); err != nil {
				return 0, 0, err
			}
			pointsInThisBlob += count
		}

		if pointsInThisBlob == 0 {
			continue
		}

		bytes, finishErr := enc.Finish()
		if finishErr != nil {
			return 0, 0, finishErr
		}
		totalBytes += len(bytes)
		totalPoints += pointsInThisBlob
	}

	return totalBytes, totalPoints, nil
}
