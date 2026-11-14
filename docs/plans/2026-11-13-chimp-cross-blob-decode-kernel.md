# Chimp Cross-Blob Decode Kernel Implementation Plan

**Status:** draft, written 2026-11-13 and revised four times the same day, after each of four external reviews.
The first found one P0 and twelve P1, and the second one P0, four P1 and two P2.
The third found no P0, one P1 and two P2, all corrections of local text, and said the design stands.
Those three verdicts were fix-then-ready.
The fourth, a precision pass, found no P0, one P1 and one P2, both wording in Task 1's tests;
its verdict was fix-then-go with no further architectural pass, and both replacements are applied.
The answers are in `tmp/2026-11-13-chimp-cross-blob-decode-kernel_v1_review_response.md`,
and in its `_v2_` and `_v3_` siblings.
The fourth report is `tmp/2026-11-13-chimp-cross-blob-decode-kernel_v4_review.md`.
The owner approved the plan for implementation on 2026-11-13.
Progress is recorded under "Results".
The owner's decisions of 2026-11-13 are recorded under "Decisions".

**Goal:** Make `BlobSet.MaterializeNumericMetricByName` and the three entry points that share its code path cheaper
when the set's members hold Chimp values,
by decoding the metric's value columns from up to eight blobs per call of an AVX-512 kernel.
The wire format, the public API and the result of every call stay as they are.

**Non-goals.**
None of these has its implementation or its dispatch changed:

- `NumericBlob.Materialize`, `NumericBlob.MaterializeMetric` and `NumericBlob.MaterializeMetricByName`
  (one blob, many columns of one payload);
- `NumericBlobSet.Materialize` and `BlobSet.MaterializeNumeric`
  (every metric of a set, decoded by a loop of its own in the same file);
- the `NumericMetric` handle, its `Materialize` included;
- the per-blob `ForEach*` and `All*` paths, `ValueAt`, and every other caller of `NumericChimpDecoder.DecodeAll`;
- the encoder;
- Gorilla, ALP, ALP-RLE and Raw values, which keep today's decode inside a set too.

Their speed is not measured by this plan, except for the `DecodeAll` control of "Measurement procedure".
An unchanged function can still move in the binary and change its timing; this plan does not rule that out for them.

**Baseline:** `main` at `280e06e` (`v1.14.1` plus four commits), Go 1.26.7, amd64.
`main` moved from `2400717` to `280e06e` on 2026-11-13, while this plan was written:
it gained the mixed-encoding set tests (`TestBlobSet_MixedEncodings`),
`BenchmarkChimpMix`, the windowed Chimp `At` and its changelog entry.
None of the four commits touches `NumericChimpDecoder.DecodeAll`, `blob/numeric_blob_set_material.go`,
`blob/numeric_blob.go`, `blob/blob_set.go` or `internal/encoding/facade.go`,
so the prototype's base (`7915921`) and the baseline agree on every line this plan changes.
Every reference implementation below is the baseline, and so is every "today".

## Background

The research is in `tmp/chimp-perf-research-2026-11-13/` (not tracked):
`findings.md`, the prototype under `blob-prototypes/decode/` (`NOTES.md`, `changes.patch`, the sources as `.txt`),
and the external review `codex-review-blob-prototypes-v1.md` (verdict for the decode prototype: sound as a prototype).
The patch applies on top of the eight scratch files of `scratch/`; `NOTES.md` says how to rebuild the tree.
A built copy was kept in a session's scratch directory;
its `ChimpLanes` tests passed there on 2026-11-13, but that directory may be gone.

| Finding | Number | Kind of evidence |
|---|---|---|
| Chimp's scalar decode is bound by flag mispredictions on data that does not repeat | 0.33 mispredictions per point, `DecodeAll` 401 → 690 ns per 150-point column | codec level, one binary, hardware counters |
| A benchmark that loops over a small data set hides that cost | the baseline reads about 1.7 times too fast below 30,000 distinct points | codec level, one binary |
| Eight columns per kernel call, one payload | 129–166 → 54.5 ns per 30-point column | codec level, one binary |
| Eight columns per kernel call, one metric across blobs | 265.8 → 170.2 µs per metric over 1,000 blobs of 30–40 points, 81% of the columns in the kernel | blob level, one binary, n=6, synthetic data |
| Near-constant columns are slower in the kernel | 137 → 256 ns per 150-point column | codec level, one binary |

All of these are single-binary measurements on one Zen 5 machine with synthetic data.
They say where to look and are not acceptance results.

The consumer (fdc) reads one metric across many blobs through `MaterializeNumericMetricByName`,
often more than a thousand blobs at a time.
Its blobs are little endian, V2 layout, shared DeltaPacked timestamps, no compression, tags rare,
30 to 40 points per metric per blob at the median, with counts that differ between blobs.
Its CPUs have AVX-512.
It is on mebo v1.14.0 and writes ALP-RLE today.
It may adopt Chimp, and that choice will be made later from real measurements.
For as long as it holds both kinds of blobs, its sets mix Chimp and ALP-RLE members,
so the kernel has to stay on in a mixed set.
It does not use `NumericBlob.Materialize` or the `NumericMetric` handle.

**Tech stack:** Go and Go assembly (AVX-512 F, BW, DQ);
the packages `blob`, `internal/encoding/value/chimp` and `internal/arch`;
`make test`, `make lint`, `make fuzz`, `scripts/check-encoder-hotpath.sh`.

## Today's code path

`BlobSet.MaterializeNumericMetricByName` and `BlobSet.MaterializeNumericMetric` (`blob/blob_set.go`)
build a `NumericBlobSet` over the set's numeric blobs and call its `MaterializeMetricByName` or `MaterializeMetric`
(`blob/numeric_blob_set_material.go`).
Each of those builds a `resolve` closure and calls `NumericBlobSet.materializeMetricCore`, which runs four steps:

1. sum `entry.Count` over the members that resolve, and return `false` when the sum is 0;
2. find out whether any member has tags;
3. allocate `timestamps`, `values` and, with tags, `tags`, each with that sum as capacity;
4. for every member in order: resolve again, append its timestamps
   (a copy of the shared group from `blob.sharedTs.lookup`, or `decodeTimestampsSlice`),
   decode its values into the tail with `NumericBlob.decodeValuesSlice`,
   append its tags (`decodeTagsInto`, or `""` padding),
   and trim the member's rows with `alignMemberRows`.

For a Chimp member `decodeValuesSlice` calls `NumericChimpDecoder.DecodeAll`, one column per call.
`DecodeAll` is in `internal/encoding/value/chimp/chimp.go`.
`TextBlobSet` has a method of the same name, `materializeMetricCore`, for text;
this plan is about the numeric one only.

One decoder reads its destination before it has written all of it.
`decodeRunsInto` (`internal/encoding/value/alp/alp_runs.go`) ignores how many values `decodePlainInto` wrote,
and then expands the runs from the destination.
On an ALP-RLE column whose nested column is empty, it returns the destination's previous contents as values.
The check at open rejects such a column on the wire; only a test that edits a decoded blob reaches it.
So what the destination holds before a member is decoded is part of the result,
and the design below keeps it equal to the baseline's.

## Global Constraints

- **Same result, without exception.**
  No encoded byte and no public signature changes.
  For every set, all four entry points return what the baseline returns:
  the same `ok`, metric ID, timestamps, tags, value bits, lengths, capacities and nil-ness,
  and the same contents in the unused capacity of the three slices.
  A set on which the baseline panics panics with the same panic value.
  This holds for three classes of input, each with its own tests:
  1. sets of blobs `Decode` accepts, well-formed or not (flipped value, timestamp and tag bytes included);
  2. bytes `Decode` rejects: `Decode` is not touched, so no such blob reaches a set;
  3. in-memory states a test crafts on a decoded blob:
     index entries with a raised or lowered count, a column cut short, a timestamp or tag range cut short or moved,
     an ALP-RLE column cut at its nested column.
  The first version of this plan proposed two accepted differences; "Exactness" below removes both.
- **A removed check is not a saving.**
  Every validation the baseline runs still runs, and `Decode` and `DecodeBlobSet` are not edited.
- **A frozen reference per task.**
  Task 0 copies the baseline `materializeMetricCore` into the test file.
  `NumericChimpDecoder.DecodeAll` is not edited by any task, so it is its own frozen reference at codec level.
  Agreement between two changed paths proves nothing; every comparison has the reference on one side.
- **Other machines keep today's path.**
  Without AVX-512 F, BW and DQ, and on every architecture but amd64,
  `materializeMetricCore` runs the baseline's step-4 loop, textually unchanged.
  Such a machine gains two tests that are always false there:
  one per member in step 1, and one before step 4 that picks the loop.
  The rows timed with the gate off measure them on this machine only.
- **No `unsafe` and no `uintptr`** in the non-test Go code this plan adds.
- **Measurement.**
  A single-binary A/B is unreliable below 30% in this repository,
  so an acceptance time comes only from a run of "Measurement procedure" below.
  The orchestrator does it, not the implementing agent,
  which runs smoke benchmarks only and never reports them as results.
  Allocation counts and bytes per operation are exact and may be quoted from one binary.
- **Benchmarks use data that does not repeat.**
  Every timed row walks at least 960,000 distinct points before it sees a point again,
  the size `BenchmarkChimpMix` uses for its "fresh" rows.
  A row with Chimp members walks that many Chimp points.
  Each row asserts its own count during setup.
- **Synthetic data decides.**
  Thresholds and task decisions come from the synthetic fixtures on this machine (owner, 2026-11-13).
  No task waits for real consumer blobs or for a run on a consumer machine.
- **How tasks run.**
  One fresh agent per task, one after the other, in the main working tree, with no git worktrees.
  All of them work on one branch, `perf/chimp-cross-blob-decode`.
  Each task below is the agent's brief: its files, steps, reference, mutation checks, gates and commit.
  For each mutation the agent seeds the defect, runs the named tests, reports the failing output, and restores the file.
  A mutation that survives is reported as such, with the reason if it is equivalent.
  **An agent that cannot do a task as written stops and reports; it does not improvise.**
  A deviation the orchestrator allows is written into this plan in the same step.
  Each task gets an external Codex review, by commit, before it is reported done.
- **Gates.**
  `GOLANGCI_LINT_CACHE=$TMPDIR/golangci-cache make lint`, `make test`, and `make fuzz` for a task that touches decoding.
  `go test ./blob` alone is not the gate.
  Also `go vet ./...` and `GOARCH=arm64 go vet ./blob/ ./internal/...` for a task that touches assembly or build tags.
  Also `scripts/check-encoder-hotpath.sh` for every task that changes a file in `internal/encoding/value/chimp`:
  the repository rule asks for it after any change to that package, and it is not part of `make test`.
- **Repository conventions.**
  Go files never go under `tmp/`; scratch Go lives in `$TMPDIR` or carries a `.txt` suffix.
  Every exported symbol has godoc.
  No research label or task code appears in code, comments, test names or commit messages:
  no `proto`, `zz_`, `lanes8`, "task 3", no path under `tmp/`.
  Commits follow the conventional format, with a subject of at most 70 characters, one scope per commit,
  without attribution trailers, and are squashed by scope before the merge.
  Documents are written one sentence per line.

## Measurement procedure

This section is frozen before any timing is taken.
A change to it is made in this plan first, with the reason, and never after a result has been seen.

**Revisions.**
`B0` is the commit of Task 0: the baseline's production code with the benchmark and the reference.
Task 0 ended with four commits, the last one a fix of two fixtures after its review.
`B0` is that last commit, `6b7cfc6`.
"Head" is the branch head after a task's commit.
"Last accepted" is the head before a task started; a dropped task leaves no commit, so it never becomes one.
Rows of the blob benchmark are compared with `B0` unless a gate names another revision.

**Builds.**
Each side is staged with `git archive <revision>` into a directory under `$TMPDIR`, outside the repository.
The build contract is the one `tests/measurev2/layouts.py` enforces (`BUILD_CMD`, `check_build_settings`):
`env GOFLAGS= CGO_ENABLED=0 go test -c -trimpath -pgo=off`, Go 1.26.7, checked with `go version -m` on every binary.
Two test binaries are built per layout and side: `./blob/` and `./internal/encoding/value/chimp/`.
For `B0` builds of the blob binary, later copies of the benchmark file are never laid over the tree:
the timed loop of a row is identical in every revision, and only setup checks and reported metrics are added later.

**Layouts.**
Four layouts per side, with the padding of `layouts.py` (`pad_source`):
a file that holds a `//go:noinline` function of K mixing steps and a package variable initialised from it,
which keeps the function in the binary.
K is 0, 1, 2 and 4, as in `LAYOUTS` there.
The file goes into the package of the lowest-addressed repository symbol of the layout-0 binary (`lowest_repo_package`),
found once per binary and side.
The orchestrator's driver imports those functions from `layouts.py`; it does not reimplement them.
`layouts.py` itself drives only the `measurev2` binary and is not edited.

**Movement check.**
`go tool nm -n -size` of the four binaries of a side must show, for every symbol in the list below,
one size and at least two different addresses modulo 64.
A listed symbol that is missing because the compiler inlined it is replaced by its caller, and the report says so.

| Binary | Revisions | Symbols |
|---|---|---|
| blob | every revision | `blob.(*NumericBlobSet).materializeMetricCore`, `chimp.NumericChimpDecoder.DecodeAll` |
| blob | from Task 2 | also `blob.(*NumericBlobSet).materializeMembersGrouped`, the `chimp.(*ColumnGroup)` method that holds the rounds, `chimp.decodeAllFrom`, `chimp.chimpGroupDecodeAVX512Asm.abi0` |
| chimp | every revision | `chimp.NumericChimpDecoder.DecodeAll` |
| chimp | from Task 1 | also the `chimp.(*ColumnGroup)` method that holds the rounds, `chimp.decodeAllFrom`, `chimp.chimpGroupDecodeAVX512Asm.abi0` |

An assembly function appears in the symbol table with the suffix `.abi0`, as the ALP kernels do today.

**Runs.**
Every invocation is
`taskset -c 6 env -u GOMEMLIMIT GOMAXPROCS=1 GOGC=100 GODEBUG= <binary> -test.run '^$' -test.bench <row> -test.benchtime 2s -test.count 1`.
The rows timed with the gate off use `GODEBUG=cpu.avx512dq=off` on both sides.
Task 3 adds one variable to that environment, on each side its own value; it is the only other exception.
There are three rounds.
In each round the eight binaries of a comparison run once, sides alternating, layouts in the order 0, 1, 2, 4;
the second round starts with the other side.
That gives three observations per layout and side, kept as such, and twelve per side when merged.
Nothing else runs on the machine:
the run is started as the background command, and only file edits happen until it ends.

**Row presence.**
A test binary exits with success when its `-test.bench` pattern matches nothing.
So every comparison has a list of the benchmark names it expects, written down before the run.
The driver reads the output of every invocation and fails the run unless each expected name is there with a time,
in every binary of both sides.
A comparison between two revisions uses only rows that exist, with the same timed loop, in both.

**Reading a row.**
For each layout, the layout median is the median of its three observations.
- "Improves by X merged" is the `benchstat` change of the twelve merged observations per side.
- "Improves by X in every layout" is the smallest improvement among the four ratios of layout medians.
- "Limit X" passes when no layout's median on the new side is more than X above the same layout's median on the old side.
- "Within X merged" bounds the merged change in both directions.

**Placement modes, decided before any data.**
A row is placement-sensitive when, on either side, its four layout medians spread by more than 8% (largest over smallest).
For such a row, and only for a "limit" requirement, the limit is read on two ratios instead of four:
the smallest layout median of the new side over the smallest of the old side, and the largest over the largest.
Both must be inside the limit.
No other regrouping of layouts is allowed, and improvement requirements are never read this way.
The report prints all eight layout medians of every row, so the reading can be checked.

**Control and invalid runs.**
The control is `BenchmarkChimpMix`: the `DecodeAll` cells of `fresh/n30` and `fresh/n150`,
from the chimp binaries of the same builds.
Its code is the same on both sides.
A run is valid when the control's merged change is within 3% in both cells.
An invalid run is discarded whole and repeated once from fresh builds with the same K.
If the repeat is invalid too, the control's movement is reported to the owner as a placement effect on `DecodeAll`,
and no acceptance is read from either run.
A row that fails a requirement in a valid run may be measured once more in a new valid run;
it passes only if the new run passes, and both runs are reported.

## Design

### Names and files

The prototype's names are research names.
Production uses these:

| Prototype | Production | Where |
|---|---|---|
| `zz_chimp_lanes_amd64.s`, `chimpDecodeLanesAVX512Asm` | `chimp_decsimd_amd64.s`, `chimpGroupDecodeAVX512Asm` | `internal/encoding/value/chimp/` |
| `zz_chimp_lanes_amd64.go`, `lanesKernel` | `chimp_decsimd_amd64.go`, `chimpGroupKernel` | same |
| `zz_chimp_lanes_other.go` | `chimp_decsimd_noasm.go` (`//go:build !amd64`) | same |
| `zz_chimp_lanes.go`: `Lanes8`, `Lanes8.Decode`, `lanesState` | `chimp_group.go`: `ColumnGroup`, `ColumnGroup.Decode`, `chimpGroupState` | same |
| `LanesOK` (exported variable) | `chimpGroupAVX512` (unexported variable) and `GroupDecodeEnabled()` | `chimp_group.go` |
| `protoResume` (in the scratch file `zz_chimp_proto.go`) | `decodeAllFrom` | `chimp_group.go` |
| `lanesMinLive`, used for entry and for continuing | `GroupMinColumns` for entry, exported because `blob` reads it; `chimpGroupMinLive` for continuing | `chimp_group.go` |
| `lanesSparseBits`, `lanesSlack`, `lanesRows` | `chimpGroupMinBitsPerValue`, `chimpGroupReadAhead`, `chimpGroupRows` | `chimp_group.go` |
| `ienc.ChimpLanes8`, `ienc.ChimpLanesOK()` | `ienc.ChimpColumnGroup`, `ienc.ChimpGroupDecodeEnabled()`, `ienc.ChimpGroupMinColumns` | `internal/encoding/facade.go` |
| `cpu.X86` read in the chimp package | `arch.X86HasAVX512BW()` beside the existing `arch.X86HasAVX512DQ()` | `internal/arch/x86_amd64.go`, `x86_stub.go` |
| `blob/zz_chimp_lanes.go`: `materializeMembersInLanes` | `materializeMembersGrouped` | `blob/numeric_blob_set_material.go` |
| `blob.chimpLanes`, `blob.chimpLanesTrace` | `blob.chimpGroupDecode`, `blob.chimpGroupObserve` | `blob/numeric_blob_set_material.go` |
| `blob/zz_chimp_lanes_test.go` | tests and fuzz target in `numeric_blob_set_material_test.go`, benchmark in `numeric_blob_set_material_bench_test.go` | `blob/` |

"Lane" stays in comments, as the ALP assembly uses it: one of the eight 64-bit elements of a vector.

**The three-file rule.**
The ALP package already sets two precedents, and this plan follows both and adds nothing new:

- a sub-component with its own three files: `alp_runs.go`, `alp_runs_test.go`, `alp_runs_bench_test.go`.
  Here: `chimp_group.go`, `chimp_group_test.go`, `chimp_group_bench_test.go`.
  The group decode does not go into `chimp.go`, which is already 1,007 lines.
