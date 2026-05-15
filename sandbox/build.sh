#!/bin/bash
# Build the homa-sandbox image.
#
#   1. Compile nous as a CGO-disabled, static-ish ELF (so it runs on the slim
#      Debian base without glibc surprises).
#   2. Capture the nous git short revision; bake it into the image as an OCI
#      label so we can trace which build is running in a sandbox.
#   3. If podman is present, run `podman build`. Otherwise leave the staged
#      artifacts in place and exit 0 with a clear message — the live image
#      build can happen on a host that has podman.
#
# Idempotent: rerun freely.

set -euo pipefail

NOUS_REPO=${NOUS_REPO:-$HOME/nous}
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
OUT=$SCRIPT_DIR
IMAGE_TAG=${IMAGE_TAG:-homa-sandbox:latest}

if [[ ! -d "$NOUS_REPO" ]]; then
  echo "build.sh: NOUS_REPO=$NOUS_REPO does not exist" >&2
  exit 1
fi

echo "==> building nous binary (linux/amd64, CGO_ENABLED=0) from $NOUS_REPO"
(
  cd "$NOUS_REPO"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags='-s -w' -o "$OUT/nous" .
)
echo "==> nous binary: $(file "$OUT/nous" | cut -d: -f2-)"

NOUS_REV=$(git -C "$NOUS_REPO" rev-parse --short HEAD)
echo "==> NOUS_REV=$NOUS_REV"

if command -v podman >/dev/null 2>&1; then
  echo "==> podman build $IMAGE_TAG"
  podman build \
    --build-arg "NOUS_REV=$NOUS_REV" \
    -t "$IMAGE_TAG" \
    -f "$OUT/Containerfile" \
    "$OUT"
  echo "==> done: $IMAGE_TAG"
else
  echo "==> podman not installed; image artifacts ready at $OUT"
  echo "    Run on a podman-enabled host:"
  echo "      podman build --build-arg NOUS_REV=$NOUS_REV -t $IMAGE_TAG -f $OUT/Containerfile $OUT"
  exit 0
fi
