#!/usr/bin/env bash
# Renders every snapshot scene to app/build/snapshots/<scene>-<light|dark>.png.
# Extra arguments go to DeskhandSnapshots, e.g. `--only components-gallery`.
set -euo pipefail

app_dir="$(cd "$(dirname "$0")/.." && pwd)"
package_dir="$app_dir/DeskhandKit"
scratch="$app_dir/build/spm"
out="$app_dir/build/snapshots"

if ! build_log="$(swift build --package-path "$package_dir" --scratch-path "$scratch" --product DeskhandSnapshots 2>&1)"; then
  echo "$build_log" >&2
  exit 1
fi
bin_dir="$(swift build --package-path "$package_dir" --scratch-path "$scratch" --show-bin-path)"
"$bin_dir/DeskhandSnapshots" --out "$out" "$@"