- a kernel as a set of architecture files: `alp_decsimd_amd64.go`, `alp_decsimd_amd64.s`, `alp_decsimd_noasm.go`,
  with the tests that need amd64 and a Unix `mmap` in `alp_decsimd_amd64_test.go` (`//go:build linux || darwin`).
  Here: `chimp_decsimd_amd64.go`, `chimp_decsimd_amd64.s`, `chimp_decsimd_noasm.go`, `chimp_decsimd_amd64_test.go`.
- a fuzz target in its own file, which the rule allows: `chimp_fuzz_test.go`.

In `blob/` no file is added: the code, its tests and its fuzz target join the component they change.

### Kernel contract

```go
//go:noescape
func chimpGroupDecodeAVX512Asm(st *chimpGroupState, steps int, out *uint64) int
```

```go
type chimpGroupState struct {
	base   [8]*byte  // first byte of the lane's column
	pos    [8]uint64 // bit position of the next value, counted from base
	end    [8]uint64 // one past the column's last bit
	prev   [8]uint64 // bits of the previous value
	stored [8]uint64 // stored leading-zero count, 65 for none
}
```

- The kernel takes up to `steps` values from each of the eight lanes and returns the number of steps it completed.
  Row `r` of `out` receives the eight values of step `r`;
  the caller provides `steps` rows of eight words, and `steps` is at most `chimpGroupRows` (64).
- One step, for all lanes at once:
  load the 64-bit window at `base + pos>>3` shifted by `pos&7` (16 bytes read per lane),
  extract flag, leading bucket and the 6-bit count, pick header and payload width by flag,
  XOR the payload into `prev`, store the row, advance `pos`, update `stored`.
  No branch depends on the data except the one that leaves the loop.
- The kernel leaves **before committing a step** that any lane cannot take, for one of four reasons:
  header plus payload wider than the 64-bit window; a reuse flag with no stored leading; a negative trailing count;
  a next position past `end`.
  It does not decide what such a value is: the scalar loop does.
- On return `pos`, `prev` and `stored` hold the state after the completed steps.
  That state is the state of `DecodeAll`'s loop after the same number of values:
  this is the invariant the hand-off rests on.
  `base` and `end` are not written.
- Lanes are independent except for the shared decision to leave.
- It needs AVX-512 F, BW (`VPSHUFB` on 512 bits) and DQ (`VPMOVQ2M`), ends with `VZEROUPPER`,
  and reads the state's fields through the `go_asm.h` offsets (`chimpGroupState_pos` and so on), not literal numbers.
  If the generated header cannot be used, literal offsets stay and a test pins each with `unsafe.Offsetof`.

### Group decode (`ColumnGroup.Decode`)

`ColumnGroup` holds up to eight columns:
`Payload[i][Off[i]:Off[i]+Len[i]]` with `Count[i]` values, decoded into `Dst[i]`.
`Decode(n)` decodes lanes 0 to n-1.
For each lane it sets `Produced[i]` to what `NumericChimpDecoder.DecodeAll(column, Count[i], Dst[i])` returns,
with the same values in `Dst[i][:Produced[i]]` and no write past that.
It never reads `Dst`.
`Columns`, `Steps` and `Calls` count the columns that entered the kernel, the steps it completed, and the kernel calls.

**Lane eligibility.**
A lane may enter the kernel when all of these hold, checked in this order in signed `int` arithmetic:
`Count >= 2`; `len(Dst) >= Count`; `Off >= 0`; `Off <= len(Payload)`; `Len >= 8`; `Len <= len(Payload) - Off`;
`len(Payload) - Off - Len >= chimpGroupReadAhead`, with `chimpGroupReadAhead` = 16;
and `Count - 1 <= (Len - 8) * 8`.
The last one was added on 2026-11-14, after the review of "Task 2, continued":
a column asked for more values than it has bits after the first comes up short whatever it holds,
and the forecast of that task had this bound while the decode had not.
No sum of `Off` and `Len` is formed, so nothing can overflow,
and each subtraction is evaluated only after the check that keeps it from going negative.
The unsigned values of the kernel's state (`end` is `Len * 8`) are computed only for a lane that passed.
A lane that fails is decoded by `DecodeAll` on `Payload[Off:Off+Len]`,
so the caller passes only columns that lie inside their payloads; `blob` ensures that with its lane check.
The 16 bytes are counted to the payload's length, never to its capacity:
the kernel reads only bytes of the lane's own payload.
Sixteen is the kernel's read extent, not a tuning value.

**Group eligibility.**
The group uses the kernel when `chimpGroupAVX512` is set, at least `GroupMinColumns` lanes are eligible,
and the eligible columns are dense enough:
`sum((Len-8)*8) >= chimpGroupMinBitsPerValue * sum(Count-1)`.
The prototype's values, 4 columns and 12 bits, are guesses; Task 4 measures them.

**Lane states.**
Every lane is in exactly one state, and a terminal state is never left.

| State | Meaning | `Produced` |
|---|---|---|
| scalar | never entered the kernel: the lane or the group was not eligible | set once, by `DecodeAll` on the column |
| live | in the kernel; its slot in `chimpGroupState` is its state | not set yet |
| complete | it has its `Count` values | set to `Count` at the transition |
| handed off | it left the kernel with values still wanted | set by `decodeAllFrom`, called at the transition from the lane's own slot |
| failed (Task 5 only) | a scalar step produced nothing: the stream ends or is invalid here | set to the values it has, at the transition |

The slot of a lane that is not live is a mirror of some live lane and carries no meaning.
`Produced` of a terminal lane is frozen:
nothing reads its slot again, and it is never passed to `decodeAllFrom` after the transition.
A lane is handed off before any mirroring can overwrite its slot.

**Rounds, for counts that differ.**
The first value of each kernel lane is read in Go, as `DecodeAll` reads it,
and the lane becomes live at `pos` 64 with `stored` 65.
Then, while at least `chimpGroupMinLive` lanes are live:

1. `steps` is the fewest values any live lane still wants, at most `chimpGroupRows`;
2. every slot that is not live is given a copy of one live lane's slot,
   so it leaves the kernel exactly when its source does,
   never costs a live lane a step, and is never a source of an address of its own;
3. the kernel runs, and the rows are copied into each live lane's `Dst`;
4. a live lane that now has its `Count` becomes complete;
5. if the kernel returned fewer steps than asked, the rounds end.

When the rounds end, every lane that is still live is handed off.
In Task 1 that is the only hand-off, and it happens for all remaining live lanes at once, with no mirroring after it.

**Hand-off to the scalar loop.**
`decodeAllFrom` is the loop of `DecodeAll`, entered at a given state.
Its parameters are the column, the count, the destination, and the state:
the values produced so far, the bit position, the previous value's bits and the stored leading count.
It returns the number of values produced, as `DecodeAll` does.
`decodeAllFrom` is a copy of that loop, not a part `DecodeAll` shares:
`DecodeAll` serves every non-goal path and stays byte for byte as it is,
and the package already keeps this windowed loop once per reader (`All`, `DecodeAll`, `At`).
A test pins the copy against `DecodeAll` from every reachable state (Task 1).

### Blob layer (`materializeMembersGrouped`)

Only `NumericBlobSet.materializeMetricCore` changes, which is what all four entry points reach.
Step 1 also counts the resolving members whose `ValueEncoding()` is `format.TypeChimp`, when `chimpGroupDecode` is set:
`if chimpGroupDecode && blob.ValueEncoding() == format.TypeChimp { chimpMembers++ }`.
Step 4 becomes:

```go
if chimpGroupDecode && chimpMembers >= ienc.ChimpGroupMinColumns {
	timestamps, values, tags = s.materializeMembersGrouped(resolve, hasTags, timestamps, values, tags)
} else {
	// the baseline loop, textually unchanged
}
```

The grouped loop lives in its own function, so the baseline path never pays for its frame.

**Which column goes where.**
The complete rule, from the member to the kernel:

1. a member is decoded ahead of its turn, in a lane, when it is a Chimp member, its group is formed (below),
   and the group check and the lane check of "Exactness" pass;
2. a lane enters the kernel when the lane is eligible and the group is eligible (above): count, column length,
   destination length, read-ahead, the number of eligible lanes, and density;
3. every other column is decoded by the scalar decoder of its encoding.

A member's layout, byte order, timestamp encoding, tags and compression play no part:
the Chimp bit stream does not depend on the byte order,
and a compressed payload is decoded from its decompressed buffer.
A set that mixes Chimp members with members of other encodings keeps the kernel for its Chimp members.

**The loop.**
`appendMember` below stands for the baseline's statements for one member, in the baseline's order:
timestamps, values, tags, `alignMemberRows`.
`materializeMembersGrouped` keeps one more integer, `written`.
It is the high-water mark of the members appended so far, in the baseline's order:
the largest value of a member's offset plus its `Count`.
Values that lanes wrote ahead of their member's turn do not count towards it.
Until the members are used up:

1. if `len(values) < written`, append the next resolving member with `appendMember`, decoding its values in its turn,
   and go to 1;
2. otherwise collect a group: take the next eight members that resolve, with their index entries kept in a fixed array
   (two resolve calls per member, as today);
3. validate the group as a whole, by the group check of "Exactness".
   If it fails, append the collected members with `appendMember`, each decoded in its turn, with no lane and no clearing,
   and go to 1;
4. give each member its planned offset:
   the place its values have when every earlier member of the group yields exactly `Count` rows.
   Give each Chimp member that passes the lane check a lane, with `values[planned : planned+Count]` as destination,
   and call `ColumnGroup.Decode`;
5. append the members in order with `appendMember`.
   A member with a lane takes its `Produced` values where they already are:
   at its planned offset, which is its real offset.
   A member without a lane is decoded in its turn by `decodeValuesSlice`, as in the baseline;
6. after each member's statements have run, raise `written` to the member's offset plus its `Count`.
   If the member did not yield exactly `Count` rows, the group is rolled back:
   clear `values` from that member's planned end to the group's planned end,
   and append the rest of the group's members without their lanes, decoding each in its turn.

### Exactness

The grouped loop differs from the baseline in one thing: it writes the values of some Chimp members before their turn.
Two rules make that invisible.

**The invariant.**
At every point of the loop, compared with the baseline at the same member:

- the rows of the members already appended are equal, and so is the destination range of the member being decoded;
- the timestamps and tags slices are equal over their whole capacity, since nothing is written to them ahead;
- `values` differs only inside the planned ranges of Chimp members of the current group that have not been appended yet;
- a rollback removes those differences before any member is decoded at an offset below its planned one.

So `values` equals the baseline's over its whole capacity whenever a group has been consumed or rolled back,
and at return.
Between the members of a group that is still ahead it does not, and nothing reads the difference.

