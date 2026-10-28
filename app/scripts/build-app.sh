#!/usr/bin/env bash
# Builds Deskhand.app; defaults to Debug in app/build. Release packaging uses an isolated build directory.
# Fails when the build emits any compiler warning. Bundles the Go CLI and both
# Windows agent variants for both architectures.
set -euo pipefail

app_dir="$(cd "$(dirname "$0")/.." && pwd)"
repo_dir="$(cd "$app_dir/.." && pwd)"
build_dir="${DESKHAND_BUILD_DIR:-$app_dir/build}"
configuration="${DESKHAND_CONFIGURATION:-Debug}"
case "$configuration" in Debug|Release) ;; *) echo 'Configuration must be Debug or Release' >&2; exit 2 ;; esac
log="$build_dir/xcodebuild.log"
mkdir -p "$build_dir"

# A library inside Deskhand.app; no external remote-desktop application is launched.
"$repo_dir/scripts/build-rdp.sh" > "$build_dir/rdp-build.log" 2>&1 || {
  tail -n 35 "$build_dir/rdp-build.log"
  exit 1
}

xcodegen generate --spec "$app_dir/project.yml" --project "$app_dir" --quiet

# -skipPackagePluginValidation trusts every package plugin in the graph (today SwiftTerm's build-info plugin).
if ! xcodebuild \
    -project "$app_dir/Deskhand.xcodeproj" \
    -scheme Deskhand \
    -configuration "$configuration" \
    -destination "platform=macOS,arch=$(uname -m)" \
    -derivedDataPath "$build_dir/DerivedData" \
    -skipPackagePluginValidation \
    build >"$log" 2>&1; then
  grep -E ': (fatal )?error: ' "$log" | sort -u || true
  tail -n 30 "$log"
  echo "빌드 실패 — 전체 로그: $log" >&2
  exit 1
fi

warnings="$(grep -E ': warning: ' "$log" | sort -u || true)"
if [ -n "$warnings" ]; then
  echo "$warnings"
  echo "경고 $(printf '%s\n' "$warnings" | wc -l | tr -d ' ')개 — 경고 없는 빌드여야 합니다. 전체 로그: $log" >&2
  exit 1
fi

app="$build_dir/Deskhand.app"
rm -rf "$app"
ditto "$build_dir/DerivedData/Build/Products/$configuration/Deskhand.app" "$app"

for binary in deskhand deskhand-agent-windows-amd64.exe deskhand-agentw-windows-amd64.exe deskhand-agent-windows-arm64.exe deskhand-agentw-windows-arm64.exe deskhand-ssh-relay-windows-amd64.exe deskhand-ssh-relay-windows-arm64.exe; do
  if [ ! -f "$repo_dir/build/$binary" ]; then
    echo "$repo_dir/build/$binary 이(가) 없습니다. scripts/build.sh를 먼저 실행하세요" >&2
    exit 1
  fi
done
main_executable="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "$app/Contents/Info.plist")"
if [ "$(printf '%s' "$main_executable" | tr '[:upper:]' '[:lower:]')" = deskhand ]; then
  echo "앱 실행 파일과 Go CLI의 이름이 겹칩니다" >&2
  exit 1
fi
cp -f "$repo_dir/build/deskhand" "$app/Contents/MacOS/deskhand"
python3 "$app_dir/scripts/bundle-rdp.py" "$repo_dir/build/rdp/engine/libDeskhandRDPEngine.dylib" "$app/Contents/Frameworks" --provider "$(brew --prefix openssl@3)/lib/ossl-modules/legacy.dylib"
mkdir -p "$app/Contents/Resources/rdp-licenses"
cp "$repo_dir/build/rdp/FreeRDP/LICENSE" "$app/Contents/Resources/rdp-licenses/FreeRDP-LICENSE"
ditto "$repo_dir/build/rdp/engine/licenses" "$app/Contents/Resources/rdp-licenses"
ditto "$repo_dir/build/rdp/codec/prefix/share/licenses" "$app/Contents/Resources/rdp-licenses"
ditto "$repo_dir/build/rdp/codec/prefix/share/ffmpeg" "$app/Contents/Resources/rdp-sources"

mkdir -p "$app/Contents/Resources/agents"
for binary in "$repo_dir"/build/deskhand-agent*-windows-*.exe "$repo_dir"/build/deskhand-ssh-relay-windows-*.exe; do
  cp -f "$binary" "$app/Contents/Resources/agents/$(basename "$binary")"
done
codesign --force --sign - "$app/Contents/MacOS/deskhand"
python3 "$app_dir/scripts/check-minimum-os.py" "$app" > "$build_dir/minimum-os.json"
codesign --force --sign - "$app"
codesign --verify --strict --deep "$app"

echo "$app"
