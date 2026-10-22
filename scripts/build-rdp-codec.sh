#!/bin/bash
# Native macOS H.264 decoder only. No Homebrew libraries or global installs.
# Sources: https://ffmpeg.org/download.html and https://ffmpeg.org/legal.html
# The vendored tar is an exact git archive of the official, pinned release tag.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CODEC="$ROOT/build/rdp/codec"
SOURCE="$CODEC/source"
BUILD="$CODEC/objects"
PREFIX="$CODEC/prefix"
LOGS="$CODEC/logs"
VERSION=9.0.2
TAG=n9.0.2
PIN=946fcce07b6dcd0331c8cc609192aeff5e1924f8
TAG_PIN=ce8f11b9fac4cd7ccfb41ca7c01bc17f5cda8cc3
REPOSITORY=https://git.ffmpeg.org/ffmpeg.git
ARCHIVE="$CODEC/downloads/ffmpeg-$VERSION-source.tar"
# SHA-256 of `git archive --format=tar --prefix=ffmpeg-9.0.2/ <PIN>`.
SOURCE_SHA256=e4897f3fbe804937465efe4e63c485c39102e39a59d6ea9a4c8ddafc138fc903

fail() { printf '%s\n' "$*" >&2; exit 1; }
[[ $# == 0 ]] || fail 'No configure arguments accepted; this build has a fixed LGPL configuration.'
[[ "$(uname -s)" == Darwin ]] || fail 'macOS is required.'
ARCH="$(uname -m)"
case "$ARCH" in arm64|x86_64) ;; *) fail "Unsupported native architecture: $ARCH" ;; esac
# Prevent caller compiler/linker/pkg-config settings from introducing dependencies.
unset CFLAGS CPPFLAGS CXXFLAGS LDFLAGS LIBRARY_PATH CPATH C_INCLUDE_PATH \
  CPLUS_INCLUDE_PATH SDKROOT MACOSX_DEPLOYMENT_TARGET CC CXX LD AR NM RANLIB \
  PKG_CONFIG_PATH PKG_CONFIG_LIBDIR PKG_CONFIG_SYSROOT_DIR PKG_CONFIG_SYSTEM_LIBRARY_PATH \
  PKG_CONFIG_SYSTEM_INCLUDE_PATH MAKEFLAGS MFLAGS DYLD_LIBRARY_PATH DYLD_FALLBACK_LIBRARY_PATH
export PATH="/usr/bin:/bin:/usr/sbin:/sbin:$PATH"
export LC_ALL=C
export ZERO_AR_DATE=1
for tool in git clang make pkg-config shasum otool lipo tar; do
  command -v "$tool" >/dev/null || fail "Required tool missing: $tool"
done
JOBS="${RDP_CODEC_JOBS:-$(sysctl -n hw.logicalcpu)}"
[[ "$JOBS" =~ ^[1-9][0-9]*$ ]] || fail 'RDP_CODEC_JOBS must be a positive integer.'
mkdir -p "$CODEC/downloads" "$LOGS"
# Atomic directory lock also protects prefix installation from simultaneous callers.
mkdir "$CODEC/.lock" 2>/dev/null || fail "Codec build already locked: $CODEC/.lock"
trap 'rmdir "$CODEC/.lock"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'code=$?; printf "Codec build failed (exit %s). Logs: %s\n" "$code" "$LOGS" >&2' ERR

if [[ ! -e "$SOURCE" ]]; then
  git clone --branch "$TAG" --depth 1 "$REPOSITORY" "$SOURCE" > "$LOGS/source.log" 2>&1
