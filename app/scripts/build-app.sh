#!/usr/bin/env bash
# Builds Deskhand.app (Debug) into app/build/Deskhand.app and prints its path.
# Fails when the build emits any compiler warning. Bundles the Go CLI and both
# Windows agent variants for both architectures.
set -euo pipefail

app_dir="$(cd "$(dirname "$0")/.." && pwd)"
repo_dir="$(cd "$app_dir/.." && pwd)"
build_dir="$app_dir/build"
log="$build_dir/xcodebuild.log"
mkdir -p "$build_dir"

xcodegen generate --spec "$app_dir/project.yml" --project "$app_dir" --quiet

# -skipPackagePluginValidation trusts every package plugin in the graph (today SwiftTerm's build-info plugin).
if ! xcodebuild \
    -project "$app_dir/Deskhand.xcodeproj" \
    -scheme Deskhand \
    -configuration Debug \
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
ditto "$build_dir/DerivedData/Build/Products/Debug/Deskhand.app" "$app"

for binary in deskhand deskhand-agent-windows-amd64.exe deskhand-agentw-windows-amd64.exe deskhand-agent-windows-arm64.exe deskhand-agentw-windows-arm64.exe; do
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
mkdir -p "$app/Contents/Resources/agents"
for binary in "$repo_dir"/build/deskhand-agent*-windows-*.exe; do
  cp -f "$binary" "$app/Contents/Resources/agents/$(basename "$binary")"
done
codesign --force --sign - "$app/Contents/MacOS/deskhand"
codesign --force --sign - "$app"
codesign --verify --strict --deep "$app"

echo "$app"
