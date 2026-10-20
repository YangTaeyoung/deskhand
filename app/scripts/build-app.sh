#!/usr/bin/env bash
# Builds Deskhand.app (Debug) into app/build/Deskhand.app and prints its path.
# Fails when the build emits any compiler warning. Copies the Go binaries from build/
# (deskhand, deskhand-agent-windows-*.exe) into Contents/MacOS when they exist.
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

main_executable="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "$app/Contents/Info.plist")"
copied=0
for binary in "$repo_dir/build/deskhand" "$repo_dir"/build/deskhand-agent-windows-*.exe; do
  if [ -f "$binary" ]; then
    name="$(basename "$binary")"
    if [ "$(printf '%s' "$name" | tr '[:upper:]' '[:lower:]')" = "$(printf '%s' "$main_executable" | tr '[:upper:]' '[:lower:]')" ]; then
      echo "$name 이(가) 앱 실행 파일 $main_executable 과 이름이 겹칩니다" >&2
      exit 1
    fi
    cp -f "$binary" "$app/Contents/MacOS/$name"
    copied=$((copied + 1))
  fi
done
if [ "$copied" -eq 0 ]; then
  echo "경고: $repo_dir/build 에 Go 바이너리가 없어 앱에 넣지 않았습니다 (scripts/build.sh로 만들 수 있습니다)" >&2
else
  for binary in "$app"/Contents/MacOS/deskhand*; do
    codesign --force --sign - "$binary"
  done
  codesign --force --sign - "$app"
fi

echo "$app"
