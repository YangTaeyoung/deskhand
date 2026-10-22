#!/usr/bin/env bash
# Build an isolated Release app, sign/notarize app and DMG, produce checksums.
set -euo pipefail
release_root="$(cd "$(dirname "$0")/../.." && pwd)"
release_version="${1:?Usage: package-release.sh VERSION SIGNING_IDENTITY NOTARY_PROFILE}"
release_identity="${2:?Developer ID Application identity required}"
release_profile="${3:?Existing notarytool profile required}"
[[ "$release_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.]+)?$ ]] || { echo 'Invalid release version' >&2; exit 2; }
[[ "$(uname -m)" == arm64 ]] || { echo 'This release pipeline currently builds Apple Silicon only' >&2; exit 2; }
cd "$release_root"
git diff --quiet && git diff --cached --quiet || { echo 'Commit tracked release changes before packaging' >&2; exit 2; }
release_commit="$(git rev-parse HEAD)"
release_dir="$release_root/build/releases/$release_version"
mkdir -p "$release_dir"
release_dmg="$release_dir/Deskhand-$release_version-macos-arm64.dmg"
[[ ! -e "$release_dmg" ]] || { echo "Refusing to replace an existing DMG: $release_dmg" >&2; exit 2; }
xcrun notarytool history --keychain-profile "$release_profile" --output-format json > "$release_dir/notary-profile-check.json"
DESKHAND_VERSION="$release_version" "$release_root/scripts/build.sh" > "$release_dir/go-build.log" 2>&1
DESKHAND_CONFIGURATION=Release DESKHAND_BUILD_DIR="$release_root/build/release-app" \
  "$release_root/app/scripts/build-app.sh" > "$release_dir/app-build.log" 2>&1
release_app="$release_root/build/release-app/Deskhand.app"
/usr/libexec/PlistBuddy -c "Add :DeskhandReleaseVersion string $release_version" "$release_app/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Add :DeskhandSourceRevision string $release_commit" "$release_app/Contents/Info.plist"
for release_library in "$release_app"/Contents/Frameworks/*.dylib; do
  codesign --force --options runtime --timestamp --sign "$release_identity" "$release_library"
done
codesign --force --options runtime --timestamp --sign "$release_identity" "$release_app/Contents/MacOS/deskhand"
codesign --force --options runtime --timestamp --sign "$release_identity" "$release_app"
codesign --verify --deep --strict "$release_app"
# A plain dlopen smoke test misses OpenSSL's runtime-loaded NTLM provider.
clang -Wall -Wextra -Werror "$release_root/native/rdp/tests/probe_auth_crypto.c" -o "$release_dir/rdp-auth-probe"
codesign --force --options runtime --timestamp --sign "$release_identity" "$release_dir/rdp-auth-probe"
OPENSSL_MODULES=/deskhand-no-external-providers "$release_dir/rdp-auth-probe" "$release_app/Contents/Frameworks" > "$release_dir/rdp-auth-verification.log" 2>&1
"$release_app/Contents/MacOS/deskhand" --json version > "$release_dir/cli-version.json"

# Notarize the app first so its stapled ticket survives drag-install from DMG.
ditto -c -k --keepParent "$release_app" "$release_dir/Deskhand-notarize.zip"
xcrun notarytool submit "$release_dir/Deskhand-notarize.zip" --keychain-profile "$release_profile" \
  --wait --timeout 15m --output-format json > "$release_dir/app-notarization.json"
python3 - "$release_dir/app-notarization.json" <<'PY'
import json,sys
r=json.load(open(sys.argv[1])); assert r['status']=='Accepted', r['status']
print('App notarization:', r['status'])
PY
xcrun stapler staple "$release_app"
xcrun stapler validate "$release_app"
spctl --assess --type execute --verbose=2 "$release_app"

release_stage="$(mktemp -d "$release_root/build/release-staging.XXXXXX")"
trap 'rm -rf "$release_stage"' EXIT
ditto "$release_app" "$release_stage/Deskhand.app"
ln -s /Applications "$release_stage/Applications"
cp "$release_root/docs/releases/INSTALL-ko.txt" "$release_stage/설치 안내.txt"
hdiutil create -volname Deskhand -srcfolder "$release_stage" -fs HFS+ -format UDZO "$release_dmg"
codesign --force --timestamp --sign "$release_identity" "$release_dmg"
xcrun notarytool submit "$release_dmg" --keychain-profile "$release_profile" \
  --wait --timeout 15m --output-format json > "$release_dir/dmg-notarization.json"
python3 - "$release_dir/dmg-notarization.json" <<'PY'
import json,sys
r=json.load(open(sys.argv[1])); assert r['status']=='Accepted', r['status']
print('DMG notarization:', r['status'])
PY
xcrun stapler staple "$release_dmg"
xcrun stapler validate "$release_dmg"
hdiutil verify "$release_dmg"
(cd "$release_dir" && shasum -a 256 "$(basename "$release_dmg")" > SHA256SUMS)
printf '%s\n' "$release_dmg"
