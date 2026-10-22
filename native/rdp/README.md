# Embedded RDP engine (Task 3)

Run `scripts/build-rdp.sh` on macOS. It prints the absolute path to
`build/rdp/engine/libDeskhandRDPEngine.dylib` after building FreeRDP and running
the C tests. No password, Windows access, Homebrew install or global configuration
change is required. Dependencies: CMake, Ninja, pkg-config, OpenSSL 3; FFmpeg is built as a fixed minimal LGPL decoder; the build does not use Homebrew FFmpeg. The checked-out FreeRDP source must be pristine at 3.32.2,
`97eff6ddfbbfaa8e42a126d362686bd001ff8bdc`. The script clones it if absent and
refuses to replace a different existing checkout.

## Loader contract

The exported C functions and their types are in `deskhand_rdp.h`.
Create a session, invoke blocking `dh_rdp_run` exactly once on a background thread,
poll state/frame/session ID from the app, call `dh_rdp_stop` from any thread, join
run and every other caller, then call `dh_rdp_free`. An active free is ignored.
Allocate a new session to reconnect. String arguments are copied. Domain may be
NULL; password must point to a string (an empty string is allowed).

States are idle=0, connecting=1, connected=2, stopped=3, failed=4. Run returns
zero for an explicit stop and nonzero for failure or a repeated run. Repeated
run does not mutate the first run's state/error. Error is a FreeRDP error or one
of the stable `0xD4000001..0xD4000009` engine constants in the header. NULL new
means invalid configuration or allocation failure. NULL state/error queries return
failed/argument error. No error strings or credential-bearing diagnostic API exist.

Initial dimensions are 200..8192 per axis. Server desktop resize and GFX surface
dimensions must fit 8192 per axis. One tightly packed BGRA32 snapshot is retained,
at most 256 MiB, with alpha forced to 255. Old snapshot storage is freed before
allocating a changed size. This is the snapshot/queue bound, not a total process
memory limit: FreeRDP GDI, surface/codec caches, TLS and caller buffers use additional
memory. Only dirty EndPaint callbacks publish a sequence. Copying a previous sequence
again returns 1. With a frame available, NULL destination/capacity zero returns -1
with dimensions/stride/sequence populated; allocate `stride * height` and retry.
All four output pointers are required. A concurrent resize can require another retry.

`dh_rdp_session_id` returns SaveSessionInfo V1/V2 `sessionId` or the extended
`logon_info_ex.LogonId` when `haveCookie` is true. Until one is known it returns
`UINT32_MAX`; it does not infer identity from ClientSessionId or other settings.
The application must keep agent input disabled while identity is unknown.

## Authentication, rendering, cancellation

Only NLA/CredSSP over TLS is enabled; TLS-only and classic RDP fallback are
disabled. `TlsSecurity=false` disables the TLS-only protocol, not NLA's TLS
transport (see pinned `libfreerdp/core/nego.c`). Normal X.224 protocol negotiation
remains enabled. Server redirection and automatic reconnection are disabled.

ExternalCertificateManagement calls VerifyX509Certificate before the ordinary PKI
accept path. The leaf is parsed from the provided PEM chain and its **DER** SHA-256
must match the supplied 64-digit hex pin (colon-separated pairs also accepted).
Acceptance lasts only for the session. No cert-ignore, TOFU trust files or prompt
callbacks are used. A PKI-trusted certificate with a different pin is rejected.
The engine erases its accessible settings password after connect returns and again
at teardown. FreeRDP's internal backup settings/SSPI allocations remain owned by
FreeRDP until context destruction in free; the caller must free promptly after join
and erase its own original password buffer.

FreeRDP client/common loads built-in drdynvc/rdpgfx/disp. GDI composes BGRA frames;
rdpgfx is attached to its graphics pipeline. The fixed LGPL FFmpeg H.264 decoder/parser supports the enabled H.264/AVC444 path. No standalone UI client or clipboard, drive, audio, input, RemoteApp or
other redirection channel is built, and no desktop input API is exported.

Stop sets an atomic flag and FreeRDP's abort event. PreConnect checks the flag after
FreeRDP resets its event, covering stop-before/during-connect races. Hostnames use
macOS asynchronous DNS-SD with a 50 ms cancellation poll and 10-second deadline;
the public TCPConnect callback delegates only numeric addresses to FreeRDP's TCP
connector, avoiding its blocking getaddrinfo path. Original hostname settings remain
available for TLS/NLA. Event processing runs on the run thread. Snapshot copy and
resize publication share a mutex. GDI/rdpgfx cleanup runs after channels close.

## Tests and bundling handoff

`rdp_test_abi` checks invalid config/pin, create/free, stop-before-run, repeated run
and racing stops. `rdp_test_engine` compiles the same implementation to exercise
private callbacks without adding exports: real blocked socket cancellation,
hostname/numeric connection, active free, NLA downgrade rejection, trusted TLS pin
match/mismatch, BGRA/alpha/stride/capacity, concurrent snapshot reads with 1000
resizes, surface/desktop bounds, server LogonId and a real H.264 IDR decode. Test
credentials are empty. No Windows login or actual server screen is claimed.

Use `-DDH_RDP_SANITIZE=ON` for engine/test ASan+UBSan instrumentation (the default
upstream FreeRDP build is not instrumented). Build logs: `build/rdp/configure.log`,
`build.log`, `tests.log`; detailed output: `engine/Testing/Temporary/LastTest.log`.
The initial missing-ABI RED result is `build/rdp/test-red.log`.

The app bundler owns recursive library copying, install-name rewriting and signing.
`engine/otool-dependencies.txt` records **unmodified** dependency names/paths;
FreeRDP/WinPR libraries remain under `engine/freerdp`, with `@rpath` install names
and build-tree LC_RPATHs. The dependency pin and license/notice files are in
`engine/dependency-pin.txt` and `engine/licenses`. Include this directory's Apache
LICENSE/NOTICE and the licenses of the actual OpenSSL/FFmpeg dependency builds in
the distribution. FFmpeg licensing depends on its build configuration.

References (validated against the pinned source):

- https://github.com/FreeRDP/FreeRDP/releases/tag/3.32.2
- https://pub.freerdp.com/api/structrdp__freerdp.html
- https://docs.openssl.org/3.6/man3/X509_digest/
- `libfreerdp/crypto/tls.c:1863`: external certificate callback before normal PKI.
- `libfreerdp/core/utils.c:360`: abort event; `core/freerdp.c:103`: reset before PreConnect.
- `libfreerdp/core/nego.c:1666`: selected protocol security settings.
- `include/freerdp/transport_io.h:79`: public TCPConnect override.
- `libfreerdp/core/tcp.c:786`: upstream blocking resolver.
- `libfreerdp/core/info.c:1473`, `include/freerdp/session.h:50`: server LogonId callback.
- `libfreerdp/gdi/gfx.c:2078`: GDI graphics pipeline; `codec/h264.c:760`: FFmpeg backend.

## Dynamic resolution

`dh_rdp_resize` accepts a bounded latest-only request. The run loop waits for
DISP caps, fits requests within the complete server area limit, and sends the
latest layout without reconnecting. `patch_disp.py` generates a narrow change
to the pinned DISP translation unit: open/send/close share callback lifetime
protection. The original checkout remains pristine. The patch and upstream
licenses are included in the app. Native tests include concurrent send/close.

The Swift UI defaults to one remote pixel per window point and Windows 100%
scale, without Retina supersampling. Very large windows are bounded by 3840 per
axis and 8,294,400 pixels. One-second pipeline metrics count reception and layer
submission; they do not measure physical display presentation.
