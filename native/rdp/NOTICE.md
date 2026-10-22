# Deskhand embedded RDP engine

FreeRDP 3.32.2, commit `97eff6ddfbbfaa8e42a126d362686bd001ff8bdc`.
Source: https://github.com/FreeRDP/FreeRDP/releases/tag/3.32.2
Copyright FreeRDP contributors. Licensed under Apache License 2.0.
The build copies the complete upstream LICENSE to engine/licenses/FreeRDP
and this notice to engine/licenses. This upstream tag has no root NOTICE file.
Deskhand modifies the DISP translation unit to synchronize writes with channel
open/close. native/rdp/patch_disp.py generates this change without editing the
pinned checkout; the patch script is included beside this license in the app.
WinPR and the drdynvc/rdpgfx/disp clients are built from the same pinned source.

OpenSSL is dynamically linked from the development machine. FFmpeg 9.0.2
is built from the official pinned source by scripts/build-rdp-codec.sh with
only avcodec/avutil/swscale and H.264 software decoding, GPL/nonfree/version3 disabled.
The fixed codec source, configuration, license and provenance are retained in
build/rdp/codec/prefix/share. No Homebrew FFmpeg is bundled by the default build.
The application includes dependency licenses and the exact LGPL codec source.
No standalone desktop client is built. VideoToolbox decoding is not enabled.