fi
[[ -d "$SOURCE/.git" && ! -L "$SOURCE" ]] || fail "Expected an official Git checkout at $SOURCE"
[[ "$(git -C "$SOURCE" remote get-url origin)" == "$REPOSITORY" ]] || fail 'Unexpected FFmpeg origin.'
[[ "$(git -C "$SOURCE" rev-parse HEAD)" == "$PIN" ]] || fail "FFmpeg HEAD must be $PIN; refusing to change checkout."
[[ "$(git -C "$SOURCE" rev-parse "refs/tags/$TAG")" == "$TAG_PIN" ]] || fail 'Unexpected release tag object.'
[[ "$(git -C "$SOURCE" rev-parse "refs/tags/$TAG^{commit}")" == "$PIN" ]] || fail 'Release tag does not resolve to pinned commit.'
pristine() {
  # Include ignored/untracked files; reject flags which can hide tracked changes.
  git -C "$SOURCE" ls-files -v > "$LOGS/source-index.txt"
  if grep -Eq '^([a-z]|S) ' "$LOGS/source-index.txt"; then
    fail 'FFmpeg index uses assume-unchanged/skip-worktree; refusing checkout.'
  fi
  git -c core.fsmonitor=false -c core.untrackedCache=false -C "$SOURCE" \
    status --porcelain=v1 --untracked-files=all --ignored > "$LOGS/source-status.txt"
  [[ ! -s "$LOGS/source-status.txt" ]] || fail "FFmpeg checkout must be pristine, including ignored/untracked files; see $LOGS/source-status.txt"
  git -C "$SOURCE" diff --no-ext-diff --exit-code HEAD -- > "$LOGS/changes.diff"
}
pristine
git -C "$SOURCE" fsck --no-reflogs > "$LOGS/source-fsck.log" 2>&1
if [[ ! -f "$ARCHIVE" ]]; then
  git -C "$SOURCE" archive --format=tar --prefix="ffmpeg-$VERSION/" "$PIN" > "$ARCHIVE.tmp"
  mv "$ARCHIVE.tmp" "$ARCHIVE"
fi
ACTUAL_SHA256="$(shasum -a 256 "$ARCHIVE")"
[[ "${ACTUAL_SHA256%% *}" == "$SOURCE_SHA256" ]] || fail 'Vendored source archive checksum mismatch; refusing to overwrite.'

CONFIGURE=(
  "$SOURCE/configure" "--prefix=$PREFIX" "--arch=$ARCH" --target-os=darwin
  --cc=/usr/bin/clang --cxx=/usr/bin/clang++
  --enable-shared --disable-static --enable-pic
  --disable-everything --disable-autodetect --disable-programs --disable-doc
  --disable-network --disable-gpl --disable-nonfree --disable-version3
  --disable-avdevice --disable-avfilter --disable-avformat
  --disable-swresample --enable-swscale
  --enable-avcodec --enable-avutil --enable-decoder=h264 --enable-parser=h264
  --disable-debug --disable-x86asm
)
mkdir -p "$BUILD"
{
  printf 'cd %q\n' "$BUILD"
  printf '%q ' "${CONFIGURE[@]}"
  printf '\n'
} > "$LOGS/configure-command.sh"
{
  cat "$LOGS/configure-command.sh"
  printf 'commit=%s\narchive_sha256=%s\n' "$PIN" "$SOURCE_SHA256"
  /usr/bin/clang --version
  xcrun --show-sdk-path
  xcrun --show-sdk-version
} > "$LOGS/build-inputs.txt"
if [[ ! -f "$BUILD/config.h" || ! -f "$BUILD/build-inputs.txt" ]] || \
    ! cmp -s "$LOGS/build-inputs.txt" "$BUILD/build-inputs.txt"; then
  if [[ -f "$BUILD/ffbuild/config.mak" ]]; then
    (cd "$BUILD" && /usr/bin/make distclean) > "$LOGS/distclean.log" 2>&1
  fi
  (cd "$BUILD" && "${CONFIGURE[@]}") > "$LOGS/configure.log" 2>&1
  cp "$LOGS/build-inputs.txt" "$BUILD/build-inputs.txt"
fi
(cd "$BUILD" && /usr/bin/make -j "$JOBS") > "$LOGS/build.log" 2>&1
(cd "$BUILD" && /usr/bin/make install) > "$LOGS/install.log" 2>&1
pristine

# Isolate pkg-config from all default/Homebrew search directories.
export PKG_CONFIG_PATH=
export PKG_CONFIG_LIBDIR="$PREFIX/lib/pkgconfig"
export PKG_CONFIG_SYSROOT_DIR=
PACKAGES=(libavcodec libavutil libswscale)
{
  for package in "${PACKAGES[@]}"; do
    [[ "$(pkg-config --variable=prefix "$package")" == "$PREFIX" ]] || fail "Wrong pkg-config prefix for $package"
    printf '%s %s\n' "$package" "$(pkg-config --modversion "$package")"
  done
  pkg-config --cflags --libs "${PACKAGES[@]}"
} > "$LOGS/pkg-config.txt"
for package in libavdevice libavfilter libavformat libswresample; do
  if pkg-config --exists "$package"; then fail "Unexpected installed package: $package"; fi
