#!/bin/sh

# Copyright (c) 2026 Zededa, Inc.
# SPDX-License-Identifier: Apache-2.0

# Populate evetest/ with the evetest harness from master so that the tests kept
# on this branch compile locally (go vet, IDE). The harness itself is not part
# of this branch (see .gitignore); the container image provides it at run time.
#
# Usage: sync-harness.sh <git-ref> [remote]
#   <git-ref>  master commit to take the harness from
#   [remote]   remote name or URL to fetch it from if missing locally (default: upstream)
#
# Tracked files (tests, netmodels, matchers, Makefile, go.mod, go.sum, sync-harness.sh) are
# never touched. Previously synced harness files are removed first.

set -e

REF="${1:?usage: $0 <git-ref> [remote]}"
REMOTE="${2:-upstream}"

DIR="$(cd "$(dirname "$0")" && pwd)"
TOP="$(git -C "$DIR" rev-parse --show-toplevel)"
SUBDIR="${DIR#"$TOP"/}"

if ! git -C "$TOP" cat-file -e "$REF^{commit}" 2>/dev/null; then
    echo "Fetching $REF from $REMOTE..."
    git -C "$TOP" fetch "$REMOTE" "$REF"
fi

KEEP=".gitignore Makefile go.mod go.sum tests netmodels matchers sync-harness.sh"

# Remove stale harness files from an earlier sync.
for entry in "$DIR"/* "$DIR"/.[!.]*; do
    [ -e "$entry" ] || continue
    name="$(basename "$entry")"
    case " $KEEP " in
        *" $name "*) ;;
        *) rm -rf "$entry" ;;
    esac
done

echo "Extracting evetest harness from $REF..."
git -C "$TOP" archive "$REF" "$SUBDIR" | tar -x -C "$DIR" --strip-components=1 \
    --exclude="$SUBDIR/tests" --exclude="$SUBDIR/netmodels" \
    --exclude="$SUBDIR/matchers" --exclude="$SUBDIR/Makefile" \
    --exclude="$SUBDIR/go.mod" --exclude="$SUBDIR/go.sum" \
    --exclude="$SUBDIR/.gitignore"

echo "Done. Harness from $REF is in $DIR (git-ignored)."
