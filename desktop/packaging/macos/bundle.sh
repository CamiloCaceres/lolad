#!/bin/sh
# Builds Lolad.app (docs/desktop.md section 4), then optionally signs,
# notarizes and packs it. Output goes to desktop/dist/ (never /Applications).
#
#   bundle.sh [--dev] [--no-dmg]
#
#   --dev     only the host architecture, no DMG (what desktop/build.sh runs)
#   --no-dmg  stop after Lolad.app and Lolad.app.zip
#
# Environment (all optional unless noted):
#   VERSION               default: git describe
#   ARCHS                 "arm64 amd64" (default). Go archs; amd64 == x86_64.
#                         Any arch that cannot be built here makes the script stop
#                         with a clear message (set ALLOW_PARTIAL=1 to continue
#                         with the archs that did build).
#   LOLABOT_SRC           directory with the pinned lolabot release (CI checks out
#                         a git tag and points this at it). Required when RELEASE=1.
#   LOLAD_PREBUILT        dir with lolad-darwin-{arm64,amd64} to use instead of building
#                         the daemon (the release workflow builds them once).
#   TMUX_BIN              a ready (universal) tmux; default: build it from pinned
#                         source with build-tmux.sh (cached in dist/tmux).
#   TMUX_HOMEBREW=1       instead take Homebrew's tmux and relocate its dylibs into
#                         Contents/Frameworks (fallback, host arch only).
#   RELEASE=1             refuse a partial bundle (missing arch or lolabot).
#   APPLE_SIGN_IDENTITY   "Developer ID Application: Name (TEAMID)": real signing
#                         with hardened runtime. Unset: ad-hoc signature.
#   APPLE_KEYCHAIN        optional keychain file that holds the identity.
#   Notarization (only with APPLE_SIGN_IDENTITY), one of:
#     NOTARY_PROFILE                            a notarytool keychain profile
#     APPLE_API_KEY_PATH, APPLE_API_KEY_ID, APPLE_API_ISSUER   App Store Connect key
#     APPLE_ID, APPLE_TEAM_ID, APPLE_APP_PASSWORD              app-specific password
#   SKIP_NOTARIZE=1       sign but do not notarize.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
desktop=$(cd "$here/../.." && pwd)
root=$(cd "$desktop/.." && pwd)
# shellcheck source=../versions.env disable=SC1091
. "$here/../versions.env"
dist="$desktop/dist"
work="$dist/work"

dev=0
dmg=1
for a in "$@"; do
  case "$a" in
    --dev) dev=1; dmg=0 ;;
    --no-dmg) dmg=0 ;;
    -h|--help) sed -n '2,32p' "$0"; exit 0 ;;
    *) echo "bundle.sh: unknown option $a" >&2; exit 1 ;;
  esac
done

VERSION=${VERSION:-$(git -C "$root" describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)}
VERSION=${VERSION#v}
# Stamp the updater: version, and the Ed25519 public key when provided (UPDATE_PUBLIC_KEY, base64).
APP_LDFLAGS="-X main.updateVersion=$VERSION"
[ -n "${UPDATE_PUBLIC_KEY:-}" ] && APP_LDFLAGS="$APP_LDFLAGS -X github.com/CamiloCaceres/lolad/desktop/updater.PublicKeyB64=$UPDATE_PUBLIC_KEY"
plist_version=$(echo "$VERSION" | sed 's/[^0-9.].*$//')
[ -n "$plist_version" ] || plist_version=0.0.0
host=$(uname -m)
case "$host" in arm64) host_go=arm64 ;; *) host_go=amd64 ;; esac
if [ "$dev" = 1 ]; then ARCHS=$host_go; fi
ARCHS=${ARCHS:-arm64 amd64}
RELEASE=${RELEASE:-0}

die() { echo "bundle.sh: $*" >&2; exit 1; }
step() { echo "==> $*"; }

command -v go >/dev/null || die "go is not installed"
command -v codesign >/dev/null || die "this script needs macOS (codesign not found)"
export MACOSX_DEPLOYMENT_TARGET=13.0

