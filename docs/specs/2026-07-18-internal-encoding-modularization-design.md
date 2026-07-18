# Internal Encoding Modularization Design

## Goal

Split `internal/encoding` into codec-focused Go packages so a maintainer can
locate, change, and test one codec without loading the unrelated timestamp,
numeric, metadata, SIMD, generated, and fused-iteration implementations.

The refactor is behavior preserving: encoded bytes, supported format types,
public `blob` behavior, allocation-sensitive paths, and architecture dispatch
must remain unchanged.

## Scope and Non-Goals

This work reorganizes implementation ownership and tests. It does not add a
format encoding type, change binary layouts, select a different default codec,
or redesign the public `blob` API.

Simple8b and BP128 remain in the tree. They are explicitly retained as
unregistered timestamp modules: neither gains a `format.EncodingType` nor a
`blob` selection path in this refactor.

Research-only ALP and block-compression tests are not deleted in this phase.
Their retention or archival is a separate, evidence-based cleanup decision.

## Target Layout

```text
internal/encoding/
  facade.go
  timestamp/
    raw/
    delta/
    deltapacked/
    simple8b/
    bp128/
  value/
    raw/
    gorilla/
    chimp/
    alp/
  metadata/
  fused/
  internal/
    bitstream/
    deltadelta/
    varint/
```

Each leaf codec package owns its encoder, decoder, codec-local helpers,
platform-specific Go files, assembly, unit tests, and benchmarks. For example,
the ALP generator and generated kernels move together into `value/alp`; the
generator continues to emit deterministic source beside the codec.

`metadata` groups the small, closely related metric-name, tag, and varstring
encodings. `fused` owns only the cross-codec sequential iteration fast paths.
`internal/bitstream` owns the Gorilla/Chimp bit-reader seam that is currently
shared implicitly through the single parent package.

Simple8b and BP128 move to `timestamp/simple8b` and `timestamp/bp128` with
their current tests. They remain isolated from registered runtime codec
selection, which makes their experimental status visible without deleting
their implementation.

## Compatibility Facade

`internal/encoding/facade.go` remains the only package imported by `blob` in
the first migration. It exposes the existing constructor, type, metric-name,
and fused-iteration names through type aliases and thin forwarding functions.

The facade is deliberately shallow and temporary in responsibility: it owns no
codec state, wire parsing, or performance-sensitive loop. Its interface keeps
the caller seam stable while implementation locality moves into the packages
above. Child packages never import the facade, preventing import cycles.

Keeping this seam gives the refactor two useful properties:

- `blob` remains focused on blob orchestration instead of importing every
  concrete codec package.
- Codec moves can land incrementally while callers, binary format, and
  benchmark labels stay stable.

## Module Rules

Each codec module has one interface: construct an encoder/decoder and perform
the existing `Write`, `WriteSlice`, `Bytes`, `Len`, `Size`, `Reset`, `Finish`,
`All`, `DecodeAll`, and `At` behavior appropriate to that codec. Existing
interface conformance assertions remain beside their concrete type.

Package-internal code may be decomposed for platform dispatch and generated
kernels, but the leaf package must retain a single codec responsibility. Keep
ordinary implementation, test, and benchmark files consolidated by codec; use
only the necessary build-tag or generated-source exceptions. Do not create a
generic codec registry or new runtime abstraction: there is one concrete
implementation per current format type, so an extra seam would be shallow.

For a codec with platform-selected CPU dispatch, a shared Go dispatch source
and its inseparable generated, assembly, or build-tagged implementation pairs
may exceed the normal three-file limit only when they implement that codec's CPU
fallback. Ordinary implementation, test, and benchmark files still follow the
limit.

The `fused` module is a distinct module because its interface is the
cross-column iteration operation, not a codec. It may depend on the timestamp,
value, and metadata modules but must not make those modules depend on it.

## Migration Order

1. Introduce the leaf packages and `internal/bitstream`; move one codec family
   at a time without changing its byte representation.
2. Add the compatibility facade and switch its existing symbols to aliases or
   forwarders as each move completes. Keep `blob` imports unchanged.
3. Move fused iteration after its source codec state is available through
   explicit child-package seams. Preserve its no-allocation callback paths.
4. Move the ALP generator with its generated output, update the generation
   directive, and prove regeneration is byte-for-byte identical in source.
5. Consolidate codec-local tests and benchmarks with their module; retain
   platform/build-tag coverage with the implementation it exercises.
6. Update package documentation to describe the layout and the retained,
   unregistered Simple8b/BP128 modules.

## Verification

Every move must preserve existing golden bytes, cross-version ALP decoding,
malformed-input behavior, `At` behavior, callback iteration semantics, and
architecture fallback behavior. The final validation includes targeted codec
tests, `go test ./...`, `make lint`, generator reproducibility, and the
existing benchmark suites needed to guard allocation-sensitive fused and SIMD
paths.

No benchmark result is accepted as an improvement merely because files moved;
the refactor must be performance-neutral within normal benchmark variation.
