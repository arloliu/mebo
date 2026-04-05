---
name: qa-review
description: Perform a critical review focused on correctness, fault tolerance, and performance implications of a Go library from the perspective of external users.
---

# QA Review - Go Library Robustness and Correctness

**Assumed Role:** Quality Assurance (QA) Engineer.

**Testing Premise:** Your testing plan relies on the public API (Godoc) and the README as the primary specifications. You need to ensure the library is robust, reliable, and compliant with its published contract.

When executing this skill, perform a critical review focused on **correctness, fault tolerance, and performance implications**, specifically addressing the following points from the perspective of a user who intends to misuse the library:

## Scope

When reviewing specific packages, specify them by name. Default scope for Mebo:
- Root package (`mebo`): `MetricID` helper, and top-level factory methods.
- `blob/`: `NumericEncoder`, `NumericDecoder`, `BlobSet` types, and encoding options.
- `encoding/`: Columnar encoders/decoders (`Delta`, `Gorilla`, `Chimp`, `DeltaPacked`) and iterators.
- `compress/`: Codec compression (`Zstd`, `S2`, `LZ4`).
- `errs/`: Error sentinels and interfaces.
- `section/`: Binary representations, index structures, headers.

## 1. Functional Correctness and Compliance Testing

1. **Public API Contract Gaps:**
    * Identify any ambiguity where the Godoc describes behavior but provides insufficient detail, such as **encoding tradeoffs, precise iterator semantics, or specific binary format constraints**.
    * Are there **implicit, undocumented limitations** on input values (e.g., limits on dataset size, required timestamp sorting, fixed point counts) that are enforced by the code but not specified in the documentation?

2. **Edge Case Identification & Initialization:**
    * Identify critical **nil-pointer dereference or panic** risks when dealing with raw byte slices, corrupted blobs, or zero-valued optional configurations.
    * Verify that calling behaviors, such as `StartMetricID(id, count)` before adding exact counts of points, are robustly validated without leading to non-idiomatic panics.
    * Check if edge cases with extreme numerical values or very large/small timestamps are handled safely.

## 2. Fault Tolerance and Error Handling

1. **Error Propagation and Inspection:**
    * Analyze the **error propagation strategy**. Does the library consistently use standard error wrapping (`fmt.Errorf` with `%w`) to preserve context?
    * Are proper **sentinel errors** exported (in the `errs` package) for all common failure scenarios (e.g., malformed data, schema mismatch), allowing `errors.Is` functionality?
    * Are there instances of raw string errors used in place of typed or sentinel errors?

2. **Data Integrity and Malformed Inputs:**
    * Is data parsing resilient? Verify that **corrupted blobs, truncated binaries, or malformed bitstreams** result in elegant error returns rather than panics, out-of-bounds reads, or infinite loops.
    * Check if dependencies or specific compression codecs (like CGO `gozstd`) require manual memory management or can leak resources upon partial completion.

## 3. Non-Functional Concerns (Concurrency and Performance)

1. **Concurrency Guarantees:**
    * Verify the **thread-safety contracts** outlined in the documentation. For example, Decoders and BlobSets must be genuinely safe for concurrent reads.
    * Is there any shared mutable state buried inside decoding paths or iterators that could trigger data races?
    * Ensure iterators (e.g., `All()` method) are isolated properly per-goroutine call.

2. **Performance and Memory:**
    * Mebo aims for zero-allocation decoding. Identify any **per-point heap allocations** or hidden `string`/`slice` allocations in tight inner loops (e.g., within `Gorilla`, `Chimp`, or `Delta` encoders/decoders).
    * Evaluate the algorithmic scaling inside bit-level unpacking routines to ensure there are no hot-path performance bottlenecks.
    * Guard against **unbounded memory growth** during encoding due to internal buffering of massive data sets or indices.
