#!/bin/sh
# Builds every file of a Lolad release locally, into dist/release-<version>/.
# It never tags, pushes or publishes: a person uploads the folder afterwards
# (docs/desktop.md section 10).
#
#   bin/release-local.sh <version> [--throwaway-key | --dry-run-key <pem>] [--no-android]
#
#   <version>        e.g. 0.2.0-beta.1 (a leading v is dropped)
#   --throwaway-key  a dry run: make a one-off update key in the work dir and
#                    stamp the apps with it. Everything is built and signed
#                    exactly like a release, but no installed app trusts that
#                    key, so the folder is marked DRY-RUN and must not be
#                    published.
#   --dry-run-key <pem>  like --throwaway-key, but reuse a test key made with
#                    `sign-manifest keygen` (its .pub.pem must sit beside it), so
#                    two test versions can update one to the other (docs/desktop.md
#                    section 17). Also marked DRY-RUN.
#   --no-android     leave out the Android app. Without it, a machine with no
#                    Android SDK or signing key stops before building anything.
#
# Environment:
#   UPDATE_SIGNING_KEY_FILE  the Ed25519 private key (PEM) that signs update.json.
#                            Required without --throwaway-key. Read, never printed.
#   UPDATE_PUBLIC_KEY        base64 public key stamped into the apps; default
#                            desktop/packaging/update-public-key.txt.
#   LOLABOT_SRC              a checkout of the pinned lolabot tag; default: cloned
#                            from versions.env into the work dir.
#   REPO                     GitHub owner/name for download URLs (CamiloCaceres/lolad)
#   APPLE_* / SIGN_PS1       optional code signing, passed through to the macOS and
#                            Windows build scripts. Unset: ad-hoc / unsigned (the beta).
#   ANDROID_HOME, JAVA_HOME, LOLAD_ANDROID_SIGNING_DIR
#                            the Android SDK, JDK and APK signing key used by
#                            clients/android-webview/scripts/build-apk.sh; defaults
#                            ~/android/sdk, ~/android/jdk/Contents/Home and
#                            ~/.local/share/lolad-android.
#
# Output (the names on the release page; the ones without a version are stable
# so /releases/latest/download/<name> links keep working):
#   Lolad.dmg  Lolad-<v>.dmg  Lolad-<v>.app.zip
#   Lolad-Setup.exe  Lolad-Setup-<v>.exe
#   Lolad-Android.apk   (in checksums.txt; not in update.json: Android is not on the updater)
#   lolad-{darwin,linux}-{amd64,arm64}  lolad-windows-{amd64,arm64}.exe
#   update.json  checksums.txt  notes.md
#   packaging/lolad.rb  packaging/winget/*.yaml   (Homebrew cask, winget manifests)
set -eu
cd "$(dirname "$0")/.."
root=$PWD

usage() { sed -n '2,44p' "$0"; exit 1; }
[ $# -ge 1 ] || usage
version=${1#v}
shift
throwaway=0
testkey=""
android=1
while [ $# -gt 0 ]; do
  case "$1" in
    --throwaway-key) throwaway=1 ;;
    --dry-run-key) [ $# -ge 2 ] || usage; throwaway=1; testkey=$2; shift ;;
    --no-android) android=0 ;;
    *) usage ;;
  esac
  shift
done
case "$version" in [0-9]*.[0-9]*.[0-9]*) ;; *) echo "release-local: '$version' is not a version" >&2; exit 1 ;; esac

die() { echo "release-local: $*" >&2; exit 1; }
step() { echo; echo "######## $*"; }

# shellcheck source=../desktop/packaging/versions.env disable=SC1091
. desktop/packaging/versions.env
REPO=${REPO:-CamiloCaceres/lolad}
tag="v$version"
base="https://github.com/$REPO/releases/download/$tag"
out="$root/dist/release-$version"
work=$(mktemp -d "${TMPDIR:-/tmp}/lolad-release.XXXXXX")
trap 'rm -rf "$work"' EXIT INT TERM

for t in go git codesign hdiutil makensis shasum; do
  command -v "$t" >/dev/null || die "$t is not installed (macOS with Go, Xcode tools and NSIS is required)"
done