**Rule 1: nothing a later decoder can read is changed.**
A decoder is given `values[offset : offset+Count]`, a slice of length `Count`, and reads and writes nothing else:
no value decoder reslices its destination past its length (none mentions the destination's capacity).
The baseline writes `values` only inside such ranges, so every element at or above `written` is still zero there.
A group is decoded ahead only when `len(values) == written`, so all its planned ranges lie in that zero region.
Inside a group that stays ahead, every member's real offset is its planned offset.
A lane then writes exactly what `DecodeAll` would write in that member's own range, and no other range.
So each member that is decoded in its turn finds its own range as the baseline has it: untouched.
When a member yields fewer rows than its `Count`, the next member's real offset drops below its planned one,
and its range would overlap values written ahead.
At that moment the values written ahead are the only difference from the baseline,
and they all lie between that member's planned end and the group's planned end.
Clearing that range restores the baseline's contents exactly, because the baseline holds zeros there.
From there the rest of the group runs the baseline's statements on the baseline's state.
The next group waits until `len(values)` has caught up with `written`, so the zero region is real again.

This is why the stale values of the first version of this plan are gone:
what the call returns holds the baseline's contents, element for element, over the whole capacity of each slice.
It also covers the decoder that reads before it writes (`decodeRunsInto`): it reads the baseline's bytes.

**Rule 2: nothing done ahead of a member's turn can panic.**
Decoding ahead adds two kinds of operation the baseline does not have: filling a lane, and clearing on a rollback.
Two checks, both made before any lane is filled, keep them from panicking.

*The group check* covers the clearing and every lane's destination.
Start with `end = len(values)`.
For each collected member in order, require `Count >= 0` and `Count <= cap(values) - end`, then add `Count` to `end`.
The comparison is written with the subtraction, so no sum can overflow.
If any member fails, the group is not decoded ahead at all (step 3 of the loop).
A group that passes has every planned range, and its planned end, inside the capacity.

*The lane check* covers the column.
A Chimp member of a group that passed gets a lane only when its `Count` is at least 2
and its value range lies inside `blob.valPayload`:
`ValueOffset >= 0`, `ValueOffset <= len(blob.valPayload)`, `ValueLength >= 0`
and `ValueLength <= len(blob.valPayload) - ValueOffset`.
`ColumnGroup.Decode` cannot panic on such a lane.

Any other member is decoded in its turn by the baseline's statements.
So every panic is raised by a baseline statement, in the baseline's order, on the baseline's state:
a member with a bad timestamp range and a bad value range panics on the timestamp range, as it does today,
and a group with a count that does not fit panics at the member and with the bounds the baseline reports.

Both rules cost a few compares per member.
A damaged member costs more: the rest of its group is decoded twice, and grouping pauses until the rows catch up.
That only happens in sets the baseline already treats as damaged.

### Memory and GC safety

The argument is sound for Go 1.25 and Go 1.26, the two releases the repository supports and CI runs.
It was verified against the runtime source:
Go 1.26.7 locally (`isAsyncSafePoint` in `runtime/preempt.go` returns false for a function flagged as assembly),
and Go 1.25.0 by this plan's first review, which found the same exclusion.

- The lane bases are `*byte` values taken with `&column[0]`,
  so the collector sees eight interior pointers that keep the payload arrays alive.
- They live in `chimpGroupState`, a field of the `ColumnGroup` on the caller's stack,
  which the kernel's pointer argument keeps reachable;
  `Decode` also calls `runtime.KeepAlive` on the group after the last kernel call.
- Inside the kernel the pointers are copied to a vector register and the derived addresses live in another.
- The kernel is a `NOSPLIT` assembly leaf with no `CALL`:
  no stack-growth check, and the runtime does not preempt assembly asynchronously.
  So no safe point occurs between loading the bases and the last read.
- The collector does not move heap objects;
  it moves only a goroutine's stack, at a growth or at a shrink during a safe point.
  Neither can happen inside the kernel,
  and between kernel calls only typed pointers refer to a payload, which stack copying adjusts.

**What must stay true.**
A change that breaks one of these breaks the argument.
They are not all detected automatically: the right column says what checks each one.

| Invariant | Checked by |
|---|---|
| the kernel has no `CALL` and stays `NOSPLIT`; a frame, if any, holds no Go pointer and nothing outlives the call | the assembly audit |
| the bases are held as `*byte`, never as `uintptr`; the non-test Go code imports no `unsafe` | `TestChimpGroup_StateLayout` |
| nothing derived from an address is written to `st`, to `out`, or returned | the assembly audit |
| a slot that is not live holds a copy of a live lane's whole slot, never a zero or stale base | the differential tests with idle lanes; review |
| the read-ahead is counted to `len(Payload)`, and `pos` never passes `end` | the guard page tests |
| `steps` never exceeds the rows the caller provides | review; the differential tests with a sentinel after the rows |
| the runtime treats assembly as not preemptible and the heap as not moving | a read of the runtime source for each supported release |

**The assembly audit.**
Every task that writes or changes assembly ends with it, and its report lists the answers:

1. from the source: the `TEXT` line of each kernel, its flags and its frame size;
2. from the binary: a complete disassembly of each kernel, with no undecoded instruction and no `call`.
   The disassembler is GNU `objdump`, which decodes EVEX;
   `go tool objdump` does not, and prints `?` bytes for these kernels.
   Take the kernel's exact symbol from `go tool nm` of the chimp test binary; it carries the suffix `.abi0`.
   Run `objdump -d --no-show-raw-insn --disassemble='<symbol>' <test binary>`.
   The output must have no `(bad)` and no `.byte` line, and the report says how many instructions it lists;
3. from that disassembly and the source:
   every instruction that writes memory, with its destination and the register it stores,
   and for each a statement that the register holds no address and nothing derived from one
   (a frame slot of the scalar-load variant is the one allowed exception, and it is dead on return);
4. `go vet` (asmdecl) is clean for amd64 and arm64.

Checked on this machine on 2026-11-13 with the existing ALP kernel: `/usr/bin/objdump` is GNU objdump 2.42,
the symbol is `github.com/arloliu/mebo/internal/encoding/value/alp.alpFusedDecodeAVX512Asm.abi0`,
and its disassembly has no undecoded line,
while `go tool objdump` breaks its first `VMOVDQU64` into `?` and unrelated instructions.
An agent on a machine without an EVEX-capable `objdump` stops and reports.

**Supported releases.**
Adding a Go release to the CI matrix needs a new read of `isAsyncSafePoint` for that release, recorded in the kernel's comment.
The GC stress test of Task 8 is supplementary evidence:
passing it does not show that a stack moved at the critical moment.

### Feature gate and off switch

- `chimpGroupAVX512 = arch.X86HasAVX512DQ() && arch.X86HasAVX512BW()` in `chimp_group.go`,
  false off amd64 through the `arch` stubs.
  This is how the ALP package gates its kernels (`alpDecAVX512` in `alp.go`).
- `blob.chimpGroupDecode` is initialised from `ienc.ChimpGroupDecodeEnabled()`.
  Tests clear either variable to force the scalar path on the same machine, and must not run in parallel while they do.
- A whole process turns the kernel off with `GODEBUG=cpu.avx512dq=off`,
  the switch the ALP kernels document; it turns those off too.
  `GODEBUG=cpu.avx512bw=off` turns off this kernel and the ALP decode kernel, and leaves the ALP encode kernels on.
  No setting turns off this kernel alone (decision 2 of "Taken by this plan").
- `blob.chimpGroupObserve`, nil outside tests, receives the columns, steps and kernel calls of one materialized metric.
  It is how a test proves the kernel ran.

## File structure

| File | Change |
|---|---|
| `internal/arch/x86_amd64.go`, `internal/arch/x86_stub.go` | add `X86HasAVX512BW` (Task 1) |
| `internal/encoding/value/chimp/chimp_group.go` | new: `ColumnGroup`, `Decode`, `decodeAllFrom`, the gate, the constants (Task 1) |
| `internal/encoding/value/chimp/chimp_decsimd_amd64.s`, `chimp_decsimd_amd64.go`, `chimp_decsimd_noasm.go` | new: the kernel, its declaration, the stub (Task 1) |
| `internal/encoding/value/chimp/chimp_group_test.go`, `chimp_decsimd_amd64_test.go`, `chimp_fuzz_test.go` | new: codec-level tests, guard page and `GODEBUG` tests, fuzz target (Task 1, Task 8) |
| `internal/encoding/value/chimp/chimp_group_bench_test.go` | new: codec-level benchmark with its `unsupported=` cells (Task 1), the threshold sweeps (Task 4) |
| `internal/encoding/facade.go` | `ChimpColumnGroup`, `ChimpGroupDecodeEnabled`, `ChimpGroupMinColumns` (Task 1) |
| `Makefile` | `FuzzNumericBlobSetMaterializeMetric` (Task 0) and `FuzzChimpGroupDecode` (Task 1) join `FUZZ_TARGETS` |
| `blob/numeric_blob_set_material.go` | `materializeMembersGrouped`, the switch, the observe hook (Task 2) |
| `blob/numeric_blob_set_material_test.go` | the frozen reference, its fixtures and the fuzz target (Task 0), grouped-path tests (Task 2, Task 8) |
| `blob/numeric_blob_set_material_bench_test.go` | the consumer-shaped benchmark (Task 0), the kernel's share of columns (Task 2) |
| `.github/workflows/ci.yaml` | the AVX-512 step also runs the Chimp kernel tests (Task 8) |
| `CHANGELOG.md`, `blob/blob_set.go` (godoc figures) | Task 9 |

`internal/encoding/value/chimp/chimp.go` is not edited.

## Task gates

One row per decision.
"Limits" are the limits of "Acceptance thresholds".
A task whose gate sends it to the owner stays uncommitted, or its commit stays unmerged, until the owner answers.

| Task | Benchmark and rows | Compared with | Passes when | Otherwise |
|---|---|---|---|---|
| 0 | every row | none | every setup assertion holds; `consumer` is within 20% of 266 µs | stop and report |
| 1 | none | none | tests, mutations and audit only | stop and report |
| 2 | `consumer`; `gauge`, `counter`, `fullprec` | `B0` | `consumer` improves by 25% merged and 20% in every layout; each kind row improves by 20% merged | under 15% merged on `consumer`: stop, the change is dropped; otherwise the task is provisional and is read again after Task 4 |
| 2 | `alprle`, `blobs=1`, `2`, `3`; the two gate-off rows; allocations of every row | `B0` | inside their limits; allocations equal | fix inside the task, or stop and report |
| 2, continued | `nearconst`, `constant`, `mixed=7of8`, `lastcolumn`; the Task 2 rows again | `B0` | `nearconst` and `constant` inside limit 5% and within 3% merged; the other two inside limit 5%; the Task 2 requirements hold | the owner decides with the numbers |
| 3 | `BenchmarkChimpGroup`; `consumer`, `gauge`, `counter`, `fullprec`, `blobs=8` | the same measurement commit, the gather kernel selected against the scalar-load kernel selected | `consumer` improves by 5% merged and no listed row exceeds limit 5% | the gather kernel stays; the measurement commit is reverted |
| 4 | the entry cells of `BenchmarkChimpGroupSweep` | same builds: the kernel forced against no group entering | `GroupMinColumns` and the bit threshold are set by the rule of Task 4 | above 6 lanes or 24 bits: stop and report |
| 4 | the continuation cells of `BenchmarkChimpGroupSweep` | same builds: the kernel continued against a hand-off, after the same kernel prefix | `chimpGroupMinLive` is set by the rule of Task 4 | above 6 lanes: stop and report |
| 4 | `nearconst`, `constant`, the `mixed=` rows; the Task 2 rows again | `B0` | inside their limits; the Task 2 requirements hold | raise the bit threshold one sweep step and measure again, twice at most; then the owner decides |
| 5 | counts on the benchmark fixtures of Task 0 | none | on a consumer-shaped fixture, 2% or more of the kernel columns' values are handed off after an early leave | the task is dropped; no commit |
| 5 | the `unsupported=` cell of `BenchmarkChimpGroup` selected by the rule of Task 5; `unsupported=100%/alllanes`; `consumer` | last accepted | the selected cell improves by 5% merged; the other two inside limit 5% | the task is dropped; no commit |
| 6 | none | none | the owner asks for it; the owner declined on 2026-11-13 (owner decision 4) | the task is dropped; no commit |
| 6 | `lastcolumn`; `consumer` | last accepted | `lastcolumn` improves by 20% in every layout; `consumer` inside limit 5% and within 2% merged | the task is dropped; no commit |
| 7 | the `blobs=` rows from 4 up, `tags`, `alprle`, `mixed=7of8` | `B0` | inside their limits | try the candidates of Task 7 in order; if none works, the owner decides |
| 7 | `consumer`, for each candidate tried | last accepted, the head before Task 7 | within 2% merged | that candidate is not kept |
| 9 | every row and the two gate-off rows | `B0` | "Acceptance thresholds", all of it | fix, or the owner decides with the numbers |

## Tasks

### Task 0: The benchmark and the frozen reference

**Files:**
- Modify: `blob/numeric_blob_set_material_bench_test.go`, `blob/numeric_blob_set_material_test.go`, `Makefile`

**Why first:** no benchmark of this path uses data that does not repeat or the consumer's shape,
and every later task needs the baseline loop kept somewhere a change cannot reach.
This task changes no production code.

- [ ] Add a fixture builder for a set in the consumer's shape:
  little endian, V2 layout, shared DeltaPacked timestamps, no compression, metrics started by name, no stored names.
  Parameters: blobs, metrics per blob, the range of points per blob (one count per blob, drawn from the range),
  the value encoding per member, tags on or off, and the value kind of each metric.
  The kinds are the four `generateChimpMixColumn` documents in the chimp package:
  a two-decimal gauge that holds its value 30% of the time, an integer counter,
  a near-constant column, a full-precision gauge.
  "The mix" is 35, 20, 18 and 27 metrics of them out of 100.
  The `blob` package cannot import that test helper, so the generator is written here.
  Every column has a seed and a start value of its own, both functions of fixture, blob and metric,
  so no two columns of a fixture hold the same values, constant columns included.
  The builder decodes the set with `DecodeBlobSet`.
  It fails unless the first and last members have the layout, the encoding, no names payload,
  and a shared timestamp group of `Count` timestamps for the first and last metric.
- [ ] Add `BenchmarkNumericBlobSet_MaterializeMetricByName`, calling `BlobSet.MaterializeNumericMetricByName`,
  reporting `ns/op`, `ns/point`, allocations and bytes.
  One operation materializes one metric of one set.
  A row has `S` sets and a list of `M` metrics, the list in a fixed shuffled order.
  Operation `i` takes pair `p = i mod (S*M)`: set `p mod S`, metric `p / S` of the list.
  So a row visits every pair of set and metric once before it repeats, and its period is `S*M` operations.
  Each row builds its fixtures inside its own sub-benchmark.
  Rows, each with 100 metrics of 30 to 40 points per blob unless it says otherwise:

  | Row | Fixture | Sets × metrics | Distinct points, about |
  |---|---|---|---|
  | `consumer` | 1,000 Chimp blobs, the mix | 1 × 100 | 3.5 million |
  | `gauge`, `counter`, `fullprec`, `nearconst` | one fixture per row: 1,000 Chimp blobs whose 100 metrics are all of that kind | 1 × 100 | 3.5 million |
  | `constant` | 1,000 Chimp blobs, every column one constant of its own | 1 × 100 | 3.5 million |
  | `lastcolumn` | 30 fixtures of 1,000 Chimp blobs with 4 full-precision metrics; the metric whose column ends every payload | 30 × 1 | 1.05 million |
  | `equalcounts` | 1,000 Chimp blobs of exactly 35 points, the mix | 1 × 100 | 3.5 million |
  | `points=10`, `points=150` | 1,000 Chimp blobs at that fixed count, the mix | 1 × 100 | 1 million, 15 million |
  | `blobs=1`, `2`, `3`, `4`, `7`, `8`, `9`, `16`, `64` | the `consumer` fixture cut into sets of k consecutive blobs, `S = 1000 / k` rounded down | S × 100 | 3.36 million or more |
  | `mixed=alternate`, `mixed=1of8` | 1,000 blobs, the mix, ALP-RLE in every second or every eighth member | 1 × 100 | 1.75 and 3.06 million Chimp points |
  | `mixed=7of8` | 2,400 blobs, the mix, ALP-RLE in seven members of eight | 1 × 100 | 1.05 million Chimp points |
  | `alprle` | 1,000 ALP-RLE blobs, the mix | 1 × 100 | 3.5 million |
  | `tags` | 1,000 Chimp blobs with 2-byte tags, the mix | 1 × 100 | 3.5 million |

- [ ] Add the setup assertions, run once per row before the timed loop.
  Walk one period and mark every pair of set and metric: each must be visited exactly once.
  Sum the points of the columns visited, and separately those of Chimp columns:
  a row with Chimp members needs at least 960,000 Chimp points, and `alprle` at least 960,000 points.
  Hash the encoded value bytes of every column visited: no two may be equal.
  For `lastcolumn`, the metric must end the value payload in every blob and hold at least two points in each.
  A failed assertion fails the row.
- [ ] Add the frozen reference to `blob/numeric_blob_set_material_test.go`:
  `referenceMaterializeMetric`, the baseline body of `materializeMetricCore` copied verbatim,
  taking the set, the metric ID and `resolve`.
  Add `requireMaterializeMatchesReference(t, blobs, name)`:
  it materializes by name and by ID through the public methods and through the reference
  (building `resolve` the way `MaterializeMetricByName` and `MaterializeMetric` do).
  It requires the same `ok`, metric ID, lengths, capacities and nil-ness,
  and the same timestamps, tags and value bits over the whole capacity of each slice, not only its length.
  It compares what a call returns, which is where "Exactness" promises equality over the capacity;
  it has no view of the slices inside the loop.
  It also compares panics: both sides panic or neither, and the recovered values are equal (type and message).
- [ ] Add the fixtures later tasks reuse, each run through that helper now, where both sides are the same code:
  - equal counts; counts 30 to 40 over 40 blobs; 1, 3, 4, 5, 7, 8, 9, 13 and 20 blobs;
  - one-point and two-point columns, alone and among longer ones;
  - the column that ends its payload with one point, with two points and with 35,
    and the column 8 bytes before the end;
  - a metric missing from 11 of 30 blobs; a metric in no blob;
  - constant and near-constant columns; random bit patterns (64-bit payloads, every leading bucket, unchanged runs);
  - a mixed set: Chimp members in both byte orders, V1 and V2, shared and own timestamps, tags, zstd, S2 and LZ4,
    interleaved with ALP, ALP-RLE, Gorilla and Raw members;
  - value bytes flipped through the public API; index entries edited on the decoded blob
    (count raised, count lowered, column cut short, column cut on a byte boundary), early in a group of eight and late;
  - **the review's fixture.**
    Member A is Chimp, declares eight values and produces one.
    Member B is Chimp and produces eight values, 101 to 108.
    Member C is ALP-RLE with four values in one run;
    after decoding, its value column is shortened exactly at the nested column, the runs header and bitmap left whole.
    Complete Chimp members follow, enough for a group of eight.
    The baseline returns four zeros for C;
  - **the same across groups.**
    The last member of a group is Chimp with 40 values and an own timestamp column cut to 5,
    so it leaves 35 values past the length.
    The next group starts with a Chimp member that declares eight values and produces one,
    then an ALP-RLE member of 20 values in one run with its nested column cut away, then complete Chimp members.
    The baseline returns one of the first member's left-over values, 20 times;
  - one member with two faults: a timestamp range outside its payload and a value range outside its payload,
    with different bounds, so the two panics differ;
  - **a count that does not fit, behind a short member.**
    Nine Chimp members whose entries are edited to counts 8, 8, 8, 8, 8, 8, 8, 8 and -16, so the capacity is 48.
    The first member has eight values and an own Delta timestamp column that yields one timestamp; the others are whole.
    The baseline appends six members and panics at the seventh, with a bound of 49;
  - a negative count inside a group of eight, at its first, a middle and its last place;
  - a planned sum that overflows: a group with one count edited to the largest `int`,
    and a matching negative count after the group, so that the capacity stays small.
  The prototype's `blob/zz_chimp_lanes_test.go.txt` has most of these as tests of the switch;
  here they are tests of the reference, under names without the research prefix.
- [ ] Add `FuzzNumericBlobSetMaterializeMetric` to the same test file, and to `FUZZ_TARGETS` in the `Makefile`.
  The input bytes are read as a bounded program.
  It has 1 to 24 members, and for each an encoding variant from the mixed set above, a count of 1 to 45 and a value kind.
  Then comes a list of damage steps.
  A step flips a byte of an encoded member's value, timestamp or tag payload, or edits a decoded member's entry:
  count up or down, value length, timestamp length, tag range, an ALP-RLE column cut at its nested column.
  A member `Decode` rejects is left out of the set.
  The target compares all four entry points with the reference through the helper above.
  The seed corpus holds the fixtures of this task that fit the program, the two review fixtures among them.
- [ ] Mutation checks, each seeded in the production `materializeMetricCore` and restored afterwards:
  the helper must fail when production drops the last value of one member, swaps two members,
  pads tags by `count` where the baseline pads by the values produced, returns `ok` for an absent metric,
  or writes one value past a member's rows into the unused capacity.
- [ ] Run `make lint`, `make test` and `make fuzz`.
  The orchestrator records the layout-averaged baseline of every row at this commit, which is `B0`.
  Expected for `consumer` from the prototype's single binary: about 266 µs per operation, 7.5 ns per point.
- [ ] Commit `test(blob): benchmark set materialization in the consumer's shape`,
  `test(blob): pin set materialization against a frozen reference`
  and `test(blob): fuzz single-metric set materialization`.

**Stop and report if** the fixture cannot be built without stored names, a setup assertion cannot be met as written,
or the baseline of `consumer` is not within 20% of 266 µs:
the plan's thresholds were derived from that figure.

### Task 1: The kernel and the group decode

**Files:**
- Create: `internal/encoding/value/chimp/chimp_group.go`,
  `chimp_decsimd_amd64.s`, `chimp_decsimd_amd64.go`, `chimp_decsimd_noasm.go`,
  `chimp_group_test.go`, `chimp_decsimd_amd64_test.go`, `chimp_group_bench_test.go`, `chimp_fuzz_test.go`
- Modify: `internal/arch/x86_amd64.go`, `internal/arch/x86_stub.go`, `internal/encoding/facade.go`, `Makefile`

**Reference:** `NumericChimpDecoder.DecodeAll`,
which this task must not edit (`git diff 280e06e -- internal/encoding/value/chimp/chimp.go` stays empty).

**Source to port:** the five `zz_chimp_lanes*.txt` files under `blob-prototypes/decode/internal/encoding/value/chimp/`:
the kernel, its declaration, the stub, the group decode and the tests.
`protoResume` is in `scratch/zz_chimp_proto.go.txt`.
The design above is the specification; the prototype is the worked example.
The prototype differs from the design in two places, and the design wins:
it resumes every admitted lane from its slot at the end, where the design freezes `Produced` at each transition;
and it has one constant for entry and for continuing, where the design has two.

- [ ] Add `X86HasAVX512BW` to both `arch` files, with godoc, in the style of `X86HasAVX512DQ`.
- [ ] Write the tests first.
  Against a `Decode` that sends every lane to `DecodeAll`, the comparisons pass and the engagement pins fail:
  - `TestChimpGroup_DecodeAllFrom`:
    for the mix's columns, edge columns (NaN, infinities, signed zero, denormals, random bit patterns,
    long unchanged runs), truncated columns and bit-flipped columns,
    trace every successfully decoded prefix with a tracing copy of `DecodeAll`'s loop kept in the test.
    For each reachable prefix length k, from 1 to the number of values `DecodeAll` actually produces,
    take the state after k values and require that `decodeAllFrom` from that state, with the original requested count,
    fills the same values and returns the same count as `DecodeAll`.
    A column that produces no value has no state to resume from:
    compare its scalar result with `DecodeAll`'s and require that the destination is untouched.
  - `TestChimpGroup_Differential`:
    3,000 random groups, each lane in a buffer of its own, compared with `DecodeAll` over the whole destination;
    a sentinel after `Count` and the untouched tail show writes past the produced count.
    Lanes short of read-ahead, cut short, over-counted, under-counted and bit-flipped.
    Every destination is filled with a pattern first, to show that `Decode` does not depend on what it holds.
  - `TestChimpGroup_Engages`: pins `Columns`, `Steps` and `Calls` for equal and differing counts, more than 64 steps,
    three and four lanes, one-point and two-point columns, lanes with 15 and 0 bytes of read-ahead,
    a lane cut short, and both sides of the density rule.
    On a CPU without the kernel it skips, or fails when `MEBO_REQUIRE_AVX512=1` (a helper like `alpRequireAVX512`).
  - `TestChimpGroup_GuardPage` in `chimp_decsimd_amd64_test.go`: every lane's payload ends at a `PROT_NONE` page;
    lanes with 0, 7, 8 and 15 bytes after the column must not enter the kernel, the lane with exactly 16 must,
    and every column claims more points than it holds so the kernel walks to its last bit.
  - `TestChimpGroupDecodeAVX512_GODEBUG` in the same file, modelled on `TestALPFusedDecodeAVX512_GODEBUG`:
    a child process with `cpu.avx512dq=off` and one with `cpu.avx512bw=off` must each see `GroupDecodeEnabled()` false.
  - `TestChimpGroup_StateLayout`: `chimpGroupState.base` is `[8]*byte` (by reflection),
    and none of `chimp_group.go`, `chimp_decsimd_amd64.go` and `chimp_decsimd_noasm.go` imports `unsafe`.
  - `FuzzChimpGroupDecode` in `chimp_fuzz_test.go`, added to `FUZZ_TARGETS`.
    The input is cut into up to eight columns with counts taken from the input.
    The harness puts each column in a buffer of its own and appends 0 to 31 further bytes, also from the input,
    so that lanes with and without read-ahead both occur.
    It compares `ColumnGroup.Decode` with `DecodeAll` per column.
    The seed corpus holds valid groups of eight encoded columns of 30 to 40 values with read-ahead.
  - `TestChimpGroup_FuzzSeedsEngage`: over the seed corpus, `Steps` is above zero,
    and at least one seed has a lane handed off.
- [ ] Port the kernel, the state, the group type with its `Decode`, `decodeAllFrom` and the gate,
  under the production names, with godoc on every exported symbol.
  `Decode` reaches the three thresholds through an unexported method that takes them as parameters,
  so a benchmark can force either path;
  the exported method passes the constants.
  Both lane constants start at the prototype's 4, and the bit threshold at 12.
- [ ] Add the facade entries.
- [ ] Add `BenchmarkChimpGroup` to `chimp_group_bench_test.go`:
  eight columns in eight buffers from the mix at 30 to 40 points with differing counts,
  at least 960,000 distinct points, `Decode` with the kernel against `DecodeAll` per column, reporting `ns/column`.
- [ ] Add the cells `BenchmarkChimpGroup/unsupported=<share>/<spread>` to the same benchmark, for Task 5.
  They land here so that every later revision has them, with one timed loop.
  The data is the full-precision gauge at 35 points, in groups of eight columns.
  A column has 34 replaceable values, those after its first, so a group has 272.
  The spread names the lanes in which values are replaced:
  one lane of each group for `onelane`, all eight for `alllanes`.
  The share is the fraction replaced within those lanes, not within the group: 0.1%, 1%, 10% and 100%.
  Each replaceable value of a selected lane is replaced by a random 64-bit pattern with that probability,
  so its positions within a column are uniform.
  That gives eight cells.
  `unsupported=100%/onelane` is one lane of 34 replaced values beside seven untouched lanes, an eighth of the group;
  `unsupported=100%/alllanes` replaces all 272.
  A replaced value nearly always makes the kernel leave: its header and payload are wider than the 64-bit window.
  Measured when the cells were built (Task 1):
  about 0.45% of random patterns are still taken, and the value after a replaced one is not taken either,
  so the realized rate is about twice the share and `100%/alllanes` reaches 0.9955, not 1.
  This is why a cell is matched by its measured rate and never by its name.
  Add a test helper that classifies every value of a column as one the kernel takes or not,
  from the tracing copy of `DecodeAll`'s loop.
  At setup each cell counts with it and reports three figures, all over the whole group:
  the rate of values the kernel does not take, among all values after the first of the eight columns;
  the mean number of lanes per group that hold such a value;
  and the share of values handed off.
  The first two are what Task 5 matches a cell by.
  The name's share only says how the cell was built, and the handed-off share is a consequence:
  neither is used to pick a cell.