rm -rf "$work"
mkdir -p "$work"
app="$dist/Lolad.app"
rm -rf "$app"

macho_arch() { case "$1" in arm64) echo arm64 ;; amd64) echo x86_64 ;; esac; }

# can_run_arch <go arch>: can this Mac run (and so cgo-link-test) that arch?
can_run_arch() {
  [ "$1" = "$host_go" ] && return 0
  [ "$1" = amd64 ] && [ "$host" = arm64 ] && /usr/bin/arch -x86_64 /usr/bin/true 2>/dev/null
}

# ---------------------------------------------------------------- Go binaries
built=""
prebuilt=0
for a in $ARCHS; do
  step "lolad daemon + CLI ($a)"
  if [ -n "${LOLAD_PREBUILT:-}" ] && [ -f "$LOLAD_PREBUILT/lolad-darwin-$a" ]; then
    cp "$LOLAD_PREBUILT/lolad-darwin-$a" "$work/daemon-$a"
    prebuilt=1
  else
    (cd "$root" && CGO_ENABLED=0 GOOS=darwin GOARCH="$a" go build -trimpath \
      -ldflags "-s -w -X main.version=$VERSION" -o "$work/daemon-$a" .)
  fi
  step "Wails app ($a)"
  if ! (cd "$desktop" && CGO_ENABLED=1 GOOS=darwin GOARCH="$a" \
      CGO_CFLAGS="-mmacosx-version-min=13.0" CGO_LDFLAGS="-mmacosx-version-min=13.0" \
      go build -trimpath -ldflags "-s -w $APP_LDFLAGS" -o "$work/app-$a" . 2>"$work/wails-$a.log"); then
    tail -15 "$work/wails-$a.log" >&2
    echo "bundle.sh: the Wails app (cgo + WebKit) could not be built for $a on this machine." >&2
    echo "           Build that arch on a macOS runner of that architecture and rerun there." >&2
    if [ "${ALLOW_PARTIAL:-0}" = 1 ] && [ "$RELEASE" != 1 ]; then continue; fi
    exit 1
  fi
  built="$built $a"
done
[ -n "$built" ] || die "no architecture was built"
# shellcheck disable=SC2086
set -- $built
nbuilt=$#
nwant=$(echo "$ARCHS" | wc -w | tr -d ' ')
if [ "$nbuilt" -ne "$nwant" ] && [ "$RELEASE" = 1 ]; then die "RELEASE=1 needs all of: $ARCHS"; fi

