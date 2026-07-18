# Internal Encoding Modularization Implementation Plan

**Goal:** Split `internal/encoding` into codec-owned Go packages while preserving byte output, blob behavior, performance-sensitive iteration, and the retained Simple8b/BP128 experiments.

**Architecture:** `internal/encoding` becomes a compatibility facade for `blob`. Concrete timestamp, value, metadata, and fused-iteration modules own their implementation and white-box tests. Shared implementation details move only to narrow internal packages; child packages never import the facade.

**Tech stack:** Go 1.24+, Go build tags and assembly, `go generate`, `golangci-lint`, existing Go tests and benchmarks.

## Global Constraints

- Preserve every registered `format.EncodingType`, encoded byte layout, decoder behavior, and existing `blob` API.
- Keep Simple8b and BP128 as unregistered timestamp modules; do not add format or blob-selection support for them.
- Keep `blob` importing `github.com/arloliu/mebo/internal/encoding` throughout this migration.
- Preserve no-allocation fused callback paths and current CPU/build-tag dispatch.
- Move each codec with its tests, benchmarks, generated code, Go wrappers, and matching assembly as one unit.
- Retain `package`-internal tests where they exercise unexported codec state.
- Before every commit, run `make lint`; use conventional commits without attribution trailers.

---

### Task 1: Make encoding test targets recursive

**Files:**
- Modify: `Makefile`
- Test: existing encoding and SIMD make targets

**Produces:** All project commands that target encoding packages use `./internal/encoding/...`, so leaf packages are covered after the first move.

- [ ] Update `ENCODING_PKG` and every encoding-specific test target from `./internal/encoding` to `./internal/encoding/...`.
- [ ] Run the current encoding target before and after the change; it must exercise the existing package successfully before the directory split.
- [ ] Run `make lint` and commit `test(encoding): cover codec subpackages`.

### Task 2: Establish shared implementation seams

**Files:**
- Create: `internal/encoding/internal/bitstream/`
- Create: `internal/encoding/internal/varint/`
- Create: `internal/encoding/internal/deltadelta/`
- Modify: `internal/encoding/numeric_gorilla.go`, `internal/encoding/numeric_chimp.go`, `internal/encoding/ts_delta*.go`, `internal/encoding/fused*.go`
- Test: focused malformed-input and SIMD tests currently covering those helpers

**Produces:** `bitstream` supplies Gorilla/Chimp’s current bit-reader semantics; `varint` supplies the current unsigned and zigzag decoding; `deltadelta` supplies Delta and DeltaPacked’s common SIMD/backend primitives. These shared packages are under `internal/encoding/internal/` so both the current root package and future leaf packages may import them.

- [ ] Copy a focused existing malformed-bitstream test into the new bitstream package and run it before implementation; it must fail because the package has no implementation.
- [ ] Move shared helpers without changing their bit order, truncation result, or allocation behavior; change each consumer to import its narrow shared package.
- [ ] Copy the Delta/DeltaPacked backend parity tests before moving the shared backend and prove the same tests pass afterward, including the complementary `amd64`, `!amd64`, and `goexperiment.simd` build tags.
- [ ] Run `go test ./internal/encoding/... -short -timeout=5m`, then `make lint`; commit `refactor(encoding): extract shared codec primitives`.

### Task 3: Move metadata codecs and add the facade

**Files:**
- Create: `internal/encoding/metadata/`
- Create: `internal/encoding/facade.go`
- Move: `metric_names*`, `tag*`, `varstring*`
- Modify: `internal/encoding/doc.go`
- Test: moved metadata tests plus a facade contract test

**Interfaces:**
- Produces facade aliases/forwarders for `TagEncoder`, `TagDecoder`, `VarStringEncoder`, `EncodeMetricNames`, `DecodeMetricNames`, and `VerifyMetricNamesHashes`.

- [ ] Create a metadata-package test for tag round trips and metric-name hash verification; confirm it fails before the implementation is moved.
- [ ] Move metadata implementation and white-box tests together; preserve endian and malformed-input behavior.
- [ ] Add facade type aliases and forwarding functions, then add a parent-package test proving the old constructors/functions still compile and round-trip through the facade.
- [ ] Run `go test ./internal/encoding/... -short -timeout=5m`, `make lint`, and commit `refactor(encoding): isolate metadata codecs`.

