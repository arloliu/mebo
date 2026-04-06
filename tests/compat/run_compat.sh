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
#   The script automatically applies "-tags v2" for any version >= v1.5.0.
#   Add a new version gate in needs_v2_tag() if future releases add more tags.
#
# Test Matrix:
#   1. NEW encodes V1 blobs → OLD decodes:    expect all PASS  (backward compat)
#   2. OLD encodes V1 blobs → NEW decodes:    expect all PASS  (forward compat)
#   3. NEW encodes new-format blobs → OLD decodes: expect ERROR, no panic
#   4. OLD self-encode / self-decode:         expect all PASS  (baseline OLD)
#   5. NEW self-encode / self-decode:         expect all PASS  (baseline NEW)
#   6. Corruption tests against both:         expect ERROR, no panic
set -euo pipefail

# ============================================================
# Arguments
# ============================================================
OLD_TAG="${1:-v1.4.3}"
NEW_TAG="${2:-v1.5.0}"

# ============================================================
# Semver helper — returns true if $1 >= v1.5.0 (needs -tags v2)
# ============================================================
needs_v2_tag() {
    local tag="${1#v}"   # strip leading 'v'
    local major minor
    IFS='.' read -r major minor _ <<< "$tag"
    [[ "$major" -gt 1 ]] || { [[ "$major" -eq 1 ]] && [[ "$minor" -ge 5 ]]; }
}

OLD_BUILD_TAGS=""
NEW_BUILD_TAGS=""
needs_v2_tag "$OLD_TAG" && OLD_BUILD_TAGS="-tags v2" || true
needs_v2_tag "$NEW_TAG" && NEW_BUILD_TAGS="-tags v2" || true

# Slug versions for directory/binary names (strip dots and 'v').
old_slug="${OLD_TAG//[v.]}"
new_slug="${NEW_TAG//[v.]}"

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
DATA_CORRUPT="${TESTDATA}/corrupted"

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

info "Building compat binary against ${OLD_TAG} ${OLD_BUILD_TAGS:-(no extra tags)}"
# shellcheck disable=SC2086
if ! (cd "${COMPAT_OLD}" && go mod tidy 2>&1 && go build ${OLD_BUILD_TAGS} -o "${BIN_OLD}" .); then
    fail "Build ${OLD_TAG}"
    echo "FATAL: cannot continue without ${OLD_TAG} binary"
    cleanup
    exit 1
fi
ok "Build ${OLD_TAG}"

info "Building compat binary against ${NEW_TAG} ${NEW_BUILD_TAGS:-(no extra tags)}"
# shellcheck disable=SC2086
if ! (cd "${COMPAT_NEW}" && go mod tidy 2>&1 && go build ${NEW_BUILD_TAGS} -o "${BIN_NEW}" .); then
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

# ============================================================
# 3. Cross-version decode matrix
# ============================================================
section "Cross-Version Decode Matrix"

# Matrix 1: NEW decodes blobs encoded by OLD  (backward compatibility)
info "Matrix 1: ${NEW_TAG} decodes blobs encoded by ${OLD_TAG} (backward compat)"
run_step "Matrix-1 backward compat (${OLD_TAG}→${NEW_TAG})" \
    "${BIN_NEW}" decode --indir "${DATA_OLD}"

# Matrix 2: OLD decodes V1-layout blobs encoded by NEW (forward compatibility)
info "Matrix 2: ${OLD_TAG} decodes V1-layout blobs encoded by ${NEW_TAG} (forward compat)"
run_step "Matrix-2 forward compat (${NEW_TAG} V1-layout→${OLD_TAG} decoder)" \
    "${BIN_OLD}" decode --indir "${DATA_NEW_V1ONLY}"

# Matrix 3: OLD must reject new-format blobs without panicking
info "Matrix 3: ${OLD_TAG} rejects new-format blobs encoded by ${NEW_TAG} (graceful reject)"
if [[ -n "$(ls "${DATA_NEW_NEWFORMAT}"/*.blob 2>/dev/null)" ]]; then
    run_step "Matrix-3 graceful reject (${NEW_TAG} new-format→${OLD_TAG})" \
        "${BIN_OLD}" reject --indir "${DATA_NEW_NEWFORMAT}"
else
    echo "    [SKIP] No new-format blobs found — ${NEW_TAG} may not introduce a new layout"
fi

# Matrix 4: OLD self-compat baseline
info "Matrix 4: ${OLD_TAG} self-compatibility (baseline)"
run_step "Matrix-4 ${OLD_TAG} self-compat" \
    "${BIN_OLD}" decode --indir "${DATA_OLD}"

# Matrix 5: NEW self-compat baseline
info "Matrix 5: ${NEW_TAG} self-compatibility (baseline)"
run_step "Matrix-5 ${NEW_TAG} self-compat" \
    "${BIN_NEW}" decode --indir "${DATA_NEW_FULL}"

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
