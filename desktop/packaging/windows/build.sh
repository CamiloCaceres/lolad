#!/bin/sh
# Cross-builds everything for the Windows installer from macOS or Linux, and
# compiles the installer when `makensis` is on PATH. (CI runs the NSIS step on
# the Windows runner, see .github/workflows/release.yml.)
#
#   build.sh [--stage-only]
#
# Output (desktop/dist/windows/):
#   stage/                    exactly what the installer installs
#   Lolad-Setup.exe           unless --stage-only or makensis is missing
#
# Environment:
#   VERSION         default: git describe (a leading v is dropped)
#   LOLABOT_SRC     directory with the pinned lolabot release (required with RELEASE=1)
#   RELEASE=1       refuse a partial build
#   SIGN_PS1        script that Authenticode-signs the file in $1; when set, the
#                   .exe files are signed before packing and passed to makensis
#                   (it signs the uninstaller and the installer).
#   PSMUX_ZIP       a local copy of the pinned psmux zip (skips the download;
#                   the checksum is still verified)
set -eu

here=$(cd "$(dirname "$0")" && pwd)
desktop=$(cd "$here/../.." && pwd)
# shellcheck source=../versions.env disable=SC1091
. "$here/../versions.env"
PSMUX_URL="https://github.com/psmux/psmux/releases/download/v$PSMUX_VERSION/psmux-v$PSMUX_VERSION-windows-x64.zip"
root=$(cd "$desktop/.." && pwd)
out="$desktop/dist/windows"
stage="$out/stage"
RELEASE=${RELEASE:-0}
stage_only=0
[ "${1:-}" = "--stage-only" ] && stage_only=1

VERSION=${VERSION:-$(git -C "$root" describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)}
VERSION=${VERSION#v}
# NSIS wants a numeric a.b.c for VIProductVersion
nsis_version=$(echo "$VERSION" | sed -E 's/^([0-9]+\.[0-9]+\.[0-9]+).*/\1/')
case "$nsis_version" in [0-9]*.[0-9]*.[0-9]*) ;; *) nsis_version=0.0.0 ;; esac

die() { echo "build.sh: $*" >&2; exit 1; }
step() { echo "==> $*"; }
sign() { if [ -n "${SIGN_PS1:-}" ]; then powershell -NoProfile -ExecutionPolicy Bypass -File "$SIGN_PS1" "$1" || die "signing $1 failed"; fi; }

rm -rf "$stage"
mkdir -p "$stage/bin/psmux" "$out"

step "lolad.exe (console CLI) and lolad-daemon.exe (no console window)"
(cd "$root" &&
  CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
    -ldflags "-s -w -X main.version=$VERSION" -o "$stage/bin/lolad.exe" . &&
  CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
    -ldflags "-s -w -H windowsgui -X main.version=$VERSION" -o "$stage/bin/lolad-daemon.exe" .)

step "Lolad.exe (Wails app, GUI subsystem)"
(cd "$desktop" &&
  CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
    -ldflags "-s -w -H windowsgui -X main.updateVersion=${VERSION#v}${UPDATE_PUBLIC_KEY:+ -X github.com/CamiloCaceres/lolad/desktop/updater.PublicKeyB64=$UPDATE_PUBLIC_KEY}" -o "$stage/Lolad.exe" .)

step "psmux $PSMUX_VERSION (pinned, checksum verified)"
zip="${PSMUX_ZIP:-$out/psmux-$PSMUX_VERSION.zip}"
if [ ! -f "$zip" ]; then
  curl -fsSL --retry 3 -o "$zip.part" "$PSMUX_URL"
  mv "$zip.part" "$zip"
fi
sum=$(shasum -a 256 "$zip" 2>/dev/null | cut -d' ' -f1 || sha256sum "$zip" | cut -d' ' -f1)
[ "$sum" = "$PSMUX_SHA256" ] || die "psmux checksum mismatch: got $sum, want $PSMUX_SHA256"
tmp="$out/psmux-extract"
rm -rf "$tmp"
mkdir -p "$tmp"
unzip -q -o "$zip" -d "$tmp"
cp "$tmp/psmux.exe" "$stage/bin/psmux/psmux.exe"
cp "$tmp/LICENSE" "$stage/bin/psmux/LICENSE"    # MIT: the notice must travel with the binary
rm -rf "$tmp"
printf '%s\n' "$MULTIPLEXER_VERSION" >"$stage/multiplexer.version"

if [ -n "${LOLABOT_SRC:-}" ]; then
  [ -d "$LOLABOT_SRC" ] || die "LOLABOT_SRC is not a directory: $LOLABOT_SRC"
  mkdir -p "$stage/lolabot"
  (cd "$LOLABOT_SRC" && tar -cf - --exclude=.git --exclude=.github --exclude=.DS_Store .) |
    (cd "$stage/lolabot" && tar -xf -)
elif [ "$RELEASE" = 1 ]; then
  die "RELEASE=1 needs LOLABOT_SRC"
else
  echo "build.sh: warning: no LOLABOT_SRC; the installer gets an empty lolabot\\ folder (dev build)" >&2
  mkdir -p "$stage/lolabot"
  : >"$stage/lolabot/.keep"
fi

[ -f "$root/LICENSE" ] && cp "$root/LICENSE" "$stage/LICENSE"
[ -f "$root/NOTICE.md" ] && cp "$root/NOTICE.md" "$stage/NOTICE.md"
[ -f "$desktop/assets/icons/Lolad.ico" ] && cp "$desktop/assets/icons/Lolad.ico" "$stage/Lolad.ico"

for f in "$stage/Lolad.exe" "$stage/bin/lolad.exe" "$stage/bin/lolad-daemon.exe"; do sign "$f"; done
# psmux.exe is third-party; sign it too only if it is not already signed (kept as shipped).

echo "$VERSION" >"$out/version.txt"
echo "staged: $stage"
[ "$stage_only" = 1 ] && exit 0

if command -v makensis >/dev/null 2>&1; then
  step "installer"
  set -- -V2 "-DVERSION=$nsis_version" "-DSTAGE=$stage" "-DOUTFILE=$out/Lolad-Setup.exe"
  [ -n "${SIGN_PS1:-}" ] && set -- "$@" "-DSIGN_PS1=$SIGN_PS1"
  makensis "$@" "$here/installer.nsi"
  echo "built: $out/Lolad-Setup.exe"
else
  echo "build.sh: makensis not found; skipped the installer. Run on the Windows runner:" >&2
  echo "  makensis /DVERSION=$nsis_version /DSTAGE=$stage /DOUTFILE=Lolad-Setup.exe $here/installer.nsi" >&2
fi