### Task 4: Move raw codecs and retained experiments

**Files:**
- Create: `internal/encoding/timestamp/raw/`
- Create: `internal/encoding/value/raw/`
- Create: `internal/encoding/timestamp/simple8b/`
- Create: `internal/encoding/timestamp/bp128/`
- Create: `internal/encoding/research/`
- Move: `ts_raw*`, `numeric_raw*`, `ts_simple8b*`, `ts_bp128*`, `bp128_proto_test.go`
- Move test-only cross-codec experiments: `poc_ts_test.go`
- Modify: `internal/encoding/facade.go`

**Interfaces:**
- Produces facade aliases/forwarders for Raw, Simple8b, and BP128 constructors/types used by internal callers.

- [ ] Move each test with its implementation first so a missing leaf implementation produces the expected build failure, then make the leaf test pass with the moved source.
- [ ] Preserve Raw’s safe/unsafe decoder selection and exact `All`, `DecodeAll`, and `At` contracts.
- [ ] Preserve Simple8b and BP128 tests, benchmarks, build-tagged assembly, and AVX-512 skip behavior; do not register either in `format` or `blob`.
- [ ] Keep cross-codec research in the test-only `research` package, importing leaf packages only from tests so it cannot create a production dependency.
- [ ] Run `go test ./internal/encoding/... -short -timeout=5m`, the retained codec benchmarks, `make lint`, and commit `refactor(encoding): split raw and experimental timestamp codecs`.

### Task 5: Move Gorilla and Chimp value codecs

**Files:**
- Create: `internal/encoding/value/gorilla/`
- Create: `internal/encoding/value/chimp/`
- Move: `numeric_gorilla*`, `numeric_chimp*`
- Move test-only experiments: `poc_block_test.go`
- Modify: `internal/encoding/facade.go`, `internal/encoding/fused_state.go`

**Interfaces:**
- `bitstream` is the only shared binary-reading dependency.
- Each codec exports the narrow incremental decoder state required by the later `fused` package; its constructor, `SetCount`, `Next`, and value accessor preserve current semantics.

```go
type GorillaValState struct{ /* opaque */ }
func NewGorillaValState([]byte) (GorillaValState, bool)
func (*GorillaValState) SetCount(int)
func (*GorillaValState) Next() bool
func (GorillaValState) Val() float64
```

`ChimpValState` has the same methods. These are concrete cursors, not generic
decoder interfaces, so the fused paths retain static dispatch.

- [ ] Add leaf-package tests that exercise malformed payloads and a first/middle/last `At` lookup before moving each codec; verify they initially fail for the absent package.
- [ ] Move Gorilla and Chimp with their benchmarks and convert shared reader calls to `internal/bitstream` without copying the implementation.
- [ ] Add explicit incremental-state seams needed by fused iteration; do not make either child package import the facade.
- [ ] Run `go test ./internal/encoding/value/... -short -timeout=5m`, `make lint`, and commit `refactor(encoding): isolate xor value codecs`.

### Task 6: Move Delta and DeltaPacked timestamp codecs

**Files:**
- Create: `internal/encoding/timestamp/delta/`
- Create: `internal/encoding/timestamp/deltapacked/`
- Move: all `ts_delta*` and `ts_delta_packed*` implementation, test, benchmark, Go, and assembly files
- Modify: `internal/encoding/facade.go`

**Interfaces:**
- Both codecs use `internal/deltadelta`; variable-length decoding uses `internal/varint`.
- Delta exports the narrow timestamp state required by fused iteration.

```go
type DeltaTsState struct{ /* opaque */ }
func NewDeltaTsState([]byte) (DeltaTsState, bool)
func (*DeltaTsState) Next([]byte) bool
func (DeltaTsState) Ts() int64
```

DeltaPacked exposes an analogous private-to-encoding cursor for its fused
callers. `deltadelta` exports only `ChunkSize`, `ShouldUse(count)`, and
`IntoActive(...)`; its backend test controls stay in that package.

