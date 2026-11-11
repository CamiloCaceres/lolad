#!/bin/sh
# Builds Lolad.app (macOS) next to this script. MVP: unsigned, current arch.
# Windows: GOOS=windows go build -ldflags "-s -w -H windowsgui" -o Lolad.exe .
# Usage: ./build.sh [--install]   (--install copies it to /Applications)
#        ./build.sh --release [...]  the full distributable bundle (universal,
#                                    tmux, daemon, lolabot, DMG) in dist/; the
#                                    arguments go to packaging/macos/bundle.sh.
#                                    See packaging/macos/bundle.sh for its env.
set -eu
cd "$(dirname "$0")"
if [ "${1:-}" = "--release" ]; then
  shift
  exec packaging/macos/bundle.sh "$@"
fi
VERSION="${VERSION:-0.1.0-mvp}"
export MACOSX_DEPLOYMENT_TARGET=13.0
export CGO_CFLAGS="-mmacosx-version-min=13.0"
export CGO_LDFLAGS="-mmacosx-version-min=13.0"
APP=Lolad.app
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
go build -trimpath -ldflags "-s -w" -o "$APP/Contents/MacOS/Lolad" .
cp assets/icons/Lolad.icns "$APP/Contents/Resources/Lolad.icns"
cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleName</key><string>Lolad</string>
  <key>CFBundleDisplayName</key><string>Lolad</string>
  <key>CFBundleIdentifier</key><string>dev.caceres.lolad.desktop</string>
  <key>CFBundleExecutable</key><string>Lolad</string>
  <key>CFBundleIconFile</key><string>Lolad</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>$VERSION</string>
  <key>CFBundleVersion</key><string>$VERSION</string>
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSPrincipalClass</key><string>NSApplication</string>
</dict></plist>
PLIST
# Ad-hoc signature: enough for a local run, not for distribution.
codesign --force --deep --sign - "$APP" >/dev/null 2>&1 || true
echo "built $PWD/$APP"
if [ "${1:-}" = "--install" ]; then
  rm -rf /Applications/Lolad.app
  cp -R "$APP" /Applications/Lolad.app
  echo "installed /Applications/Lolad.app"
fi
