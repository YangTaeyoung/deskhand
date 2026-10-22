# Signed RDP authentication regression (2026-10-22)

The Preview 1 DMG started and loaded its engine, but that was not sufficient validation of login.

## Reproduction without user credentials

`build/rdp/probe-crypto-provider.c` loads the released app's OpenSSL library and attempts to load the legacy provider and fetch MD4. With an ad-hoc executable both succeed. The same executable signed with Developer ID and hardened runtime fails: macOS rejects Homebrew's `legacy.dylib` because it has a different Team ID. Evidence: `build/rdp/provider-{adhoc,hardened}.log`.

FreeRDP's pinned WinPR SSL initialization explicitly loads `legacy` for MD4 (`winpr/libwinpr/utils/ssl.c:327-338`); this build has `WITH_INTERNAL_MD4/RC4=OFF`. The module was absent from the app because runtime provider loading is invisible to `otool -L` on the engine. OpenSSL documents MD4/RC4 in its [legacy provider](https://docs.openssl.org/3.5/man7/OSSL_PROVIDER-legacy/).

## Change and checks

- Bundle the matching legacy provider, rewrite its libcrypto dependency, and sign it with the app's Team ID.
- Treat MH_BUNDLE correctly: it has no install ID, so its first dependency must not be skipped. The new test fails before this bundler correction and passes after it.
- Resolve the provider directory from the engine image before FreeRDP initialization, overriding external module search paths without disabling library validation.
- `native/rdp/tests/probe_auth_crypto.c` creates/frees a session without running it or sending a network request and fetches MD4/RC4. Release packaging compiles/signs this probe with hardened runtime and runs it with an invalid `OPENSSL_MODULES` directory.
- Original Release probe: FAIL (`build/rdp/rdp-auth-old-release-red.log`). Fixed Release probe: all three PASS (`build/rdp/rdp-auth-fixed-green.log`). Code signing and native tests pass. This proves the missing provider is corrected; it does not substitute for a successful Windows login.

## Login memory

The app now offers per-PC username/password storage in the Mac keychain, writes only after NLA-connected state, and clears pending credentials on failure/disconnect. It restores fields on the next launch without automatic network retries. Delete/opt-out removes the saved record; the preference and certificate-bound record index contain no password and live in UserDefaults. Test doubles cover successful persistence, failed-attempt protection, host/certificate isolation, opt-out, deletion, and failed writes without touching the user's keychain.

The fixed signed app is running from `build/login-fix-app/Deskhand.app`. Windows login and persistence across relaunch are pending user entry. Do not publish a replacement DMG or claim end-to-end login success before those checks.