done
for library in "$PREFIX"/lib/*; do
  case "${library##*/}" in libavcodec*.dylib|libavutil*.dylib|libswscale*.dylib|pkgconfig) ;; *) fail "Unexpected installed library: $library" ;; esac
done
: > "$LOGS/otool-dependencies.txt"
for library in "$PREFIX"/lib/*.dylib; do
  [[ ! -L "$library" ]] || continue
  [[ "$(lipo -archs "$library")" == "$ARCH" ]] || fail "Wrong library architecture: $library"
  otool -L "$library" > "$LOGS/otool-current.txt"
  cat "$LOGS/otool-current.txt" >> "$LOGS/otool-dependencies.txt"
  # otool emits the dylib ID followed by load commands. Only our three libraries
  # and Apple system libraries/frameworks may appear, including transitively.
  while IFS= read -r dependency; do
    case "$dependency" in
      "$PREFIX"/lib/libavcodec*.dylib|"$PREFIX"/lib/libavutil*.dylib|"$PREFIX"/lib/libswscale*.dylib)
        [[ -f "$dependency" ]] || fail "Missing dependency: $dependency" ;;
      /usr/lib/*|/System/Library/Frameworks/*|/System/Library/PrivateFrameworks/*) ;;
      *) fail "Non-system dependency: $dependency" ;;
    esac
  done < <(awk 'NR > 1 { sub(/^[ \t]+/, ""); sub(/ \(compatibility version.*$/, ""); print }' "$LOGS/otool-current.txt")
done

cat > "$LOGS/verify.c" <<'C'
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <libavcodec/avcodec.h>
#include <libavutil/avutil.h>
#include <libswscale/swscale.h>

int main(int argc, char **argv) {
    const char *license = "LGPL version 2.1 or later";
    const char *config = avcodec_configuration();
    if (strcmp(avcodec_license(), license) || strcmp(avutil_license(), license) || strcmp(swscale_license(), license)) return 1;
    if (strcmp(config, avutil_configuration()) || strcmp(config, swscale_configuration())) return 2;
    const char *required[] = { "--disable-gpl", "--disable-nonfree", "--disable-version3",
        "--disable-autodetect", "--disable-network", "--disable-static", "--enable-shared",
        "--disable-everything", "--disable-programs", "--enable-decoder=h264", "--enable-parser=h264" };
    for (unsigned i = 0; i < sizeof(required)/sizeof(required[0]); ++i)
        if (!strstr(config, required[i])) return 3;
    if (strstr(config, "--enable-gpl") || strstr(config, "--enable-nonfree") ||
        strstr(config, "--enable-version3")) return 4;
    void *iter = NULL;
    const AVCodec *codec;
    unsigned count = 0;
    while ((codec = av_codec_iterate(&iter))) {
        if (codec->id != AV_CODEC_ID_H264 || !av_codec_is_decoder(codec)) return 5;
        ++count;
    }
    if (count != 1) return 6;
    AVCodecParserContext *parser = av_parser_init(AV_CODEC_ID_H264);
    if (!parser) return 15;
    av_parser_close(parser);
    AVCodecContext *ctx = avcodec_alloc_context3(avcodec_find_decoder(AV_CODEC_ID_H264));
    if (!ctx || avcodec_open2(ctx, ctx->codec, NULL) < 0) return 7;
    printf("avcodec_license: %s\navutil_license: %s\nswscale_license: %s\nconfiguration: %s\nH264 decoder: opened\n",
           avcodec_license(), avutil_license(), swscale_license(), config);
    /* Optional fixture: a single complete Annex B access unit, as used by RDP. */
    if (argc == 2) {
        FILE *f = fopen(argv[1], "rb");
        if (!f || fseek(f, 0, SEEK_END)) return 8;
        long size = ftell(f);
        if (size <= 0 || size > 16 * 1024 * 1024 || fseek(f, 0, SEEK_SET)) return 9;
        AVPacket *pkt = av_packet_alloc();
        AVFrame *frame = av_frame_alloc();
        if (!pkt || !frame || av_new_packet(pkt, (int)size) < 0) return 10;
        if (fread(pkt->data, 1, (size_t)size, f) != (size_t)size) return 11;
        fclose(f);
        if (avcodec_send_packet(ctx, pkt) < 0) return 12;
        int result = avcodec_receive_frame(ctx, frame);
        if (result == AVERROR(EAGAIN)) {
            if (avcodec_send_packet(ctx, NULL) < 0) return 13;
            result = avcodec_receive_frame(ctx, frame);
        }
        if (result < 0 || frame->width <= 0 || frame->height <= 0) return 14;
        printf("H264 fixture: decoded %dx%d\n", frame->width, frame->height);
        av_frame_free(&frame);
        av_packet_free(&pkt);
    }
    avcodec_free_context(&ctx);
    return 0;
}
C
# Resolve the compilation paths through the isolated pkg-config files. Arrays
# preserve spaces in project paths without evaluating pkg-config output as code.
CFLAGS=("-I$(pkg-config --variable=includedir libavcodec)" "-I$(pkg-config --variable=includedir libavutil)")
LIBS=("-L$(pkg-config --variable=libdir libavcodec)" "-L$(pkg-config --variable=libdir libavutil)" -lavcodec -lavutil -lswscale)
pkg-config --exists "${PACKAGES[@]}"
/usr/bin/clang -Wall -Wextra -Werror "${CFLAGS[@]}" "$LOGS/verify.c" \
  "${LIBS[@]}" -o "$LOGS/verify" > "$LOGS/verify-build.log" 2>&1