- [ ] Move the existing byte-golden, jitter, batch-encoder, and `At` tests with their codec so each leaf package retains white-box coverage.
- [ ] Preserve all architecture fallback pairs exactly: Delta’s `goexperiment.simd && amd64` versus fallback and DeltaPacked’s `amd64` versus `!amd64` implementations.
- [ ] Run `go test ./internal/encoding/timestamp/... -short -timeout=5m` and `GOEXPERIMENT=simd go test ./internal/encoding/... -short -timeout=5m` when the installed Go toolchain supports it.
- [ ] Run `make lint` and commit `refactor(encoding): isolate delta timestamp codecs`.

### Task 7: Move ALP with its generator and generated kernels

**Files:**
- Create: `internal/encoding/value/alp/`
- Move: all `numeric_alp*` Go, assembly, test, benchmark, and generated files
- Move: `internal/encoding/gen/alpkernels/` to `internal/encoding/value/alp/gen/alpkernels/`
- Move test-only cross-codec experiments: `poc_alp*_test.go`
- Modify: `internal/encoding/facade.go`, ALP `go:generate` directive, generator package declaration

**Interfaces:**
- Produces facade aliases/forwarders for `NumericALPEncoder` and `NumericALPDecoder`.

- [ ] Move the ALP golden and cross-version tests with the codec; add a leaf test that imports ALP through the facade to preserve the parent seam.
- [ ] Update generation so `go generate ./internal/encoding/value/alp` emits `package alp` and no timestamps or nondeterministic bytes.
- [ ] Run generation, then `git diff --exit-code -- internal/encoding/value/alp/numeric_alp_kernels_gen.go`; run normal cross-version tests and the explicit verification gate when requested.
- [ ] Run `go test ./internal/encoding/value/alp -timeout=5m`, ALP benchmarks, `make lint`, and commit `refactor(encoding): isolate alp value codec`.

### Task 8: Move fused iteration and complete facade compatibility

**Files:**
- Create: `internal/encoding/fused/`
- Move: `fused.go`, `fused_each.go`, `fused_state.go`, `fused_test.go`, `fused_bench_test.go`
- Modify: `internal/encoding/facade.go`, `internal/encoding/doc.go`, active implementation-path documentation

**Interfaces:**
- The facade forwards every `Fused*`, `RawValuesEach`, `RawTimestampsEach`, and incremental-state name currently used by `blob`.
- Fused imports leaf codec states directly and never imports the facade.

- [ ] Move the callback short-circuit, malformed raw payload, tag alignment, and no-overtaking tests with fused iteration; confirm the leaf package fails before its implementation is present and passes after the move.
- [ ] Rewire each fused path to the explicit Delta/Gorilla/Chimp/metadata state seam while retaining callback order and allocation behavior.
- [ ] Keep `RawValuesEach` and `RawTimestampsEach` with their raw codec modules; add facade forwarding functions from the old package location.
- [ ] Run `go test ./internal/encoding/fused -timeout=5m` and `go test ./internal/encoding/fused -run='^$' -bench='Fused' -benchmem -timeout=5m`.
- [ ] Run `go test ./blob ./internal/encoding/... -timeout=5m`, `make lint`, and commit `refactor(encoding): isolate fused iteration`.

### Task 9: Final compatibility and validation pass

**Files:**
- Modify: active documentation that names current implementation paths
- Modify: `internal/encoding/doc.go`, `Makefile` if any leaf test target remains nonrecursive
- Test: full project and generator verification

- [ ] Search for stale `internal/encoding/<old-file>.go` implementation references and update only active documentation; preserve historical references with a relocation note when they are explanatory.
- [ ] Verify the facade exposes every symbol referenced by `blob` with `rg -n 'ienc\\.' blob` and a clean build.
- [ ] Run `go test ./...`, `make test`, `make lint`, `go generate ./internal/encoding/value/alp`, and confirm generated source has no diff.
- [ ] Compare targeted benchmark allocations with the pre-move baseline and investigate any material regression before committing `refactor(encoding): modularize internal codecs`.
