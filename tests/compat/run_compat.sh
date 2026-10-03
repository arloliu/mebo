#!/usr/bin/env bash
# run_compat.sh — Black-box cross-version compatibility test for mebo releases.
#
# Usage:
#   bash run_compat.sh [OLD_TAG [NEW_TAG]]
#
#   OLD_TAG  Tag of the previous release to test against (default: v1.4.3)
#   NEW_TAG  Tag of the new    release to verify         (default: v1.5.0)
#
# Examples:
#   bash run_compat.sh                  # v1.4.3 ↔ v1.5.0
#   bash run_compat.sh v1.5.0 v1.6.0   # v1.5.0 ↔ v1.6.0
#
# Prerequisites:
#   - Git repository at the working directory containing both tags
#   - Go 1.24+
#
# Build tags:
#   The script automatically applies "-tags v2" for any version >= v1.5.0,
#   "-tags alp" for any version >= v1.8.0, "-tags alpvalidate" for any
#   version >= v1.9.0, "-tags metricnames" for any version >= v1.10.0, and
#   "-tags alprle" for any version >= v1.12.0. Add a new version gate next to needs_v2_tag() /
#   needs_alp_tag() / needs_metricnames_tag() / needs_alprle_tag() if future releases introduce
#   more capability tags.
#
# Capability-gated encodings:
#   Scenarios for a value encoding that older releases cannot read (ALP is
#   the first) use their own ID prefix and are listed in CAPABILITY_BUCKETS.
#   Matrix 2 drops a bucket OLD does not support, and Matrix 3b then requires
#   OLD to reject that bucket gracefully; when OLD supports it, OLD must
#   decode it like any other blob. Adding a new encoding = one tag gate plus
#   one CAPABILITY_BUCKETS entry.
#
# Test Matrix:
#   1. OLD encodes → NEW decodes:                 expect all PASS  (backward compat: new code reads old data)
#   2. NEW encodes → OLD decodes:                 expect all PASS  (forward compat: old code reads new data)
#        - when OLD already supports V2 (>= v1.5.0), this includes NEW's
#          V2/V2Ext output and every metric-names scenario (mn-*) — OLD is
#          expected to decode all of it. V2 is no longer treated as a
#          rejected "new format" once both sides understand it.
#        - when OLD predates V2, only NEW's V1-layout output is used here,
#          and NEW's V2-only output is exercised in matrix 3 as a "must
#          reject" case instead.
#  3b. OLD and NEW's capability-gated output (one bucket per encoding, e.g. alp-*):
#        - OLD supports the encoding:        expect all PASS
#        - OLD predates the encoding:        expect ERROR, no panic (graceful reject)
#   3. OLD decodes NEW's V2-only output:
#        - OLD supports V2:                  expect all PASS  (V2 is an understood format)
#        - OLD predates V2:                  expect ERROR, no panic (graceful reject)
#   4. OLD self-encode / self-decode:             expect all PASS  (baseline OLD)
#   5. NEW self-encode / self-decode:             expect all PASS  (baseline NEW)
#   6. Metric-names adversarial fixtures (NEW only, when NEW supports the
#      metricnames capability): A/B/A repeated-name and unsorted-by-ID
#      V2 index must both be rejected by NEW's decoder. expect ERROR, no panic
#   7. Corruption tests against both:             expect ERROR, no panic
set -euo pipefail

# ============================================================
# Arguments
# ============================================================
OLD_TAG="${1:-v1.4.3}"
NEW_TAG="${2:-v1.5.0}"

# ============================================================
# Semver helpers — each returns true if $1 is >= the version that
# introduced the matching capability build tag.
# ============================================================

# is_semver_ref returns true if $1 looks like a "vMAJOR.MINOR[.PATCH...]"
# release tag (leading 'v' optional). A ref that does NOT look like this —
# "HEAD", a branch name (e.g. "feat/metric-names-v1.10.0"), or a bare
# commit SHA — cannot be version-compared by the IFS/read parsing below.
is_semver_ref() {
    [[ "$1" =~ ^v?[0-9]+\.[0-9]+ ]]
}