# The Android app is checked up front, so a missing SDK stops the run before
# twenty minutes of building rather than after. Same defaults as build-apk.sh.
apk_script=clients/android-webview/scripts/build-apk.sh
if [ "$android" = 1 ]; then
  sdk=${ANDROID_HOME:-$HOME/android/sdk}
  jdk=${JAVA_HOME:-$HOME/android/jdk/Contents/Home}
  sign_dir=${LOLAD_ANDROID_SIGNING_DIR:-$HOME/.local/share/lolad-android}
  no_android() { die "$1. Install it, or pass --no-android to release without the Android app."; }
  [ -x "$apk_script" ] || no_android "$apk_script is missing"
  command -v zsh >/dev/null || no_android "zsh is not installed (build-apk.sh needs it)"
  [ -x "$sdk/build-tools/35.0.0/apksigner" ] || no_android "no Android SDK build-tools 35.0.0 in $sdk (set ANDROID_HOME)"
  [ -x "$jdk/bin/java" ] || no_android "no JDK in $jdk (set JAVA_HOME)"
  [ -f "$sign_dir/release.p12" ] || no_android "no Android signing key in $sign_dir (set LOLAD_ANDROID_SIGNING_DIR)"
fi

# ------------------------------------------------------------- update key
step "update key"
if [ -n "$testkey" ]; then
  [ -r "$testkey" ] || die "cannot read $testkey"
  key=$(cd "$(dirname "$testkey")" && pwd)/$(basename "$testkey")
  pubpem="${key%.*}.pub.pem"
  [ -r "$pubpem" ] || die "no $pubpem beside the test key"
  # SubjectPublicKeyInfo DER for Ed25519 is a 12-byte header plus the 32-byte key.
  UPDATE_PUBLIC_KEY=$(sed '/-----/d' "$pubpem" | tr -d '\n' | base64 -d | tail -c 32 | base64)
  echo "test key (dry run): public $UPDATE_PUBLIC_KEY"
elif [ "$throwaway" = 1 ]; then
  key="$work/throwaway-update-key.pem"
  UPDATE_PUBLIC_KEY=$(cd desktop && go run ./tools/sign-manifest keygen --out "$key" | sed -n 's/^ldflags:.*PublicKeyB64=//p')
  [ -n "$UPDATE_PUBLIC_KEY" ] || die "could not create the throwaway key"
  echo "throwaway key (dry run): public $UPDATE_PUBLIC_KEY"
else
  key=${UPDATE_SIGNING_KEY_FILE:-}
  [ -n "$key" ] && [ -r "$key" ] || die "set UPDATE_SIGNING_KEY_FILE to the update signing key (or use --throwaway-key for a dry run)"
  if [ -z "${UPDATE_PUBLIC_KEY:-}" ] && [ -f desktop/packaging/update-public-key.txt ]; then
    UPDATE_PUBLIC_KEY=$(tr -d ' \r\n' <desktop/packaging/update-public-key.txt)
  fi
  [ -n "${UPDATE_PUBLIC_KEY:-}" ] || die "no public key: commit desktop/packaging/update-public-key.txt or set UPDATE_PUBLIC_KEY"
fi
export UPDATE_PUBLIC_KEY

# ------------------------------------------------------------- lolabot
if [ -z "${LOLABOT_SRC:-}" ]; then
  step "lolabot $LOLABOT_TAG"
  git clone --quiet --depth 1 --branch "$LOLABOT_TAG" "$LOLABOT_REPO" "$work/lolabot" 2>&1 | grep -v 'detached HEAD' || true
  [ -d "$work/lolabot" ] || die "could not clone $LOLABOT_REPO at $LOLABOT_TAG"
  LOLABOT_SRC="$work/lolabot"
fi
export LOLABOT_SRC

rm -rf "$out"
mkdir -p "$out/packaging/winget"

# ------------------------------------------------------------- CLI binaries
step "lolad CLI for every platform"
bin/build-release.sh "$version"
for f in dist/lolad-darwin-amd64 dist/lolad-darwin-arm64 dist/lolad-linux-amd64 dist/lolad-linux-arm64 \
         dist/lolad-windows-amd64.exe dist/lolad-windows-arm64.exe; do
  [ -f "$f" ] || die "missing $f"
  cp "$f" "$out/"
done

# ------------------------------------------------------------- macOS
step "Mac app (universal, bundled tmux and lolabot)"
RELEASE=1 VERSION="$version" LOLAD_PREBUILT="$root/dist" desktop/build.sh --release
cp desktop/dist/Lolad.dmg "$out/Lolad-$version.dmg"
cp desktop/dist/Lolad.dmg "$out/Lolad.dmg"
cp desktop/dist/Lolad.app.zip "$out/Lolad-$version.app.zip"
lipo -archs desktop/dist/Lolad.app/Contents/MacOS/Lolad | grep -q 'x86_64 arm64\|arm64 x86_64' || die "the app is not universal"