mk_universal() { # out prefix
  out=$1; prefix=$2; set --
  for a in $built; do set -- "$@" "$work/$prefix-$a"; done
  if [ $# -gt 1 ]; then lipo -create "$@" -output "$out"; else cp "$1" "$out"; fi
}

# ------------------------------------------------------------------- the tmux
tmux_src=""
tmux_ver=""
frameworks=0
if [ "${TMUX_HOMEBREW:-0}" = 1 ]; then
  [ "$RELEASE" != 1 ] || die "RELEASE=1 does not allow TMUX_HOMEBREW (not universal, depends on the build machine)"
  tmux_src=$(command -v tmux) || die "tmux not found"
  frameworks=1
elif [ -n "${TMUX_BIN:-}" ]; then
  tmux_src=$TMUX_BIN
else
  step "tmux from pinned source"
  want=""
  for a in $built; do want="$want $(macho_arch "$a")"; done
  rc=0
  # shellcheck disable=SC2086
  TMUX_WORK="$dist/tmux/work" "$here/build-tmux.sh" "$dist/tmux" $want || rc=$?
  if [ "$rc" = 2 ] && [ "${ALLOW_PARTIAL:-0}" != 1 ]; then
    die "tmux could not be built for every arch on this machine (see above). Use a runner of the missing arch, or ALLOW_PARTIAL=1 for a host-only bundle."
  elif [ "$rc" != 0 ] && [ "$rc" != 2 ]; then
    die "build-tmux.sh failed"
  fi
  tmux_src="$dist/tmux/tmux"
fi
[ -x "$tmux_src" ] || die "tmux binary not found: $tmux_src"
tmux_ver=$("$tmux_src" -V 2>/dev/null) || die "$tmux_src -V failed"

# --------------------------------------------------------------- assemble
step "assembling $app"
c="$app/Contents"
mkdir -p "$c/MacOS" "$c/Helpers" "$c/Resources" "$c/Library/LaunchAgents"
mk_universal "$c/MacOS/Lolad" app
mk_universal "$c/Helpers/lolad" daemon
cp "$tmux_src" "$c/Helpers/tmux"
chmod 755 "$c/MacOS/Lolad" "$c/Helpers/lolad" "$c/Helpers/tmux"
cp "$here/tmux.conf" "$c/Resources/tmux.conf"
printf '%s\n' "$MULTIPLEXER_VERSION" >"$c/Resources/multiplexer.version"
if [ "$tmux_ver" != "tmux $TMUX_VERSION" ]; then
  [ "$RELEASE" != 1 ] || die "bundled tmux reports '$tmux_ver', versions.env pins $TMUX_VERSION"
  echo "bundle.sh: warning: bundled tmux is '$tmux_ver', not the pinned $TMUX_VERSION (multiplexer.version still says $MULTIPLEXER_VERSION)" >&2
fi
cp "$here/dev.caceres.lolad.plist" "$c/Library/LaunchAgents/dev.caceres.lolad.plist"

icon_key=""
for ic in "${ICON_ICNS:-}" "$desktop/assets/icons/Lolad.icns" "$here/Lolad.icns"; do
  if [ -n "$ic" ] && [ -f "$ic" ]; then
    cp "$ic" "$c/Resources/Lolad.icns"
    icon_key="<key>CFBundleIconFile</key><string>Lolad</string>"
    break
  fi
done

if [ -n "${LOLABOT_SRC:-}" ]; then
  [ -d "$LOLABOT_SRC" ] || die "LOLABOT_SRC is not a directory: $LOLABOT_SRC"
  mkdir -p "$c/Resources/lolabot"
  # a pinned copy: no .git, no CI/editor leftovers
  (cd "$LOLABOT_SRC" && tar -cf - --exclude=.git --exclude=.github --exclude=.DS_Store .) |
    (cd "$c/Resources/lolabot" && tar -xf -)
  if [ -d "$LOLABOT_SRC/.git" ]; then
    git -C "$LOLABOT_SRC" describe --tags --always 2>/dev/null >"$c/Resources/lolabot.version" || true
  fi
elif [ "$RELEASE" = 1 ]; then
  die "RELEASE=1 needs LOLABOT_SRC (a checkout of the pinned lolabot tag)"
else
  echo "bundle.sh: warning: no LOLABOT_SRC, Resources/lolabot is not included (fine for a dev build)" >&2
fi

cat >"$c/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleName</key><string>Lolad</string>
  <key>CFBundleDisplayName</key><string>Lolad</string>
  <key>CFBundleIdentifier</key><string>dev.caceres.lolad.desktop</string>
  <key>CFBundleExecutable</key><string>Lolad</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>$VERSION</string>
  <key>CFBundleVersion</key><string>$plist_version</string>
  $icon_key
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <key>LSUIElement</key><false/>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSPrincipalClass</key><string>NSApplication</string>
  <key>NSAppTransportSecurity</key><dict><key>NSAllowsLocalNetworking</key><true/></dict>
</dict></plist>
PLIST
plutil -lint "$c/Info.plist" >/dev/null

# deps <file>: install names of every dylib the file (all slices) loads
deps() { otool -L "$1" | awk '/^[[:space:]]/{print $1}'; }

# ------------------------------------------- relocate Homebrew dylibs (fallback)
# Copies every non-system dependency of the tmux into Contents/Frameworks and
# rewrites the load commands to @executable_path/../Frameworks/<name>.
relocate() ( # file (a subshell body: the recursion must not clobber f/dep/base)
  f=$1
  deps "$f" | while read -r dep; do
    case "$dep" in
      /usr/lib/*|/System/*|@*) continue ;;
    esac
    base=$(basename "$dep")
    if [ ! -f "$c/Frameworks/$base" ]; then
      mkdir -p "$c/Frameworks"
      cp "$dep" "$c/Frameworks/$base"
      chmod 644 "$c/Frameworks/$base"
      install_name_tool -id "@executable_path/../Frameworks/$base" "$c/Frameworks/$base"
      relocate "$c/Frameworks/$base"
    fi
    install_name_tool -change "$dep" "@executable_path/../Frameworks/$base" "$f"
  done
)
if [ "$frameworks" = 1 ]; then
  step "relocating Homebrew dylibs into Frameworks"
  relocate "$c/Helpers/tmux"
  # install_name_tool voids the signatures and arm64 refuses to run unsigned code;
  # the real signature is applied in the signing step below.
  for f in "$c/Frameworks"/* "$c/Helpers/tmux"; do codesign --force --sign - "$f" 2>/dev/null; done
fi

# ------------------------------------------------- verify the bundled tmux
step "verifying the bundled tmux"
for f in "$c/Helpers/tmux" "$c/Frameworks"/*; do
  [ -f "$f" ] || continue
  bad=$(deps "$f" | grep -E '^(/opt/homebrew|/usr/local|/opt/local)' || true)
  [ -z "$bad" ] || die "$f still links against: $bad"
  # every dependency must be system, or inside the bundle
  stray=$(deps "$f" | grep -vE '^(/usr/lib/|/System/|@executable_path/|@rpath/|@loader_path/)' || true)
  [ -z "$stray" ] || die "$f has non-system dependencies: $stray"
done
sock="lolad-pkgtest-$$-$(od -An -N2 -tu2 /dev/urandom | tr -d ' ')"
for a in $built; do
  can_run_arch "$a" || { echo "bundle.sh: note: cannot run $a here; its tmux slice is checked with otool only" >&2; continue; }
  if [ "$a" = "$host_go" ]; then pre=""; else pre="/usr/bin/arch -x86_64"; fi
  # shellcheck disable=SC2086
  $pre "$c/Helpers/tmux" -L "$sock" -f /dev/null new-session -d -s pkgtest 'sleep 60' ||
    die "bundled tmux ($a) failed to start a session"
  # shellcheck disable=SC2086
  $pre "$c/Helpers/tmux" -L "$sock" has-session -t pkgtest || die "bundled tmux ($a) lost its session"
  # shellcheck disable=SC2086
  $pre "$c/Helpers/tmux" -L "$sock" kill-server || die "bundled tmux ($a) could not kill its server"
  echo "   tmux $a: started and killed a session on -L $sock"
done
dv=$("$c/Helpers/lolad" version 2>&1 | head -1) || true
case "$dv" in
  ""|*Wails*|*INF*) die "Helpers/lolad is not the lolad CLI (got: $dv)" ;;
esac
if [ "$prebuilt" = 0 ]; then
  case "$dv" in *"$VERSION"*) ;; *) die "Helpers/lolad reports '$dv', expected $VERSION" ;; esac
fi
echo "   Helpers/lolad version: $dv"

# ------------------------------------------------------------------ signing
sign_one() { # path
  if [ -n "${APPLE_SIGN_IDENTITY:-}" ]; then
    codesign --force --timestamp --options runtime --entitlements "$here/entitlements.plist" \
      ${APPLE_KEYCHAIN:+--keychain "$APPLE_KEYCHAIN"} --sign "$APPLE_SIGN_IDENTITY" "$1"
  else
    codesign --force --sign - "$1"
  fi
}
sign_app() {
  step "signing every Mach-O inside-out (${APPLE_SIGN_IDENTITY:-ad-hoc})"
  # Deepest paths first; the main executable and the bundle last.
  find "$c" -type f ! -path "$c/MacOS/Lolad" -print | awk '{print length, $0}' | sort -rn | cut -d' ' -f2- |
    while read -r f; do
      if file -b "$f" | grep -q 'Mach-O'; then sign_one "$f" || exit 1; fi
    done
  sign_one "$c/MacOS/Lolad"
  sign_one "$app"
}
sign_app
codesign --verify --deep --strict --verbose=2 "$app"
echo "   codesign --verify --deep --strict: OK"

notarize() { # file-to-submit
  if [ -n "${NOTARY_PROFILE:-}" ]; then
    xcrun notarytool submit "$1" --keychain-profile "$NOTARY_PROFILE" --wait
  elif [ -n "${APPLE_API_KEY_PATH:-}" ] && [ -n "${APPLE_API_KEY_ID:-}" ] && [ -n "${APPLE_API_ISSUER:-}" ]; then
    xcrun notarytool submit "$1" --key "$APPLE_API_KEY_PATH" --key-id "$APPLE_API_KEY_ID" --issuer "$APPLE_API_ISSUER" --wait
  elif [ -n "${APPLE_ID:-}" ] && [ -n "${APPLE_TEAM_ID:-}" ] && [ -n "${APPLE_APP_PASSWORD:-}" ]; then
    xcrun notarytool submit "$1" --apple-id "$APPLE_ID" --team-id "$APPLE_TEAM_ID" --password "$APPLE_APP_PASSWORD" --wait
  else
    return 1
  fi
}
have_notary() {
  [ -n "${NOTARY_PROFILE:-}" ] ||
    { [ -n "${APPLE_API_KEY_PATH:-}" ] && [ -n "${APPLE_API_KEY_ID:-}" ] && [ -n "${APPLE_API_ISSUER:-}" ]; } ||
    { [ -n "${APPLE_ID:-}" ] && [ -n "${APPLE_TEAM_ID:-}" ] && [ -n "${APPLE_APP_PASSWORD:-}" ]; }
}

notarized=0
if [ -n "${APPLE_SIGN_IDENTITY:-}" ] && [ "${SKIP_NOTARIZE:-0}" != 1 ] && have_notary; then
  step "notarizing Lolad.app"
  ditto -c -k --keepParent "$app" "$work/notarize.zip"
  notarize "$work/notarize.zip" || die "notarization failed"
  xcrun stapler staple "$app"
  xcrun stapler validate "$app"
  notarized=1
elif [ -n "${APPLE_SIGN_IDENTITY:-}" ]; then
  echo "bundle.sh: signed but NOT notarized (no notary credentials, or SKIP_NOTARIZE=1)" >&2
fi

# ------------------------------------------------------------------ packing
step "Lolad.app.zip (the updater's artifact)"
rm -f "$dist/Lolad.app.zip"
ditto -c -k --keepParent "$app" "$dist/Lolad.app.zip"

if [ "$dmg" = 1 ]; then
  step "Lolad.dmg"
  stage="$work/dmg"
  rm -rf "$stage"
  mkdir -p "$stage"
  ditto "$app" "$stage/Lolad.app"
  ln -s /Applications "$stage/Applications"
  rm -f "$dist/Lolad.dmg"
  hdiutil create -quiet -volname "Lolad" -srcfolder "$stage" -fs HFS+ -format UDZO -ov "$dist/Lolad.dmg"
  if [ -n "${APPLE_SIGN_IDENTITY:-}" ]; then
    codesign --force --timestamp ${APPLE_KEYCHAIN:+--keychain "$APPLE_KEYCHAIN"} --sign "$APPLE_SIGN_IDENTITY" "$dist/Lolad.dmg"
    if [ "$notarized" = 1 ]; then
      notarize "$dist/Lolad.dmg" || die "DMG notarization failed"
      xcrun stapler staple "$dist/Lolad.dmg"
    fi
  fi
fi

rm -rf "$work"
echo
echo "built:   $app  (version $VERSION, archs:$(for a in $built; do printf ' %s' "$(macho_arch "$a")"; done), $tmux_ver)"
echo "         $dist/Lolad.app.zip"
[ "$dmg" = 1 ] && echo "         $dist/Lolad.dmg"
if [ -z "${APPLE_SIGN_IDENTITY:-}" ]; then
  cat <<'MSG'

This build is ad-hoc signed, not notarized. On another Mac, Gatekeeper will
refuse the first open. Either right-click Lolad.app > Open > Open, or run:
    xattr -dr com.apple.quarantine /Applications/Lolad.app
(System Settings > Privacy & Security > "Open Anyway" also works.)
MSG
fi