"$LOGS/verify" > "$LOGS/verification.txt" 2>&1
if [[ -n "${RDP_CODEC_H264_FIXTURE:-}" ]]; then
  "$LOGS/verify" "$RDP_CODEC_H264_FIXTURE" >> "$LOGS/verification.txt" 2>&1
fi

LICENSES="$PREFIX/share/licenses/ffmpeg"
PROVENANCE="$PREFIX/share/ffmpeg"
mkdir -p "$LICENSES" "$PROVENANCE"
cp "$SOURCE/COPYING.LGPLv2.1" "$SOURCE/LICENSE.md" "$LICENSES/"
cp "$ARCHIVE" "$PROVENANCE/"
cp "$LOGS/configure-command.sh" "$LOGS/build-inputs.txt" "$LOGS/changes.diff" \
  "$LOGS/verification.txt" "$LOGS/pkg-config.txt" "$LOGS/otool-dependencies.txt" "$PROVENANCE/"
cp "$BUILD/config.h" "$BUILD/ffbuild/config.mak" "$PROVENANCE/"
cat > "$PROVENANCE/source-pin.txt" <<EOF
FFmpeg $VERSION
repository $REPOSITORY
tag $TAG
tag_object $TAG_PIN
commit $PIN
source_archive ffmpeg-$VERSION-source.tar
source_archive_sha256 $SOURCE_SHA256
archive_command git archive --format=tar --prefix=ffmpeg-$VERSION/ $PIN
official_download https://ffmpeg.org/download.html
official_release_tarball https://ffmpeg.org/releases/ffmpeg-$VERSION.tar.xz
source_archive_origin Official Git release tag; exported unchanged, with no local patches.
license LGPL-2.1-or-later
architecture $ARCH
libraries avcodec avutil swscale
swscale enabled: required by FreeRDP h264_ffmpeg.c
videotoolbox disabled; hardware decoding is not claimed
EOF
cat > "$PROVENANCE/README.txt" <<'EOF'
This software uses libraries from FFmpeg under LGPL version 2.1 or later.
The accompanying source archive is exported from the pinned official Git commit.
COPYING.LGPLv2.1 and LICENSE.md are in ../licenses/ffmpeg/.
configure-command.sh, build-inputs.txt, config.h and config.mak record this build.
changes.diff is empty: the source checkout is pristine and builds out of tree.
The script uses native /usr/bin/clang and make; it does not promise bit-identical
output across toolchain/SDK versions. Re-run scripts/build-rdp-codec.sh to build
and verify; RDP_CODEC_JOBS controls parallelism. RDP_CODEC_H264_FIXTURE optionally
tests a single complete Annex B H.264 access unit (up to 16 MiB).
Only H.264 software decoding, avcodec, avutil and required swscale are enabled. No programs,
network, external libraries, GPL, nonfree, version3, avformat, avdevice,
avfilter, swresample or VideoToolbox. FreeRDP integration and app distribution
notices/source delivery remain the caller's responsibility.
EOF
printf '%s\n' "$PREFIX"