- [ ] Mutation checks, all against `go test ./internal/encoding/value/chimp/ -run 'ChimpGroup'`:
  - kernel: the second window qword reads the first again;
    `stored` not written back; `pos` not written back; `prev` not written back;
    the end-of-lane check removed; every lane given lane 0's base;
  - group decode: no read-ahead required;
    read-ahead of 15 (the guard page test must catch it); read-ahead counted to the capacity;
    the density rule removed; the density threshold doubled;
    idle slots not mirrored; a lane's produced count set to zero, not one, after its first value is stored;
    the hand-off forgets `stored`; the hand-off restarts at the column's start; the row copy reads another lane;
    `end` one byte late; the kernel allowed for one live lane;
    one-point columns admitted; columns under 8 bytes admitted;
    a lane made complete one value early; the first value not stored;
    a complete lane passed to `decodeAllFrom` from a mirrored slot with its count raised by one;
  - `base` declared as `[8]uintptr` (the layout test must fail);
  - the kernel declared unavailable on a CPU that has it:
    with `MEBO_REQUIRE_AVX512=1` the engagement test must fail, not skip.
  The prototype found four equivalent mutations:
  `KeepAlive` removed, `VZEROUPPER` removed, the retire test loosened to `>=`, the zero-step guard removed.
  Report them as equivalent if they survive again.
- [ ] Do the assembly audit of "Memory and GC safety" and put its four answers in the report.
- [ ] Run `go vet ./...`, `GOARCH=arm64 go vet ./blob/ ./internal/...`, `make lint`, `make test`, `make fuzz`
  and `scripts/check-encoder-hotpath.sh`.
- [ ] Commit `perf(encoding): decode Chimp columns of several buffers together`, with the `arch` helper in it,
  and `test(encoding): fuzz the Chimp group decode`.

Nothing calls the group decode from `blob` yet, so no acceptance timing belongs to this task.

**Stop and report if** the kernel cannot be written without a `CALL` or without holding the bases as pointers,
or if any test needs `DecodeAll` edited.

### Task 2: Group the members in `materializeMetricCore`

**Files:**
- Modify: `blob/numeric_blob_set_material.go`, `blob/numeric_blob_set_material_test.go`,
  `blob/numeric_blob_set_material_bench_test.go` (setup and reported metrics only)

**Reference:** `referenceMaterializeMetric` from Task 0.

**Source to port:** `blob-prototypes/decode/blob/zz_chimp_lanes.go.txt`,
and the eleven changed lines of `numeric_blob_set_material.go` in `changes.patch`.
The prototype moves a member's values down with `copy` after a short member; the design does not.
It clears and decodes again, as "Blob layer" and "Exactness" say, and the design wins.

- [ ] Extend the Task 0 helper first.
  Every fixture now compares the reference with the public methods twice, with `chimpGroupDecode` off and with it on,
  by name and by ID each time.
  With the switch on it collects columns, steps and calls through `chimpGroupObserve`.
- [ ] Pin the engagement for every Task 0 fixture, as the prototype's tests do:
  the exact columns and steps the kernel takes.
  Examples from the prototype: 16 blobs of 30 dense points give 16 columns and 2×29 steps;
  a metric whose column ends its payload gives 0 and 0; 1 and 3 blobs give 0.
  For the two review fixtures, pin that the kernel took columns before the damaged member,
  so the fixtures pass through the clearing and not around it.
  Add the fixture the prototype has as `TestChimpLanes_GuardPage`:
  `fuzzGuardedCopy` moves every member's value payload so that it ends at an inaccessible page.
  That helper reports false where guard pages are unavailable, and the test then skips.
  Add a member whose payload has capacity past its length: its last column must stay out of the kernel.
  Add 400 random sets of 1 to 30 members:
  every encoding and variant, metrics missing at random, counts 1 to 45,
  and in every third set damage to a member of any encoding, value, timestamp or tag side.
- [ ] Add `chimpGroupDecode`, `chimpGroupObserve` and `materializeMembersGrouped`, and the Chimp member count in step 1.
  The baseline loop stays textually as it is inside the `else`.
- [ ] Add a test that materializes concurrently from eight goroutines on one set under the race detector,
  each result compared with the reference.
- [ ] Add `TestNumericBlobSet_MaterializeMetric_ChimpGroupFuzzSeeds`:
  over the seed corpus of `FuzzNumericBlobSetMaterializeMetric`, the kernel takes columns in at least one seed,
  and at least one seed reaches the clearing after a short member.
- [ ] In the benchmark, count the kernel's columns once per row, before the timed loop, through `chimpGroupObserve`.
  Report the share as `kernel-columns/column`.
  Fail the `consumer` row when the kernel is available and the share is 0.
  The timed loop of no row is edited, in this task or any later one.
- [ ] Assert the allocation contract:
  the same allocations per call as the reference on the `consumer` fixture, switch on and off,
  with the `raceEnabled` skip the package's other allocation assertions use.
