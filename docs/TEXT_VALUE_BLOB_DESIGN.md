# Text Value Blob Design

## Core Concept
The text value blob is different than NUMERIC storage, which using columnar storage. It has the following concepts:

**Data Point**
- Timestamp (int64): the time stamp usually represent in unix microseconds, but it's caller's decision.
- Value (string): the value represents in UTF-8 string.
- Tag (string): the tag of data point.

**Header**
Similar to NUMERIC blob, it needs to record:
- `Flags`: magic number, compression algorithm...etc.
- `MetricCount` (uint16): The number of unique metrics stored in the blob, max to 65535.
- `StartTime` (int64): The earliest timestamps in the blob, unix timestamp in microseconds, allowing for fast sorting of multiple blobs.

**Metric Index**
Record the offset of each metric data point sequencem, provide O(1) metric lookup capability. The metric ID is the same as NUMERIC blob.

## Key Requirements
- Compact and small encoded size
- Memory efficiency
- Direct memory access
- Fast look up, O(1)
- Fast encoding/decoding - one pass is prefered
- Fast and in-memory iteration
- The random access is less important than iteraion.
- Provide metric collision tracking like numeric blob in both encoder/decoder/blob.
- Similar to numeric blob, the tag is optional
-

# Answers for quesitons
1. String Pool Complexity: No, it's too complicated, don't use it.
2. Index Entry Size: keep it compact if no string pool needs
3. Compression Granularity: compress data points.
4. Tag Storage: tag per point, try to save space if tag is empty (should be 1 byte i guess if using varint)
5. Encoding Consistency: text-specific encoding types is good, give the recommendataion for me.
6. One-Pass Encoding: accept trade-off for space savings.

**Data Blocks with a Footer Index might be a idea.**

The entire byte slice or file is organized into three distinct sections:

Data Section: Contains the tightly packed, serialized data points for all metrics, one after another.

Index Table: A directory that maps each metric id to the position and size of its data in the Data Section.

Footer: A small, fixed-size block at the very end that points to the start of the Index Table and contains a "magic number" to identify the format.