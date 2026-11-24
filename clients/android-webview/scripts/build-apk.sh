#!/bin/zsh
set -euo pipefail
cd "${0:A:h:h}"
export JAVA_HOME="${JAVA_HOME:-$HOME/android/jdk/Contents/Home}"
export ANDROID_HOME="${ANDROID_HOME:-$HOME/android/sdk}"
export GRADLE_USER_HOME="${GRADLE_USER_HOME:-$HOME/android/gradle-home}"
signing_dir="${LOLAD_ANDROID_SIGNING_DIR:-$HOME/.local/share/lolad-android}"
[[ -f "$signing_dir/release.p12" ]] || { echo "no signing key in $signing_dir; run ../android/scripts/build-apk.sh once"; exit 1; }
# LOLAD_VERSION (set by bin/release-local.sh) stamps the APK: versionName is
# the release version, versionCode only ever grows, so a newer APK installs over
# an older one. X.Y.Z[-alpha|beta|rc.N] -> X*10000000 + Y*100000 + Z*1000 + S,
# where S is 100+N, 300+N or 500+N for a prerelease and 900 for the release
# (0.2.0-beta.5 -> 200305, 0.2.0 -> 200900). Without it: the dev defaults.
props=()
if [[ -n "${LOLAD_VERSION:-}" ]]; then
  v=${LOLAD_VERSION#v}
  [[ $v =~ '^([0-9]+)\.([0-9]+)\.([0-9]+)(-(alpha|beta|rc)\.([0-9]+))?$' ]] ||
    { echo "build-apk: '$v' is not X.Y.Z or X.Y.Z-alpha|beta|rc.N"; exit 1; }
  major=$match[1] minor=$match[2] patch=$match[3] label=$match[5] n=${match[6]:-0}
  (( minor < 100 && patch < 100 && n < 100 && major <= 200 )) ||
    { echo "build-apk: '$v' is outside the versionCode scheme (minor, patch and N under 100)"; exit 1; }
  case $label in
    alpha) stage=$((100 + n)) ;; beta) stage=$((300 + n)) ;; rc) stage=$((500 + n)) ;; *) stage=900 ;;
  esac
  code=$((major * 10000000 + minor * 100000 + patch * 1000 + stage))
  props=(-Plolad.versionName="$v" -Plolad.versionCode="$code")
  echo "APK version $v (versionCode $code)"
fi
mkdir -p dist
./gradlew lintRelease assembleRelease --console=plain $props
bt="$ANDROID_HOME/build-tools/35.0.0"
"$bt/zipalign" -f -p 4 app/build/outputs/apk/release/app-release-unsigned.apk dist/lolad-web.apk
"$bt/apksigner" sign --ks "$signing_dir/release.p12" --ks-key-alias lolad --ks-pass "file:$signing_dir/password" dist/lolad-web.apk
"$bt/apksigner" verify dist/lolad-web.apk
if [[ -n "${LOLAD_VERSION:-}" ]]; then
  "$bt/aapt2" dump badging dist/lolad-web.apk | grep -q "versionCode='$code' versionName='$v'" ||
    { echo "build-apk: the APK does not carry version $v ($code)"; exit 1; }
fi
(cd dist && shasum -a 256 lolad-web.apk > SHA256SUMS)
