#!/bin/sh
# Does this plugin need updating after an upstream CLIProxyAPI release?
#
# A plugin binary is tied to the host only through the plugin ABI, so the answer
# is decided by whether sdk/pluginabi changed and whether sdk/pluginapi changed
# in a breaking way -- not by the host's version number. This prints that verdict
# for the SDK version go.mod pins, against any target tag.
#
#   scripts/check-plugin-abi.sh v7.3.12
set -e

TARGET="${1:?usage: $0 <CLIProxyAPI tag, e.g. v7.3.12>}"
REPO=router-for-me/CLIProxyAPI

PINNED=$(sed -n 's|.*github.com/router-for-me/CLIProxyAPI/v7 \(v[0-9][0-9.]*\).*|\1|p' go.mod | head -1)
[ -n "$PINNED" ] || { echo "cannot read the pinned CLIProxyAPI version from go.mod"; exit 1; }

CACHE=$(go env GOMODCACHE)
OLD=$(ls -d "$CACHE"/github.com/router-for-me/*/v7@"$PINNED"/sdk 2>/dev/null | head -1)
[ -n "$OLD" ] || { echo "pinned SDK $PINNED is not in the module cache; run: go mod download"; exit 1; }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

for dir in pluginabi pluginapi; do
    mkdir -p "$WORK/$dir"
    for file in "$OLD/$dir"/*.go; do
        base=$(basename "$file")
        case "$base" in *_test.go) continue;; esac
        curl -sSf "https://raw.githubusercontent.com/$REPO/$TARGET/sdk/$dir/$base" -o "$WORK/$dir/$base" \
            || { echo "cannot fetch sdk/$dir/$base at $TARGET"; exit 1; }
    done
done

echo "pinned $PINNED  ->  target $TARGET"
echo

# The two constants that decide compatibility, printed from both sides.
for constant in ABIVersion SchemaVersion; do
    # Declared as "Name uint32 = N". Anchored to the line start so that
    # SchemaVersion does not also match SchemaVersionStreamChunkOmitRequestBody.
    old_value=$(sed -n "s/^[[:space:]]*$constant uint32 = \([0-9]*\).*/\1/p" "$OLD/pluginabi/types.go" | head -1)
    new_value=$(sed -n "s/^[[:space:]]*$constant uint32 = \([0-9]*\).*/\1/p" "$WORK/pluginabi/types.go" | head -1)
    if [ "$old_value" = "$new_value" ]; then
        echo "  $constant:     $old_value  (unchanged)"
    else
        echo "  $constant:     $old_value  ->  $new_value   <-- CHANGED"
    fi
done
echo

# Compare file by file. diff -r cannot be used for the verdict: the old side
# still holds the *_test.go files this script skips, which reads as "only in
# old" and would report every ABI as changed.
removed_lines=0
changed_any=0
for dir in pluginabi pluginapi; do
    for file in "$WORK/$dir"/*.go; do
        base=$(basename "$file")
        old="$OLD/$dir/$base"
        if [ ! -f "$old" ]; then
            echo "  sdk/$dir/$base: NEW FILE at the target"
            changed_any=1
            continue
        fi
        added=$(diff "$old" "$file" | grep -c '^>' || true)
        gone=$(diff "$old" "$file" | grep -c '^<' || true)
        [ "$added" = "0" ] && [ "$gone" = "0" ] && continue
        echo "  sdk/$dir/$base:  +$added  -$gone"
        changed_any=1
        removed_lines=$((removed_lines + gone))
        diff -u "$old" "$file" | grep -E '^[+-][^+-]' | sed 's/^/      /'
    done
done
[ "$changed_any" = "0" ] && echo "  no source file differs"

echo
echo "---"
if [ "$removed_lines" -gt 0 ]; then
    echo "VERDICT: lines were REMOVED from the SDK, which can break this plugin."
    echo "         Read the diff above before rebuilding."
elif [ "$changed_any" = "0" ]; then
    echo "VERDICT: SDK unchanged. No update needed."
else
    echo "VERDICT: additions only. The ABI is compatible, so no update is required:"
    echo "         rebuild only to adopt a new field or capability deliberately."
fi