# warn_non_semver logs (to stderr, so it always surfaces regardless of stdout
# capture) that $1 isn't a comparable release tag and is being treated as
# "newest" — i.e. at least as capable as every version-gated build tag this
# script knows about. This is deliberate: a branch or SHA passed as OLD_TAG
# or NEW_TAG is by definition under-test dev code, which for every capability
# gate introduced so far has been >= the latest tagged release. Falling
# through the IFS/read parsing below on a non-numeric value instead is
# fragile and ref-shape-dependent: under this script's `set -u`, a ref
# containing a letter (e.g. "HEAD", or a branch name) makes `[[ "$major" -gt
# 1 ]]` try to arithmetic-expand that word as a variable name and abort the
# whole script with "unbound variable" — a confusing hard failure with no
# indication it's a ref-format problem. A differently-shaped non-semver ref
# could just as easily parse as 0 and silently downgrade capabilities
# instead (dropping -tags metricnames, and every mn-*/v2-only scenario with
# it, with no warning and no effect on the exit code). Neither outcome is
# acceptable; this function replaces both with one explicit, correct path.
warn_non_semver() {
    echo "    [WARN] '$1' is not a vMAJOR.MINOR[.PATCH] release tag;" \
         "treating it as the newest known version (>= every capability gate) rather than silently downgrading capabilities." >&2
}

needs_v2_tag() {
    local ref="$1"
    if ! is_semver_ref "$ref"; then
        warn_non_semver "$ref"
        return 0
    fi
    local tag="${ref#v}"   # strip leading 'v'
    local major minor
    IFS='.' read -r major minor _ <<< "$tag"
    [[ "$major" -gt 1 ]] || { [[ "$major" -eq 1 ]] && [[ "$minor" -ge 5 ]]; }
}

# needs_alp_tag returns true if $1 >= v1.8.0, the first release with
# format.TypeALP; the ALP scenarios (scenarios_alp.go) reference it, so they
# live behind this tag (mirrors needs_v2_tag).
needs_alp_tag() {
    local ref="$1"
    if ! is_semver_ref "$ref"; then
        warn_non_semver "$ref"
        return 0
    fi
    local tag="${ref#v}"
    local major minor
    IFS='.' read -r major minor _ <<< "$tag"
    [[ "$major" -gt 1 ]] || { [[ "$major" -eq 1 ]] && [[ "$minor" -ge 8 ]]; }
}

# needs_alpvalidate_tag returns true if $1 >= v1.9.0, the first release that
# validates ALP columns when a blob is opened. Without it (v1.8.0) the
# harness tolerates the known panic on alp-corrupt-flipped-values only; see
# alpOpenValidation in verify.go.
needs_alpvalidate_tag() {
    local ref="$1"
    if ! is_semver_ref "$ref"; then
        warn_non_semver "$ref"
        return 0
    fi
    local tag="${ref#v}"
    local major minor
    IFS='.' read -r major minor _ <<< "$tag"
    [[ "$major" -gt 1 ]] || { [[ "$major" -eq 1 ]] && [[ "$minor" -ge 9 ]]; }
}

# needs_metricnames_tag returns true if $1 >= v1.10.0 (needs -tags
# metricnames). v1.10.0 adds new public symbols (WithMetricNames,
# WithoutMetricNames, StripMetricNames, StripMetricNamesInPlace,
# NewNumericDecoderBorrowed, NewTextDecoderBorrowed); a binary built against
# an older module doesn't have them, so the compat scenarios that reference
# them live behind this tag (mirrors needs_v2_tag).
needs_metricnames_tag() {
    local ref="$1"
    if ! is_semver_ref "$ref"; then
        warn_non_semver "$ref"
        return 0
    fi
    local tag="${ref#v}"
    local major minor
    IFS='.' read -r major minor _ <<< "$tag"
    [[ "$major" -gt 1 ]] || { [[ "$major" -eq 1 ]] && [[ "$minor" -ge 10 ]]; }
}

# needs_alprle_tag returns true if $1 >= v1.12.0, the first release planned to
# ship format.TypeALPRLE (value encoding 0x7); the ALP-RLE scenarios
# (scenarios_alprle.go) reference it, so they live behind this tag (mirrors
# needs_alp_tag). Branch refs pass every gate, like the other tags. Update the
# version here if ALP-RLE ships in a different release.
needs_alprle_tag() {
    local ref="$1"
    if ! is_semver_ref "$ref"; then
        warn_non_semver "$ref"
        return 0
    fi
    local tag="${ref#v}"
    local major minor
    IFS='.' read -r major minor _ <<< "$tag"
    [[ "$major" -gt 1 ]] || { [[ "$major" -eq 1 ]] && [[ "$minor" -ge 12 ]]; }
}

