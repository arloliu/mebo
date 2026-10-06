#!/usr/bin/env bash
# check-encoder-hotpath.sh: confirm the Gorilla and Chimp encoders' bit-spill hot path is still inlined and barrier-free.
#
# Each 8-byte spill stores into the byte buffer with a self-reslice, which writes only the slice length,
# so it takes no GC write barrier (docs/specs/encoder-write-barriers-design.md).
# appendBits has little inline-budget headroom, so an edit or a Go release can silently undo either property.
# check_encoder_hotpath.py holds the rules and their parser fixtures, which run first.
# Run this before committing changes to these encoders; it is not part of make test.
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd)
cd "$repo"
module=github.com/arloliu/mebo
checker=scripts/check_encoder_hotpath.py
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "toolchain: $(go version)"
echo "env: $(go env GOOS GOARCH GOAMD64 | paste -sd ' ') GOFLAGS='$(go env GOFLAGS)' GOEXPERIMENT='$(go env GOEXPERIMENT)'"
python3 "$checker" selftest
status=0
for codec in gorilla chimp; do
  pkg=$module/internal/encoding/value/$codec
  dir=./internal/encoding/value/$codec
  # -a forces a rebuild: a cached build prints no compiler diagnostics.
  echo "cmd: go build -a -gcflags='$pkg=-m' $dir; go build -a -gcflags='$pkg=-S' $dir"
  go build -a -gcflags="$pkg=-m" "$dir" 2>"$tmp/$codec.m"
  go build -a -gcflags="$pkg=-S" "$dir" 2>"$tmp/$codec.S"
  python3 "$checker" check "$codec" "internal/encoding/value/$codec/$codec.go" "$tmp/$codec.m" "$tmp/$codec.S" || status=1
done
exit $status
