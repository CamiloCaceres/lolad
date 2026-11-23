#!/bin/zsh
set -euo pipefail
cd "${0:A:h:h}"
export JAVA_HOME="${JAVA_HOME:-$HOME/android/jdk/Contents/Home}"
export ANDROID_HOME="${ANDROID_HOME:-$HOME/android/sdk}"
export GRADLE_USER_HOME="${GRADLE_USER_HOME:-$HOME/android/gradle-home}"
signing_dir="${LOLAD_ANDROID_SIGNING_DIR:-$HOME/.local/share/lolad-android}"
[[ -f "$signing_dir/release.p12" ]] || { echo "no signing key in $signing_dir; run ../android/scripts/build-apk.sh once"; exit 1; }
mkdir -p dist
./gradlew lintRelease assembleRelease --console=plain
bt="$ANDROID_HOME/build-tools/35.0.0"
"$bt/zipalign" -f -p 4 app/build/outputs/apk/release/app-release-unsigned.apk dist/lolad-web.apk
"$bt/apksigner" sign --ks "$signing_dir/release.p12" --ks-key-alias lolad --ks-pass "file:$signing_dir/password" dist/lolad-web.apk
"$bt/apksigner" verify dist/lolad-web.apk
(cd dist && shasum -a 256 lolad-web.apk > SHA256SUMS)