# build_tags_for returns the combined "-tags a,b" argument (or "") for the
# given release tag/ref, based on which capabilities it supports.
build_tags_for() {
    local tag="$1"
    local tags=()
    needs_v2_tag "$tag" && tags+=("v2")
    needs_alp_tag "$tag" && tags+=("alp")
    needs_alpvalidate_tag "$tag" && tags+=("alpvalidate")
    needs_metricnames_tag "$tag" && tags+=("metricnames")
    needs_alprle_tag "$tag" && tags+=("alprle")
    if [[ ${#tags[@]} -eq 0 ]]; then
        echo ""
    else
        local IFS=,
        echo "-tags ${tags[*]}"
    fi
}

OLD_BUILD_TAGS="$(build_tags_for "$OLD_TAG")"
NEW_BUILD_TAGS="$(build_tags_for "$NEW_TAG")"

# Whether OLD already understands the V2/V2Ext wire layout (both tags in a
# v1.9.0<->v1.10.0-class pair do). When true, OLD is expected to DECODE
# everything NEW produces (V1 and V2 alike) rather than reject V2 as an
# unknown format — see Matrix 2/3 below.
OLD_SUPPORTS_V2=false
needs_v2_tag "$OLD_TAG" && OLD_SUPPORTS_V2=true || true

# Capability-gated encodings, as "name:scenario-id-prefix:gate-function".
# Each bucket's blobs are kept out of Matrix 2 when OLD lacks the gate, and
# Matrix 3b asserts OLD decodes (gate passes) or gracefully rejects them.
CAPABILITY_BUCKETS=(
    "alp:alp-:needs_alp_tag"
    "alprle:alprle-:needs_alprle_tag"
)

# Whether NEW was built with the metric-names (v1.10.0+) capability, i.e.
# whether the metric-names-specific "must decode" scenarios and "must
# reject" adversarial fixtures (mn-*) exist at all for this pair.
NEW_SUPPORTS_METRICNAMES=false
needs_metricnames_tag "$NEW_TAG" && NEW_SUPPORTS_METRICNAMES=true || true

# Slug versions for directory/binary names (strip dots and 'v').
# Slugs are used as path components, so anything other than [A-Za-z0-9_-]
# (e.g. the '/' in a branch ref like feat/x) becomes '-'.
slugify() {
    local s="${1//[v.]}"
    echo "${s//[^A-Za-z0-9_-]/-}"
}
old_slug="$(slugify "${OLD_TAG}")"
new_slug="$(slugify "${NEW_TAG}")"

# ============================================================
# Configuration
# ============================================================
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMPAT_SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORK_DIR="${TMPDIR:-/tmp}/mebo-compat-$$"
TESTDATA="${COMPAT_SRC}/testdata"

BIN_OLD="${WORK_DIR}/compat-${old_slug}"
BIN_NEW="${WORK_DIR}/compat-${new_slug}"

WORK_OLD="${WORK_DIR}/mebo-${OLD_TAG}"
WORK_NEW="${WORK_DIR}/mebo-${NEW_TAG}"
COMPAT_OLD="${WORK_DIR}/compat-src-${old_slug}"
COMPAT_NEW="${WORK_DIR}/compat-src-${new_slug}"

DATA_OLD="${TESTDATA}/encoded-by-${old_slug}"
DATA_NEW_FULL="${TESTDATA}/encoded-by-${new_slug}-full"
DATA_NEW_NEWFORMAT="${TESTDATA}/encoded-by-${new_slug}-newformat"
DATA_NEW_V1ONLY="${TESTDATA}/encoded-by-${new_slug}-v1only"
DATA_NEW_FORWARD="${TESTDATA}/encoded-by-${new_slug}-forward-${old_slug}"
DATA_CORRUPT="${TESTDATA}/corrupted"
DATA_MN_REJECT="${TESTDATA}/metricnames-reject-${new_slug}"

PASS=0
FAIL=0
FAILURES=()

# ============================================================
# Helpers
# ============================================================
info()    { echo ""; echo "==> $*"; }
ok()      { echo "    [PASS] $*"; PASS=$((PASS+1)); }
fail()    { echo "    [FAIL] $*"; FAIL=$((FAIL+1)); FAILURES+=("$*"); }
section() { echo ""; echo ""; echo "### $* ###"; }

cleanup() {
    info "Cleaning up worktrees"
    git -C "${REPO_ROOT}" worktree remove --force "${WORK_OLD}" 2>/dev/null || true
    git -C "${REPO_ROOT}" worktree remove --force "${WORK_NEW}" 2>/dev/null || true
    rm -rf "${WORK_DIR}"
}

run_step() {
    local label="$1"; shift
    if "$@"; then
        ok "$label"
    else
        fail "$label"
    fi
}

# ============================================================
# 0. Setup
# ============================================================
section "Setup (${OLD_TAG} ↔ ${NEW_TAG})"
echo "  OLD_TAG=${OLD_TAG}  build_tags='${OLD_BUILD_TAGS}'"
echo "  NEW_TAG=${NEW_TAG}  build_tags='${NEW_BUILD_TAGS}'"
mkdir -p "${WORK_DIR}" "${TESTDATA}"
# Start every run from empty output directories: testdata/ persists between
# runs (it is gitignored), and DATA_CORRUPT is shared by every version pair,
# so leftover fixtures from an earlier pair would otherwise be re-checked here.
rm -rf "${DATA_OLD}" "${DATA_NEW_FULL}" "${DATA_NEW_NEWFORMAT}" "${DATA_NEW_V1ONLY}" \
    "${DATA_NEW_FORWARD}" "${DATA_CORRUPT}" "${DATA_MN_REJECT}"
for bucket in "${CAPABILITY_BUCKETS[@]}"; do
    rm -rf "${TESTDATA}/encoded-by-${new_slug}-${bucket%%:*}"
done

info "Creating worktree for ${OLD_TAG}"
git -C "${REPO_ROOT}" worktree add --detach "${WORK_OLD}" "${OLD_TAG}" 2>&1
info "Creating worktree for ${NEW_TAG}"
git -C "${REPO_ROOT}" worktree add --detach "${WORK_NEW}" "${NEW_TAG}" 2>&1

# Copy compat source into each worktree directory, then patch go.mod's replace
# directive to point at the correct worktree root.
cp -a "${COMPAT_SRC}" "${COMPAT_OLD}"
cp -a "${COMPAT_SRC}" "${COMPAT_NEW}"

sed -i "s|replace github.com/arloliu/mebo => .*|replace github.com/arloliu/mebo => ${WORK_OLD}|" \
    "${COMPAT_OLD}/go.mod"
sed -i "s|replace github.com/arloliu/mebo => .*|replace github.com/arloliu/mebo => ${WORK_NEW}|" \
    "${COMPAT_NEW}/go.mod"

# ============================================================
# 1. Build
# ============================================================
section "Build"

# -buildvcs=false: these are throwaway compat-test binaries built from a
# `cp -a` copy of this directory (COMPAT_OLD/COMPAT_NEW) whose module
# dependency is a detached git worktree (WORK_OLD/WORK_NEW). VCS stamping
# has no value here, and in some sandboxed/CI environments `go build`'s VCS
# auto-detection fails outright ("error obtaining VCS status") when walking
# up from a directory tree like that; skip it unconditionally.
info "Building compat binary against ${OLD_TAG} ${OLD_BUILD_TAGS:-(no extra tags)}"
# shellcheck disable=SC2086
if ! (cd "${COMPAT_OLD}" && go mod tidy 2>&1 && go build -buildvcs=false ${OLD_BUILD_TAGS} -o "${BIN_OLD}" .); then
    fail "Build ${OLD_TAG}"
    echo "FATAL: cannot continue without ${OLD_TAG} binary"
    cleanup
    exit 1
fi
ok "Build ${OLD_TAG}"

info "Building compat binary against ${NEW_TAG} ${NEW_BUILD_TAGS:-(no extra tags)}"
# shellcheck disable=SC2086
if ! (cd "${COMPAT_NEW}" && go mod tidy 2>&1 && go build -buildvcs=false ${NEW_BUILD_TAGS} -o "${BIN_NEW}" .); then
    fail "Build ${NEW_TAG}"
    echo "FATAL: cannot continue without ${NEW_TAG} binary"
    cleanup
    exit 1
fi
ok "Build ${NEW_TAG}"

# ============================================================
# 2. Encode phase
# ============================================================
section "Encode Phase"

info "${OLD_TAG} encodes all its scenarios → ${DATA_OLD}"
run_step "encode with ${OLD_TAG}" \
    "${BIN_OLD}" encode --outdir "${DATA_OLD}"

info "${NEW_TAG} encodes all its scenarios → ${DATA_NEW_FULL}"
run_step "encode with ${NEW_TAG}" \
    "${BIN_NEW}" encode --outdir "${DATA_NEW_FULL}"

# Separate new-format-only blobs (prefixed num-v2-) for rejection testing.
# A future release may add different prefix conventions; adjust the glob below.
mkdir -p "${DATA_NEW_NEWFORMAT}"
for f in "${DATA_NEW_FULL}"/num-v2-*.blob "${DATA_NEW_FULL}"/num-v2-*.json; do
    [[ -e "$f" ]] && cp "$f" "${DATA_NEW_NEWFORMAT}/" || true
done
ok "Separate new-format blobs for rejection tests"

# Filter NEW's output to V1-layout-only scenarios understood by OLD decoder.
mkdir -p "${DATA_NEW_V1ONLY}"
for f in "${DATA_NEW_FULL}"/num-v1-*.blob "${DATA_NEW_FULL}"/num-v1-*.json \
         "${DATA_NEW_FULL}"/txt-v1-*.blob "${DATA_NEW_FULL}"/txt-v1-*.json \
         "${DATA_NEW_FULL}"/blobset-v1-*.blob "${DATA_NEW_FULL}"/blobset-v1-*.json; do
    [[ -e "$f" ]] && cp "$f" "${DATA_NEW_V1ONLY}/" || true
done
ok "Filter V1-only blobs for OLD decoder"

# Split NEW's output by capability bucket. DATA_NEW_FORWARD is NEW's full
# output minus every bucket OLD cannot read; each bucket also gets its own
# directory for Matrix 3b.
mkdir -p "${DATA_NEW_FORWARD}"
cp -a "${DATA_NEW_FULL}/." "${DATA_NEW_FORWARD}/"
#
# A bucket NEW supports must be non-empty and every manifest must have its
# blob; otherwise Matrix 3b would skip or run zero scenarios and pass
# without testing anything. Copy errors fail the step instead of being
# swallowed.
for bucket in "${CAPABILITY_BUCKETS[@]}"; do
    IFS=':' read -r cap_name cap_prefix cap_gate <<< "${bucket}"
    cap_dir="${TESTDATA}/encoded-by-${new_slug}-${cap_name}"
    mkdir -p "${cap_dir}"
    cap_files=()
    while IFS= read -r -d '' f; do
        cap_files+=("$f")
    done < <(find "${DATA_NEW_FULL}" -maxdepth 1 -type f -name "${cap_prefix}*" -print0)
    if [[ ${#cap_files[@]} -gt 0 ]] && ! cp "${cap_files[@]}" "${cap_dir}/"; then
        fail "Copy ${cap_name} bucket into ${cap_dir}"
    fi
    if "${cap_gate}" "${NEW_TAG}" 2>/dev/null; then
        n_json=0
        missing_blob=""
        for m in "${cap_dir}"/*.json; do
            [[ -e "$m" ]] || continue
            n_json=$((n_json+1))
            [[ -e "${m%.json}.blob" ]] || missing_blob+=" $(basename "${m%.json}")"
        done
        if [[ "${n_json}" -eq 0 || -n "${missing_blob}" ]]; then
            fail "Capability bucket ${cap_name}: ${NEW_TAG} supports it but produced ${n_json} manifest(s); missing blobs:${missing_blob:- none}"
        fi
    fi
    if ! "${cap_gate}" "${OLD_TAG}" 2>/dev/null; then
        rm -f "${DATA_NEW_FORWARD}/${cap_prefix}"*
    fi
done
ok "Split capability-gated blobs (${#CAPABILITY_BUCKETS[@]} bucket(s))"

# ============================================================
# 3. Cross-version decode matrix
# ============================================================
section "Cross-Version Decode Matrix"

# Matrix 1: NEW decodes blobs encoded by OLD  (backward compatibility)
info "Matrix 1: ${NEW_TAG} decodes blobs encoded by ${OLD_TAG} (backward compat)"
run_step "Matrix-1 backward compat (${OLD_TAG}→${NEW_TAG})" \
    "${BIN_NEW}" decode --indir "${DATA_OLD}"

# Matrix 2: OLD decodes blobs encoded by NEW (forward compatibility).
#
# When OLD already supports V2 (>= v1.5.0), V2 is an understood format,
# not a rejected "new format" — decode NEW's FULL output (every scenario,
# including V2/V2Ext and every metric-names mn-* scenario) rather than the
# V1-only filtered subset. Older pairs (e.g. the v1.4.3<->v1.5.0 default)
# keep the original V1-only restriction, since OLD there genuinely predates
# V2 and cannot be expected to decode it.
if [[ "${OLD_SUPPORTS_V2}" == "true" ]]; then
    info "Matrix 2: ${OLD_TAG} decodes ALL blobs encoded by ${NEW_TAG} (forward compat, full — OLD already supports V2)"
    run_step "Matrix-2 forward compat (${NEW_TAG} full→${OLD_TAG} decoder)" \
        "${BIN_OLD}" decode --indir "${DATA_NEW_FORWARD}"
else
    info "Matrix 2: ${OLD_TAG} decodes V1-layout blobs encoded by ${NEW_TAG} (forward compat)"
    run_step "Matrix-2 forward compat (${NEW_TAG} V1-layout→${OLD_TAG} decoder)" \
        "${BIN_OLD}" decode --indir "${DATA_NEW_V1ONLY}"
fi

# Matrix 3: OLD and NEW's V2-only output.
#
# Reclassified from a reject case: when OLD supports V2 it must decode
# NEW's V2-only blobs successfully (same expectation as matrix 2, asserted again here for
# an isolated V2-specific signal). Only when OLD predates V2 does it still
# need to reject that output gracefully (no panic) — the original behaviour
# for pairs like v1.4.3<->v1.5.0.
if [[ "${OLD_SUPPORTS_V2}" == "true" ]]; then
    info "Matrix 3: ${OLD_TAG} decodes new-format (V2) blobs encoded by ${NEW_TAG} (both >= v1.5.0 — V2 expected to decode)"
    if [[ -n "$(ls "${DATA_NEW_NEWFORMAT}"/*.blob 2>/dev/null)" ]]; then
        run_step "Matrix-3 V2 decode (${NEW_TAG} V2→${OLD_TAG})" \
            "${BIN_OLD}" decode --indir "${DATA_NEW_NEWFORMAT}"
    else
        echo "    [SKIP] No new-format blobs found — ${NEW_TAG} may not introduce a new layout"
    fi
else
    info "Matrix 3: ${OLD_TAG} rejects new-format blobs encoded by ${NEW_TAG} (graceful reject)"
    if [[ -n "$(ls "${DATA_NEW_NEWFORMAT}"/*.blob 2>/dev/null)" ]]; then
        run_step "Matrix-3 graceful reject (${NEW_TAG} new-format→${OLD_TAG})" \
            "${BIN_OLD}" reject --indir "${DATA_NEW_NEWFORMAT}"
    else
        echo "    [SKIP] No new-format blobs found — ${NEW_TAG} may not introduce a new layout"
    fi
fi

# Matrix 3b: capability-gated encodings. OLD must decode a bucket it
# supports and reject (gracefully, no panic) a bucket it predates.
for bucket in "${CAPABILITY_BUCKETS[@]}"; do
    IFS=':' read -r cap_name cap_prefix cap_gate <<< "${bucket}"
    cap_dir="${TESTDATA}/encoded-by-${new_slug}-${cap_name}"
    if [[ -z "$(ls "${cap_dir}"/*.blob 2>/dev/null)" ]]; then
        if "${cap_gate}" "${NEW_TAG}" 2>/dev/null; then
            fail "Matrix-3b ${cap_name}: ${NEW_TAG} supports ${cap_name} but its bucket is empty"
        else
            info "Matrix 3b (${cap_name}): skipped — ${NEW_TAG} predates ${cap_name}"
        fi
        continue
    fi
    if "${cap_gate}" "${OLD_TAG}" 2>/dev/null; then
        info "Matrix 3b (${cap_name}): ${OLD_TAG} decodes ${cap_name} blobs encoded by ${NEW_TAG}"
        run_step "Matrix-3b ${cap_name} decode (${NEW_TAG}→${OLD_TAG})" \
            "${BIN_OLD}" decode --indir "${cap_dir}"
    else
        info "Matrix 3b (${cap_name}): ${OLD_TAG} predates ${cap_name}; must reject ${NEW_TAG}'s blobs gracefully"
        run_step "Matrix-3b ${cap_name} graceful reject (${NEW_TAG}→${OLD_TAG})" \
            "${BIN_OLD}" reject --indir "${cap_dir}"
    fi
done

# Matrix 4: OLD self-compat baseline
info "Matrix 4: ${OLD_TAG} self-compatibility (baseline)"
run_step "Matrix-4 ${OLD_TAG} self-compat" \
    "${BIN_OLD}" decode --indir "${DATA_OLD}"

# Matrix 5: NEW self-compat baseline
info "Matrix 5: ${NEW_TAG} self-compatibility (baseline)"
run_step "Matrix-5 ${NEW_TAG} self-compat" \
    "${BIN_NEW}" decode --indir "${DATA_NEW_FULL}"

# Matrix 6: metric-names adversarial fixtures (duplicate metric name and
# unsorted-by-ID V2 index reject cases).
#
# Only meaningful when NEW was built with the metricnames capability: the
# fixtures (A/B/A repeated-name and unsorted-by-ID V2) are built once,
# deterministically, from the committed real collision pair
# (internal/collisiontest) via `compat mncorrupt`, then decoded through
# ONLY the public blob.NewNumericDecoder/Decode API via `compat reject`
# (same mechanism as the existing corruption matrix). They are never fed to
# OLD — OLD's decoder predates the duplicate-name and unsorted-index
# hardening these fixtures target.
if [[ "${NEW_SUPPORTS_METRICNAMES}" == "true" ]]; then
    info "Matrix 6: generating metric-names adversarial fixtures (A/B/A repeated name, unsorted V2 index)"
    run_step "Generate metric-names fixtures" \
        "${BIN_NEW}" mncorrupt --outdir "${DATA_MN_REJECT}"

    info "Matrix 6: ${NEW_TAG} rejects metric-names adversarial fixtures (no panic)"
    run_step "Matrix-6 ${NEW_TAG} rejects mn-dup-name (ErrDuplicateMetricName) + mn-unsorted-v2 (ErrUnsortedIndex)" \
        "${BIN_NEW}" reject --indir "${DATA_MN_REJECT}"
else
    info "Matrix 6: skipped — ${NEW_TAG} was not built with the metricnames capability"
fi

# ============================================================
# 4. Corruption / robustness tests
# ============================================================
section "Robustness Tests"

info "Generating corrupted blobs from ${DATA_OLD}"
run_step "Generate corrupted blobs" \
    "${BIN_OLD}" corrupt --indir "${DATA_OLD}" --outdir "${DATA_CORRUPT}"

info "${OLD_TAG} rejects all corrupted blobs (no panic)"
run_step "${OLD_TAG} reject corrupted" \
    "${BIN_OLD}" reject --indir "${DATA_CORRUPT}"

info "${NEW_TAG} rejects all corrupted blobs (no panic)"
run_step "${NEW_TAG} reject corrupted" \
    "${BIN_NEW}" reject --indir "${DATA_CORRUPT}"

# ============================================================
# 5. Summary
# ============================================================
section "Results"
echo ""
echo "  Passed: ${PASS}"
echo "  Failed: ${FAIL}"

if [[ "${FAIL}" -gt 0 ]]; then
    echo ""
    echo "Failed steps:"
    for f in "${FAILURES[@]}"; do
        echo "  - ${f}"
    done
fi

cleanup

if [[ "${FAIL}" -gt 0 ]]; then
    echo ""
    echo "COMPATIBILITY TEST FAILED (${OLD_TAG} ↔ ${NEW_TAG})"
    exit 1
else
    echo ""
    echo "COMPATIBILITY TEST PASSED (${OLD_TAG} ↔ ${NEW_TAG})"
    exit 0
fi
