#!/usr/bin/env bash
# (Re)builds the deterministic QA-Bench dataset under the test account
# paveryutu72@hotmail.com and verifies it is fully committed server-side, so
# the FUSE cache battery (fuse_bench.py) reads identical committed data for
# the baseline and current binaries.
#
# Does NOT touch the primary account. Uses the CURRENT binary for the seed
# write (content is random but sizes fixed); the battery only READS it.
#
# Usage: ./seed_data.sh [BINARIO_CURRENT]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="${1:-$SCRIPT_DIR/bin/onecloudriver-current}"
ACCOUNT=paveryutu72@hotmail.com
MP="$HOME/OneDrive/$ACCOUNT"
B="$MP/QA-Bench"

# --- helper: mount a given binary at $MP with an isolated session cache-dir ---
mount_at() {
    # $1 = cache_dir, $2 = log path
    local cache="$1" log="$2"
    if grep -q "$MP" /proc/mounts; then fusermount3 -u "$MP" 2>/dev/null || true; sleep 1; fi
    rm -rf "$cache"; mkdir -p "$cache"
    # Pass values through the environment so the inner sh -c needs no nested quotes.
    BIN="$BIN" ACCOUNT="$ACCOUNT" MP="$MP" CACHE="$cache" LOG="$log" \
        setsid sh -c 'exec "$BIN" mount "$MP" -a "$ACCOUNT" --cache-dir "$CACHE" --pre-warm-depth 1 >>"$LOG" 2>&1 </dev/null' &
    disown
    for _ in $(seq 1 40); do grep -q "$MP" /proc/mounts && break; sleep 1; done
    sleep 2
}

echo ">> rebuilding dataset in $B"

mount_at /tmp/ocr-bench/cache-seed /tmp/ocr-bench/seed.log

# remove any leftovers of previous benchmark writes so the set is deterministic
rm -f "$B"/coldfile_* "$B"/wrb_*.tmp "$B"/mix_*.tmp "$B"/large.bin "$B"/med.bin "$B"/small.txt
rm -rf "$B/metadir"

mkdir -p "$B/metadir"
dd if=/dev/urandom of="$B/large.bin" bs=1M count=8 2>/dev/null
dd if=/dev/urandom of="$B/med.bin" bs=1M count=1 2>/dev/null
head -c 1024 /dev/urandom > "$B/small.txt"
for i in $(seq 1 100); do printf 'entry-%03d data line\n' "$i" > "$B/metadir/f$i.txt"; done
for i in $(seq 1 100); do head -c 256 /dev/urandom > "$B/coldfile_$i"; done
sync

echo ">> forcing server sync + uploads to drain (3 passes with waits)"
for _ in 1 2 3; do
    "$BIN" sync -a "$ACCOUNT" 2>&1 | tail -1
    sleep 30
done

# Graceful unmount lets the FUSE daemon flush any still-queued content uploads
echo ">> graceful unmount (flush upload queue)"
fusermount3 -u "$MP" 2>&1 || true
sleep 3

# Re-mount fresh and verify from the server's committed state (authoritative)
echo ">> verifying committed dataset (fresh mount, authoritative)"
mount_at /tmp/ocr-bench/cache-seed-verify /tmp/ocr-bench/seed-verify.log

CF=0; for i in $(seq 1 100); do stat -c '%s' "$B/coldfile_$i" >/dev/null 2>&1 && CF=$((CF+1)); done
MF=0; for i in $(seq 1 100); do stat -c '%s' "$B/metadir/f$i.txt" >/dev/null 2>&1 && MF=$((MF+1)); done
echo "  coldfiles committed : $CF/100"
echo "  metadir entries     : $MF/100"
echo "  large=$(stat -c%s "$B/large.bin" 2>/dev/null) med=$(stat -c%s "$B/med.bin" 2>/dev/null) small=$(stat -c%s "$B/small.txt" 2>/dev/null)"
if [ "$CF" -ne 100 ] || [ "$MF" -ne 100 ]; then
    echo "  ERROR: dataset incomplete; re-run seed_data.sh"
    fusermount3 -u "$MP" 2>/dev/null || true
    exit 1
fi

echo ">> unmounting verify instance"
fusermount3 -u "$MP" 2>&1 || true
sleep 1
echo "done."