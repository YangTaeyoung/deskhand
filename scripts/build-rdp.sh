#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RDP_ROOT="$ROOT/build/rdp"
SOURCE="$RDP_ROOT/FreeRDP"
ENGINE="$RDP_ROOT/engine"
PIN=97eff6ddfbbfaa8e42a126d362686bd001ff8bdc
VERSION=3.32.2
[[ "$(uname -s)" == Darwin ]] || { echo 'macOS is required' >&2; exit 1; }
mkdir -p "$RDP_ROOT"
if [[ ! -d "$SOURCE/.git" ]]; then
  git clone --branch "$VERSION" --depth 1 https://github.com/FreeRDP/FreeRDP.git "$SOURCE" >&2
fi
[[ "$(git -C "$SOURCE" rev-parse HEAD)" == "$PIN" ]] || {
  echo "FreeRDP must be $VERSION at $PIN; refusing to change an existing checkout" >&2; exit 1;
}
[[ -z "$(git -C "$SOURCE" status --porcelain --untracked-files=no)" ]] || {
  echo 'FreeRDP checkout must be pristine' >&2; exit 1;
}
OPENSSL_PREFIX="$(brew --prefix openssl@3)"
# Use our fixed LGPL decoder build; never silently bundle a developer's GPL FFmpeg.
FFMPEG_PREFIX="$("$ROOT/scripts/build-rdp-codec.sh")"
export PKG_CONFIG_PATH="$FFMPEG_PREFIX/lib/pkgconfig:$OPENSSL_PREFIX/lib/pkgconfig"
FFMPEG=ON
pkg-config --exists libavcodec libavutil
cmake -S "$ROOT/native/rdp" -B "$ENGINE" -G Ninja \
  -U '*FFmpeg*' -U '*FFMPEG*' \
  -DCMAKE_BUILD_TYPE=RelWithDebInfo -DFREERDP_SOURCE_DIR="$SOURCE" \
  -DCMAKE_PREFIX_PATH="$OPENSSL_PREFIX${FFMPEG_PREFIX:+;$FFMPEG_PREFIX}" \
  -DOPENSSL_ROOT_DIR="$OPENSSL_PREFIX" -DDH_RDP_FFMPEG="$FFMPEG" \
  -DDH_RDP_CODEC_ROOT="$FFMPEG_PREFIX" \
  -DDH_RDP_TESTS=ON "$@" > "$RDP_ROOT/configure.log" 2>&1 || {
    tail -80 "$RDP_ROOT/configure.log" >&2; exit 1;
  }
cmake --build "$ENGINE" --target DeskhandRDPEngine rdp_test_abi rdp_test_engine rdp_test_disp \
  > "$RDP_ROOT/build.log" 2>&1 || { tail -80 "$RDP_ROOT/build.log" >&2; exit 1; }
# Match the engine's explicit provider search directory in unbundled native tests.
cp -f "$OPENSSL_PREFIX/lib/ossl-modules/legacy.dylib" "$ENGINE/legacy.dylib"
ctest --test-dir "$ENGINE" --output-on-failure > "$RDP_ROOT/tests.log" 2>&1 || {
  cat "$RDP_ROOT/tests.log" >&2; exit 1;
}
mkdir -p "$ENGINE/licenses/FreeRDP"
cp "$SOURCE/LICENSE" "$ENGINE/licenses/FreeRDP/LICENSE"
if [[ -f "$SOURCE/NOTICE" ]]; then
  cp "$SOURCE/NOTICE" "$ENGINE/licenses/FreeRDP/NOTICE"
fi
cp "$ROOT/native/rdp/patch_disp.py" "$ENGINE/licenses/FreeRDP/deskhand-disp-patch.py"
cp "$ROOT/native/rdp/NOTICE.md" "$ENGINE/licenses/NOTICE.md"
cp "$OPENSSL_PREFIX/LICENSE.txt" "$ENGINE/licenses/OpenSSL-LICENSE.txt"
JSON_PREFIX="$(brew --prefix json-c 2>/dev/null || true)"
if [[ -f "$JSON_PREFIX/COPYING" ]]; then cp "$JSON_PREFIX/COPYING" "$ENGINE/licenses/json-c-COPYING"; fi
printf 'FreeRDP %s\ncommit %s\nFFmpeg H.264 %s\n' "$VERSION" "$PIN" "$FFMPEG" > "$ENGINE/dependency-pin.txt"
# Keep original install names intact for the app worker's recursive bundling.
find "$ENGINE" -name '*.dylib' -type f -exec otool -L {} \; > "$ENGINE/otool-dependencies.txt"
printf '%s\n' "$ENGINE/libDeskhandRDPEngine.dylib"