# ------------------------------------------------------------- Windows
step "Windows installer"
RELEASE=1 VERSION="$version" desktop/packaging/windows/build.sh
cp desktop/dist/windows/Lolad-Setup.exe "$out/Lolad-Setup-$version.exe"
cp desktop/dist/windows/Lolad-Setup.exe "$out/Lolad-Setup.exe"

# ------------------------------------------------------------- Android
if [ "$android" = 1 ]; then
  step "Android app (clients/android-webview)"
  rm -f clients/android-webview/dist/lolad-web.apk
  "$apk_script" # lint, build, zipalign, sign with the release key, apksigner verify
  [ -f clients/android-webview/dist/lolad-web.apk ] || die "build-apk.sh did not produce dist/lolad-web.apk"
  cp clients/android-webview/dist/lolad-web.apk "$out/Lolad-Android.apk"
else
  step "Android app: skipped (--no-android)"
fi

# ------------------------------------------------------------- update.json
step "update.json (Ed25519)"
(cd desktop && go run ./tools/sign-manifest generate \
  --version "$version" \
  --multiplexer-version "$MULTIPLEXER_VERSION" \
  --notes-url "https://github.com/$REPO/releases/tag/$tag" \
  --url-base "$base/" \
  --darwin-arm64 "$out/Lolad-$version.app.zip" \
  --darwin-amd64 "$out/Lolad-$version.app.zip" \
  --windows-amd64 "$out/Lolad-Setup-$version.exe" \
  --key "$key" \
  --out "$out/update.json")
# The installed apps will check it against the key they are stamped with.
(cd desktop && go run ./tools/sign-manifest verify --in "$out/update.json" --pub "$UPDATE_PUBLIC_KEY")

# ------------------------------------------------------------- package managers
step "Homebrew cask and winget manifests"
date_iso=$(date -u +%Y-%m-%d)
dmg_sha=$(shasum -a 256 "$out/Lolad-$version.dmg" | cut -d' ' -f1)
exe_sha=$(shasum -a 256 "$out/Lolad-Setup-$version.exe" | cut -d' ' -f1)
fill() {
  sed -e "s|@VERSION@|$version|g" -e "s|@DMG_SHA256@|$dmg_sha|g" -e "s|@EXE_SHA256@|$exe_sha|g" \
      -e "s|@DOWNLOAD_BASE@|$base|g" -e "s|@DATE@|$date_iso|g" "$1" >"$2"
}
fill desktop/packaging/homebrew/lolad.rb "$out/packaging/lolad.rb"
for f in desktop/packaging/winget/*.yaml; do fill "$f" "$out/packaging/winget/$(basename "$f")"; done
if grep -rn '@[A-Z0-9_]*@' "$out/packaging"; then die "unfilled placeholder in a package manifest"; fi

# ------------------------------------------------------------- notes
restart_note=""
if [ "${MULTIPLEXER_CHANGED:-0}" = 1 ]; then
  restart_note=", except this one: it updates the terminal engine, so your agents restart once and pick up where they left off"
fi
sed -e "s|@VERSION@|$version|g" -e "s|@AGENT_RESTART_NOTE@|$restart_note|g" desktop/packaging/release-notes.md >"$out/notes.md"
if [ "$android" = 0 ]; then
  sed '/Lolad-Android\.apk/d' "$out/notes.md" >"$work/notes" && mv "$work/notes" "$out/notes.md"
fi
if [ "$throwaway" = 1 ]; then
  { echo "> **DRY RUN, do not publish.** Built with a throwaway update key."; echo; cat "$out/notes.md"; } >"$work/notes" && mv "$work/notes" "$out/notes.md"
fi

# ------------------------------------------------------------- checksums
step "checksums.txt"
(cd "$out" && find . -maxdepth 1 -type f ! -name notes.md | sed 's|^\./||' | sort |
  xargs shasum -a 256 >"$work/checksums.txt")
mv "$work/checksums.txt" "$out/checksums.txt"
cat "$out/checksums.txt"

if [ "$throwaway" = 1 ]; then
  echo "Built with a throwaway update key. No installed app trusts it. Do not publish this folder." >"$out/DRY-RUN-DO-NOT-PUBLISH"
fi

echo
echo "release files: $out"
[ "$throwaway" = 1 ] && echo "DRY RUN: built with a throwaway update key. Do not publish."
echo "Nothing was tagged, pushed or published."
