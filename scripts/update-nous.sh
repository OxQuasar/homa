#!/usr/bin/env bash
#
# update-nous.sh — rebuild the sandbox image from a target nous revision
# and (optionally) recreate running user containers so they pick it up.
#
# The sandbox image bakes a pre-built nous binary (Containerfile does
# `COPY nous /usr/local/bin/nous`). This script:
#   1. optionally checks out a target ref in ~/nous (guarded: refuses on
#      a dirty tree unless --force),
#   2. shows the rev diff (deployed image label vs target HEAD),
#   3. calls sandbox/build.sh, which compiles the CGO-disabled binary +
#      runs `podman build --build-arg NOUS_REV=<sha>` (stamps the image
#      OCI revision label so the running version is always traceable),
#   4. optionally recreates running homa-user-* containers so active
#      sandboxes move to the new image.
#
# Usage:
#   ./scripts/update-nous.sh                    # build current ~/nous HEAD, no recreate
#   ./scripts/update-nous.sh --ref master       # checkout+pull master, then build
#   ./scripts/update-nous.sh --recreate         # build + recreate user containers
#   ./scripts/update-nous.sh --ref master --recreate
#   ./scripts/update-nous.sh --dry-run          # show the rev diff, change nothing
#
# Flags:
#   --ref REF     git checkout REF in ~/nous before building. Pulls if REF
#                 is a branch with an upstream. Default: build current HEAD
#                 in place (no checkout, no pull).
#   --recreate    after a successful build, `podman rm -f` every running
#                 homa-user-* container. They respawn on the next /editor
#                 visit (or WS dial) on the new image. WARNING: this drops
#                 active sandbox WS sessions — users see "Sandbox
#                 disconnected → Reconnect".
#   --force       allow --ref checkout even with a dirty working tree
#                 (stashes are NOT created; uncommitted changes may be
#                 left on the wrong branch — use deliberately).
#   --dry-run     print the deployed-vs-target rev diff + the container
#                 list that would be recreated; build nothing.
#
# homa-main is NOT touched — it serves the public site via vite and is
# managed by the mainsite watchdog, not this script.
set -euo pipefail

NOUS_REPO=${NOUS_REPO:-$HOME/nous}
HOMA_ROOT=${HOMA_ROOT:-$HOME/homa}
SANDBOX_DIR="$HOMA_ROOT/sandbox"
IMAGE_TAG=${IMAGE_TAG:-homa-sandbox:latest}

REF=""
RECREATE=0
FORCE=0
DRY_RUN=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --ref)      REF="${2:?--ref needs an argument}"; shift 2 ;;
    --recreate) RECREATE=1; shift ;;
    --force)    FORCE=1; shift ;;
    --dry-run)  DRY_RUN=1; shift ;;
    -h|--help)  sed -n '2,40p' "$0"; exit 0 ;;
    *) echo "update-nous.sh: unknown flag $1" >&2; exit 2 ;;
  esac
done

[[ -d "$NOUS_REPO/.git" ]] || { echo "update-nous.sh: $NOUS_REPO is not a git repo" >&2; exit 1; }
[[ -f "$SANDBOX_DIR/build.sh" ]] || { echo "update-nous.sh: $SANDBOX_DIR/build.sh missing" >&2; exit 1; }

# --- deployed revision (from the live image's OCI label) ---
deployed_rev() {
  podman image inspect "$IMAGE_TAG" \
    --format '{{index .Labels "org.opencontainers.image.revision"}}' 2>/dev/null \
    || echo "(no image)"
}
DEPLOYED=$(deployed_rev)

# --- optional checkout/pull of the target ref ---
if [[ -n "$REF" ]]; then
  if [[ -n "$(git -C "$NOUS_REPO" status --porcelain)" && "$FORCE" -ne 1 ]]; then
    echo "update-nous.sh: $NOUS_REPO has uncommitted changes; refusing to checkout $REF." >&2
    echo "  Commit/stash first, or pass --force to override." >&2
    exit 1
  fi
  if [[ "$DRY_RUN" -ne 1 ]]; then
    echo "==> git checkout $REF"
    git -C "$NOUS_REPO" checkout "$REF"
    # Pull only if the ref tracks an upstream (i.e. it's a branch).
    if git -C "$NOUS_REPO" rev-parse --abbrev-ref --symbolic-full-name '@{u}' >/dev/null 2>&1; then
      echo "==> git pull --ff-only"
      git -C "$NOUS_REPO" pull --ff-only
    fi
  else
    echo "(dry-run) would: git checkout $REF [+ pull if branch]"
  fi
fi

TARGET=$(git -C "$NOUS_REPO" rev-parse --short HEAD)
TARGET_BRANCH=$(git -C "$NOUS_REPO" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "?")
TARGET_SUBJECT=$(git -C "$NOUS_REPO" log -1 --format='%s')

echo
echo "  deployed image rev : $DEPLOYED"
echo "  target  nous   rev : $TARGET  ($TARGET_BRANCH)  — $TARGET_SUBJECT"
if [[ "$DEPLOYED" == "$TARGET" ]]; then
  echo "  → already up to date."
else
  echo "  → would move: $DEPLOYED → $TARGET"
fi
echo

# --- running user containers (recreation targets) ---
mapfile -t USER_CONTAINERS < <(podman ps --format '{{.Names}}' 2>/dev/null | grep '^homa-user-' || true)
if [[ "${#USER_CONTAINERS[@]}" -gt 0 ]]; then
  echo "  running user containers (${#USER_CONTAINERS[@]}):"
  printf '    %s\n' "${USER_CONTAINERS[@]}"
else
  echo "  running user containers: none"
fi
echo

if [[ "$DRY_RUN" -eq 1 ]]; then
  echo "(dry-run) no build, no recreate. Re-run without --dry-run to apply."
  exit 0
fi

# --- build ---
echo "==> building image $IMAGE_TAG from nous $TARGET"
NOUS_REPO="$NOUS_REPO" IMAGE_TAG="$IMAGE_TAG" "$SANDBOX_DIR/build.sh"

NEW_DEPLOYED=$(deployed_rev)
echo "==> image rev now: $NEW_DEPLOYED"

# --- recreate ---
if [[ "$RECREATE" -eq 1 ]]; then
  if [[ "${#USER_CONTAINERS[@]}" -gt 0 ]]; then
    echo "==> recreating ${#USER_CONTAINERS[@]} user container(s) (drops active sessions)"
    for c in "${USER_CONTAINERS[@]}"; do
      podman rm -f "$c" >/dev/null 2>&1 && echo "    removed $c (will respawn on next visit)"
    done
  else
    echo "==> --recreate: no running user containers to recreate"
  fi
else
  echo "==> not recreating containers (no --recreate)."
  echo "    Running sandboxes keep the OLD image until they're next"
  echo "    restarted. New spawns use $NEW_DEPLOYED immediately."
fi

echo "==> done."