- [ ] Mutation checks, against `go test ./blob/ -run 'MaterializeMetric'`:
  - no clearing after a short member (the review's fixture must fail);
  - clearing only up to the next member's planned end;
  - the rest of the group keeps its lanes after a short member;
  - a group formed while `len(values) < written` (the fixture across groups must fail);
  - `written` not raised for a member decoded in its turn;
  - the lane check dropped for the value range (the two-fault fixture must show the other panic);
  - the group check dropped (the nine-member fixture must panic with the bound 64 instead of 49);
  - the group check written as a sum, `end + Count <= cap(values)` (the overflow fixture must fail);
  - the group check without `Count >= 0`;
  - a group that failed its check cleared on its first short member;
  - planned offsets skip members of other encodings; Gorilla members given a lane;
  - the payload passed with its capacity as length; `Produced` replaced by the count;
  - a group's lanes not reset before the next group; a destination one value early;
  - the observe hook reports nothing; the Chimp member count ignores `resolve`;
  - the grouped loop taken with the switch off.
- [ ] Run `make lint`, `make test`, `make fuzz`.
- [ ] The orchestrator measures every Task 0 row against `B0` and applies the two Task 2 rows of "Task gates".
  The rows that only gate later are recorded here:
  `nearconst`, `constant` and the `mixed=` rows at Task 4, the `blobs=` rows from 4 up and `tags` at Task 7,
  because the thresholds are still the prototype's guesses.
- [ ] Commit `perf(blob): decode a metric's Chimp columns across blobs in groups`.

**Stop and report if** any fixture differs from the reference, in any element of any slice or in a panic value.

### Task 2, continued: Groups that cannot enter the kernel

Added on 2026-11-14, after Task 2 was measured and reviewed; see "Results".
It keeps its place between Task 2 and Task 3 so that the other tasks keep their numbers.

**Open item:** the grouped loop learns too late that a group cannot use the kernel:
only after it has collected eight members, checked the group, filled eight lanes and called `Decode`.
Then every column is decoded by the scalar loop, ahead of its turn.
Against `B0` that costs 10% on `nearconst` and `constant` (density under the threshold),
6% merged on `mixed=7of8` (one Chimp member in eight) and 4% on `lastcolumn` (no read-ahead).
None of these rows has a column in the kernel, so no threshold of Task 4 can bring them inside their limits.

**Files:**
- Modify: `blob/numeric_blob_set_material.go`, `blob/numeric_blob_set_material_test.go`,
  `blob/numeric_blob_set_material_bench_test.go` (outside the timed loops only);
  `internal/encoding/value/chimp/chimp_group.go`, `internal/encoding/facade.go` and their tests,
  only for an exported way to ask whether a set of columns would enter the kernel

**Reference:** `referenceMaterializeMetric`.

**Rule that keeps the result exact:** declining to decode ahead is always exact,
as long as it is decided before any lane writes to a destination,
or after the group in hand has been consumed or rolled back.
A member that is not decoded ahead is appended by the baseline's statements, which is what the reference does.
So every change of this task only moves the point at which the loop stops speculating; it adds no new way to write.

- [ ] **Profile first.**
  With `perf` on the `nearconst`, `mixed=7of8` and `lastcolumn` rows, at the head and at `B0`,
  say where the extra time per member goes:
  collecting the group, the group check, filling lanes, the bookkeeping and admission inside `Decode`,
  the scalar decode ahead, the append bookkeeping (`ahead`, `written`), the frame.
  The report gives the breakdown; the changes below are tried in the order the profile supports.
- [ ] **Decide before any lane is filled.**
  While a group is collected, decide from the index entries and the payload lengths alone
  (encoding, `Count`, `Off`, `Len`, `len(payload)`; no payload byte is read and nothing is sliced)
  whether the group would enter the kernel: the lane eligibility, the lane minimum and the density rule of "Group decode".
  The rule lives in one place, the chimp package, and the blob layer asks it; it is not copied.
  It uses the same constants as `Decode`, so Task 4 moves both together.
  It may answer "would not enter" for a group that would, on arithmetic it cannot bound.
  An answer of "would enter" is never trusted alone: `Decode` keeps every check it has and checks again.
  A group that would not enter is appended member by member with the baseline's statements, with no lane and no `Decode`.
- [ ] **Stop looking after a group that cannot enter**, if the first change is not enough.
  After a group that would not enter the kernel, the rest of the call appends members one by one and forms no group.
  A set whose first group is sparse and whose later groups are dense then runs at the baseline's speed;
  that is the price, and the report says how the fixtures and benchmark rows are affected.
  A softer form (look again after a number of members) is tried only if a benchmark row needs it.
- [ ] **Make the one-by-one path cheap.**
  A member appended without a group should cost what it costs in the baseline loop:
  no copy of its index entry into the group's arrays, no second pass.
- [ ] **Count the kernel's share after the timed loop, not before it.**
  Task 2 materializes one period before the timed loop to report `kernel-columns/column`; `B0` does not,
  so the two sides of a comparison with `B0` start their timed loops with different heaps and caches.
  Move that pass, and the check that fails the `consumer` row at a share of 0, behind the timed loop.
  The timed loop itself stays byte for byte as it is.
- [ ] Move `materializeMembersGrouped` and `decodeChimpGroupAhead` to the places `.agents/rules/file-organization.md` gives them.
- [ ] Tests.
  Every fixture still equals the reference, group decode off and on.
  Re-derive the engagement pins that move, and pin for `nearconst`-like, `mixed=7of8`-like and last-column fixtures
  that no lane was filled: the observation hook reports the groups asked and the groups decoded ahead.
  Add fixtures where the decision changes inside a call:
  sparse groups then dense ones, dense then sparse, a group that would enter followed by a short member and a rollback,
  and a group one lane or one bit short of entering on each rule.
- [ ] Mutation checks, against `go test ./blob/ -run 'MaterializeMetric'` and the chimp package's group tests:
  the early decision always answers "would enter"; always "would not"; ignores read-ahead; ignores density;
  counts lanes of other encodings; the stop after a sparse group also skips a group already decoded ahead;
  the early decision made after lanes were filled, with their writes left in place.
- [ ] Run `go vet ./...`, `GOARCH=arm64 go vet ./blob/ ./internal/...`, `make lint`, `make test`, `make fuzz`,
  and `scripts/check-encoder-hotpath.sh` if the chimp package changed.
- [ ] The orchestrator measures every Task 0 row against `B0`.
  The task passes when `nearconst` and `constant` are inside limit 5% and within 3% merged,
  `mixed=7of8` and `lastcolumn` inside limit 5%, the Task 2 requirements still hold,
  and `alprle`, `blobs=1`, `2`, `3` and the two gate-off rows stay inside their limits, with allocations equal.
  A row still over its limit after both changes goes to the owner with the numbers.
- [ ] Commit `perf(blob): leave groups that cannot use the Chimp kernel alone`.

**Stop and report if** any fixture differs from the reference, or the early decision cannot be made without reading a payload.

### Task 3: The window load

**Open item:** the kernel loads each lane's window with two `VPGATHERQQ` and is bound by their latency,
about 82 cycles a step at 2.3 instructions per cycle.
A variant with scalar loads was sketched and not built.
Gathers are also the instruction whose cost differs most between CPUs (see Risks).
The decision is taken from this machine's measurements (owner, 2026-11-13).

**Files:**
- Modify: `chimp_decsimd_amd64.s`, `chimp_decsimd_amd64.go`, `chimp_group_bench_test.go`, the kernel tests;
  for the selector and its assertions, and only until the decision:
  `chimp_decsimd_noasm.go`, `internal/encoding/facade.go`, `blob/numeric_blob_set_material_bench_test.go`

**Reference:** `DecodeAll`, and the Task 1 kernel, which stays in the file until the decision.

- [ ] Add a second kernel beside the first, same signature and contract.
  Only the window load differs: move the eight byte addresses out of the vector (to registers, or to a 64-byte frame),
  load 16 bytes per lane with eight `VMOVDQU`, build two 512-bit vectors with `VINSERTI64X2`,
  and split low and high qwords with two `VPERMT2Q`.
  With a frame the kernel is `NOSPLIT, $64-32`, as `alpEFSearchAVX512` already has a frame;
  the addresses in it are integers the runtime never scans,
  because an assembly frame has no pointer map and the function has no safe point.
  Restate the safety comment for the variant.
- [ ] Run every Task 1 kernel test over both kernels (a table over the kernel function), the guard page test included.
- [ ] Add a temporary selector for the measurement.
  `chimp_decsimd_amd64.go` tests an unexported flag and calls either kernel directly,
  so both sides pay the same test.
  The first text of this step asked for a function variable.
  It was changed on 2026-11-14, when the task was implemented: an argument of an indirect call escapes,
  which moved the caller's `ColumnGroup` to the heap (3 allocations per operation where the baseline has 2).
  Its package initialisation reads the environment variable `MEBO_CHIMP_GROUP_KERNEL`:
  `gather`, the default, or `loads`; any other value panics.
  A temporary exported `GroupKernelName()` returns the choice,
  and the facade passes it on as `ienc.ChimpGroupKernelName()`.
  The variable is read by the package, not by a test, so it selects the kernel in the blob test binary too.
- [ ] Add a setup assertion, outside the timed loops,
  to `BenchmarkChimpGroup` and to `BenchmarkNumericBlobSet_MaterializeMetricByName`:
  the row logs the kernel's name, and fails when the variable is set and the name differs from it.
- [ ] Mutation checks for the new kernel: one lane's load reads another lane's address; the high and low qwords swapped;
  one lane loads 8 bytes; the addresses read from the frame after `pos` advanced.
- [ ] Do the assembly audit for both kernels.
- [ ] Commit both kernels and the selector as a measurement commit:
  `perf(encoding): add a scalar-load Chimp group kernel`.
  The orchestrator archives that one commit for both sides.
  The two sides differ only in the environment of "Measurement procedure":
  `MEBO_CHIMP_GROUP_KERNEL=gather` on one, `MEBO_CHIMP_GROUP_KERNEL=loads` on the other.
  The driver checks the logged name in every output.
- [ ] The orchestrator measures kernel against kernel and applies the Task 3 row of "Task gates".
- [ ] **Decision rule.**
  The scalar-load kernel replaces the gather kernel when the gate passes.
  Then a second commit deletes the gather kernel, the selector, the name functions, the two setup assertions
  and the test table: `perf(encoding): load Chimp group windows without gathers`.
  Otherwise the gather kernel stays, and the measurement commit is reverted,
  which leaves the head at the last accepted commit.
  The result is recorded under "Results".
- [ ] **Check the removal**, whichever way the decision went.
  The search covers Go and assembly files, tests included, under `internal/` and `blob/`;
  this plan and the review records name the selector and are not part of it.
  This command must print nothing:
  `grep -rnE --include='*.go' --include='*.s' 'MEBO_CHIMP_GROUP_KERNEL|GroupKernelName|chimpGroupKernels|chimpGroupKernelLoads|SelectKernel|chimpGroupEachKernel|chimpGroupBenchKernel' internal/ blob/`.
  Its patterns cover the variable, `GroupKernelName`, `ChimpGroupKernelName`, `chimpGroupKernelName`, `chimpGroupKernels`,
  `chimpGroupKernelLoads`, `chimpGroupSelectKernel`, its test, and the two test helpers that walk the kernels.
  Read `chimp_decsimd_amd64.go` and confirm that `chimpGroupKernel` again calls the surviving assembly kernel directly,
  with no flag and no package initialisation left from the selector.
  When the gather kernel stays, `chimpGroupFirstQwords` goes with the scalar-load kernel.
- [ ] Run `go vet ./...`, `make lint`, `make test`, `make fuzz` and `scripts/check-encoder-hotpath.sh`,
  on the measurement commit and again on the final state.

**Stop and report if** the variant needs a `CALL`, a pointer in its frame, or more than the 16 bytes of read-ahead.

### Task 4: Measure the thresholds

**Open item:** a group goes scalar under 12 bits per value or with fewer than 4 eligible lanes,
and the rounds end with fewer than 4 live lanes.
All three figures are guesses.
The kernel pays one step per value whatever the value is, and a step costs the same for one lane or eight.
Entering the kernel also pays for setting a group up; continuing does not.

**Files:**
- Modify: `chimp_group.go` (the three constants), `chimp_group_bench_test.go`, `chimp_group_test.go`,
  `blob/numeric_blob_set_material_test.go` (engagement pins that move)

**Reference:** `DecodeAll`.

- [ ] Add `BenchmarkChimpGroupSweep` with two families of cells, each over at least 960,000 distinct points.
  Both report `ns/column`, and both have two sides per cell, through the unexported method.
  A side is a complete tuple of the three thresholds: entry minimum, continuation minimum, bit threshold.
  - **Entry cells.**
    Groups of eight columns, all of 35 points, so no lane completes before the others.
    The first axis is the share of values that change: 0, 2, 5, 10, 15, 20, 25, 33, 50 and 100%,
    from a two-decimal gauge that holds its value the rest of the time.
    The second axis is the number of eligible lanes, 1 to 8;
    the other lanes lack read-ahead and go to `DecodeAll` inside the same call.
    The forced side is (1, 1, 0): any eligible lane enters, and the rounds go on down to one live lane.
    The other side is (9, 9, 0): no group can enter, so every lane goes to `DecodeAll` through the same call.
    A cell also reports its bits per value after the first.
    At setup, every forced cell asserts that `Calls` and `Steps` are above zero,
    the cells of one, two and three lanes included.
  - **Continuation cells.**
    Groups of eight eligible columns in which every value changes.
    `8 - k` lanes have 35 points and `k` lanes have `35 + r` points.
    The axes are `k`, 1 to 7, and the residual length `r`: 4, 8, 16, 32 and 64.
    After the short lanes complete, `k` lanes remain with `r` values each.
    Both sides enter the same way and differ only in the continuation minimum.
    The continuing side is (1, 1, 0): the `k` lanes stay in the kernel.
    The hand-off side is (1, 8, 0):
    the rounds end as soon as fewer than eight lanes are live, and the `k` lanes are handed off.
    Both run the same kernel prefix of 34 steps with eight lanes.
    At setup each cell asserts it: the hand-off side completes 34 steps, and the continuing side `34 + r`.
- [ ] The orchestrator runs both families once from one binary to find the crossovers,
  then layout-averaged on the cells next to them.
- [ ] **Decision rule.**
  `chimpGroupMinBitsPerValue` is the lowest measured bits per value at which,
  with eight lanes, the kernel side of the entry cells is at least 10% faster in every layout.
  `GroupMinColumns` is the fewest lanes at which, on the entry cells where every value changes,
  the kernel side is at least 10% faster in every layout.
  `chimpGroupMinLive` is the fewest lanes `k` at which, for every residual length of 8 or more,
  continuing is at least 10% faster in every layout.
  Then the corner entry cell (`GroupMinColumns` lanes at the density of the bit threshold) is checked:
  if the kernel side is more than 5% slower there, the bit threshold is raised until it is not.
- [ ] Set the constants.
  Derive the test fixtures on both sides of each rule from the constants, not from literal numbers.
  Re-pin the engagement counts of Task 1 and Task 2 that move.
- [ ] The orchestrator applies the last Task 4 row of "Task gates"; the decision rule above is its first two rows.
- [ ] Mutation checks: each constant raised and lowered by one step of its sweep must fail an engagement test.
- [ ] Run `make lint`, `make test` and `scripts/check-encoder-hotpath.sh`.
  Commit `perf(encoding): set the Chimp group thresholds from measurements`.

**Stop and report if** a measured lane minimum is above 6 or the bit threshold is above 24:
the kernel is then much weaker than the prototype suggested, and the owner decides whether the plan goes on.

### Task 5: Keep the other lanes in the kernel after an early leave

**Open item:** one value the kernel cannot take sends every live lane of the group to the scalar loop,
for the rest of its column.

**Files:**
- Modify: `chimp_group.go`, `chimp_group_test.go`

**Reference:** `DecodeAll`.

- [ ] **Measure before any code.**
  Count three things on every benchmark fixture of Task 0, over the columns the kernel takes,
  with the classifier of Task 1 and a test-only counter on `ColumnGroup`:
  the group-wide rate of values the kernel does not take, among all values after the first of a group's columns,
  the same figure the cells of Task 1 report;
  the mean number of lanes per group that hold such a value;
  and, as their consequence, the share of values handed off after an early leave.
  One such value early in one lane can hand off almost all of a group, so the first and the third are far apart.
  The research saw no early leave on any of its 13 synthetic shapes, so all three are expected to be 0 on the mix.
- [ ] **Decision rule, part one:** the first Task 5 row of "Task gates".
  It reads the handed-off share, because that is the loss this task can win back.
- [ ] **Select the timed cell** among the `unsupported=` cells of Task 1, which exist in both revisions compared.
  Take the fixture with the largest handed-off share.
  The handed-off share picks that fixture and nothing else: it is not used as a replacement share or to pick a cell.
  The spread is `onelane` when the fixture's mean is under two lanes per group, and `alllanes` otherwise.
  Among the four cells of that spread, the cell is the one whose reported group-wide rate is the lowest
  that is not below the fixture's measured group-wide rate.
  If no cell of that spread reaches the fixture's rate, the `100%` cell of that spread is used,
  and the report states both rates.
  That cell is the most the spread can hold: every replaceable value of its lanes.
- [ ] If the task goes on, implement these transitions, with the kernel unchanged.
  After a round that returned fewer steps than asked, while at least `chimpGroupMinLive` lanes are live,
  give every live lane one scalar step:
  `decodeAllFrom` limited to one more value, which now also returns the state it stopped in
  (bit position, previous value, stored leading count).
  - A step that produced nothing makes the lane failed: `Produced` is set to the values it has, and the slot is dead.
  - A step that produced the lane's last value makes it complete.
  - Any other lane stays live, and its slot takes the returned state.

  Then the rounds go on.
  After two rounds in a row that complete no step, the rounds end and every live lane is handed off.
  A failed or complete lane is never resumed, whatever its mirrored slot holds.
- [ ] Tests, all through `TestChimpGroup_Differential`'s comparison, with pinned steps and calls:
  - one lane has a 64-bit payload at step k and the others are plain, for k at the start, the middle and the end;
  - two such lanes;
  - a lane that fails at that value, followed by at least three more kernel rounds of the other lanes;
  - a lane whose scalar step produces exactly its last value;
  - data where every value is wide.
- [ ] Mutation checks: the scalar step skipped for the lanes that could have gone on;
  the returned state dropped;
  a failed lane resumed from its slot at the end (the failed-lane test must fail);
  a lane completed by its scalar step kept live;
  the two-round stop removed (only the `unsupported=100%/alllanes` cell shows it, since no output changes).
- [ ] **Decision rule, part two:** the second Task 5 row of "Task gates".
- [ ] Run `make lint`, `make test`, `make fuzz` and `scripts/check-encoder-hotpath.sh`.
  Commit `perf(encoding): keep Chimp group lanes in the kernel past a wide value`.

### Task 6: Columns at the end of a payload

**Open item:** a column with fewer than 16 bytes of its payload after it is always scalar.
That is the last column of every payload, and also the one before it when the last column is shorter than 16 bytes.
A V2 encoder orders the columns by metric ID,
so in a set of like blobs it is the same metric in every blob, and that metric never reaches the kernel.

**Files:**
- Modify: `chimp_group.go` and, for candidate (a), `chimp_decsimd_amd64.s`; the tests of both packages

**Reference:** `DecodeAll` and `referenceMaterializeMetric`.

- [ ] **Estimate the gain before any code.**
  If every metric is materialized equally often and costs the same,
  the affected share is one metric in the number per blob: 1% at 100 metrics, 0.5% at 200.
  At the prototype's 36% per metric that is under half a percent of the materialize time.
  This is a uniform-access estimate, not a bound:
  a consumer that reads the last metric most of the time gains on most of its calls.
- [ ] **Decision rule, part one:** the first Task 6 row of "Task gates".
  The task is dropped: the owner decided on 2026-11-13 not to do it (owner decision 4).
- [ ] Otherwise build one of:
  - (a) **a read limit per lane.**
    Let `avail` be `len(Payload) - Off`, the bytes of the payload from the column's base, a signed `int`.
    A lane may enter the kernel when the checks of Task 1 other than the read-ahead hold
    (`Count >= 2`, the destination, `Off >= 0`, `Off <= len(Payload)`, `Len >= 8`, `Len <= avail`)
    and `avail >= 24`: the first load, at position 64, reads bytes 8 to 23 from the base.
    A lane with fewer than 24 bytes from its base stays scalar, however valid its column is.
    Only for a lane that passed, the state gains its `limit`, a `uint64`:
    `uint64(avail-16)*8 + 7`, the highest bit position from which the 16-byte load stays inside the payload.
    The subtraction is done in `int` after the check, so it cannot go negative,
    and the multiplication is done in `uint64`, so it cannot overflow on any platform's `int`, whatever the build tag.
    The kernel gains a fifth reason to leave: a lane's `pos` is above its `limit`.
    Unlike the other four it is tested at the top of a step, before either load of that step.
    After any round that returned fewer steps than asked, Go finds the read-limited lanes itself:
    the live lanes whose `pos` is above their `limit`.
    Each is handed off at once, from its own slot, and its slot is left as it is.
    Mirroring is not done here: it is step 2 of the next round, which runs only when enough lanes are still live,
    so there is always a live lane to mirror.
    If too few remain, the rounds end,
    and the last read-limited lane is handed off like the others, with no mirroring at all.
    If at least one lane was read-limited, the rounds go on with the others, with no scalar step.
    If none was, the leave is a value the kernel cannot take, and the rules of Task 1 or Task 5 apply.
    This needs nothing from Task 5.
    It costs one more compare per step.
  - (b) **a padded copy.**
    `ColumnGroup` gains eight scratch buffers of 512 bytes, one per lane.
    A column that lacks read-ahead and is at most 496 bytes long is copied into its lane's buffer,
    followed by 16 zero bytes, and the lane's base points into that buffer for the whole `Decode`.
    Each lane has a buffer of its own, so two copied columns of one group never share storage.
    It costs the copy and 4 KB more stack per call, and changes no assembly.
  - Not allowed: reading past `len(Payload)` into its capacity or up to the page's end.
    The ALP kernel's page cap (`alpDecPageSize`) only bounds where the masked-off bytes of a masked load fall;
    it never reads past the slice.
- [ ] Tests.
  New fixtures in both packages whose last column has at least two points:
  it enters the kernel, and no read passes the payload.
  The existing guard page fixtures whose last columns hold one point keep their scalar expectation:
  one point never enters the kernel.
  The column 8 bytes before the end; a payload that is one column.
  For (a), guard page groups of seven dense, longer lanes and one lane whose payload ends at the guarded page,
  with 8, 9, 15, 16, 23 and 24 bytes from its base: only the last enters the kernel, and none faults.
  The 9-byte case is a valid two-point column alone in its payload.
  For (a) also: a read-limited lane followed by at least three more rounds of the other lanes,
  and a group in which the last live lanes become read-limited in the same round.
- [ ] Mutation checks: the limit one byte late (a guard page test must fault);
  the `avail >= 24` check lowered to 23:
  no load passes the payload, because the limit is then 63 and position 64 is refused before either load,
  so the mutation must fail the pinned `Columns` of the 23-byte case, not fault;
  the `avail >= 24` check removed (the 8-, 9- and 15-byte cases must fault: the limit wraps);
  the limit computed before the check while the check stays, which may be equivalent and is then reported as such;
  the limit tested after the first load;
  a handed-off lane's slot mirrored at once, with no live lane left;
  a read-limited lane resumed from a mirrored slot; the scratch buffer one byte short; the copy bound ignored;
  two lanes given the same buffer.
- [ ] Do the assembly audit when (a) is built.
- [ ] **Decision rule, part two:** the second Task 6 row of "Task gates".
- [ ] Run `go vet ./...`, `make lint`, `make test`, `make fuzz` and `scripts/check-encoder-hotpath.sh`.
  Commit `perf(encoding): decode a payload's last Chimp column in the kernel`.

### Task 7: The cost per call, and short sets

**Open item:** a `ColumnGroup` is about 5 KB (64 rows of eight words, the state, the lane slices),
zeroed on the stack once per materialized metric.
Over 1,000 blobs that is nothing; over 4 to 16 blobs it may not be.
Task 2 already keeps sets with fewer Chimp members than `GroupMinColumns` on the baseline loop.

**Files:**
- Modify: `chimp_group.go` or `blob/numeric_blob_set_material.go`, and their tests, only if the measurement asks for it

**Reference:** `referenceMaterializeMetric`.

- [ ] **Measure before any code.**
  The orchestrator applies the Task 7 row of "Task gates" at the head.
- [ ] **Decision rule.**
  If every row is within its limit, no code changes; record the numbers.
  Otherwise try these in order.
  Keep the first that brings the failing rows inside their limits against `B0`
  and leaves `consumer` within 2% merged of the last accepted commit, the head before this task.
  `B0` stays the reference for every limit; the last accepted commit is used for this one stability check only.
  1. fewer scratch rows: the smallest of 40 and 48 that leaves `consumer` unchanged (its columns need at most 39 steps);
  2. a higher member count before the grouped loop is taken, measured from the `blobs=` rows;
  3. the scratch rows moved out of `ColumnGroup` into a buffer the caller owns.
- [ ] Mutation check for whichever change is kept.
  A different number of scratch rows changes how the steps are split into kernel calls, not their total:
  pin `Calls` on a column that needs one step more than the rows, and the bound off by one must change it.
  A different member count must fail an engagement pin of the blob tests.
- [ ] Run `make lint`, `make test`, and `scripts/check-encoder-hotpath.sh` if the chimp package changed.
  Commit `perf(blob): bound the fixed cost of grouped Chimp decode`, or no commit.

### Task 8: Close the coverage gaps of the prototype's review

**Files:**
- Modify: `blob/numeric_blob_set_material_test.go`,
  `internal/encoding/value/chimp/chimp_group_test.go`, `.github/workflows/ci.yaml`

**Reference:** `referenceMaterializeMetric` and `DecodeAll`.
This task runs last among the code tasks so that it tests the kernel and the grouping in their final form.
It changes no production code; a defect it finds is fixed in a commit of its own and reviewed.

- [ ] **Timestamp damage.**
  With the group's Chimp columns whole, compare with the reference:
  - a member whose own timestamp column decodes to fewer than `Count`, for Raw, Delta and DeltaPacked,
    as the first, a middle and the last member of a group of eight, and in two members of one group;
  - a shared group that decodes to fewer than `Count`;
  - timestamp bytes flipped through the public API, where `Decode` accepts the blob;
  - a member short in timestamps followed by a member short in values, and the reverse;
  - the same with a non-Chimp member between them.
  Each fixture pins that the kernel took columns,
  so the clearing after a member trimmed by its timestamps is exercised, not only decoded around.
- [ ] **Tag damage.**
  A tagged member whose tag range is outside the tag payload (it yields no row);
  one whose tag column decodes to fewer than `Count`;
  a set that mixes tagged and untagged members, where the untagged ones are padded by the values produced;
  each combined with a short value column in the same group.
- [ ] **Panics.**
  Every crafted set that panics in the reference panics in the grouped loop with an equal panic value, type and message.
  One faulty member:
  a timestamp range outside its payload, a value range outside its payload, an ALP column cut in memory.
  One member with two faults, in each combination of those, with different bounds.
  Two faulty members in one group, the later one a Chimp member that would have had a lane.
- [ ] **GC and stack moves**, in `chimp_group_test.go`:
  - eight columns of more than 64 values are copied into an array on the test goroutine's stack,
    so the bases point into the stack and stay in use across several kernel calls of one `Decode`;
  - before each `Decode` the goroutine recurses deep and returns, so its stack has grown and is due to shrink;
  - other goroutines loop over `runtime.GC()` and allocate, with `debug.SetGCPercent(1)`;
  - every result is compared with `DecodeAll` on heap copies of the columns;
    several thousand iterations, fewer under `-short`.
  A second variant uses heap payloads that nothing but the group references.
  Run it under the race detector as well.
  The report says whether `-gcflags=-m` shows the array staying on the stack.
  This test is supplementary: it cannot show that a stack moved between two kernel calls.
- [ ] **A kernel that is idle must fail.**
  Every test that pins columns or steps fails, not skips, on a CPU without the kernel when `MEBO_REQUIRE_AVX512=1`,
  in the chimp package and in `blob`.
  Extend the AVX-512 step of `ci.yaml`: when the runner has `avx512dq` and `avx512bw`, run
  `MEBO_REQUIRE_AVX512=1 go test ./internal/encoding/value/chimp/ ./blob/ -count=1 -run 'ChimpGroup'`.
  Name the tests so that pattern selects them (`TestChimpGroup_…`, `TestNumericBlobSet_MaterializeMetric_ChimpGroup…`).
  Task 2 already makes the benchmark's `consumer` row fail when the kernel is available and takes no column.
- [ ] **Go 1.25.**
  `go.mod` allows Go 1.25.
  Run the tests of this plan in both packages, the stress test included, under a Go 1.25 toolchain on this machine,
  where the kernel runs: a CI runner may lack AVX-512.
  This machine has no 1.25 toolchain in its module cache on 2026-11-13, so that needs a download.
  If none can be had, the read of the 1.25.0 source by this plan's first review stands, and the report says so.
- [ ] Mutation checks:
  no clearing after a member trimmed by its timestamps; tag padding by `Count`;
  `chimpGroupDecode` forced false with `MEBO_REQUIRE_AVX512=1` (the engagement tests must fail);
  `base` as `uintptr` (the layout test fails; report whether the stress test also does).
  The stress test is not expected to notice a missing `runtime.KeepAlive`:
  the bases in the state keep the payloads alive by themselves.
- [ ] Run `make lint`, `make test`, `make fuzz` and `scripts/check-encoder-hotpath.sh`.
  Commit `test(blob): cover damaged timestamps and tags in grouped decode`,
  `test(encoding): stress the Chimp group kernel under GC and stack moves`,
  and `ci: require the Chimp group kernel where the runner has AVX-512`.

- [ ] Added after the review of Task 2: the 400 random sets schedule damage in 134 of their rounds and change an input in 107;
  the changed members are Chimp and ALP-RLE only.
  Add a small fixed matrix that damages a member of every encoding on every side that applies
  (value, timestamp, tag), in a group of eight, and count what was really changed:
  the test fails if an encoding or a side went unchanged.

### Task 9: Final measurement, changelog and release preparation

- [ ] The orchestrator runs every Task 0 row against `B0`, and the two rows with the gate off,
  and applies "Acceptance thresholds".
  Fill "Results" below, with all layout medians and both commit IDs.
- [ ] Run `scripts/check-encoder-hotpath.sh`, `make lint`, `make test` and `make fuzz` on the branch head.
- [ ] Repeat Task 3's removal check on the branch head, the final candidate: the same search prints nothing,
  and `chimpGroupKernel` calls its assembly kernel directly.
- [ ] Run `tests/compat/run_compat.sh v1.14.0 <branch>` and `tests/compat/run_compat.sh v1.10.0 <branch>`:
  no byte the branch writes changed, so both must pass as they do on `main`.
- [ ] `tests/measurev2` times encode, decode, iteration, `ValueAt` and `TimestampAt` on one blob.
  None of that code is changed, and the report's one `Materialize` row is the blob-level path, a non-goal.
  The report is not regenerated for this change.
  Its numbers can still move with code placement at its next regular regeneration; this plan does not measure that.
- [ ] In `blob/blob_set.go`, the godoc of `MaterializeNumericMetric` and of `MaterializeNumericMetricByName` gives a cost:
  "about 2–5 ns per point without tags".
  Check that figure against the measured rows and correct it if it is off.
- [ ] Add the CHANGELOG entry under `[Unreleased]`, `### Performance`:
  what got faster, by how much, on which data and CPU, measured how;
  that the data is synthetic and the thresholds were set from it;
  that it needs AVX-512 F, BW and DQ; the `GODEBUG` switch;
  that machines without those instructions run the code they ran before.
- [ ] Squash by scope, open the pull request with the baseline and the results,
  get the external review, and ask the owner before merging.
- [ ] Release: a patch release, `v1.14.2` (owner decision 3).
  Tagging and publishing need the owner's go-ahead at that point.
- [ ] **Optional, not a gate.**
  The consumer may run the benchmark binary of Task 0, or its own tests, on one of its machines.
  A result from there is recorded under "Results"; nothing in this plan waits for it.

### Task 10: Look again after a group that stays out

Added on 2026-11-14, after Tasks 0 to 9 were merged, on the owner's instruction of that day:
the softer form of "Task 2, continued" is wanted before the release is tagged, as a pull request of its own.
The reason is the third open question: a metric that is quiet for one group of blobs loses the kernel for the rest of the call,
and the release is what the consumer measures Chimp with.

**The change.**
After a group that would not enter the kernel, the next `chimpGroupRetryAfter` contributing members are appended in turn,
by `appendMembersInTurn`, and then groups are formed again.
The value is 56: one group in 64 members is asked while a call stays sparse.
"Task 2, continued" counted 172 instructions per member over `B0` when every group is asked and 17 with the stop,
so about 17 + 155 × 8 / (8 + 56) = 36 are expected; this is checked in one binary before the layout runs.
`chimpGroupRetryAfter` is a package variable, so that tests can lower it.

**Exactness.**
`appendMembersInTurn` takes the number of members to append and returns the index of the next blob.
It also carries `written` forward with the statement of the grouped loop.
Today no group follows it, so a member that comes up short inside it leaves elements above the rows that nobody reads again.
Once groups form after it, a lane must not be planned over such elements:
the unused capacity would differ from the reference.

- [x] **The rows first, in a commit of their own**, with no production change.
  Two rows of 1,000 Chimp blobs whose metrics change kind with the blob:
  `quiet=start` (constant for the first 16 blobs, gauges after them) and
  `quiet=spells` (constant for 16 blobs of every 250, gauges between).
  The data of every existing row stays identical.
  Before the change, `kernel-columns/column` of `quiet=start` must read about 0, or the row does not exercise the stop.
- [x] The change, as above.
  The comments of `materializeMembersGrouped` and `appendMembersInTurn` say what is true afterwards.
- [x] Tests.
  Every fixture and the fuzz target equal the frozen reference with `chimpGroupRetryAfter` at 0, 1, 3 and 56, group decode off and on:
  the fuzz programs hold at most 24 members, so at 56 no group forms after a stretch.
  A fixture has a member that comes up short inside a stretch, followed by a dense group.
  The pins of groups asked and groups decoded ahead are derived again, not loosened.
  Mutation checks, each caught by a test:
  the `written` carry removed; the stretch one member too long and one too short;
  the next index returned off by one; the stop restored.
- [x] The orchestrator measures the commit of the change against the commit of the rows,
  so that both sides hold one benchmark file, and applies the gate below.
  It was written before any timing.

| Rows | Requirement | When it fails |
|---|---|---|
| `quiet=start`, `quiet=spells` | each improves by at least 20% merged; the kernel's share of columns rises | the change is dropped |
| `nearconst`, `constant`, `lastcolumn`, `mixed=7of8` | within 1.5% merged, limit 3% | double `chimpGroupRetryAfter` once and measure again; then the owner decides with the numbers |
| every other row, the two gate-off rows | within 1.5% merged, limit 3% | fix inside the task, or stop and report |
| allocations, every row | equal | fix inside the task |
| the control | within 3% merged in both cells | the run is invalid |

- [x] The external review of the change, by commit.
- [x] The changelog sentence about the stop, and its figures for the sets that stay on the old code, say what is true afterwards.
- [x] The push and the pull request are covered by the owner's instruction; the merge and the tag are asked for again.

---

## Acceptance thresholds

Read as "Measurement procedure" says, the branch head against `B0`.

| Row | Requirement |
|---|---|
| `consumer` | improves by at least 25% merged and at least 20% in every layout |
| `gauge`, `counter`, `fullprec` | each improves by at least 20% merged |
| `equalcounts`, `points=150` | each improves by at least 20% merged |
| `points=10` | limit 5%; improvement reported |
| `nearconst`, `constant` | limit 5%, and within 3% merged |
| `mixed=alternate`, `mixed=1of8`, `mixed=7of8` | limit 5%; improvement reported |
| `alprle` (no Chimp member) | limit 3% |
| `blobs=1`, `2`, `3` | limit 3% |
| `blobs=4`, `7`, `8`, `9`, `16`, `64` | limit 5%; improvement reported |
| `tags` | limit 5%; improvement reported |
| `lastcolumn` | limit 5% when Task 6 was dropped; improves by at least 20% in every layout when it was kept |
| `consumer` and `blobs=4` with `GODEBUG=cpu.avx512dq=off` on both sides | limit 3%, and within 2% merged |
| the control | within 3% merged in both cells, or the run is invalid |
| allocations and bytes per operation, every row | allocations equal; bytes equal, or within 0.01% on the two rows named below |

Bytes per operation is an average over the operations of a run.
At `B0` it already differs from one observation to the next on two rows, by a few bytes in more than half a million:
`lastcolumn` (572,346 to 572,349) and `mixed=7of8` (1,343,495 to 1,343,496).
This was seen in the baseline record of Task 0, before any comparison was run, and the 0.01% bound was set then.

The two gate-off rows are a measurement of the fallback on this machine, a Zen 5.
They say nothing about the speed of other CPUs or of arm64.
arm64 gets static validation only: its build has the stub and the baseline loop,
shown by `GOARCH=arm64 go vet` and by the build tags.

## Drop rules

- **The whole change.**
  If `consumer` improves by less than 15% merged at Task 2, stop and report:
  the kernel and the grouping are not merged, Task 0 is.
  From 15% up, a Task 2 that misses any of its requirements (the merged figure, a layout, a kind row) is provisional.
  Tasks 3 and 4 run, and the Task 2 requirements are read again after Task 4.
  If they still do not hold, the owner decides with the numbers.
- **A row over its limit.**
  Fix it, or route that shape to the baseline loop with a stated rule, and say which.
  A limit that cannot be met either way goes to the owner with the numbers.
- **A result that differs from the reference** is a defect, in any element or any panic value.
  If it cannot be fixed inside the design, the task is dropped.
- **Tasks 3, 5, 6 and 7** each carry their own rule and may end without a commit;
  Task 3 then ends with its measurement commit reverted.
  A dropped task leaves the head where it was, so the next task's "last accepted" is the head before the dropped one.
  None of the four depends on another.
- **Task 4** always ends with measured constants; it is not droppable.
- **The safety argument.**
  If any item of "What must stay true" cannot be kept, or an assembly audit cannot answer one of its four points,
  or the stress test fails in a way that is not a test defect, the change is not merged.

## Risks

| Risk | How it is detected, or that it is not |
|---|---|
| The runtime starts preempting assembly, or the collector starts moving heap objects, in a Go release added later | not automatically; adding a release needs a read of its runtime source, recorded in the kernel's comment; the tests run in CI on every release in the matrix, with the kernel only where the runner has AVX-512 |
| A later edit breaks an assembly invariant (a `CALL`, a lost `NOSPLIT`, a stored address) | the assembly audit of every task that touches assembly; review afterwards |
| A read past a payload | guard page tests in both packages; the read-ahead mutations of Task 1; no `unsafe` in the Go code |
| Gathers cost more on other CPUs: Intel parts with the gather data sampling mitigation, Zen 4 | not detected by this plan, which decides on this machine (owner, 2026-11-13); the optional consumer-side run of Task 9; `GODEBUG` turns the kernel off |
| 512-bit code lowers the core frequency on older Intel servers | not detected by this plan; the same optional run |
| Real data is easier to predict than the synthetic mix, so the scalar baseline is faster and the gain smaller | not detected by this plan (owner, 2026-11-13); the changelog says the data is synthetic |
| The kernel is silently idle: a CI runner without AVX-512, a gate broken by a refactor | pinned columns and steps; `MEBO_REQUIRE_AVX512=1` in CI where the runner has the instructions; the benchmark reports the kernel's share of columns |
| A layout effect is read as a gain or a loss | "Measurement procedure": four layouts, the placement rule fixed beforehand, the control's 3% bound |
| A non-goal path changes speed through code placement | not measured, except the `DecodeAll` control |
| `decodeAllFrom` drifts from `DecodeAll` | `TestChimpGroup_DecodeAllFrom` from every state; the fuzz target; `DecodeAll` is never edited by this plan |
| The assembly and the Go struct disagree on offsets | `go_asm.h` offsets, or the pinned `unsafe.Offsetof` test; `go vet` asmdecl |
| The grouped loop and the baseline loop drift apart in a later change to timestamps, tags or alignment | the frozen reference compares both paths on every fixture and in the fuzz target; both loops use the same member statements |
| A decoder reads its destination in a way "Exactness" did not foresee | the helper compares every element up to the capacity; the blob fuzz target damages members of every encoding |
| A 5 KB frame on a deep stack makes the stack grow | Task 7's short-set rows |
| Thresholds tuned on one CPU misroute groups on another | only time is at stake, never the result |

## Decisions

### Owner, 2026-11-13

1. **Plan and implement on the basis that the consumer may adopt Chimp.**
   The choice between ALP-RLE and Chimp is made later, from real measurements.
   No task waits for that choice.
2. **Thresholds are set from synthetic data.**
   No task is gated on real consumer blobs or on a run on a consumer machine.
   Such a run may follow as an optional check (Task 9).
   The window-load task is decided from this machine's measurements; the consumer's CPUs do not block it.
3. **The change ships in a patch release, `v1.14.2`,**
   together with the Chimp `At` entry already under `[Unreleased]`.
   Tagging and publishing still need the owner's go-ahead at that point.
4. **Task 6 is not done.**
   A column at the end of its payload stays scalar.
5. **The plan is approved for implementation as written,**
   after four external reviews.
   The owner agreed to each of the five choices under "Taken by this plan" below, the same day,
   and asked for the tasks to be carried out without a check-in between them.
   Pushing, the pull request, the merge and the tag still wait for the owner each time.

### Taken by this plan

1. **Exact equality, no accepted difference.**
   The grouped loop returns the baseline's result in every element, capacity included, and the baseline's panic.
   The first version of this plan asked to accept two differences; "Exactness" removes both.
2. **The off switch is `GODEBUG=cpu.avx512dq=off` and nothing else.**
   It is what the ALP kernels document, it adds no surface, and it also turns those kernels off.
   Pinning `v1.14.1` is the other way back.
3. **Scope.**
   `NumericBlobSet.Materialize` and `BlobSet.MaterializeNumeric` decode every metric of every member.
   They stay as they are.
   They could group columns too, within a payload or across blobs; that is decided after this change is measured.
4. **File layout.**
   `chimp_group.go` as a sub-component beside `chimp.go`, the kernel as a `chimp_decsimd_*` set, both after the ALP package,
   and no new file in `blob/`.
5. **`DecodeAll` is not edited.**
   The hand-off loop is a pinned copy, so no path outside this plan's scope gets new code.

### Open questions for the owner

The release kind and the last column were answered on 2026-11-13 (owner decisions 3 and 4).

One, open since 2026-11-14: **the rows of the baseline loop against `B0`.**
The final measurement of Task 9 has every row inside its limit, `alprle` included (+2.6% in its worst layout).
The stop is still the owner's to close: "Task gates" keeps a task that was sent to the owner unmerged until the owner answers.
The orchestrator recommends accepting the row as measured by Task 9, with its four readings on record
(+3.2%, +3.5%, +1.9% and +2.6% in the worst layout), and asks for that answer together with the go-ahead to merge,
and with it for the one byte per operation on `mixed=alternate`, a row the thresholds on bytes do not name.
**Answered on 2026-11-14: the owner accepts both**, the `alprle` row as measured by Task 9
and the one byte on `mixed=alternate`.
This question is closed; none of the four ways below is taken.
The text that follows is as it was written when `alprle` first failed.
`alprle` is over its limit of 3% in two acceptance readings (+3.2% and +3.5% in the worst layout),
and the comparison itself moves these rows by up to 4% with identical production code;
see "Task 2, continued" under "Results".
The answer applies to every row that runs the baseline loop:
`alprle`, `blobs=1`, `2`, `3`, the gate-off rows, `nearconst`, `constant`, `lastcolumn` and `mixed=7of8`.
The ways forward that were found:

1. **A separate function for sets with no Chimp blob.**
   `materializeMetricCore` gets the machine code of `B0` again.
   About 45 duplicated lines, a count of Chimp blobs kept per set, another review.
   The only figure for it is from one binary, and there `alprle` was still +1.4%.
2. **Pass the index entry by pointer through `resolve`.**
   It would remove the sensitivity to the stack position, if the inferred cause is the right one.
   It edits the baseline path, which this plan promised to leave alone, so it would be a plan of its own.
3. **Change how these rows are read:**
   compare with `B0` at an equal closure frame, or widen their limits to what identical code is seen to move.
   This changes a frozen procedure after a result was seen.
   At an equal closure frame `alprle` reads +1.1% merged and +1.9% in its worst layout.
4. **Accept the reading as it is**, +2.0% merged on a set with no Chimp member.

A second, open since 2026-11-14: **the shape of the density rule.**
Task 4 left the three constants where they were, as its rule says; see "Task 4" under "Results".
Its sweep suggests a rule on the bits a group carries per step (bits per value times eligible lanes, about 50)
in place of the bits per value alone, which would let full groups of eight in from about 7 bits per value.
That changes "Group eligibility", the forecast and the engagement pins, on the evidence of one family of synthetic values,
and none of the benchmark rows would move.
It is not done without the owner.

A third, recorded on 2026-11-14: **the stop after one group that stays out.**
"Task 2, continued" took the hard form of its second change: after the first group of a call that cannot enter the kernel,
the rest of that call is appended member by member.
The softer form, looking again after a number of members, was to be tried only if a benchmark row needed it,
and no row can: every fixture keeps one kind of value per metric across all its blobs, so none turns dense after a sparse group.
A real metric over hundreds of blobs can be quiet for eight of them, and then loses the kernel for the blobs behind.
The changelog says so.
Whether a row for that shape and the softer form are wanted is the owner's call.
**Answered on 2026-11-14: the owner wants both, before the tag**; see Task 10.

## Results

To be filled by the orchestrator: the table of the tasks, then the branch against `B0`,
each row with before, after, change and all eight layout medians, the control, and the raw output's location.

**Commit names.**
The branch was squashed by scope on 2026-11-14, before it was pushed, into six commits.
The hashes this plan cites are those of the commits before the squash, which are not in the published history.
The squash changed no file: the tree of the last commit before it and the tree after it are the same.
`B0`, `6b7cfc6`, is the commit "test(blob): benchmark, pin and fuzz single-metric set materialization" there;
the two differ in this plan alone, which the published commit does not hold yet.
The branch of Task 10 was tidied the same way before it was pushed:
`89cded7` and `8b23f91` are one commit there, "perf(blob): form Chimp groups again after a group that stays out",
and `0f7c97c`, which only set the stretch to 112 members for the second run, is not in it.

### Task 0

Commits `00f3272`, `aa6ac58`, `3ff20cb` and, after the external review (no P0, one P1, one P2, both fixed), `6b7cfc6`.
No production code changed.
All five mutation checks were caught.

The baseline record at `B0` = `6b7cfc6`, one side, four layouts, three rounds, twelve observations per row:

| Row | ns/op, merged median | ns/point | Layout spread |
|---|---|---|---|
| `consumer` | 259,376 | 7.42 | 0.8% |
| `gauge` | 308,936 | 8.83 | 0.6% |
| `counter` | 223,152 | 6.38 | 1.0% |
| `fullprec` | 302,909 | 8.66 | 1.3% |
| `nearconst` | 126,319 | 3.61 | 1.2% |
| `constant` | 96,940 | 2.77 | 1.4% |
| `lastcolumn` | 308,537 | 8.82 | 0.3% |
| `equalcounts` | 259,057 | 7.40 | 1.4% |
| `points=10` | 121,042 | 12.11 | 1.8% |
| `points=150` | 924,597 | 6.16 | 2.0% |
| `blobs=1` | 336.5 | 9.62 | 1.3% |
| `blobs=2` | 628.2 | 8.98 | 1.7% |
| `blobs=3` | 895.2 | 8.53 | 1.3% |
| `blobs=4` | 1,168 | 8.35 | 2.1% |
| `blobs=7` | 1,993 | 8.14 | 1.6% |
| `blobs=8` | 2,230 | 7.97 | 1.6% |
| `blobs=9` | 2,555 | 8.12 | 1.0% |
| `blobs=16` | 4,321 | 7.72 | 0.6% |
| `blobs=64` | 16,389 | 7.31 | 2.6% |
| `mixed=alternate` | 200,861 | 5.74 | 0.9% |
| `mixed=1of8` | 243,719 | 6.97 | 0.9% |
| `mixed=7of8` | 507,046 | 6.04 | 2.8% |
| `alprle` | 140,535 | 4.02 | 2.5% |
| `tags` | 491,607 | 14.05 | 0.5% |
| `consumer`, gate off | 259,504 | 7.42 | 0.9% |
| `blobs=4`, gate off | 1,181 | 8.44 | 1.3% |
| control, `fresh/n30` | 14,533 | 145.3 per column | 0.4% |
| control, `fresh/n150` | 68,911 | 689.1 per column | 0.4% |

`consumer` is 2.5% under the 266 µs the thresholds were derived from, so Task 0's gate passes.
No row is placement-sensitive at `B0`: the largest spread is 2.8%.
Every row allocates twice per operation, except `tags` with 1,003.
In the four layouts, the two movement-check symbols take residue 0 modulo 64 three times and residue 32 once,
which meets the rule of "Movement check" with nothing to spare.
The raw output is kept outside the repository, in the orchestrator's `tmp/chimp-kernel-impl/b0-baseline/`.

### Task 1

Commits `07c072d` and `046980c`, and after the external review `78d5b6c`.
`chimp.go` is unchanged since `280e06e`.

- Every mutation of the task's list was caught, except three of the four the prototype had found equivalent
  (`KeepAlive` removed, `VZEROUPPER` removed, the completion test loosened to `>=`), which survived again.
  The fourth, the zero-step guard removed, is now caught by a test that calls the kernel directly, `TestChimpGroup_KernelState`,
  which the task did not list.
- With read-ahead lowered to 15 and the test's column pins removed, the kernel faulted on the guard page.
- Assembly audit, GNU objdump 2.42 on `chimp.chimpGroupDecodeAVX512Asm.abi0`:
  98 instructions, all decoded, no `call`; `NOSPLIT`, frame size 0;
  five memory writes (the output row, `pos`, `prev`, `stored`, the return slot), none of them an address;
  the state's offsets come from `go_asm.h`.
- The unexported method that takes the three thresholds is `decodeWith`, since the linter rejects `decode` beside `Decode`.
  Its loop runs on `max(minLive, 1)` live lanes, so a threshold of zero cannot run the kernel on slots with no live source.
- The external review found no defect in the production code.
  It found one assertion that could not fail (base pointers compared by what they point to, fixed to compare identity
  and shown to catch a swapped base), a layout assertion that would fail on a 32-bit build (now amd64 only),
  and a missing godoc parameter.
- No acceptance timing belongs to this task.

### Task 2

Commit `53cf4d1`.
Every fixture equals the frozen reference with the group decode off and on.
All nineteen mutations of the task's list were caught, each in the way the task names where it names one
(the bound 64 for 49, the other panic of the two-fault member, the overflow fixtures).
One variant survived and is equivalent:
a group that failed its check has nothing decoded ahead, so clearing it up to its real end touches only zeros.

`53cf4d1` against `B0`, four layouts, three rounds, twelve observations per side.
The control moved by -1.9% and -2.0%, so the run is valid.
Allocations are equal on every row.
No row is placement-sensitive on either side.

| Row | `B0`, ns/op | Head, ns/op | Merged | Worst layout | Kernel share of columns |
|---|---|---|---|---|---|
| `consumer` | 259,227 | 180,239 | -30.5% | -30.0% | 0.81 |
| `gauge` | 306,997 | 187,696 | -38.9% | -38.3% | 0.99 |
| `counter` | 222,968 | 168,680 | -24.4% | -23.8% | 0.99 |
| `fullprec` | 302,681 | 210,852 | -30.3% | -30.2% | 0.99 |
| `equalcounts` | 259,443 | 162,228 | -37.5% | -37.2% | 0.81 |
| `points=10` | 120,818 | 90,207 | -25.3% | -24.9% | 0.81 |
| `points=150` | 921,876 | 594,028 | -35.6% | -34.9% | 0.81 |
| `nearconst` | 126,441 | 139,067 | +10.0% | +11.4% | 0 |
| `constant` | 96,792 | 106,357 | +9.9% | +10.4% | 0 |
| `lastcolumn` | 305,743 | 317,187 | +3.7% | +4.0% | 0 |
| `blobs=1` | 335.8 | 336.1 | +0.1% | +0.4% | 0 |
| `blobs=2` | 622.1 | 621.9 | 0.0% | +0.4% | 0 |
| `blobs=3` | 891.4 | 896.9 | +0.6% | +1.6% | 0 |
| `blobs=4` | 1,169 | 1,042 | -10.9% | -10.5% | 0.81 |
| `blobs=7` | 2,003 | 1,497 | -25.3% | -24.3% | 0.81 |
| `blobs=8` | 2,227 | 1,607 | -27.9% | -27.5% | 0.81 |
| `blobs=9` | 2,552 | 1,964 | -23.1% | -22.1% | 0.72 |
| `blobs=16` | 4,327 | 3,083 | -28.8% | -28.4% | 0.81 |
| `blobs=64` | 16,484 | 11,450 | -30.5% | -30.5% | 0.81 |
| `mixed=alternate` | 201,355 | 188,077 | -6.6% | -5.4% | 0.41 |
| `mixed=1of8` | 243,704 | 185,000 | -24.1% | -23.5% | 0.71 |
| `mixed=7of8` | 498,964 | 528,876 | +6.0% | +8.4% | 0 |
| `alprle` | 141,392 | 141,546 | +0.1% | +1.6% | 0 |
| `tags` | 490,352 | 431,389 | -12.0% | -10.9% | 0.81 |
| `consumer`, gate off | 259,776 | 260,031 | +0.1% | +0.9% | 0 |
| `blobs=4`, gate off | 1,185 | 1,186 | +0.1% | +0.6% | 0 |

"Worst layout" is the least favourable of the four ratios of layout medians.

Both Task 2 rows of "Task gates" pass, so the task is accepted and is not provisional:
`consumer` improves by 30.5% merged and 30.0% in its worst layout, and the three kind rows by 24% to 39% merged;
`alprle`, `blobs=1`, `2`, `3` and the two gate-off rows are inside their limits.

Recorded for the later gates:

- `nearconst`, `constant` and `mixed=7of8` are over their final limits, and no column of theirs enters the kernel.
  Their cost is not the kernel on sparse data, which the density rule already keeps out:
  it is the grouped loop itself, taken for every group that then goes scalar.
  Raising the bit threshold, the remedy the Task 4 gate names, cannot help these rows.
  See "Task 2, continued" below Task 2.
- `lastcolumn` is at +4.0% in its worst layout against a limit of 5%, for the same reason.
- Bytes per operation differ by one byte on `mixed=alternate` (573,446 against 573,445),
  a row "Acceptance thresholds" does not name.
  The figure is an average: a fixed amount allocated outside the timed call is divided by the number of operations,
  and the faster side runs more of them.
  This reading was made after the result was seen, and is reported to the owner as such.
- `materializeMetricCore` grew from a frame of 768 bytes to 824 and from 3,438 bytes of code to 3,694,
  more than the two tests "Global Constraints" speaks of: the member counter lives across the `resolve` call.
  The rows that measure it (`alprle`, `blobs=1` to `3`, gate off) moved by 0.0% to 0.6% merged.
- `materializeMembersGrouped` has a frame of 6,656 bytes, not the 5 KB of "Risks".
- The engagement pins hard-code today's thresholds; Task 4 re-derives them.

The raw output is in the orchestrator's `tmp/chimp-kernel-impl/t2-vs-b0/`.

The external review of `53cf4d1` found no P0 and no P1, and no way to make a result or a panic differ from the reference.
Its two P2 and one P3 are taken up in "Task 2, continued" (the benchmark's pass before the timed loop, the order of declarations)
and in Task 8 (what the random sets really damage).

### Task 2, continued

Commit `8d0b682`.
Every fixture equals the frozen reference with the group decode off and on.
All twenty mutation checks were caught.

What changed:

- The chimp package answers, from index entries and payload lengths alone, whether a group would enter the kernel
  (`GroupAdmission`, which shares the lane rule and the constants with `Decode`).
- After one group that would not enter, the call forms no further group
  and appends its members in turn (`appendMembersInTurn`).
  The early decision alone was not enough:
  counted in one binary, `nearconst` ran 327 instructions per member over `B0` at `53cf4d1`,
  172 with the early decision, and 17 with the stop; `constant` 335, 176 and 17.
- The benchmark counts the kernel's share behind the timed loop.

`8d0b682` against `B0`, four layouts, three rounds, twelve observations per side.
The control moved by -0.6% and -0.8%, so the run is valid.
Allocations are equal on every row;
bytes per operation differ by one byte on `mixed=alternate` and `mixed=1of8`,
the averaging effect recorded under Task 2.

| Row | `B0`, ns/op | Head, ns/op | Merged | Worst layout | Kernel share of columns |
|---|---|---|---|---|---|
| `consumer` | 260,612 | 179,668 | -31.1% | -30.2% | 0.81 |
| `gauge` | 307,159 | 189,735 | -38.2% | -37.9% | 0.99 |
| `counter` | 222,950 | 171,822 | -22.9% | -22.4% | 0.99 |
| `fullprec` | 301,919 | 213,830 | -29.2% | -28.3% | 0.99 |
| `equalcounts` | 258,304 | 163,368 | -36.8% | -36.4% | 0.81 |
| `points=10` | 121,553 | 91,129 | -25.0% | -24.0% | 0.81 |
| `points=150` | 921,250 | 594,945 | -35.4% | -34.8% | 0.81 |
| `nearconst` | 126,137 | 126,798 | +0.5% | +0.8% | 0 |
| `constant` | 96,384 | 96,975 | +0.6% | +2.4% | 0 |
| `lastcolumn` | 306,833 | 306,989 | +0.1% | +0.5% | 0 |
| `blobs=1` | 337.2 | 336.9 | -0.1% | +0.2% | 0 |
| `blobs=2` | 627.6 | 625.9 | -0.3% | +0.4% | 0 |
| `blobs=3` | 898.7 | 898.0 | -0.1% | +0.7% | 0 |
| `blobs=4` | 1,165 | 1,040 | -10.7% | -10.1% | 0.81 |
| `blobs=7` | 1,996 | 1,505 | -24.6% | -23.6% | 0.81 |
| `blobs=8` | 2,227 | 1,613 | -27.6% | -27.1% | 0.81 |
| `blobs=9` | 2,554 | 1,966 | -23.0% | -21.5% | 0.72 |
| `blobs=16` | 4,349 | 3,092 | -28.9% | -28.1% | 0.81 |
| `blobs=64` | 16,472 | 11,418 | -30.7% | -30.1% | 0.81 |
| `mixed=alternate` | 201,149 | 188,112 | -6.5% | -6.1% | 0.41 |
| `mixed=1of8` | 244,889 | 186,302 | -23.9% | -23.1% | 0.71 |
| `mixed=7of8` | 510,306 | 516,292 | +1.2% | +2.4% | 0 |
| `alprle` | 140,671 | 143,446 | +2.0% | **+3.2%** | 0 |
| `tags` | 501,242 | 436,586 | -12.9% | -10.1% | 0.81 |
| `consumer`, gate off | 260,416 | 261,068 | +0.3% | +0.1% | 0 |
| `blobs=4`, gate off | 1,186 | 1,194 | +0.7% | +1.9% | 0 |

- The four rows this task was written for are inside their limits:
  `nearconst` and `constant` (from +10%), `mixed=7of8` (from +6.0%), `lastcolumn` (from +3.7%).
  The Task 2 requirements still hold.
- `tags` is placement-sensitive on the `B0` side in this run (a layout spread of 13.6%; 0.5% in the baseline record).
  Its figures are the merged change and the worst layout, as the procedure says.
- **`alprle` is over its limit of 3%.**
  It was measured once more, as the procedure allows: +1.8% merged, +3.5% in the worst layout.
  The row has no Chimp member and runs the baseline loop, whose statements are unchanged.

#### What was looked at for `alprle`

None of the runs below is an acceptance reading.
Each used the frozen procedure on a subset of rows; the last four also changed the benchmark closure of one side.

| Old | New | `alprle` merged | `alprle` worst layout | Other rows |
|---|---|---|---|---|
| `53cf4d1` | `8d0b682` | +1.2% | +2.7% | |
| `B0` | `8d0b682`, gate off on both sides | +1.6% | +2.3% | `nearconst` no change, +2.2% worst |
| `B0` | `B0`, closure frame +32 bytes | no change | +0.6% | `nearconst`, `blobs=1` and gate-off `nearconst` no change |
| `B0`, closure frame +32 bytes | `8d0b682` | +2.5% | +4.8% | `nearconst` +2.0% worst |
| `B0` | `B0`, closure frame +56 bytes | +0.6% | +1.2% | `nearconst` +1.0%, +1.3% worst; gate-off `nearconst` +2.4%, +4.0% worst; `blobs=1` and `consumer` no change |
| `B0`, closure frame +56 bytes | `8d0b682` | +1.1% | +1.9% | `nearconst`, `blobs=1` and gate-off `nearconst` no change; `consumer` -30.6% |

The closure is the benchmark row's own function.
Its frame is 496 bytes at `B0` and 552 at `53cf4d1` and at `8d0b682`, because the row gained local variables.
A padding of unused words in the closure of the `B0` side gave the two other sizes:
three words add 32 bytes, six add 56.

Measured:

- The frame size of the benchmark closure alone moves rows of the baseline loop,
  with the production code identical on both sides:
  56 bytes more cost `alprle` 0.6% and gate-off `nearconst` 2.4% merged, 4.0% in its worst layout;
  32 bytes more cost nothing.
  So a comparison with `B0` carries a harness effect on these rows that is as large as their limits.
- With the two closures at the same frame size, `alprle` is +1.1% merged and +1.9% in its worst layout, inside its limit.
  The two parts add up to the +1.8% to +2.0% of the acceptance readings.
- That +1.1% is the remainder at an equal closure frame, not the isolated cost of the production change:
  the frame of `materializeMetricCore` itself grew by 56 bytes (760 to 816),
  so the baseline loop's locals still sit differently.
- Counted in one binary, `alprle` runs 17 instructions per member more than at `B0`.

Not established:

- Why the stack position matters.
  The task's agent attributes it to the 64-byte index entry that `resolve` returns by value:
  copied with 16-byte moves, it crosses a cache line when the frame sits at 8 modulo 16.
  This is inferred; the agent's counters did not separate the two positions by line-crossing loads.
- The +1.2% from `53cf4d1` to `8d0b682`.
  Both have the same closure frame and the same `materializeMetricCore` frame.
  An earlier reading of mine, that Task 2's pass before the timed loop had warmed the caches, is withdrawn.
- A candidate change, a separate function for sets with no Chimp blob,
  gives `materializeMetricCore` the machine code of `B0` again, instruction for instruction.
  In one binary `alprle` was still +1.4% with it.
  It was not measured by the procedure.

**Stopped for the owner**, as "Drop rules" says of a limit that cannot be met either way:
see "Open questions for the owner".
`nearconst` is the only row of the baseline loop that has a reading at an equal closure frame;
`constant`, `lastcolumn`, `mixed=7of8`, `blobs=2`, `blobs=3` and the two gate-off rows have not,
so the harness effect may flatter or hurt them by the same order.

The external review of `8d0b682` found no P0, and no way to make a result or a panic differ from the reference.
It ran no test, since its sandbox could not write a build cache.
Its one P1 and one P2 are fixed in `ae08a37`:

- The forecast left out a column asked for more values than it has bits, and the decode counted it,
  so the forecast could accept a group that the decode then kept out of the kernel:
  lanes filled, every column decoded ahead by the scalar loop, and no stop.
  The bound is now part of the lane rule both share ("Lane eligibility").
  Five engagement pins moved with it: a column cut to its first value no longer enters the kernel,
  and the other columns of its group now do.
  `TestChimpGroup_AdmissionMatchesDecode` fails with the fix reverted.
- The observation hook counts groups, so a group filled into its lanes and then left undecoded reported the same as one left alone.
  A test now asks `decodeChimpGroupAhead` directly and compares the group and the lane numbers with what they held;
  it fails when the lanes are filled before the forecast is asked.

`ae08a37` has not been timed; the final measurement of Task 9 covers it.

The raw output is in the orchestrator's `tmp/chimp-kernel-impl/`:
`t2c-vs-b0/`, `t2c-vs-b0-alprle2/`, `diag-alprle-t2-vs-t2c/`, `diag-alprle-gateoff/`,
`parity-b0-vs-b0pad3words/`, `parity-b0pad3words-vs-head/`, `parity-b0-vs-b0pad6/` and `parity-b0pad6-vs-head/`.

### Task 3

The gather kernel stays.
The measurement commit `999ff08` is reverted by `525dbdc`; the code is again what it was at `ae08a37`,
on which the gates had run, and the removal check prints nothing.

The scalar-load kernel is the gather kernel with another window load: 125 instructions against 98,
`NOSPLIT, $64-32`, no `call`, the eight byte addresses stored to its frame and read back into registers.
Its four mutations were caught, and every kernel test and the fuzz target passed with either kernel selected.
The selector was a flag with two direct calls, not the function variable the task first asked for; see Task 3.

`999ff08` with `MEBO_CHIMP_GROUP_KERNEL=gather` against the same commit with `loads`,
four layouts, three rounds, twelve observations per side.
Every output logs the kernel that was asked for.
The control moved by +0.1% and 0.0%, so the run is valid; no row is placement-sensitive.

| Row | Gather, ns/op | Loads, ns/op | Merged | Worst layout |
|---|---|---|---|---|
| `consumer` | 179,304 | 175,966 | -1.9% | -1.2% |
| `gauge` | 188,829 | 184,013 | -2.6% | -1.9% |
| `counter` | 170,851 | 175,974 | +3.0% | +3.6% |
| `fullprec` | 209,474 | 190,868 | -8.9% | -7.3% |
| `blobs=8` | 1,592 | 1,578 | -0.9% | -0.6% |
| `BenchmarkChimpGroup/mix/kernel` | 593.5 | 626.0 | +5.5% | +5.7% |
| `unsupported=0.1%/onelane` | 603.4 | 529.6 | -12.2% | -11.9% |
| `unsupported=1%/onelane` | 771.7 | 724.9 | -6.1% | -5.6% |
| `unsupported=10%/onelane` | 1,438 | 1,437 | 0.0% | +0.3% |
| `unsupported=100%/onelane` | 1,608 | 1,610 | +0.2% | +0.4% |
| `unsupported=0.1%/alllanes` | 728.6 | 676.5 | -7.2% | -6.9% |
| `unsupported=1%/alllanes` | 1,358 | 1,356 | -0.1% | +0.2% |
| `unsupported=10%/alllanes` | 1,712 | 1,712 | 0.0% | +0.2% |
| `unsupported=100%/alllanes` | 940.8 | 943.6 | +0.3% | +0.4% |

The gate asks for 5% merged on `consumer` and no listed row over limit 5%.
`consumer` improves by 1.9%, and `mix/kernel` is 5.7% slower in its worst layout, so the gate fails on both counts.
The two kernels trade places by the kind of value: the scalar loads win on `fullprec` and lose on `counter` and on the mix.

Not tried, and left as leads:
the frame store of 64 bytes is unaligned, so it may split a cache line and delay the eight reads that follow it;
and with lanes 0, 2, 4, 6 in one vector and 1, 3, 5, 7 in the other,
`VPUNPCKLQDQ` and `VPUNPCKHQDQ` would split the qwords without the two index registers of `VPERMT2Q`.

The raw output is in the orchestrator's `tmp/chimp-kernel-impl/t3-gather-vs-loads/`,
and the kernel as a patch in `tmp/chimp-kernel-impl/scalar-load-kernel-999ff08.patch`.

### Task 4

The three constants stay as they are: `GroupMinColumns` 4, `chimpGroupMinBitsPerValue` 12, `chimpGroupMinLive` 4.
Commits `0965bc4` (the sweep, 230 cells, test code only) and `af28f49` (one assertion); no production code changed.

The whole sweep was run once from one binary, then the cells next to the crossovers by the procedure:
four layouts, three rounds, twelve observations per side, the two sides of a cell being two sub-benchmarks of one build.
The control moved by 0.1% or less in both runs; no cell is placement-sensitive.
A negative figure means the kernel side, or the continuing side, is faster.

| Entry cell | Bits per value | Merged | Worst layout |
|---|---|---|---|
| 8 lanes, 5% of the values change | 4.45 | +38.2% | +38.8% |
| 8 lanes, 10% | 6.81 | -11.4% | -11.3% |
| 4 lanes, 15% | 9.18 | +10.4% | +10.7% |
| 4 lanes, 20% | 11.51 | -0.4% | -0.2% |
| 4 lanes, 25% | 13.86 | -7.9% | -7.6% |
| 2 lanes, every value changes | 48.3 | -0.3% | -0.1% |
| 3 lanes, every value changes | 48.3 | -12.1% | -11.4% |
| 4 lanes, every value changes | 48.3 | -23.5% | -22.9% |

| Continuation cell, worst layout | `r=8` | `r=16` | `r=32` | `r=64` |
|---|---|---|---|---|
| `k=3` | -5.9% | -10.0% | -13.4% | -17.9% |
| `k=4` | -11.4% | -17.3% | -24.1% | -30.6% |
| `k=5` | -16.8% | -23.8% | -32.4% | -40.3% |

The decision rule, step by step:

- `chimpGroupMinLive` is 4: three lanes miss the 10% at `r=8`, four lanes have it at every length.
- The lane rule gives 3 for `GroupMinColumns`.
  The rule did not see that the two constants are tied:
  the rounds run only while `chimpGroupMinLive` lanes are live, so a group that entered with three would fill its lanes and never call the kernel.
  The entry minimum is therefore 4, and `af28f49` pins that it is not below the live minimum.
- The bit rule gives 6.81 bits with eight lanes.
  The corner cell, four lanes at that density, is far over 5% slower; it still is at 9.18 bits (+10.7%) and no longer at 11.51 (-0.2%).
  The constant is an integer, and 12 is the first at or above the lowest density that was measured to pass.
- Each constant moved by one step of its sweep, up and down, fails engagement tests in both packages (six mutations).

Not done, because no constant moved: the fixtures still hold literal numbers and are not derived from the constants.
The last Task 4 row of "Task gates" (the rows against `B0`) was not measured again:
the constants are those of the "Task 2, continued" measurement, and Task 9 measures every row.

A lead the rule cannot use; see "Open questions for the owner".
In the whole sweep the kernel breaks even where the eligible lanes together carry about 46 to 50 encoded bits per step,
whatever the number of lanes from three up:
at 6.8 bits per value with eight lanes, at 11.5 with four.
A threshold on bits per value alone, set at the corner of four lanes, keeps full groups of eight out between 7 and 12 bits per value,
where the kernel read 13% to 45% faster in one binary.
The sweep has one family of values, in which the bits per value and the share of values that change are the same axis,
so this is not shown for a column whose values all change by a few bits.
None of the benchmark's kinds lies in that band:
`constant` has 2.2 bits per value, `nearconst` 4.4, `counter` 15, `gauge` 35, `fullprec` 50.

The raw output is in the orchestrator's `tmp/chimp-kernel-impl/`: `t4-sweep-single-binary/`, `t4-entry/` and `t4-cont/`.

### Task 5

Dropped at its first gate; no commit.
On every benchmark fixture of Task 0, one whole period of each row was materialized at `af28f49`
with two temporary counters in `run`, removed afterwards:
the rounds that returned fewer steps than asked, and the values still owed by the live lanes at that moment.
Both are 0 on every row, over 0.7 to 12 million values taken by the kernel per row,
so the share handed off after an early leave is 0, under the 2% the gate asks for.
The classifier's two other figures were not counted, since no lane met such a value.

### Task 7

No code changes: every row of the gate is inside its limit at the head.

`af28f49` against `B0`, four layouts, three rounds, twelve observations per side.
The control moved by -0.6% and -0.7%; no row is placement-sensitive; allocations are equal.
This is also the first timing of `ae08a37`, whose code `af28f49` has.

| Row | `B0`, ns/op | Head, ns/op | Merged | Worst layout |
|---|---|---|---|---|
| `blobs=4` | 1,166 | 1,036 | -11.1% | -10.2% |
| `blobs=7` | 1,998 | 1,494 | -25.3% | -24.8% |
| `blobs=8` | 2,215 | 1,596 | -27.9% | -27.5% |
| `blobs=9` | 2,550 | 1,956 | -23.3% | -22.5% |
| `blobs=16` | 4,326 | 3,067 | -29.1% | -28.2% |
| `blobs=64` | 16,398 | 11,342 | -30.8% | -29.6% |
| `tags` | 489,394 | 428,708 | -12.4% | -11.6% |
| `alprle` | 141,098 | 142,598 | +1.1% | +1.9% |
| `mixed=7of8` | 508,504 | 509,822 | +0.3% | +3.8% |
| `consumer` | 259,684 | 179,836 | -30.7% | -29.8% |

`alprle` reads +1.9% in its worst layout here, where it read +3.2% and +3.5% at `8d0b682`.
Between the two commits nothing on its path changed:
the set has no Chimp member, runs the baseline loop, and the benchmark file is the same.
What changed is the rule of the Chimp lanes and two test files, so the test binary is laid out differently.
This is one more sign that the row moves by more than its limit for reasons outside the code it measures;
it does not withdraw the two earlier readings, and the question to the owner stays open until Task 9 has measured the final head.

The raw output is in the orchestrator's `tmp/chimp-kernel-impl/t7-head-vs-b0/`.

### Task 8

Commits `fab786b`, `8728f45`, `279ab32` and, after the external review, `b4ea376`.
No production code changed, and no new test found a result or a panic that differs from the reference.

- **Timestamps.**
  A member's own Raw, Delta and DeltaPacked column cut short, and a short shared group,
  as the first, a middle and the last member of a group of eight and in two members of one;
  timestamp bytes flipped through the public API; short timestamps and short values in one group, in both orders,
  with and without a Gorilla member between.
  Every fixture pins the columns the kernel took.
  A Raw column that is cut yields no row, not a shorter one.
- **Tags.**
  A range outside the tag payload, a cut tag column, and a mix of tagged and untagged members,
  each with a value column cut to one value; and the same behind a group that was left alone.
- **Panics.**
  One fault, two faults in one member in each combination, and two faulty members in one group.
  The recovered values are compared themselves, not only their type and message (`b4ea376`).
- **The damage matrix** has 25 cells: five value encodings on the value side and the tag side,
  and each on the timestamp side under the three timestamp encodings.
  A cell that changes nothing fails; all 25 changed within seven flips.
- **The stress test** runs 4,000 decodes per variant (300 under `-short`) while two goroutines collect and allocate,
  after a deep recursion before each decode; it costs 0.1 s in the race pass of `make test`.
  The variant with payloads on the stack cannot exist:
  `decodeWith` stores the address of a column's first byte in the state, so escape analysis moves the array to the heap.
  A lane's base therefore never points into a stack, which is stronger than what "Memory and GC safety" assumes.
  The test stays as a variant whose group, state and rows are on the stack.
- **An idle kernel fails.**
  Seven blob tests were renamed so that `-run 'ChimpGroup'` selects every test that pins columns or steps.
  With `GODEBUG=cpu.avx512dq=off MEBO_REQUIRE_AVX512=1` that selection fails 23 tests in the two packages;
  without the variable they pass or skip.
  CI runs it where the runner has `avx512dq` and `avx512bw`, without asking for VBMI.
- A gap the task did not name: with `chimpGroupDecode` false at start, no test failed,
  since every test set the switch itself.
  `TestNumericBlobSet_MaterializeMetric_ChimpGroupOnByDefault` now does.
- **Mutations.**
  No clearing after a member trimmed by its timestamps, the clear deleted, tag padding by `Count` in either loop,
  the switch off at start, and the kernel unavailable under `MEBO_REQUIRE_AVX512=1` are each caught.
  `base` as `uintptr` fails the layout test; the stress test does not notice it, in five plain and three race runs.
- **Go 1.25 was not run.**
  The toolchain could not be downloaded in the sandbox,
  so the first review's read of the 1.25.0 source stands, as the task allows.
- The external review of the three commits found no P0 and no P1, from a static read.
  Of its two P2 and one P3, the panic comparison is fixed.
  A second grouped fixture for tag padding by `Count` was not added, since an older test already catches that mutation,
  and the row count of the cut tag column stays read from the decoder:
  the assertions beside it show the column was really cut.

### Task 9: the branch against `B0`

`b4ea376` against `B0` = `6b7cfc6`, four layouts (K = 0, 1, 2, 4), three rounds, twelve observations per side.
The layout medians are in the order of the layouts.
The commits after `b4ea376` change comments, the changelog and this plan.
The test binaries of both packages, built as the procedure builds them at `b4ea376` and at `ce1d162`,
have the same symbol table, every address and size, in the layouts compared (K = 0 and K = 4):
the timing of `b4ea376` is that of the head.

| Row | Requirement | `B0`, ns/op | Head, ns/op | Merged | Worst layout | `B0` layout medians | Head layout medians |
|---|---|---|---|---|---|---|---|
| `consumer` | 25% merged, 20% in every layout | 259,392 | 180,459 | -30.4% | -29.5% | 259,909 257,090 260,561 259,252 | 179,349 181,289 180,728 179,365 |
| `gauge` | 20% merged | 307,444 | 190,468 | -38.0% | -38.0% | 308,641 308,010 306,877 306,717 | 189,699 190,889 190,397 189,161 |
| `counter` | 20% merged | 222,208 | 172,182 | -22.5% | -22.1% | 222,310 223,592 220,937 222,107 | 171,779 172,780 172,031 172,037 |
| `fullprec` | 20% merged | 302,112 | 214,410 | -29.0% | -28.2% | 305,056 299,323 302,318 301,307 | 209,399 211,818 216,577 216,305 |
| `nearconst` | limit 5%, within 3% merged | 126,726 | 127,800 | +0.8% | +1.0% | 126,572 127,760 126,881 126,541 | 127,819 128,799 127,719 127,267 |
| `constant` | limit 5%, within 3% merged | 96,362 | 97,607 | +1.3% | +1.9% | 98,319 96,181 98,352 95,779 | 97,868 97,967 98,097 96,653 |
| `lastcolumn` | limit 5% | 308,173 | 308,270 | +0.0% | +0.6% | 308,580 308,070 308,232 306,634 | 308,650 305,631 308,199 308,341 |
| `equalcounts` | 20% merged | 258,020 | 162,807 | -36.9% | -36.5% | 258,595 257,861 258,128 260,555 | 162,826 162,646 163,963 162,599 |
| `points=10` | limit 5% | 121,327 | 91,210 | -24.8% | -24.1% | 121,189 120,620 121,121 122,765 | 91,124 91,509 90,906 91,317 |
| `points=150` | 20% merged | 923,488 | 594,820 | -35.6% | -34.7% | 927,424 912,676 935,554 916,585 | 593,877 594,369 596,623 598,122 |
| `blobs=1` | limit 3% | 334.9 | 335.6 | +0.2% | +0.3% | 335.1 334.5 335.6 334.5 | 335.7 332.9 335.9 335.6 |
| `blobs=2` | limit 3% | 622.5 | 623.0 | +0.1% | +0.6% | 622.5 620.3 622.7 624.1 | 622.3 618.2 626.5 623.0 |
| `blobs=3` | limit 3% | 890.8 | 895.8 | +0.6% | +1.6% | 891.2 889.2 889.8 891.8 | 893.2 886.3 903.9 900.5 |
| `blobs=4` | limit 5% | 1,170 | 1,044 | -10.7% | -10.2% | 1,171 1,157 1,186 1,170 | 1,039 1,039 1,056 1,050 |
| `blobs=7` | limit 5% | 1,988 | 1,505 | -24.3% | -22.8% | 2,000 1,976 1,986 1,999 | 1,499 1,509 1,533 1,504 |
| `blobs=8` | limit 5% | 2,214 | 1,609 | -27.3% | -26.8% | 2,213 2,214 2,211 2,216 | 1,607 1,611 1,618 1,601 |
| `blobs=9` | limit 5% | 2,549 | 1,962 | -23.0% | -23.0% | 2,543 2,538 2,558 2,558 | 1,949 1,947 1,969 1,964 |
| `blobs=16` | limit 5% | 4,330 | 3,084 | -28.8% | -27.8% | 4,342 4,292 4,344 4,324 | 3,093 3,100 3,082 3,073 |
| `blobs=64` | limit 5% | 16,443 | 11,407 | -30.6% | -30.2% | 16,483 16,345 16,450 16,538 | 11,411 11,389 11,403 11,536 |
| `mixed=alternate` | limit 5% | 200,322 | 188,160 | -6.1% | -5.5% | 199,970 200,565 199,749 200,212 | 188,710 188,119 188,871 187,798 |
| `mixed=1of8` | limit 5% | 243,436 | 185,178 | -23.9% | -23.5% | 243,904 242,357 243,405 243,468 | 184,880 185,381 185,117 185,239 |
| `mixed=7of8` | limit 5% | 505,024 | 506,104 | +0.2% | +3.2% | 504,630 502,659 511,882 505,418 | 499,386 502,077 505,147 521,856 |
| `alprle` | limit 3% | 140,247 | 142,714 | +1.8% | +2.6% | 140,136 141,065 140,347 140,147 | 143,829 142,516 142,342 142,912 |
| `tags` | limit 5% | 492,828 | 432,146 | -12.3% | -11.4% | 493,234 492,423 491,706 494,614 | 434,683 436,453 429,609 426,110 |
| `consumer`, gate off | limit 3%, within 2% merged | 259,570 | 261,318 | +0.7% | +1.2% | 261,113 256,818 260,292 258,703 | 261,939 259,466 261,389 261,765 |
| `blobs=4`, gate off | limit 3%, within 2% merged | 1,186 | 1,180 | -0.4% | +0.1% | 1,182 1,175 1,189 1,194 | 1,182 1,176 1,182 1,174 |
| `ChimpMix/fresh/n30/DecodeAll` | the control: within 3% merged | 14,526 | 14,558 | +0.2% | +0.5% | 14,560 14,497 14,508 14,531 | 14,557 14,533 14,577 14,544 |
| `ChimpMix/fresh/n150/DecodeAll` | the control: within 3% merged | 69,040 | 69,050 | +0.0% | +0.1% | 69,007 69,063 69,124 69,014 | 69,097 69,036 69,072 68,960 |

- **Every requirement of "Acceptance thresholds" on time is met.**
  The control moved by +0.2% and 0.0%, so the run is valid; no row is placement-sensitive.
- Allocations are equal on every row.
  Bytes per operation are equal, except on `lastcolumn` (572,346 to 572,348, inside the 0.01% it is allowed)
  and on `mixed=alternate`, which the thresholds do not name:
  573,446 on every observation of `B0`, and 573,445 or 573,446 at the head.
  That one byte is the averaging effect recorded under Task 2; by the letter of the thresholds it is a difference,
  and it is reported to the owner as one.
- `alprle` reads +1.8% merged and +2.6% in its worst layout, inside its limit of 3%.
  Its four readings by the procedure are +3.2% and +3.5% at `8d0b682`, +1.9% at `af28f49` and +2.6% here,
  with nothing on its path changed between those commits.
  The row passes at the final head, and it does so with less room than identical code is seen to move by.
- The gates passed on the head: the hot-path script, `go vet` for amd64 and arm64, `make lint`, `make test` and `make fuzz`.
- The removal check of Task 3 prints nothing, and `chimpGroupKernel` calls the gather kernel directly.
- `tests/compat/run_compat.sh` passes for `v1.14.0` and for `v1.10.0` against the branch.
- The godoc of `MaterializeNumericMetric` and `MaterializeNumericMetricByName` said "about 2–5 ns per point without tags".
  The Chimp rows measure 2.8 to 8.8 ns at the head, and 4.9 to 6.1 ns where the kernel takes the columns,
  so it now reads "about 3–9 ns" with Chimp values, "5–6 ns" where the group decode takes the columns,
  and "about 4 ns" with ALP-RLE values, with the CPU named (`391f231`, reworded in `f2d4482`).
- The changelog entry is `1fbe978`, reworded in `35cffdf`.
- The external review of the whole branch, a static read, found no P0 and no P1,
  no input on which the grouped path and the reference differ, and nothing left of the second kernel.
  Its three P2 were about wording and are fixed in `f2d4482` and `35cffdf`:
  the comment of the lane rule promised that nothing overflows for any `int`, which holds only for lengths of payloads in memory;
  the godoc tied the cost per point to the CPU alone, where `lastcolumn` runs at 8.8 ns on this CPU;
  and the changelog compared the unchanged sets with `v1.14.1`, where the measurement compares with `B0`.
  These two commits change comments and the changelog only, and were not timed.
- The owner asked for the squash by scope, the push and the pull request on 2026-11-14.
  Not done, and waiting for the owner each: the merge and the tag.

The raw output is in the orchestrator's `tmp/chimp-kernel-impl/t9-final-vs-b0/`.

### Task 10

Commits `29057fb` (the two rows), `89cded7` (the change) and `8b23f91` (a pin of the starting value, after the review).
Every fixture and the fuzz target equal the frozen reference at 0, 1, 3 and 56 members, group decode off and on.
All five mutation checks were caught; the one that removes the `written` carry is caught by a comparison with the reference.
The external review of `89cded7`, a static read, found no P0 and no P1 and no input on which a result or a panic differs.
Its one P2 is fixed in `8b23f91`: no test saw a change of the value the package starts with.

Before the change the kernel takes no column of either new row; after it, 0.93 of `quiet=start` and 0.74 of `quiet=spells`.

Two runs, each against `29057fb`, four layouts, three rounds, twelve observations per side; the control moved by 0.0% in both.
Allocations are equal on every row.

| Row | 56 members: merged | worst layout | 112 members: merged | worst layout | Requirement |
|---|---|---|---|---|---|
| `quiet=start` | -36.5% | -36.2% | -34.3% | -33.9% | improves by 20% merged |
| `quiet=spells` | -29.6% | -29.6% | **-19.7%** | -19.0% | improves by 20% merged |
| `nearconst` | **+1.9%** | +2.4% | **+2.0%** | +2.1% | within 1.5% merged, limit 3% |
| `constant` | +1.4% | **+3.7%** | +1.0% | +1.0% | within 1.5% merged, limit 3% |
| `lastcolumn` | +0.3% | +0.4% | +0.6% | +0.9% | within 1.5% merged, limit 3% |
| `mixed=7of8` | +0.1% | +6.7% | +0.1% | +1.8% | within 1.5% merged, limit 3% |
| `consumer` | -0.2% | +0.1% | -0.5% | -0.2% | within 1.5% merged, limit 3% |
| `fullprec` | +2.0% | +4.4% | -0.1% | +3.4% | within 1.5% merged, limit 3% |
| `alprle` | +0.8% | +1.5% | +1.0% | +1.2% | within 1.5% merged, limit 3% |

The rows not listed are within 1.2% merged in both runs;
`blobs=9` (+3.1%) and `blobs=64` (+5.2%) are over the limit in one layout of the first run and inside it in the second.

- At 56 members `nearconst` is over its requirement, so the stretch was doubled once, as the task says (`0f7c97c`, a measurement commit).
- At 112 members `nearconst` did not move, and `quiet=spells` fell under its own requirement:
  it is quiet for 16 blobs of every 250, and a longer stretch gives up more of the blobs between.
  So the cost on `nearconst` is not the number of groups asked alone.
  Where it is was not found: instruction counts in one binary differ by more from run to run than the two values differ.
- The first run was disturbed: single observations of `fullprec`, `mixed=7of8` and `blobs=64` are 30% to 60% off their layout's others,
  with a load average near 2 on the machine.
  `fullprec` and `mixed=7of8` are placement-sensitive in it for that reason.
- Against `B0` the two rows were at +0.8% (`nearconst`) and +1.3% (`constant`) before this task.
  Added to this task's figures at 56 members they come to about +2.7% each, against "within 3% merged";
  that sum was not measured.

The task's rule hands this to the owner with the numbers: 56 members, 112 members, or the change dropped.
The orchestrator recommended 56: it keeps both new rows well over their requirement,
and 112 buys nothing on `nearconst`.

**Answered on 2026-11-14: the owner takes 56 members**, with `nearconst` at +1.9% merged against the 1.5% of this task's gate.
The published branch holds 56, and the changelog gives the two measured costs separately:
0% to 2% against `B0` before this task, and up to 2% more for a metric that stays quiet, against `29057fb`.
No run compares the head of this task with `B0`.

The raw output is in the orchestrator's `tmp/chimp-kernel-impl/t10-retry56/` and `t10-retry112/`.

## Out of scope, recorded

- **The same kernel for one payload** (`NumericBlob.Materialize`, `NumericBlobSet.Materialize`):
  the research measured it at codec level
  (129–166 → 54.5 ns per 30-point column); decision 3 of "Taken by this plan".
- **The `NumericMetric` handle's `Materialize`**: one metric across blobs like this path,
  but it writes per-part columns;
  the consumer does not use it.
- **Per-blob `ForEach*`**: one column per call, so the kernel does not apply;
  decoding into a buffer first measured 17% to 22% from one binary.
- **The encoder**: a classification kernel measured 20% at blob level from one binary;
  it needs a profile of the blob encoder first.
- **Gorilla**: the same method should apply, and was not measured.
- **A kernel for machines without AVX-512**, arm64 included:
  the pure Go branch-free decode measured 9% slower for one column.
- **Two groups of eight interleaved** in one kernel call, to hide the load latency: not prototyped.
- **`decodeRunsInto` returning its destination's old contents** for a runs column whose nested column is empty:
  it was there before this plan, the 2026-11-12 plan records it, and this plan keeps the baseline's outcome.
