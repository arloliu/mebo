package main

import (
	"fmt"
	"log"
	"time"

	"github.com/arloliu/mebo/blob"
	"github.com/arloliu/mebo/internal/hash"
)

func main() {
	// Simulate 3 time windows (e.g., 3 hours of data, 1 blob per hour)
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create blob for hour 1 (00:00-00:59)
	blob1, err := createBlobForHour(startTime, 0, "cpu.usage", "memory.used")
	if err != nil {
		log.Fatalf("Failed to create blob 1: %v", err)
	}
	fmt.Println("Created blob 1 for hour 0")

	// Create blob for hour 2 (01:00-01:59)
	blob2, err := createBlobForHour(startTime, 1, "cpu.usage", "memory.used", "disk.io")
	if err != nil {
		log.Fatalf("Failed to create blob 2: %v", err)
	}
	fmt.Println("Created blob 2 for hour 1")

	// Create blob for hour 3 (02:00-02:59)
	blob3, err := createBlobForHour(startTime, 2, "cpu.usage", "memory.used")
	if err != nil {
		log.Fatalf("Failed to create blob 3: %v", err)
	}
	fmt.Println("Created blob 3 for hour 2")

	// Create BlobSet - blobs will be automatically sorted by start time
	blobSet, err := blob.NewNumericBlobSet([]blob.NumericBlob{blob3, blob1, blob2})
	if err != nil {
		log.Fatalf("Failed to create blob set: %v", err)
	}

	fmt.Printf("\nBlobSet created with %d blobs\n", blobSet.Len())

	// Get time range
	start, end := blobSet.TimeRange()
	fmt.Printf("Time range: %s to %s\n", start.Format("2006-01-02 15:04"), end.Format("2006-01-02 15:04"))

	// Query metric across all blobs - note that disk.io only exists in blob 2
	fmt.Println("\n=== CPU Usage (exists in all blobs) ===")
	queryMetric(blobSet, "cpu.usage")

	fmt.Println("\n=== Memory Used (exists in all blobs) ===")
	queryMetric(blobSet, "memory.used")

	fmt.Println("\n=== Disk IO (only exists in blob 2) ===")
	queryMetric(blobSet, "disk.io")

	// Iterate through individual blobs
	fmt.Println("\n=== Individual Blob Information ===")
	for i := range blobSet.Len() {
		b := blobSet.BlobAt(i)
		if b != nil {
			fmt.Printf("Blob %d: exists\n", i)
		}
	}
}

func createBlobForHour(baseTime time.Time, hourOffset int, metricNames ...string) (blob.NumericBlob, error) {
	startTime := baseTime.Add(time.Duration(hourOffset) * time.Hour)

	// Create encoder
	encoder, err := blob.NewNumericEncoder(startTime)
	if err != nil {
		return blob.NumericBlob{}, fmt.Errorf("failed to create encoder: %w", err)
	}

	// Encode data for each metric (4 data points per hour, one every 15 minutes)
	for i, metricName := range metricNames {
		timestamps := make([]int64, 4)
		values := make([]float64, 4)

		for j := range 4 {
			timestamps[j] = startTime.Add(time.Duration(j) * 15 * time.Minute).UnixMicro()
			// Generate different values for different metrics
			values[j] = float64((i+1)*10 + j + hourOffset*100)
		}

		err = encoder.StartMetricName(metricName, len(timestamps))
		if err != nil {
			return blob.NumericBlob{}, fmt.Errorf("failed to start metric: %w", err)
		}

		err = encoder.AddDataPoints(timestamps, values, nil)
		if err != nil {
			return blob.NumericBlob{}, fmt.Errorf("failed to add data points: %w", err)
		}

		err = encoder.EndMetric()
		if err != nil {
			return blob.NumericBlob{}, fmt.Errorf("failed to end metric: %w", err)
		}
	}

	// Finish encoding
	data, err := encoder.Finish()
	if err != nil {
		return blob.NumericBlob{}, fmt.Errorf("failed to finish encoding: %w", err)
	}

	// Decode to create blob
	decoder, err := blob.NewNumericDecoder(data)
	if err != nil {
		return blob.NumericBlob{}, fmt.Errorf("failed to create decoder: %w", err)
	}

	decodedBlob, err := decoder.Decode()
	if err != nil {
		return blob.NumericBlob{}, fmt.Errorf("failed to decode: %w", err)
	}

	return decodedBlob, nil
}

func queryMetric(blobSet *blob.NumericBlobSet, metricName string) {
	metricID := hash.ID(metricName)
	count := 0
	var firstTimestamp, lastTimestamp int64
	var firstValue, lastValue float64

	for _, dp := range blobSet.All(metricID) {
		if count == 0 {
			firstTimestamp = dp.Ts
			firstValue = dp.Val
		}
		lastTimestamp = dp.Ts
		lastValue = dp.Val
		count++
	}

	if count == 0 {
		fmt.Printf("Metric '%s': No data points found\n", metricName)
		return
	}

	fmt.Printf("Metric '%s': %d data points\n", metricName, count)
	fmt.Printf("  First: %s = %.2f\n", time.UnixMicro(firstTimestamp).Format("15:04:05"), firstValue)
	fmt.Printf("  Last:  %s = %.2f\n", time.UnixMicro(lastTimestamp).Format("15:04:05"), lastValue)
}
